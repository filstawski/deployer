package githubapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func testIdentity(t *testing.T) identity {
	t.Helper()
	k, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	return identity{ID: 42, Slug: "deployer-test", PEM: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))}
}
func configured(t *testing.T) *App {
	t.Helper()
	a, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a.identity = testIdentity(t)
	a.key, e = parseIdentity(a.identity)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func TestRepositoryScopedTokensAndJWT(t *testing.T) {
	a := configured(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		jwt := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		parts := strings.Split(jwt, ".")
		if len(parts) != 3 {
			t.Error("missing JWT")
			w.WriteHeader(401)
			return
		}
		signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if e := rsa.VerifyPKCS1v15(&a.key.PublicKey, crypto.SHA256, digest[:], signature); e != nil {
			t.Error(e)
		}
		claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var v map[string]int64
		json.Unmarshal(claims, &v)
		if v["iss"] != 42 || v["exp"] <= time.Now().Unix() || v["exp"] > time.Now().Add(10*time.Minute).Unix() {
			t.Error("invalid JWT claims")
		}
		switch r.URL.Path {
		case "/repos/owner/private/installation":
			io.WriteString(w, `{"id":123}`)
		case "/app/installations/123/access_tokens":
			var input struct {
				Repositories []string          `json:"repositories"`
				Permissions  map[string]string `json:"permissions"`
			}
			if e := json.NewDecoder(r.Body).Decode(&input); e != nil {
				t.Error(e)
			}
			if len(input.Repositories) != 1 || input.Repositories[0] != "private" || len(input.Permissions) != 1 || input.Permissions["contents"] != "read" {
				t.Errorf("overbroad token: %+v", input)
			}
			json.NewEncoder(w).Encode(map[string]any{"token": "installation-secret", "expires_at": time.Now().Add(time.Hour)})
		default:
			t.Error("unexpected path", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	a.api = server.URL
	token, e := a.Token(context.Background(), "https://github.com/owner/private.git")
	if e != nil || token != "installation-secret" || calls != 2 {
		t.Fatalf("token %q calls %d error %v", token, calls, e)
	}
	for _, repo := range []string{"https://github.com.evil.test/owner/private.git", "https://user@github.com/owner/private.git", "https://github.com:443/owner/private.git", "https://github.com/owner/../private.git", "https://github.com/owner/private.git?q=x"} {
		token, e = a.Token(context.Background(), repo)
		if e != nil || token != "" || calls != 2 {
			t.Fatal("credential used for untrusted repository", repo)
		}
	}
}
func TestFailuresNeverExposeGitHubBodyOrFollowRedirect(t *testing.T) {
	a := configured(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://example.com/stolen")
		w.WriteHeader(302)
		io.WriteString(w, "secret-upstream-body")
	}))
	defer server.Close()
	a.api = server.URL
	_, e := a.Token(context.Background(), "https://github.com/owner/repo.git")
	if e == nil || strings.Contains(e.Error(), "secret-upstream-body") {
		t.Fatal(e)
	}
}
func TestBrowserSetupPersistsIdentityAndRejectsReplay(t *testing.T) {
	dir := t.TempDir()
	a, _ := Open(dir)
	v := testIdentity(t)
	conversions := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conversions++
		if r.URL.Path != "/app-manifests/code123/conversions" {
			t.Error(r.URL.Path)
		}
		json.NewEncoder(w).Encode(v)
	}))
	defer server.Close()
	a.api = server.URL
	link, e := a.Begin("https://api.example.com", "", "admin")
	if e != nil {
		t.Fatal(e)
	}
	active := func(_ context.Context, id string) bool { return id == "admin" }
	page := httptest.NewRecorder()
	a.Browser(page, httptest.NewRequest("GET", link.URL, nil), active)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "contents") || len(page.Result().Cookies()) != 1 {
		t.Fatal(page.Body.String())
	}
	state := strings.TrimPrefix(link.URL, "https://api.example.com/v1/github/setup/")
	callback := "https://api.example.com/v1/github/callback?code=code123&state=" + url.QueryEscape(state)
	fail := httptest.NewRecorder()
	a.Browser(fail, httptest.NewRequest("GET", callback, nil), active)
	if fail.Code != 400 || conversions != 0 {
		t.Fatal("callback without browser binding accepted")
	}
	request := httptest.NewRequest("GET", callback, nil)
	request.AddCookie(page.Result().Cookies()[0])
	success := httptest.NewRecorder()
	a.Browser(success, request, active)
	if success.Code != 303 || conversions != 1 || success.Header().Get("Location") != "https://github.com/apps/deployer-test/installations/new" {
		t.Fatalf("setup failed %d %s", success.Code, success.Body.String())
	}
	st, e := os.Stat(a.path)
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("unsafe secret file", e)
	}
	loaded, e := Open(dir)
	if e != nil || !loaded.Configured() {
		t.Fatal("identity did not survive restart", e)
	}
	replay := httptest.NewRecorder()
	a.Browser(replay, request, active)
	if replay.Code != 400 || conversions != 1 {
		t.Fatal("callback replay accepted")
	}
	if _, e = a.Begin("https://api.example.com", "", "admin"); e == nil {
		t.Fatal("identity overwrite allowed")
	}
}
func TestExpiredAndRevokedSetup(t *testing.T) {
	for _, expired := range []bool{false, true} {
		a, _ := Open(t.TempDir())
		link, _ := a.Begin("https://api.example.com", "", "admin")
		if expired {
			a.pending.expires = time.Now().Add(-time.Second)
		}
		w := httptest.NewRecorder()
		a.Browser(w, httptest.NewRequest("GET", link.URL, nil), func(context.Context, string) bool { return false })
		if w.Code != 400 && w.Code != 403 {
			t.Fatal("invalid setup accepted")
		}
	}
}
