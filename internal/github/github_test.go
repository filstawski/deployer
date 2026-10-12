package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

// fakeGitHub serves the API endpoints the App uses.
func fakeGitHub(t *testing.T, key string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app-manifests/{code}/conversions", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("code") != "good-code" {
			w.WriteHeader(422)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 42, "slug": "deployer-test", "pem": key})
	})
	authorized := func(r *http.Request) bool { return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") }
	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) || r.PathValue("repo") != "private" {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 7})
	})
	mux.HandleFunc("POST /app/installations/7/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Repositories []string          `json:"repositories"`
			Permissions  map[string]string `json:"permissions"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if !authorized(r) || len(body.Repositories) != 1 || body.Repositories[0] != "private" || body.Permissions["contents"] != "read" {
			w.WriteHeader(400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"token": "ghs_scoped"})
	})
	mux.HandleFunc("GET /app/installations", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"id": 7, "account": map[string]string{"login": "acme"}, "repository_selection": "selected"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSetupFlowAndCredentials(t *testing.T) {
	key := testKey(t)
	gh := fakeGitHub(t, key)
	path := filepath.Join(t.TempDir(), "github-app.json")
	app, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	app.API, app.Web = gh.URL, gh.URL

	// Unconfigured: anonymous access for public repositories.
	if user, pass, err := app.Credentials(context.Background(), "https://github.com/acme/private.git"); err != nil || user != "" || pass != "" {
		t.Fatalf("unconfigured credentials: %q %q %v", user, pass, err)
	}

	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app.ServeBrowser(w, r, func(context.Context, string) bool { return true })
	}))
	defer api.Close()
	link, err := app.BeginSetup(SetupRequest{Origin: api.URL, AppName: "Deployer for a-very-long-domain.example.com", Actor: "tok_admin"})
	if err != nil {
		t.Fatal(err)
	}
	noRedirects := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Transport: api.Client().Transport, Jar: jar, CheckRedirect: noRedirects}

	resp, err := browser.Get(link.URL)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(page), "Create GitHub App") || !strings.Contains(string(page), "contents") {
		t.Fatalf("setup page: %d %s", resp.StatusCode, page)
	}
	state := strings.TrimPrefix(link.URL, api.URL+SetupPath)

	// The link works in one browser only: reloading is fine, another browser is refused.
	other := &http.Client{Transport: api.Client().Transport, CheckRedirect: noRedirects}
	if resp, _ := browser.Get(link.URL); resp.StatusCode != 200 {
		t.Fatalf("reload in the same browser: %d", resp.StatusCode)
	}
	if resp, _ := other.Get(link.URL); resp.StatusCode != http.StatusConflict {
		t.Fatalf("second browser opened the setup link: %d", resp.StatusCode)
	}
	// A callback from a browser without the setup cookie is refused.
	resp, _ = other.Get(api.URL + CallbackPath + "?state=" + state + "&code=good-code")
	if resp.StatusCode != 400 || app.Configured() {
		t.Fatalf("cookie-less callback: %d configured=%v", resp.StatusCode, app.Configured())
	}

	resp, err = browser.Get(api.URL + CallbackPath + "?" + url.Values{"state": {state}, "code": {"good-code"}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != gh.URL+"/apps/deployer-test/installations/new" {
		t.Fatalf("callback: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("saved identity: %v %v", info, err)
	}

	// The link is single-use.
	if resp, _ := browser.Get(link.URL); resp.StatusCode != 400 {
		t.Fatalf("reused setup link: %d", resp.StatusCode)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.API, reopened.Web = gh.URL, gh.URL
	user, pass, err := reopened.Credentials(context.Background(), "https://github.com/acme/private.git")
	if err != nil || user != "x-access-token" || pass != "ghs_scoped" {
		t.Fatalf("installation token: %q %q %v", user, pass, err)
	}
	// Repositories without an installation fall back to anonymous access.
	if _, pass, err := reopened.Credentials(context.Background(), "https://github.com/acme/public.git"); err != nil || pass != "" {
		t.Fatalf("public repository: %q %v", pass, err)
	}
	status, err := reopened.Status(context.Background())
	if err != nil || !status.Configured || len(status.Installations) != 1 || status.Installations[0].Account != "acme" {
		t.Fatalf("status: %+v %v", status, err)
	}
	if _, err := reopened.BeginSetup(SetupRequest{Origin: api.URL, Actor: "tok_admin"}); err == nil {
		t.Fatal("setup allowed twice")
	}
}

func TestBeginSetupValidation(t *testing.T) {
	app, _ := Open(filepath.Join(t.TempDir(), "x.json"))
	for _, origin := range []string{"http://api.example.com", "https://api.example.com/path", "nope"} {
		if _, err := app.BeginSetup(SetupRequest{Origin: origin}); err == nil {
			t.Errorf("origin %q accepted", origin)
		}
	}
	if _, err := app.BeginSetup(SetupRequest{Origin: "https://api.example.com", Organization: "bad org"}); err == nil {
		t.Fatal("invalid organization accepted")
	}
}
