package daemon

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/filstawski/deployer/internal/cli"
	"github.com/filstawski/deployer/internal/config"
	"github.com/filstawski/deployer/internal/container/docker"
	"github.com/filstawski/deployer/internal/controller"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/github"
	"github.com/filstawski/deployer/internal/secretfile"
	"github.com/filstawski/deployer/internal/store"
)

// fixtureSource serves the hello example as a prebuilt binary, so builds
// don't depend on pulling a Go toolchain image. Each ref gets its own commit.
// The ref "broken" starts a program that doesn't exist.
type fixtureSource struct{ binary []byte }

func (s fixtureSource) Fetch(_ context.Context, _, ref, pinned, dest string) (string, error) {
	os.RemoveAll(dest)
	os.MkdirAll(dest, 0o700)
	files := map[string]string{
		"Dockerfile":    "FROM scratch\nCOPY hello /hello\nUSER 65532:65532\nENTRYPOINT [\"/hello\"]\n",
		"deployer.yaml": "version: 1\nsource:\n  dockerfile: Dockerfile\nhttp:\n  port: 8080\n  healthcheck:\n    timeout: 20s\n",
	}
	if ref == "broken" {
		files["Dockerfile"] = "FROM scratch\nCOPY hello /hello\nENTRYPOINT [\"/missing\"]\n"
	}
	for name, body := range files {
		os.WriteFile(filepath.Join(dest, name), []byte(body), 0o644)
	}
	os.WriteFile(filepath.Join(dest, "hello"), s.binary, 0o755)
	if pinned != "" {
		return pinned, nil
	}
	sum := sha1.Sum([]byte(ref))
	return hex.EncodeToString(sum[:]), nil
}

