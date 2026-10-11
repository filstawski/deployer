// Package docker owns only resources labelled with this installation's ID.
// Commands use argument arrays, never a shell or user-supplied Compose files.
package docker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"deployer/internal/command"
	"deployer/internal/config"
	"deployer/internal/credentials"
	"deployer/internal/domain"
	"deployer/internal/gateway"
)

const ownerLabel = "build.deployer.installation"
const deploymentLabel = "build.deployer.deployment"

type Runtime struct {
	Config                      config.Server
	ID, Gateway, Builder, token string
	HTTP                        *http.Client
}

func New(c config.Server, id string) *Runtime {
	return &Runtime{Config: c, ID: id, Gateway: "dpl-gateway-" + id, Builder: "dpl-builder-" + id, HTTP: &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (r *Runtime) run(ctx context.Context, log io.Writer, args ...string) (string, error) {
	out, err := command.Run(ctx, "", nil, log, "docker", args...)
	if err != nil {
		return out, fmt.Errorf("docker %s: %w: %s", args[0], err, out)
	}
	return out, nil
}
func (r *Runtime) labels(id string) []string {
	return []string{"--label", ownerLabel + "=" + r.ID, "--label", deploymentLabel + "=" + id}
}
func (r *Runtime) app(d domain.Deployment) string     { return "dpl-app-" + d.ID }
func (r *Runtime) network(d domain.Deployment) string { return "dpl-net-" + d.ID }
func (r *Runtime) tag(d domain.Deployment) string {
	return "deployer-local/" + r.ID + "/" + d.ID + ":preview"
}

// owned fails closed on daemon errors, name conflicts, and resources owned by others.
func (r *Runtime) owned(ctx context.Context, kind, name, id string) (bool, error) {
	out, err := r.run(ctx, nil, kind, "inspect", name)
	if err != nil {
		if strings.Contains(out, "No such ") || kind == "network" && strings.Contains(out, "network "+name+" not found") {
			return false, nil
		}
		return false, err
	}
	var items []struct {
		Labels map[string]string
		Config struct{ Labels map[string]string }
	}
	if err = json.Unmarshal([]byte(out), &items); err != nil || len(items) != 1 {
		return false, fmt.Errorf("invalid Docker inspect result")
	}
	labels := items[0].Labels
	if labels == nil {
		labels = items[0].Config.Labels
	}
	if labels[ownerLabel] != r.ID || labels[deploymentLabel] != id {
		return false, fmt.Errorf("refusing to change an unowned %s %s", kind, name)
	}
	return true, nil
}
func (r *Runtime) Ensure(ctx context.Context) error {
	if _, err := r.run(ctx, nil, "info", "--format", "{{.OSType}}"); err != nil {
		return err
	}
	if _, err := r.run(ctx, nil, "image", "inspect", r.Config.Runtime.GatewayImage); err != nil {
		return fmt.Errorf("build gateway image first with make gateway-image: %w", err)
	}
	tokenPath := filepath.Join(r.Config.StateDir, "gateway.token")
	token, err := credentials.Read(tokenPath)
	if os.IsNotExist(err) {
		f, e := credentials.Create(tokenPath)
		if e != nil {
			return e
		}
		random := make([]byte, 32)
		if _, e = rand.Read(random); e != nil {
			f.Close()
			return e
		}
		secret := "dpl_" + hex.EncodeToString(random)
		e = credentials.Write(f, secret)
		f.Close()
		if e != nil {
			return e
		}
		token = secret
		err = nil
	}
	if err != nil {
		return err
	}
	r.token = token
	exists, err := r.owned(ctx, "container", r.Gateway, "gateway")
	if err != nil {
		return err
	}
	if exists {
		out, e := r.run(ctx, nil, "container", "inspect", r.Gateway)
		if e != nil {
			return e
		}
		var items []struct {
			Image  string
			Config struct{ Cmd []string }
		}
		if e = json.Unmarshal([]byte(out), &items); e != nil || len(items) != 1 {
			return fmt.Errorf("cannot inspect gateway configuration")
		}
		desired, e := r.run(ctx, nil, "image", "inspect", "--format", "{{.Id}}", r.Config.Runtime.GatewayImage)
		if e != nil {
			return e
		}
		scheme := "http"
		for n, v := range items[0].Config.Cmd {
			if v == "--public-scheme" && n+1 < len(items[0].Config.Cmd) {
				scheme = items[0].Config.Cmd[n+1]
			}
		}
		if items[0].Image != strings.TrimSpace(desired) || scheme != r.Config.PreviewScheme() {
			if _, e = r.run(ctx, nil, "rm", "--force", r.Gateway); e != nil {
				return e
			}
			exists = false
		}
	}
	if !exists {
		args := []string{"create", "--name", r.Gateway, "--restart", "unless-stopped", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--sysctl", "net.ipv4.ip_forward=0", "--memory", "128m", "--cpus", "0.5", "--pids-limit", "128", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--mount", "type=bind,src=" + tokenPath + ",dst=/run/gateway.token,readonly", "-p", fmt.Sprintf("127.0.0.1:%d:8080", r.Config.Runtime.GatewayPort), "-p", fmt.Sprintf("127.0.0.1:%d:8081", r.Config.Runtime.ControlPort)}
		args = append(args, r.labels("gateway")...)
		args = append(args, r.Config.Runtime.GatewayImage, "--listen", "0.0.0.0:8080", "--control-listen", "0.0.0.0:8081", "--control-token-file", "/run/gateway.token", "--public-scheme", r.Config.PreviewScheme())
		if _, err = r.run(ctx, nil, args...); err != nil {
			return err
		}
	}
	if _, err = r.run(ctx, nil, "start", r.Gateway); err != nil {
		return err
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		if err = r.control(ctx, "GET", "/healthz", nil); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("gateway control did not start: %w", err)
		case <-time.After(200 * time.Millisecond):
		}
	}
	// A distinct resource-limited builder never changes the user's default builder.
	out, err := r.run(ctx, nil, "buildx", "ls", "--format", "{{.Name}}")
	if err != nil {
		return err
	}
	found := false
	for _, name := range strings.Fields(out) {
		if name == r.Builder {
			found = true
		}
	}
	if !found {
		if _, err = r.run(ctx, nil, "buildx", "create", "--name", r.Builder, "--driver", "docker-container", "--driver-opt", "memory=2g,cpu-period=100000,cpu-quota=200000"); err != nil {
			return err
		}
	}
	_, err = r.run(ctx, nil, "buildx", "inspect", r.Builder, "--bootstrap")
	return err
}
func (r *Runtime) Build(ctx context.Context, d domain.Deployment, recipe config.Recipe, root string, log io.Writer) (string, error) {
	file, err := config.SafePath(root, recipe.Source.Dockerfile)
	if err != nil {
		return "", err
	}
	dir, err := config.SafePath(root, recipe.Source.Context)
	if err != nil {
		return "", err
	}
	args := []string{"buildx", "build", "--builder", r.Builder, "--load", "--progress", "plain", "--tag", r.tag(d), "--file", file}
	args = append(args, r.labels(d.ID)...)
	args = append(args, dir)
	if _, err = r.run(ctx, log, args...); err != nil {
		return "", domain.Fail("BUILD_FAILED", "image build failed; inspect deployment build logs")
	}
	out, err := r.run(ctx, nil, "image", "inspect", "--format", "{{.Id}}", r.tag(d))
	return strings.TrimSpace(out), err
}
func (r *Runtime) Start(ctx context.Context, d domain.Deployment) error {
	exists, err := r.owned(ctx, "network", r.network(d), d.ID)
	if err != nil {
		return err
	}
	if !exists {
		args := []string{"network", "create", "--internal", "--driver", "bridge", "--opt", "com.docker.network.bridge.gateway_mode_ipv4=isolated"}
		args = append(args, r.labels(d.ID)...)
		args = append(args, r.network(d))
		if _, err = r.run(ctx, nil, args...); err != nil {
			return err
		}
	}
	if err = r.Attach(ctx, d); err != nil {
		return err
	}
	exists, err = r.owned(ctx, "container", r.app(d), d.ID)
	if err != nil {
		return err
	}
	if !exists {
		args := []string{"create", "--name", r.app(d), "--network", r.network(d), "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--memory", "512m", "--cpus", "1", "--pids-limit", "256", "--log-driver", "json-file", "--log-opt", "max-size=5m", "--log-opt", "max-file=2"}
		args = append(args, r.labels(d.ID)...)
		args = append(args, "--env", "PORT="+strconv.Itoa(d.Port), "--env", "DEPLOYER_COMMIT="+d.Commit)
		for k, v := range d.Environment {
			args = append(args, "--env", k+"="+v)
		}
		args = append(args, d.Image)
		if _, err = r.run(ctx, nil, args...); err != nil {
			return err
		}
	}
	_, err = r.run(ctx, nil, "start", r.app(d))
	return err
}
func (r *Runtime) Upstream(d domain.Deployment) string {
	return fmt.Sprintf("http://%s:%d", r.app(d), d.Port)
}
func (r *Runtime) Running(ctx context.Context, d domain.Deployment) (bool, error) {
	exists, err := r.owned(ctx, "container", r.app(d), d.ID)
	if err != nil || !exists {
		return false, err
	}
	out, err := r.run(ctx, nil, "container", "inspect", "--format", "{{.State.Running}}", r.app(d))
	return strings.TrimSpace(out) == "true", err
}
func (r *Runtime) Stop(ctx context.Context, d domain.Deployment) error {
	exists, err := r.owned(ctx, "container", r.app(d), d.ID)
	if err != nil || !exists {
		return err
	}
	_, err = r.run(ctx, nil, "stop", "--time", "5", r.app(d))
	return err
}
func (r *Runtime) Remove(ctx context.Context, d domain.Deployment) error {
	exists, err := r.owned(ctx, "container", r.app(d), d.ID)
	if err != nil {
		return err
	}
	if exists {
		if _, err = r.run(ctx, nil, "rm", "--force", "--volumes", r.app(d)); err != nil {
			return err
		}
	}
	exists, err = r.owned(ctx, "network", r.network(d), d.ID)
	if err != nil {
		return err
	}
	if exists {
		out, e := r.run(ctx, nil, "network", "inspect", "--format", "{{json .Containers}}", r.network(d))
		if e != nil {
			return e
		}
		var members map[string]struct{ Name string }
		if e = json.Unmarshal([]byte(out), &members); e != nil {
			return e
		}
		for _, m := range members {
			if m.Name == r.Gateway {
				if _, e = r.run(ctx, nil, "network", "disconnect", r.network(d), r.Gateway); e != nil {
					return e
				}
			}
		}
		if _, err = r.run(ctx, nil, "network", "rm", r.network(d)); err != nil {
			return err
		}
	}
	exists, err = r.owned(ctx, "image", r.tag(d), d.ID)
	if err != nil {
		return err
	}
	if exists {
		_, err = r.run(ctx, nil, "image", "rm", r.tag(d))
	}
	return err
}
func (r *Runtime) Logs(ctx context.Context, d domain.Deployment) (string, error) {
	exists, err := r.owned(ctx, "container", r.app(d), d.ID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", domain.Fail("NOT_FOUND", "container logs are not available")
	}
	return r.run(ctx, nil, "logs", "--tail", "500", r.app(d))
}
func (r *Runtime) Publish(ctx context.Context, routes []gateway.Route) error {
	b, err := json.Marshal(routes)
	if err != nil {
		return err
	}
	return r.control(ctx, "PUT", "/routes", b)
}
func (r *Runtime) Probe(ctx context.Context, host string) error {
	return r.control(ctx, "POST", "/probe?host="+url.QueryEscape(host), nil)
}
func (r *Runtime) control(ctx context.Context, method, path string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", r.Config.Runtime.ControlPort, path), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return fmt.Errorf("gateway control returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// RemoveInfrastructure is intended for integration-test installations only.
// Normal shutdown preserves the gateway and builder for controller restarts.
func (r *Runtime) RemoveInfrastructure(ctx context.Context) error {
	exists, err := r.owned(ctx, "container", r.Gateway, "gateway")
	if err != nil {
		return err
	}
	if exists {
		if _, err = r.run(ctx, nil, "rm", "--force", r.Gateway); err != nil {
			return err
		}
	}
	_, err = r.run(ctx, nil, "buildx", "rm", r.Builder)
	return err
}

// Attach restores an owned preview network after gateway replacement, without
// starting an application that was intentionally stopped or has expired.
func (r *Runtime) Attach(ctx context.Context, d domain.Deployment) error {
	exists, err := r.owned(ctx, "network", r.network(d), d.ID)
	if err != nil || !exists {
		return err
	}
	// Inspect membership before connecting; do not conceal unexpected connect errors.
	out, err := r.run(ctx, nil, "container", "inspect", "--format", "{{json .NetworkSettings.Networks}}", r.Gateway)
	if err != nil {
		return err
	}
	var networks map[string]any
	if err = json.Unmarshal([]byte(out), &networks); err != nil {
		return err
	}
	if _, ok := networks[r.network(d)]; !ok {
		if _, err = r.run(ctx, nil, "network", "connect", r.network(d), r.Gateway); err != nil {
			return err
		}
	}
	return nil
}
