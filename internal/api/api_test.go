package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/testenv"
)

type response struct {
	status int
	header http.Header
	body   domain.Envelope[json.RawMessage]
}

func call(t *testing.T, env *testenv.Env, token, method, path string, body any, headers ...string) response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, env.Server.URL+path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out response
	out.status, out.header = resp.StatusCode, resp.Header
	if err := json.NewDecoder(resp.Body).Decode(&out.body); err != nil {
		t.Fatalf("%s %s: invalid JSON: %v", method, path, err)
	}
	return out
}

func (r response) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body.Data, v); err != nil {
		t.Fatal(err)
	}
}

func (r response) code() domain.Code {
	if r.body.Error == nil {
		return ""
	}
	return r.body.Error.Code
}

func TestAuthentication(t *testing.T) {
	env := testenv.New(t)
	if r := call(t, env, "", "GET", "/healthz", nil); r.status != 200 {
		t.Fatalf("healthz: %d", r.status)
	}
	r := call(t, env, "", "GET", "/v1/server", nil)
	if r.status != 401 || r.code() != domain.CodeUnauthenticated || r.header.Get("WWW-Authenticate") == "" || r.body.RequestID == "" {
		t.Fatalf("anonymous: %d %+v", r.status, r.body)
	}
	if r := call(t, env, domain.NewTokenSecret(), "GET", "/v1/server", nil); r.status != 401 {
		t.Fatalf("unknown token: %d", r.status)
	}
	var status domain.ServerStatus
	r = call(t, env, env.AdminToken, "GET", "/v1/server", nil)
	r.decode(t, &status)
	if r.status != 200 || status.Domain != "previews.test" || status.APIVersion != 1 || status.Deployments.Max != 10 {
		t.Fatalf("status: %d %+v", r.status, status)
	}
	if r := call(t, env, env.AdminToken, "GET", "/v1/nope", nil); r.status != 404 || r.code() != domain.CodeNotFound {
		t.Fatalf("unknown route: %d", r.status)
	}
}

func TestScopesAndProjectIsolation(t *testing.T) {
	env := testenv.New(t)
	var shop, blog domain.Project
	call(t, env, env.AdminToken, "POST", "/v1/projects", domain.CreateProjectRequest{Name: "shop", Repository: "https://github.com/acme/shop"}).decode(t, &shop)
	call(t, env, env.AdminToken, "POST", "/v1/projects", domain.CreateProjectRequest{Name: "blog", Repository: "https://github.com/acme/blog"}).decode(t, &blog)

	viewer := env.Token(t, "viewer", domain.Roles["viewer"], shop.ID)
	var visible []domain.Project
	call(t, env, viewer, "GET", "/v1/projects", nil).decode(t, &visible)
	if len(visible) != 1 || visible[0].Name != "shop" {
		t.Fatalf("visible projects: %+v", visible)
	}
	if r := call(t, env, viewer, "GET", "/v1/projects/blog", nil); r.status != 404 {
		t.Fatalf("other project visible: %d", r.status)
	}
	if r := call(t, env, viewer, "POST", "/v1/deployments", domain.CreateDeploymentRequest{Project: "shop", Ref: "main"}); r.status != 403 || r.code() != domain.CodeForbidden {
		t.Fatalf("viewer deployed: %d", r.status)
	}
	if r := call(t, env, viewer, "GET", "/v1/tokens", nil); r.status != 403 {
		t.Fatalf("viewer listed tokens: %d", r.status)
	}

	deployer := env.Token(t, "ci", domain.Roles["deployer"], shop.ID)
	if r := call(t, env, deployer, "POST", "/v1/projects", domain.CreateProjectRequest{Name: "x", Repository: "https://github.com/acme/x"}); r.status != 403 {
		t.Fatalf("project-scoped token created a project: %d", r.status)
	}
	if r := call(t, env, deployer, "POST", "/v1/deployments", domain.CreateDeploymentRequest{Project: "blog", Ref: "main"}); r.status != 404 {
		t.Fatalf("deployed to another project: %d", r.status)
	}
	var created domain.CreateDeploymentResponse
	r := call(t, env, deployer, "POST", "/v1/deployments", domain.CreateDeploymentRequest{Project: "shop", Ref: "main", Name: "shop-main"})
	r.decode(t, &created)
	if r.status != 202 || created.Deployment.URL != "https://shop-main.previews.test" || created.Operation.Kind != domain.OpCreateDeployment {
		t.Fatalf("deploy: %d %+v", r.status, r.body)
	}
	// The project-scoped viewer can follow it; a token for another project cannot.
	if r := call(t, env, viewer, "GET", "/v1/operations/"+created.Operation.ID, nil); r.status != 200 {
		t.Fatalf("viewer reading operation: %d", r.status)
	}
	other := env.Token(t, "other", domain.Roles["deployer"], blog.ID)
	for _, path := range []string{"/v1/operations/" + created.Operation.ID, "/v1/deployments/shop-main", "/v1/deployments/shop-main/logs"} {
		if r := call(t, env, other, "GET", path, nil); r.status != 404 {
			t.Errorf("%s visible to another project's token: %d", path, r.status)
		}
	}
}