func (fixtureSource) Verify(context.Context, string) (string, error) {
	return strings.Repeat("a", 40), nil
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func run(t *testing.T, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestEndToEnd runs the real controller, gateway, and Docker. Enable it with
// DEPLOYER_DOCKER_TEST=1 (make e2e).
func TestEndToEnd(t *testing.T) {
	if os.Getenv("DEPLOYER_DOCKER_TEST") != "1" {
		t.Skip("set DEPLOYER_DOCKER_TEST=1 to run against Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	// Run with the systemd service's umask, so files Deployer creates get the
	// same permissions as on a real server.
	defer syscall.Umask(syscall.Umask(0o077))
	root, _ := filepath.Abs("../..")
	arch := run(t, nil, "docker", "version", "--format", "{{.Server.Arch}}")

	// Build the gateway image and the app binary for the Docker host.
	build := t.TempDir()
	goEnv := []string{"GOOS=linux", "GOARCH=" + arch, "CGO_ENABLED=0"}
	run(t, goEnv, "go", "build", "-C", root, "-o", filepath.Join(build, "deployerd"), "./cmd/deployerd")
	run(t, goEnv, "go", "build", "-C", root, "-o", filepath.Join(build, "hello"), "./examples/hello")
	gatewayBinary, err := os.ReadFile(filepath.Join(build, "deployerd"))
	if err != nil {
		t.Fatal(err)
	}
	gatewayImage := "deployer-gateway:e2e-" + domain.RandomString(6)
	if err := docker.BuildGatewayImage(ctx, gatewayBinary, build, gatewayImage); err != nil {
		t.Fatal(err)
	}
	defer exec.Command("docker", "image", "rm", "--force", gatewayImage).Run()
	hello, err := os.ReadFile(filepath.Join(build, "hello"))
	if err != nil {
		t.Fatal(err)
	}
	newSource = func(config.Server, *github.App) controller.Source { return fixtureSource{binary: hello} }

	cfg := config.Defaults()
	cfg.Domain = "e2e.localhost"
	cfg.StateDir = t.TempDir()
	cfg.API.Listen = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	cfg.Gateway.Listen = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	cfg.Gateway.ControlListen = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	cfg.Gateway.Image = gatewayImage
	cfg.Previews.Scheme = "http"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	current := start(ctx, cfg, log)
	defer func() {
		current.stop()
		if current.err != nil {
			t.Errorf("Serve: %v", current.err)
		}
		st, err := store.Open(cfg.DatabasePath())
		if err == nil {
			id, _ := st.InstallationID(context.Background())
			st.Close()
			if err := docker.New(docker.Options{InstallationID: id, StateDir: cfg.StateDir}).RemoveAll(context.Background()); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	}()

	token := waitForToken(t, ctx, cfg, current)
	try := func(args ...string) (int, string) {
		var out, errOut bytes.Buffer
		env := cli.Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut, ConfigDir: t.TempDir(), WorkDir: t.TempDir(), Now: time.Now,
			Getenv: func(k string) string {
				return map[string]string{"DEPLOYER_URL": "http://" + cfg.API.Listen, "DEPLOYER_TOKEN": token}[k]
			}}
		code := cli.Run(ctx, env, append(args, "--json"))
		return code, out.String() + errOut.String()
	}
	deployer := func(args ...string) string {
		t.Helper()
		code, out := try(args...)
		if code != 0 {
			t.Fatalf("deployer %v: exit %d\n%s", args, code, out)
		}
		return out
	}
	visit := func(host, path string) (int, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+cfg.Gateway.Listen+path, nil)
		req.Host = host
		req.Header.Set("Accept", "text/html")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	deployer("project", "add", "hello", "--repo", "https://github.com/example/hello")

	// While building, the reserved hostname shows a progress page.
	var queued domain.CreateDeploymentResponse
	json.Unmarshal([]byte(deployer("deploy", "hello", "--ref", "design/a", "--name", "alpha", "--detach")), &queued)
	if code, body := visit("alpha.e2e.localhost", "/"); code != 503 || !strings.Contains(body, "Preparing your preview") && !strings.Contains(body, "starting") {
		t.Fatalf("progress page: %d %s", code, body)
	}
	deployer("operation", "wait", queued.Operation.ID)
	var beta domain.CreateDeploymentResponse
	json.Unmarshal([]byte(deployer("deploy", "hello", "--ref", "design/b", "--name", "beta")), &beta)

	var alpha domain.Deployment
	json.Unmarshal([]byte(deployer("inspect", "alpha")), &alpha)
	if code, body := visit("alpha.e2e.localhost", "/deep/link?x=1"); code != 200 || !strings.Contains(body, alpha.Commit) || !strings.Contains(body, `"path":"/deep/link"`) {
		t.Fatalf("alpha: %d %s", code, body)
	}
	if code, body := visit("beta.e2e.localhost", "/"); code != 200 || !strings.Contains(body, beta.Deployment.Commit) || beta.Deployment.Commit == alpha.Commit {
		t.Fatalf("beta: %d %s", code, body)
	}
	t.Log("two branches serve their own commits")

	// The app container is isolated and limited.
	var containers []struct {
		HostConfig struct {
			PortBindings map[string]any
			Memory       int64
			NanoCpus     int64
			CapDrop      []string
		}
		NetworkSettings struct{ Networks map[string]any }
	}
	json.Unmarshal([]byte(run(t, nil, "docker", "container", "inspect", "deployer-"+alpha.ID)), &containers)
	c := containers[0]
	if len(c.HostConfig.PortBindings) != 0 || c.HostConfig.Memory != 512<<20 || c.HostConfig.NanoCpus != 1e9 || len(c.NetworkSettings.Networks) != 1 {
		t.Fatalf("container isolation: %+v", c)
	}

	if logs := deployer("logs", "alpha", "--build"); !strings.Contains(logs, "Building design/a") {
		t.Fatalf("build log: %s", logs)
	}
	if logs := deployer("logs", "alpha"); !strings.Contains(logs, "listening on") {
		t.Fatalf("app log: %s", logs)
	}

	deployer("stop", "alpha")
	if code, body := visit("alpha.e2e.localhost", "/"); code != 503 || !strings.Contains(body, "offline") {
		t.Fatalf("stopped: %d %s", code, body)
	}
	if code, _ := visit("beta.e2e.localhost", "/"); code != 200 {
		t.Fatalf("stopping alpha affected beta: %d", code)
	}
	deployer("start", "alpha")
	if code, _ := visit("alpha.e2e.localhost", "/"); code != 200 {
		t.Fatalf("restarted: %d", code)
	}
	deployer("extend", "alpha", "--by", "1h")

	// An update keeps the name and URL, serves the new commit, and removes
	// the previous image.
	var updated domain.CreateDeploymentResponse
	json.Unmarshal([]byte(deployer("update", "alpha", "--ref", "design/c")), &updated)
	if code, body := visit("alpha.e2e.localhost", "/"); code != 200 || updated.Deployment.Commit == alpha.Commit || !strings.Contains(body, updated.Deployment.Commit) {
		t.Fatalf("updated alpha: %d %s", code, body)
	}
	images := run(t, nil, "docker", "image", "ls", "--all", "--quiet", "--filter", "label=io.deployer.deployment="+alpha.ID)
	if n := len(strings.Fields(images)); n != 1 {
		t.Fatalf("alpha has %d images after the update", n)
	}
	t.Log("updated alpha in place")

	// A version that never becomes healthy is replaced by the previous one.
	if code, out := try("update", "alpha", "--ref", "broken"); code != 1 || !strings.Contains(out, "START_FAILED") || !strings.Contains(out, "alpha still runs") {
		t.Fatalf("broken update: exit %d\n%s", code, out)
	}
	if code, body := visit("alpha.e2e.localhost", "/"); code != 200 || !strings.Contains(body, updated.Deployment.Commit) {
		t.Fatalf("after the broken update: %d %s", code, body)
	}
	images = run(t, nil, "docker", "image", "ls", "--all", "--quiet", "--filter", "label=io.deployer.deployment="+alpha.ID)
	if n := len(strings.Fields(images)); n != 1 {
		t.Fatalf("alpha has %d images after the broken update", n)
	}
	t.Log("a failed update kept the previous version")

	deployer("destroy", "beta", "--yes")
	if code, body := visit("beta.e2e.localhost", "/"); code != 404 || !strings.Contains(body, "not found") {
		t.Fatalf("destroyed: %d %s", code, body)
	}
	if out, _ := exec.Command("docker", "container", "inspect", "deployer-"+beta.Deployment.ID).CombinedOutput(); !strings.Contains(string(out), "No such") {
		t.Fatalf("destroyed container remains: %s", out)
	}
	t.Log("verified progress pages, routing, isolation, logs, stop/start, extend, update, and destroy")

	// A controller restart keeps serving existing previews.
	current.stop()
	if current.err != nil {
		t.Fatalf("Serve: %v", current.err)
	}
	current = start(ctx, cfg, log)
	waitForToken(t, ctx, cfg, current)
	if code, _ := visit("alpha.e2e.localhost", "/after-restart"); code != 200 {
		t.Fatalf("after controller restart: %d", code)
	}
	deployer("destroy", "alpha", "--yes")
}

// server is a controller running in the test process.
type server struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func start(ctx context.Context, cfg config.Server, log *slog.Logger) *server {
	ctx, cancel := context.WithCancel(ctx)
	s := &server{cancel: cancel, done: make(chan struct{})}
	go func() { s.err = Serve(ctx, cfg, log); close(s.done) }()
	return s
}

// stop cancels the controller and waits for it to exit. It is safe to call
// more than once.
func (s *server) stop() {
	s.cancel()
	<-s.done
}

func waitForToken(t *testing.T, ctx context.Context, cfg config.Server, s *server) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-s.done:
			t.Fatalf("Serve exited: %v", s.err)
		default:
		}
		if token, err := secretfile.ReadToken(cfg.AdminTokenPath()); err == nil {
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+cfg.API.Listen+"/v1/server", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					return token
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("controller did not start")
	return ""
}
