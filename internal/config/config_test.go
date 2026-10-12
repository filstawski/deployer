package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadServer(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "server.yaml", "version: 1\ndomain: previews.example.com\nstate_dir: state\ndeployments:\n  default_ttl: 4h\n  max_ttl: 2d\n")
	c, err := LoadServer(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.StateDir != filepath.Join(dir, "state") || c.Deployments.DefaultTTL.D() != 4*time.Hour || c.Deployments.MaxTTL.D() != 48*time.Hour {
		t.Fatalf("unexpected config: %+v", c)
	}
	if got := c.PreviewURL("design-a"); got != "https://design-a.previews.example.com" {
		t.Fatal(got)
	}
	c.Previews.Scheme = "http"
	if got := c.PreviewURL("a"); got != "http://a.previews.example.com:8080" {
		t.Fatal(got)
	}
	if c.PublicAPIURL() != "https://api.previews.example.com" {
		t.Fatal(c.PublicAPIURL())
	}

	// Round trip through Marshal keeps durations readable and loadable.
	out := write(t, dir, "again.yaml", string(c.Marshal()))
	if b, _ := os.ReadFile(out); !strings.Contains(string(b), "default_ttl: 4h") {
		t.Fatalf("durations not human-readable:\n%s", b)
	}
	if _, err := LoadServer(out); err != nil {
		t.Fatal(err)
	}
}

func TestServerValidation(t *testing.T) {
	cases := map[string]string{
		"unknown field":      "version: 1\ndomain: example.com\nbogus: 1\n",
		"missing domain":     "version: 1\n",
		"uppercase domain":   "version: 1\ndomain: Example.com\n",
		"public api listen":  "version: 1\ndomain: example.com\napi:\n  listen: 0.0.0.0:7420\n",
		"port clash":         "version: 1\ndomain: example.com\ngateway:\n  listen: 127.0.0.1:7420\n",
		"bad ttl":            "version: 1\ndomain: example.com\ndeployments:\n  default_ttl: soon\n",
		"default above max":  "version: 1\ndomain: example.com\ndeployments:\n  default_ttl: 8d\n",
		"http public url":    "version: 1\ndomain: example.com\napi:\n  public_url: http://api.example.com\n",
		"two documents":      "version: 1\ndomain: example.com\n---\nversion: 1\n",
		"unsupported scheme": "version: 1\ndomain: example.com\npreviews:\n  scheme: ftp\n",
	}
	for name, body := range cases {
		if _, err := LoadServer(write(t, t.TempDir(), "s.yaml", body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRecipe(t *testing.T) {
	root := t.TempDir()
	write(t, root, "deployer.yaml", "version: 1\nsource:\n  dockerfile: apps/web/Dockerfile\nhttp:\n  port: 3000\nenvironment:\n  NODE_ENV: production\n")
	write(t, root, "apps/web/Dockerfile", "FROM scratch\n")
	r, err := LoadRecipe(root)
	if err != nil {
		t.Fatal(err)
	}
	if r.HTTP.Healthcheck.Path != "/health" || r.HTTP.Healthcheck.Timeout.D() != 90*time.Second || !strings.HasSuffix(r.Dockerfile, "apps/web/Dockerfile") {
		t.Fatalf("defaults not applied: %+v", r)
	}

	bad := map[string]string{
		"no dockerfile":  "version: 1\nhttp:\n  port: 3000\n",
		"no port":        "version: 1\nsource:\n  dockerfile: Dockerfile\n",
		"unknown field":  "version: 1\nsource:\n  dockerfile: Dockerfile\nhttp:\n  port: 1\nservices: {}\n",
		"reserved env":   "version: 1\nsource:\n  dockerfile: Dockerfile\nhttp:\n  port: 1\nenvironment:\n  PORT: '1'\n",
		"bad health":     "version: 1\nsource:\n  dockerfile: Dockerfile\nhttp:\n  port: 1\n  healthcheck:\n    path: health\n",
		"long timeout":   "version: 1\nsource:\n  dockerfile: Dockerfile\nhttp:\n  port: 1\n  healthcheck:\n    timeout: 1h\n",
		"other service":  "version: 1\nsource:\n  dockerfile: Dockerfile\nhttp:\n  service: api\n  port: 1\n",
		"absolute paths": "version: 1\nsource:\n  dockerfile: /etc/passwd\nhttp:\n  port: 1\n",
	}
	for name, body := range bad {
		if _, err := ParseRecipe([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRecipePathsStayInsideRepository(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, outside, "Dockerfile", "FROM scratch\n")
	if err := os.Symlink(filepath.Join(outside, "Dockerfile"), filepath.Join(root, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "deployer.yaml", "version: 1\nsource:\n  dockerfile: Dockerfile\nhttp:\n  port: 80\n")
	if _, err := LoadRecipe(root); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("symlink escape not rejected: %v", err)
	}
	empty := t.TempDir()
	if _, err := LoadRecipe(empty); err == nil || !strings.Contains(err.Error(), "deployer init") {
		t.Fatalf("missing recipe error is not actionable: %v", err)
	}
}