func TestDeploymentEndpoints(t *testing.T) {
	env := testenv.New(t)
	admin := env.AdminToken
	call(t, env, admin, "POST", "/v1/projects", domain.CreateProjectRequest{Name: "shop", Repository: "git@github.com:acme/shop.git"})

	r := call(t, env, admin, "POST", "/v1/deployments", domain.CreateDeploymentRequest{Project: "shop", Ref: "design/a", Name: "design-a", TTLSeconds: 7200}, "Idempotency-Key", "k1")
	var created domain.CreateDeploymentResponse
	r.decode(t, &created)
	again := call(t, env, admin, "POST", "/v1/deployments", domain.CreateDeploymentRequest{Project: "shop", Ref: "design/a", Name: "design-a", TTLSeconds: 7200}, "Idempotency-Key", "k1")
	var replay domain.CreateDeploymentResponse
	again.decode(t, &replay)
	if replay.Deployment.ID != created.Deployment.ID {
		t.Fatal("idempotent retry created a second deployment")
	}
	waitOp(t, env, created.Operation.ID)

	var d domain.Deployment
	call(t, env, admin, "GET", "/v1/deployments/design-a", nil).decode(t, &d)
	if d.State != domain.StateRunning || d.Project != "shop" || d.ExpiresAt == nil {
		t.Fatalf("deployment: %+v", d)
	}
	var logs domain.LogsResponse
	call(t, env, admin, "GET", "/v1/deployments/design-a/logs?source=build&offset=0", nil).decode(t, &logs)
	if !strings.Contains(logs.Logs, "step 2/2") || logs.Offset == 0 {
		t.Fatalf("build logs: %+v", logs)
	}
	if r := call(t, env, admin, "GET", "/v1/deployments/design-a/logs?source=nope", nil); r.status != 400 {
		t.Fatalf("invalid log source: %d", r.status)
	}

	before := *d.ExpiresAt
	r = call(t, env, admin, "POST", "/v1/deployments/design-a/extend", domain.ExtendRequest{Seconds: 3600})
	r.decode(t, &d)
	if r.status != 200 || !d.ExpiresAt.Equal(before.Add(time.Hour)) {
		t.Fatalf("extend: %d %+v", r.status, r.body.Error)
	}
	if r := call(t, env, admin, "POST", "/v1/deployments/design-a/extend", map[string]any{"seconds": 1, "bogus": true}); r.status != 400 {
		t.Fatalf("unknown field accepted: %d", r.status)
	}

	var op domain.Operation
	env.Source.Push("design/a")
	r = call(t, env, admin, "POST", "/v1/deployments/design-a/update", domain.UpdateDeploymentRequest{})
	r.decode(t, &op)
	if r.status != 202 || op.Kind != domain.OpUpdateDeployment {
		t.Fatalf("update: %d %+v", r.status, r.body.Error)
	}
	if op = waitOp(t, env, op.ID); op.Status != domain.OpSucceeded || !strings.Contains(string(op.Result), `"changed":true`) {
		t.Fatalf("update result: %+v %s", op.Error, op.Result)
	}
	if r := call(t, env, admin, "POST", "/v1/deployments/design-a/update", map[string]any{"branch": "x"}); r.status != 400 {
		t.Fatalf("unknown field accepted: %d", r.status)
	}

	call(t, env, admin, "POST", "/v1/deployments/design-a/stop", nil).decode(t, &op)
	waitOp(t, env, op.ID)
	if r := call(t, env, admin, "POST", "/v1/deployments/design-a/stop", nil); r.status != 409 || r.code() != domain.CodeInvalidState {
		t.Fatalf("stop twice: %d", r.status)
	}
	call(t, env, admin, "DELETE", "/v1/deployments/design-a", nil).decode(t, &op)
	waitOp(t, env, op.ID)

	var list []domain.Deployment
	call(t, env, admin, "GET", "/v1/deployments", nil).decode(t, &list)
	if len(list) != 0 {
		t.Fatalf("destroyed deployment listed: %d", len(list))
	}
	call(t, env, admin, "GET", "/v1/deployments?all=true&project=shop", nil).decode(t, &list)
	if len(list) != 1 || list[0].State != domain.StateDestroyed {
		t.Fatalf("history: %+v", list)
	}
	if r := call(t, env, admin, "DELETE", "/v1/projects/shop", nil); r.status != 200 {
		t.Fatalf("delete project: %d %+v", r.status, r.body.Error)
	}
}

