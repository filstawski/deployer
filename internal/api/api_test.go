package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"deployer/internal/config"
	"deployer/internal/store/sqlite"
)

func fixture(t *testing.T) (*sqlite.Store, http.Handler, string) {
	t.Helper()
	s, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	_, secret, err := s.IssueToken(context.Background(), sqlite.TokenInput{Name: "operator", Scopes: []string{"admin"}, Projects: []string{"*"}, TTLSeconds: 3600}, "local", true)
	if err != nil {
		t.Fatal(err)
	}
	a := &Server{Store: s, Config: config.Defaults(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return s, a.Handler(), secret
}
func request(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Idempotency-Key", "request-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAuthenticationAndProjectIsolation(t *testing.T) {
	s, h, admin := fixture(t)
	ctx := context.Background()
	if w := request(h, "GET", "/v1/projects", "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated status %d", w.Code)
	}
	if w := request(h, "GET", "/healthz", "", ""); w.Code != 200 {
		t.Fatalf("liveness %d", w.Code)
	}
	p1, err := s.CreateProject(ctx, "one", "https://example.com/one.git", "actor", "")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.CreateProject(ctx, "two", "https://example.com/two.git", "actor", "")
	if err != nil {
		t.Fatal(err)
	}
	tok, secret, err := s.IssueToken(ctx, sqlite.TokenInput{Name: "reader", Scopes: []string{"projects:read"}, Projects: []string{p1.ID}, TTLSeconds: 3600}, "local", false)
	if err != nil {
		t.Fatal(err)
	}
	w := request(h, "GET", "/v1/projects", secret, "")
	if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(p2.ID)) || !bytes.Contains(w.Body.Bytes(), []byte(p1.ID)) {
		t.Fatalf("project list leaked or missing: %s", w.Body)
	}
	if w = request(h, "GET", "/v1/projects/"+p2.ID, secret, ""); w.Code != 404 {
		t.Fatalf("other project status %d", w.Code)
	}
	if w = request(h, "GET", "/v1/server", secret, ""); w.Code != 403 {
		t.Fatalf("server scope status %d", w.Code)
	}
	if w = request(h, "POST", "/v1/projects", secret, `{"name":"three","repository":"https://example.com/three.git"}`); w.Code != 403 {
		t.Fatalf("reader mutated project: %d", w.Code)
	}
	if w = request(h, "POST", "/v1/tokens", secret, `{"name":"escalated","scopes":["admin"],"projects":["*"],"ttl_seconds":60}`); w.Code != 403 {
		t.Fatalf("reader escalated privileges %d", w.Code)
	}
	if w = request(h, "DELETE", "/v1/tokens/"+tok.ID, admin, ""); w.Code != 200 {
		t.Fatalf("revoke status %d", w.Code)
	}
	if w = request(h, "GET", "/v1/projects", secret, ""); w.Code != 401 {
		t.Fatalf("revoked token accepted %d", w.Code)
	}
}
func TestInputValidationAndIdempotency(t *testing.T) {
	_, h, secret := fixture(t)
	for _, body := range []string{`{"name":"test","repository":"file:///etc/passwd"}`, `{"name":"test","repository":"https://user:password@example.com/repo.git"}`, `{"name":"test","repository":"https://example.com/repo.git","privileged":true}`, `{} {}`, `null`} {
		if w := request(h, "POST", "/v1/projects", secret, body); w.Code != 400 {
			t.Fatalf("accepted invalid input %q: %d %s", body, w.Code, w.Body)
		}
	}
	body := `{"name":"test","repository":"https://example.com/repo.git"}`
	w1 := request(h, "POST", "/v1/projects", secret, body)
	w2 := request(h, "POST", "/v1/projects", secret, body)
	if w1.Code != 201 || w2.Code != 201 {
		t.Fatalf("creation failed %s %s", w1.Body, w2.Body)
	}
	var a, b struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &a)
	_ = json.Unmarshal(w2.Body.Bytes(), &b)
	if a.Data.ID == "" || a.Data.ID != b.Data.ID {
		t.Fatal("retry created duplicate")
	}
	if w := request(h, "POST", "/v1/projects", secret, `{"name":"other","repository":"https://example.com/other.git"}`); w.Code != 409 {
		t.Fatalf("conflict status %d", w.Code)
	}
}
func TestOperationAuthorizationAndRequiredKey(t *testing.T) {
	s, h, admin := fixture(t)
	ctx := context.Background()
	p, _ := s.CreateProject(ctx, "one", "https://example.com/one.git", "actor", "")
	other, _ := s.CreateProject(ctx, "two", "https://example.com/two.git", "actor", "")
	w := request(h, "POST", "/v1/projects/"+p.ID+"/verify", admin, `{}`)
	if w.Code != 202 {
		t.Fatalf("queue failed %s", w.Body)
	}
	var result struct {
		Data sqlite.Operation `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	_, secret, _ := s.IssueToken(ctx, sqlite.TokenInput{Name: "other", Scopes: []string{"projects:read"}, Projects: []string{other.ID}, TTLSeconds: 60}, "local", false)
	if w := request(h, "GET", "/v1/operations/"+result.Data.ID, secret, ""); w.Code != 404 {
		t.Fatalf("operation leaked %d", w.Code)
	}
	r := httptest.NewRequest("POST", "/v1/projects/"+p.ID+"/verify", bytes.NewBufferString(`{}`))
	r.Header.Set("Authorization", "Bearer "+admin)
	noKey := httptest.NewRecorder()
	h.ServeHTTP(noKey, r)
	if noKey.Code != 400 {
		t.Fatalf("missing key accepted %d", noKey.Code)
	}
}
