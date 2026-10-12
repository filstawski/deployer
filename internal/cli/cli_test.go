package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/fakes"
	"github.com/filstawski/deployer/internal/testenv"
)

type result struct {
	code           int
	stdout, stderr string
}

func (r result) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(r.stdout), v); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, r.stdout)
	}
}

// harness runs the CLI in-process against a test server.
type harness struct {
	t      *testing.T
	server *testenv.Env
	vars   map[string]string
	config string
	dir    string
}

func newHarness(t *testing.T) *harness {
	return &harness{t: t, server: testenv.New(t), vars: map[string]string{}, config: t.TempDir(), dir: t.TempDir()}
}

func (h *harness) run(stdin string, args ...string) result {
	h.t.Helper()
	var out, errOut bytes.Buffer
	env := Env{
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errOut,
		Getenv: func(k string) string { return h.vars[k] }, ConfigDir: h.config, WorkDir: h.dir, Now: time.Now,
	}
	code := Run(context.Background(), env, args)
	return result{code, out.String(), errOut.String()}
}

func (h *harness) ok(args ...string) result {
	h.t.Helper()
	r := h.run("", args...)
	if r.code != 0 {
		h.t.Fatalf("deployer %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r
}

func (h *harness) login() {
	h.t.Helper()
	r := h.run(h.server.AdminToken+"\n", "login", h.server.Server.URL, "--name", "test", "--token-stdin")
	if r.code != 0 {
		h.t.Fatalf("login: %d %s %s", r.code, r.stdout, r.stderr)
	}
}

func TestLoginAndContexts(t *testing.T) {
	h := newHarness(t)
	if r := h.run("", "status"); r.code != ExitAuth || !strings.Contains(r.stderr, "deployer login") {
		t.Fatalf("status before login: %d %s", r.code, r.stderr)
	}
	if r := h.run("not-a-token\n", "login", h.server.Server.URL, "--token-stdin"); r.code != ExitUsage {
		t.Fatalf("invalid token: %d", r.code)
	}
	if r := h.run(domain.NewTokenSecret()+"\n", "login", h.server.Server.URL, "--token-stdin"); r.code != ExitAuth {
		t.Fatalf("unknown token: %d %s", r.code, r.stderr)
	}
	h.login()
	if !strings.Contains(h.ok("status").stdout, "admin") {
		t.Fatal("status does not show the token")
	}
	config, _ := os.ReadFile(filepath.Join(h.config, "config.json"))
	if strings.Contains(string(config), "dpl_") {
		t.Fatal("config.json contains a token")
	}
	info, err := os.Stat(filepath.Join(h.config, "tokens", "test.token"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v %v", info, err)
	}
	var contexts struct {
		Current string `json:"current"`
	}
	h.ok("context", "list", "--json").json(t, &contexts)
	if contexts.Current != "test" {
		t.Fatalf("current context: %q", contexts.Current)
	}
	h.ok("logout")
	if r := h.run("", "status"); r.code != ExitAuth {
		t.Fatalf("status after logout: %d", r.code)
	}
}

func TestEnvironmentConnection(t *testing.T) {
	h := newHarness(t)
	h.vars["DEPLOYER_URL"] = h.server.Server.URL
	if r := h.run("", "status"); r.code != ExitUsage {
		t.Fatalf("URL without token: %d %s", r.code, r.stderr)
	}
	h.vars["DEPLOYER_TOKEN"] = h.server.AdminToken
	var status domain.ServerStatus
	h.ok("status", "--json").json(t, &status)
	if status.Domain != "previews.test" {
		t.Fatalf("status: %+v", status)
	}
}

func TestDeployLifecycle(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.ok("project", "add", "shop", "--repo", "git@github.com:acme/shop.git")

	var created domain.CreateDeploymentResponse
	r := h.ok("deploy", "shop", "--ref", "design/a", "--name", "design-a", "--ttl", "2h", "--json")
	r.json(t, &created)
	if created.Deployment.State != domain.StateRunning || created.Operation.Status != domain.OpSucceeded || created.Deployment.TTLSeconds != 7200 {
		t.Fatalf("deploy result: %+v", created)
	}
	if !strings.Contains(r.stderr, "Deploying design/a as design-a") {
		t.Fatalf("no progress on stderr: %q", r.stderr)
	}

	human := h.ok("list").stdout
	if !strings.Contains(human, "design-a") || !strings.Contains(human, "running") || !strings.Contains(human, "https://design-a.previews.test") {
		t.Fatalf("list:\n%s", human)
	}
	var list []domain.Deployment
	h.ok("list", "--json").json(t, &list)
	if len(list) != 1 {
		t.Fatalf("list --json: %d", len(list))
	}
	if !strings.Contains(h.ok("inspect", "design-a").stdout, "design/a @ ") {
		t.Fatal("inspect does not show the commit")
	}
	if !strings.Contains(h.ok("logs", "design-a", "--build").stdout, "step 2/2") {
		t.Fatal("build logs")
	}
	if !strings.Contains(h.ok("extend", "design-a", "--by", "1h").stdout, "now expires") {
		t.Fatal("extend")
	}
	h.ok("stop", "design-a")
	if r := h.run("", "stop", "design-a"); r.code != ExitError || !strings.Contains(r.stderr, "not running") {
		t.Fatalf("stop twice: %d %s", r.code, r.stderr)
	}
	h.ok("start", "design-a")
	if r := h.run("", "destroy", "design-a"); r.code != ExitUsage || !strings.Contains(r.stderr, "--yes") {
		t.Fatalf("destroy without confirmation: %d %s", r.code, r.stderr)
	}
	h.ok("destroy", "design-a", "--yes")

	r = h.run("", "inspect", "missing", "--json")
	var failure struct {
		Error domain.Error `json:"error"`
	}
	r.json(t, &failure)
	if r.code != ExitNotFound || failure.Error.Code != domain.CodeNotFound {
		t.Fatalf("not found: %d %+v", r.code, failure)
	}
}

func TestDeployFailureShowsHint(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.ok("project", "add", "shop", "--repo", "https://github.com/acme/shop")
	h.server.Runtime.Set(func(r *fakes.Runtime) { r.BuildErr = domain.Errorf(domain.CodeBuildFailed, "the image build failed") })
	r := h.run("", "deploy", "shop", "--ref", "main", "--name", "broken")
	if r.code != ExitError || !strings.Contains(r.stderr, "Error: the image build failed") || !strings.Contains(r.stderr, "Hint:  see the build output: deployer logs broken --build") {
		t.Fatalf("failed deploy: %d %s", r.code, r.stderr)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestDeployFromRepository(t *testing.T) {
	h := newHarness(t)
	h.login()
	git(t, h.dir, "init", "--quiet", "-b", "feature/login")
	git(t, h.dir, "remote", "add", "origin", "git@github.com:Acme/Shop.git")
	os.WriteFile(filepath.Join(h.dir, "README"), []byte("x"), 0o644)
	git(t, h.dir, "add", ".")
	git(t, h.dir, "commit", "--quiet", "-m", "init")
	os.WriteFile(filepath.Join(h.dir, "dirty"), []byte("x"), 0o644)

	var created domain.CreateDeploymentResponse
	r := h.ok("deploy", "--json")
	r.json(t, &created)
	if created.Deployment.Project != "shop" || created.Deployment.Ref != "feature/login" || !strings.HasPrefix(created.Deployment.Name, "shop-feature-login-") {
		t.Fatalf("auto deploy: %+v", created.Deployment)
	}
	for _, want := range []string{"Registered project shop", "no upstream", "uncommitted"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.stderr)
		}
	}
	// The second deploy reuses the project.
	h.ok("deploy", "--detach")
	var projects []domain.Project
	h.ok("project", "list", "--json").json(t, &projects)
	if len(projects) != 1 {
		t.Fatalf("projects: %+v", projects)
	}
}

func TestTokens(t *testing.T) {
	h := newHarness(t)
	h.login()
	r := h.ok("token", "create", "laptop")
	if !strings.Contains(r.stdout, "dpl_") || !strings.Contains(r.stdout, "shown once") {
		t.Fatalf("token create:\n%s", r.stdout)
	}
	out := filepath.Join(t.TempDir(), "ci.token")
	r = h.ok("token", "create", "ci", "--role", "viewer", "--ttl", "never", "--output", out)
	if strings.Contains(r.stdout, "dpl_") {
		t.Fatal("secret printed despite --output")
	}
	if info, err := os.Stat(out); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("output file: %v %v", info, err)
	}
	var tokens []domain.Token
	h.ok("token", "list", "--json").json(t, &tokens)
	if len(tokens) != 3 {
		t.Fatalf("tokens: %d", len(tokens))
	}
	if r := h.run("", "token", "create", "x", "--role", "superuser"); r.code != ExitUsage {
		t.Fatalf("unknown role: %d", r.code)
	}
	h.ok("token", "revoke", tokens[1].ID)
}

func TestInitAndValidate(t *testing.T) {
	h := newHarness(t)
	if r := h.run("", "init"); r.code != ExitUsage || !strings.Contains(r.stderr, "Dockerfile") {
		t.Fatalf("init without Dockerfile: %d %s", r.code, r.stderr)
	}
	os.WriteFile(filepath.Join(h.dir, "Dockerfile"), []byte("FROM node:22\nEXPOSE 3000\nCMD [\"node\", \"server.js\"]\n"), 0o644)
	if !strings.Contains(h.ok("init").stdout, "port 3000") {
		t.Fatal("init did not detect the port")
	}
	if r := h.run("", "init"); r.code != ExitUsage {
		t.Fatal("init overwrote deployer.yaml")
	}
	var summary map[string]any
	h.ok("validate", "--json").json(t, &summary)
	if summary["port"] != float64(3000) {
		t.Fatalf("validate: %v", summary)
	}
	os.WriteFile(filepath.Join(h.dir, "deployer.yaml"), []byte("version: 2\n"), 0o644)
	if r := h.run("", "validate"); r.code != ExitUsage || !strings.Contains(r.stderr, "version") {
		t.Fatalf("invalid recipe: %d %s", r.code, r.stderr)
	}
}

func TestUsageErrors(t *testing.T) {
	h := newHarness(t)
	if r := h.run("", "deploy", "--bogus"); r.code != ExitUsage || !strings.Contains(r.stderr, "--help") {
		t.Fatalf("unknown flag: %d %s", r.code, r.stderr)
	}
	if r := h.run("", "inspect"); r.code != ExitUsage {
		t.Fatalf("missing argument: %d", r.code)
	}
	if r := h.ok("--help"); !strings.Contains(r.stdout, "Deployments:") || !strings.Contains(r.stdout, "deploy") {
		t.Fatalf("help:\n%s", r.stdout)
	}
	for _, args := range [][]string{{"bogus"}, {"project", "rmove", "x"}, {"token", "revok", "tok_x"}, {"operation", "wiat"}} {
		if r := h.run("", args...); r.code != ExitUsage || !strings.Contains(r.stderr, "unknown command") {
			t.Errorf("deployer %v: exit %d %s", args, r.code, r.stderr)
		}
	}
	if r := h.ok("project"); !strings.Contains(r.stdout, "Available Commands") {
		t.Fatalf("bare group should show help:\n%s", r.stdout)
	}
}