func TestTokens(t *testing.T) {
	env := testenv.New(t)
	var created domain.CreateTokenResponse
	r := call(t, env, env.AdminToken, "POST", "/v1/tokens", domain.CreateTokenRequest{Name: "laptop", Scopes: domain.Roles["deployer"], Projects: []string{"*"}, TTLSeconds: 3600})
	r.decode(t, &created)
	if r.status != 201 || !domain.ValidTokenSecret(created.Secret) || created.Token.ExpiresAt == nil {
		t.Fatalf("create token: %d %+v", r.status, r.body.Error)
	}
	if r := call(t, env, created.Secret, "GET", "/v1/server", nil); r.status != 200 {
		t.Fatalf("new token rejected: %d", r.status)
	}
	var tokens []domain.Token
	call(t, env, env.AdminToken, "GET", "/v1/tokens", nil).decode(t, &tokens)
	if len(tokens) != 2 {
		t.Fatalf("tokens: %d", len(tokens))
	}
	for _, raw := range []string{string(call(t, env, env.AdminToken, "GET", "/v1/tokens", nil).body.Data)} {
		if strings.Contains(raw, "dpl_") || strings.Contains(raw, "secret") {
			t.Fatal("token list exposes secrets")
		}
	}
	if r := call(t, env, env.AdminToken, "DELETE", "/v1/tokens/"+created.Token.ID, nil); r.status != 200 {
		t.Fatalf("revoke: %d", r.status)
	}
	if r := call(t, env, created.Secret, "GET", "/v1/server", nil); r.status != 401 {
		t.Fatalf("revoked token accepted: %d", r.status)
	}
	if r := call(t, env, env.AdminToken, "POST", "/v1/tokens", domain.CreateTokenRequest{Name: "bad", Scopes: []domain.Scope{"root"}, Projects: []string{"*"}}); r.status != 400 {
		t.Fatalf("invalid scope accepted: %d", r.status)
	}
}

func TestGitHubEndpoints(t *testing.T) {
	env := testenv.New(t)
	var status domain.GitHubStatus
	call(t, env, env.AdminToken, "GET", "/v1/github", nil).decode(t, &status)
	if status.Configured {
		t.Fatal("unconfigured GitHub reported as configured")
	}
	var link domain.GitHubSetupResponse
	r := call(t, env, env.AdminToken, "POST", "/v1/github/setup", nil)
	r.decode(t, &link)
	if r.status != 201 || !strings.HasPrefix(link.URL, "https://api.previews.test/v1/github/setup/") {
		t.Fatalf("setup link: %d %+v", r.status, link)
	}
	deployer := env.Token(t, "ci", domain.Roles["deployer"], "*")
	if r := call(t, env, deployer, "POST", "/v1/github/setup", nil); r.status != 403 {
		t.Fatalf("non-admin started GitHub setup: %d", r.status)
	}
	resp, err := http.Get(env.Server.URL + "/v1/github/setup/" + strings.TrimPrefix(link.URL, "https://api.previews.test/v1/github/setup/"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("browser setup page: %d", resp.StatusCode)
	}
}

func waitOp(t *testing.T, env *testenv.Env, id string) domain.Operation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var op domain.Operation
		call(t, env, env.AdminToken, "GET", "/v1/operations/"+id, nil).decode(t, &op)
		if op.Status.Done() {
			if op.Status != domain.OpSucceeded {
				t.Fatalf("operation %s %s: %+v", id, op.Status, op.Error)
			}
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation %s did not finish", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
