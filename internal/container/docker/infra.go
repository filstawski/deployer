package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/execx"
	"github.com/filstawski/deployer/internal/gateway"
	"github.com/filstawski/deployer/internal/secretfile"
)

// GatewayOptions configures the gateway container.
type GatewayOptions struct {
	// Image overrides the gateway image. When empty, the image is built from
	// the running deployerd executable.
	Image string
	// Listen and ControlListen are host addresses published by the container.
	Listen        string
	ControlListen string
	// PublicScheme is forwarded to applications as X-Forwarded-Proto.
	PublicScheme string
}

const gatewayRole = "gateway"

// CheckDaemon verifies that the Docker daemon is reachable and runs Linux
// containers. It returns the daemon's architecture, such as "amd64".
func (d *Docker) CheckDaemon(ctx context.Context) (string, error) {
	out, err := d.run(ctx, nil, "version", "--format", "{{.Server.Os}}/{{.Server.Arch}}")
	if err != nil {
		return "", fmt.Errorf("cannot reach the Docker daemon: %w", err)
	}
	osName, arch, _ := strings.Cut(strings.TrimSpace(out), "/")
	if osName != "linux" {
		return "", fmt.Errorf("Docker must run Linux containers (found %q)", osName)
	}
	return arch, nil
}

// EnsureGateway starts the gateway container, replacing it if its image or
// settings changed. It returns a client for the gateway's control API.
// After a replacement, the caller must reattach deployment networks.
func (d *Docker) EnsureGateway(ctx context.Context) (*gateway.Client, error) {
	g := d.opts.Gateway
	arch, err := d.CheckDaemon(ctx)
	if err != nil {
		return nil, err
	}
	tokenPath := filepath.Join(d.opts.StateDir, "gateway.token")
	token, err := secretfile.ReadToken(tokenPath)
	if errors.Is(err, os.ErrNotExist) {
		token = domain.NewTokenSecret()
		err = secretfile.WriteNew(tokenPath, token)
	}
	if err != nil {
		return nil, err
	}
	image := g.Image
	if image == "" {
		if image, err = d.buildGatewayImage(ctx, arch); err != nil {
			return nil, err
		}
	}
	imageID, err := d.run(ctx, nil, "image", "inspect", "--format", "{{.Id}}", image)
	if err != nil {
		return nil, fmt.Errorf("gateway image %s not found; build it with make gateway-image: %w", image, err)
	}
	user := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	args := []string{"--listen", "0.0.0.0:8080", "--control-listen", "0.0.0.0:8081", "--token-file", "/run/deployer/gateway.token", "--scheme", g.PublicScheme}
	spec, _ := json.Marshal([]any{strings.TrimSpace(imageID), args, g.Listen, g.ControlListen, user})
	wantSpec := hashBytes(spec)[:16]

	labels, exists, err := d.inspectLabels(ctx, "container", d.gateway, gatewayRole)
	if err != nil {
		return nil, err
	}
	if exists && labels[labelSpec] != wantSpec {
		if _, err := d.run(ctx, nil, "rm", "--force", d.gateway); err != nil {
			return nil, err
		}
		exists = false
	}
	if !exists {
		create := []string{"create", "--name", d.gateway, "--restart", "unless-stopped",
			"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
			"--memory", "256m", "--cpus", "1", "--pids-limit", "512",
			// Run as the controller's user so the token file stays owner-only.
			"--user", user,
			"--mount", "type=bind,src=" + tokenPath + ",dst=/run/deployer/gateway.token,readonly",
			"--publish", g.Listen + ":8080", "--publish", g.ControlListen + ":8081",
			"--log-driver", "json-file", "--log-opt", "max-size=10m", "--log-opt", "max-file=3",
			"--label", labelSpec + "=" + wantSpec}
		create = append(create, d.labels(gatewayRole)...)
		if _, err := d.run(ctx, nil, append(append(create, image), args...)...); err != nil {
			return nil, fmt.Errorf("create gateway container: %w", err)
		}
	}
	if _, err := d.run(ctx, nil, "start", d.gateway); err != nil {
		return nil, fmt.Errorf("start gateway container: %w", err)
	}
	client := gateway.NewClient("http://"+g.ControlListen, token)
	deadline := time.Now().Add(20 * time.Second)
	for {
		err := client.Healthy(ctx)
		if err == nil {
			return client, nil
		}
		if time.Now().After(deadline) {
			logs, _ := d.run(ctx, nil, "logs", "--tail", "20", d.gateway)
			return nil, fmt.Errorf("gateway did not become ready: %w\n%s", err, logs)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// buildGatewayImage packages the running executable as the gateway image.
// The tag is derived from the executable's hash, so upgrading deployerd
// produces a new image and the gateway container is replaced.
func (d *Docker) buildGatewayImage(ctx context.Context, daemonArch string) (string, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != daemonArch {
		return "", fmt.Errorf("this deployerd (%s/%s) cannot run inside the Docker daemon's linux/%s containers; build an image with make gateway-image and set gateway.image in server.yaml",
			runtime.GOOS, runtime.GOARCH, daemonArch)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		return "", err
	}
	tag := "deployer-gateway:" + hashBytes(binary)[:16]
	if _, err := d.run(ctx, nil, "image", "inspect", tag); err == nil {
		return tag, nil
	}
	if err := BuildGatewayImage(ctx, binary, d.opts.StateDir, tag, d.labels(gatewayRole)...); err != nil {
		return "", err
	}
	d.removeOldGatewayImages(ctx, tag)
	return tag, nil
}

// BuildGatewayImage packages a static Linux deployerd binary as a gateway
// image tagged tag, using a scratch directory inside workDir. buildArgs are
// added to docker build, such as labels.
func BuildGatewayImage(ctx context.Context, binary []byte, workDir, tag string, buildArgs ...string) error {
	dir, err := os.MkdirTemp(workDir, "gateway-image-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	files := []struct {
		name string
		data []byte
		mode os.FileMode
	}{{"deployerd", binary, 0o755}, {"Dockerfile", []byte(GatewayDockerfile), 0o644}}
	for _, f := range files {
		path := filepath.Join(dir, f.name)
		if err := os.WriteFile(path, f.data, f.mode); err != nil {
			return err
		}
		// The service runs with umask 077, and the gateway runs as an
		// unprivileged user without capabilities: set modes explicitly.
		if err := os.Chmod(path, f.mode); err != nil {
			return err
		}
	}
	args := append([]string{"build", "--network", "none", "--tag", tag}, buildArgs...)
	if _, err := execx.Run(ctx, execx.Cmd{Name: "docker", Args: append(args, dir)}); err != nil {
		return fmt.Errorf("build gateway image: %w", err)
	}
	return nil
}

// removeOldGatewayImages deletes gateway images of earlier releases.
func (d *Docker) removeOldGatewayImages(ctx context.Context, current string) {
	out, err := d.run(ctx, nil, "image", "ls", "--format", "{{.Repository}}:{{.Tag}}",
		"--filter", "label="+labelInstallation+"="+d.opts.InstallationID, "--filter", "label="+labelDeployment+"="+gatewayRole)
	if err != nil {
		return
	}
	for _, image := range strings.Fields(out) {
		if image != current {
			d.run(ctx, nil, "image", "rm", image) // Fails harmlessly while in use.
		}
	}
}

// GatewayDockerfile packages a static deployerd binary as the gateway.
const GatewayDockerfile = `FROM scratch
COPY deployerd /deployerd
USER 65532:65532
ENTRYPOINT ["/deployerd", "gateway"]
`

// EnsureBuilder creates the dedicated, resource-limited BuildKit builder.
// It never changes the host's default builder.
func (d *Docker) EnsureBuilder(ctx context.Context) error {
	if _, err := d.run(ctx, nil, "buildx", "inspect", d.builder); err != nil {
		if _, err := d.run(ctx, nil, "buildx", "create", "--name", d.builder, "--driver", "docker-container",
			"--driver-opt", "memory=2g,cpu-period=100000,cpu-quota=200000"); err != nil {
			return fmt.Errorf("create build container: %w", err)
		}
	}
	if _, err := d.run(ctx, nil, "buildx", "inspect", "--bootstrap", d.builder); err != nil {
		return fmt.Errorf("start build container: %w", err)
	}
	return nil
}

// PruneBuildCache removes build cache entries unused for longer than age.
func (d *Docker) PruneBuildCache(ctx context.Context, age time.Duration) error {
	_, err := d.run(ctx, io.Discard, "buildx", "prune", "--builder", d.builder, "--force", "--filter", "until="+age.String())
	return err
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
