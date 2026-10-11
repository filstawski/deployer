package deployments_test

import (
	"bytes"
	"context"
	"deployer/internal/api"
	"deployer/internal/cli"
	"deployer/internal/command"
	"deployer/internal/credentials"
	"deployer/internal/deployments"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deployer/internal/config"
	"deployer/internal/domain"
	"deployer/internal/operations"
	docker "deployer/internal/runtime/docker"
	"deployer/internal/store/sqlite"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
func TestDockerLifecycle(t *testing.T) {
	if os.Getenv("DEPLOYER_DOCKER_TEST") != "1" {
		t.Skip("opt in with make docker-test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	c := config.Defaults()
	c.StateDir = t.TempDir()
	c.Runtime.Enabled = true
	c.Runtime.GatewayPort = freePort(t)
	c.Runtime.ControlPort = freePort(t)
	store, err := sqlite.Open(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id, err := store.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	runtime := docker.New(c, id)
	defer func() {
		clean, done := context.WithTimeout(context.Background(), 90*time.Second)
		defer done()
		all, _ := store.Deployments(clean)
		for _, d := range all {
			if err := runtime.Remove(clean, d); err != nil {
				t.Error("cleanup", err)
			}
		}
		if err := runtime.RemoveInfrastructure(clean); err != nil {
			t.Error("infrastructure cleanup", err)
		}
	}()
	if err = runtime.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../../.cache/docker-fixture/hello")
	if err != nil {
		t.Fatal(err)
	}
	fetchCount := 0
	service := &deployments.Service{Store: store, Config: c, Runtime: runtime, Log: slog.Default(), Fetch: func(ctx context.Context, repo, ref, pinned, dest string) (string, error) {
		fetchCount++
		if err := os.MkdirAll(dest, 0700); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dest, "hello"), fixture, 0755); err != nil {
			return "", err
		}
		for name, body := range map[string]string{"Dockerfile": "FROM scratch\nCOPY hello /hello\nUSER 65532:65532\nENTRYPOINT [\"/hello\"]\n", "deployer.yaml": "version: 1\nsource:\n  dockerfile: Dockerfile\nhttp:\n  port: 8080\n  healthcheck:\n    path: /health\n    timeout: 10s\n"} {
			if err := os.WriteFile(filepath.Join(dest, name), []byte(body), 0600); err != nil {
				return "", err
			}
		}
		if ref == "design/a" {
			return strings.Repeat("a", 40), nil
		}
		return strings.Repeat("b", 40), nil
	}}
	worker := &operations.Worker{Store: store, Handle: service.Handle, Failed: service.Failed, Log: slog.Default()}
	_, secret, err := store.IssueToken(ctx, sqlite.TokenInput{Name: "test", Scopes: []string{"admin"}, Projects: []string{"*"}, TTLSeconds: 3600}, "local", true)
	if err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(c.StateDir, "operator.token")
	file, err := credentials.Create(credential)
	if err != nil {
		t.Fatal(err)
	}
	if err = credentials.Write(file, secret); err != nil {
		t.Fatal(err)
	}
	file.Close()
	server := httptest.NewServer((&api.Server{Store: store, Config: c, Deployments: service, Log: slog.Default()}).Handler())
	defer server.Close()
	call := func(args ...string) json.RawMessage {
		t.Helper()
		var out, diagnostic bytes.Buffer
		code := cli.Run(ctx, append([]string{"--server", server.URL, "--token-file", credential, "--json"}, args...), &out, &diagnostic)
		if code != 0 {
			t.Fatalf("CLI %v: %d %s %s", args, code, &out, &diagnostic)
		}
		var result struct{ Data json.RawMessage }
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Data
	}
	call("project", "add", "hello", "--repo", "https://example.com/hello.git")
	step := func(o sqlite.Operation) domain.Deployment {
		t.Helper()
		worked, err := worker.Step(ctx)
		if err != nil || !worked {
			t.Fatalf("worker: %v %v", worked, err)
		}
		op, err := store.Operation(ctx, o.ID)
		if err != nil || op.Status != "succeeded" {
			t.Fatalf("operation: %+v %v", op, err)
		}
		d, err := store.Deployment(ctx, o.DeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	queue := func(ref, sub string) sqlite.Operation {
		t.Helper()
		var o sqlite.Operation
		if err := json.Unmarshal(call("deploy", "hello", "--ref", ref, "--subdomain", sub, "--ttl", "1m"), &o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	visit := func(d domain.Deployment, path string, want int, fragment string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d%s", c.Runtime.GatewayPort, path), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = d.Hostname
		req.Header.Set("Accept", "text/html")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want || !strings.Contains(string(b), fragment) {
			t.Fatalf("visit %s: HTTP %d %s", d.Hostname, resp.StatusCode, b)
		}
	}
	aop := queue("design/a", "alpha")
	a, _ := store.Deployment(ctx, aop.DeploymentID)
	if err = service.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	visit(a, "/demo/customer", 503, "preparing")
	a = step(aop)
	b := step(queue("design/b", "beta"))
	visit(a, "/demo/customer", 200, strings.Repeat("a", 40))
	visit(b, "/other", 200, strings.Repeat("b", 40))
	t.Log("two branch previews route independently through CLI/API")
	// TLS setup replaces only the owned gateway, then reconnects persisted networks.
	runtime.Config.Preview.Scheme = "https"
	if err = runtime.Ensure(ctx); err != nil {
		t.Fatal("gateway replacement", err)
	}
	for _, d := range []domain.Deployment{a, b} {
		if err = runtime.Attach(ctx, d); err != nil {
			t.Fatal("gateway reconnect", err)
		}
	}
	if err = service.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	visit(a, "/after-gateway-replacement", 200, strings.Repeat("a", 40))
	visit(b, "/after-gateway-replacement", 200, strings.Repeat("b", 40))

	var listed []domain.Deployment
	if err = json.Unmarshal(call("list"), &listed); err != nil || len(listed) != 2 {
		t.Fatalf("CLI list: %v %+v", err, listed)
	}
	for _, d := range []domain.Deployment{a, b} {
		out, err := command.Run(ctx, "", nil, nil, "docker", "container", "inspect", "dpl-app-"+d.ID)
		if err != nil {
			t.Fatal(err)
		}
		var containers []struct {
			HostConfig struct {
				PortBindings map[string]any
				Memory       int64
				NanoCpus     int64
			}
			NetworkSettings struct{ Networks map[string]any }
		}
		if err = json.Unmarshal([]byte(out), &containers); err != nil {
			t.Fatal(err)
		}
		v := containers[0]
		if len(v.HostConfig.PortBindings) != 0 || v.HostConfig.Memory != 512*1024*1024 || v.HostConfig.NanoCpus != 1e9 || len(v.NetworkSettings.Networks) != 1 {
			t.Fatalf("container isolation/limits not applied: %+v", v)
		}
	}
	action := func(d domain.Deployment, kind string, seconds int64) domain.Deployment {
		t.Helper()
		args := []string{kind, d.ID}
		if kind == "extend" {
			args = append(args, "--by", fmt.Sprintf("%ds", seconds))
		}
		if kind == "destroy" {
			args = append(args, "--yes")
		}
		var o sqlite.Operation
		if err := json.Unmarshal(call(args...), &o); err != nil {
			t.Fatal(err)
		}
		return step(o)
	}
	old := *a.ExpiresAt
	a = action(a, "extend", 60)
	if !a.ExpiresAt.Equal(old.Add(time.Minute)) {
		t.Fatal("lease extension did not persist")
	}
	a = action(a, "stop", 0)
	visit(a, "/demo/customer", 503, "offline")
	visit(b, "/", 200, strings.Repeat("b", 40))
	a = action(a, "wake", 0)
	visit(a, "/demo/customer", 200, strings.Repeat("a", 40))
	if fetchCount != 2 {
		t.Fatal("wake fetched source instead of using built image")
	}
	var logResult struct{ Logs string }
	if err = json.Unmarshal(call("logs", a.ID, "--build"), &logResult); err != nil || !strings.Contains(logResult.Logs, "DONE") {
		t.Fatalf("CLI build logs: %v %s", err, logResult.Logs)
	}
	// A fresh controller service reconstructs routing from durable state.
	service = &deployments.Service{Store: store, Config: c, Runtime: runtime, Log: slog.Default()}
	if err = service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	visit(a, "/after-restart", 200, strings.Repeat("a", 40))
	expired := time.Now().Add(-time.Second)
	a.ExpiresAt = &expired
	if err = store.SaveDeployment(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err = service.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	visit(a, "/", 410, "expired")
	if err = service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	running, err := runtime.Running(ctx, a)
	if err != nil || running {
		t.Fatalf("expired container still running: %v %v", running, err)
	}
	b = action(b, "destroy", 0)
	visit(b, "/", 404, "not found")
	running, err = runtime.Running(ctx, b)
	if err != nil || running {
		t.Fatalf("destroyed container remains: %v %v", running, err)
	}
	t.Log("verified readiness page, isolated routes, logs, extend, stop, wake, route recovery, expiry, and destruction")
}
