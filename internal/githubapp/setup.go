package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"deployer/internal/domain"
)

type setup struct {
	state, origin, organization, actor, browser string
	expires                                     time.Time
	busy                                        bool
}
type SetupLink struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

func random() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (a *App) Begin(origin, organization, actor string) (SetupLink, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return SetupLink{}, domain.Fail("INVALID_INPUT", "public URL must be an HTTPS origin without a path")
	}
	if organization != "" && !component.MatchString(organization) {
		return SetupLink{}, domain.Fail("INVALID_INPUT", "invalid GitHub organization")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.key != nil {
		return SetupLink{}, domain.Fail("ALREADY_EXISTS", "GitHub App is already configured; use github connect")
	}
	if a.pending != nil && a.pending.busy {
		return SetupLink{}, domain.Fail("OPERATION_IN_PROGRESS", "GitHub setup is completing")
	}
	p := &setup{state: random(), origin: origin, organization: organization, actor: actor, expires: time.Now().Add(10 * time.Minute)}
	a.pending = p
	return SetupLink{URL: origin + "/v1/github/setup/" + p.state, ExpiresAt: p.expires}, nil
}

var page = template.Must(template.New("setup").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Connect GitHub · Deployer</title><h1>Connect GitHub to Deployer</h1><p>Create an app owned by your GitHub account. It requests read-only repository contents. After creating it, choose which repositories to share with this Deployer server.</p><p>The app can be installed by other repository owners, but its private key stays on your server.</p><form method="post" action="{{.Action}}"><input type="hidden" name="manifest" value="{{.Manifest}}"><button>Create GitHub App</button></form></html>`))

// Browser endpoints carry no Deployer bearer token. Only a short-lived setup
// capability minted by an administrator can open or complete this handshake.
func (a *App) Browser(w http.ResponseWriter, r *http.Request, activeActor func(context.Context, string) bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action https://github.com; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	a.mu.Lock()
	p := a.pending
	state := r.URL.Query().Get("state")
	if strings.HasPrefix(r.URL.Path, "/v1/github/setup/") {
		state = strings.TrimPrefix(r.URL.Path, "/v1/github/setup/")
	}
	if p == nil || p.busy || a.key != nil || time.Now().After(p.expires) || subtle.ConstantTimeCompare([]byte(state), []byte(p.state)) != 1 {
		a.mu.Unlock()
		http.Error(w, "Setup link is invalid or expired. Run deployer github setup again.", 400)
		return
	}
	if !activeActor(r.Context(), p.actor) {
		a.pending = nil
		a.mu.Unlock()
		http.Error(w, "Administrator access expired or was revoked.", 403)
		return
	}
	if r.URL.Path != "/v1/github/callback" {
		if p.browser == "" {
			p.browser = random()
		}
		http.SetCookie(w, &http.Cookie{Name: "deployer_github_setup", Value: p.browser, Path: "/v1/github/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 600})
		action := "https://github.com/settings/apps/new?state=" + p.state
		if p.organization != "" {
			action = "https://github.com/organizations/" + p.organization + "/settings/apps/new?state=" + p.state
		}
		manifest, _ := json.Marshal(map[string]any{"name": "Deployer", "url": p.origin, "redirect_url": p.origin + "/v1/github/callback", "public": true, "hook_attributes": map[string]any{"url": p.origin + "/v1/github/events", "active": false}, "default_permissions": map[string]string{"contents": "read"}, "default_events": []string{}})
		a.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(w, map[string]string{"Action": action, "Manifest": string(manifest)})
		return
	}
	cookie, err := r.Cookie("deployer_github_setup")
	code := r.URL.Query().Get("code")
	if err != nil || p.browser == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(p.browser)) != 1 || !component.MatchString(code) {
		a.mu.Unlock()
		http.Error(w, "Invalid GitHub callback. Open the setup link in this browser first.", 400)
		return
	}
	p.busy = true
	a.mu.Unlock()
	err = a.finish(r.Context(), code)
	a.mu.Lock()
	a.pending = nil
	a.mu.Unlock()
	if err != nil {
		http.Error(w, "Could not complete GitHub App setup. Run setup again. Check GitHub App settings before creating another app.", 502)
		return
	}
	link, _ := a.InstallURL()
	http.Redirect(w, r, link, http.StatusSeeOther)
}
func (a *App) finish(ctx context.Context, code string) error {
	var v identity
	if err := a.request(ctx, "POST", "/app-manifests/"+code+"/conversions", "", nil, &v); err != nil {
		return err
	}
	key, err := parseIdentity(v)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(a.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(a.path), ".github-app-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	b, _ := json.Marshal(v)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// A hard link publishes the fully written secret atomically without replacing
	// an existing identity, even if another administrator created one meanwhile.
	if err = os.Link(f.Name(), a.path); err != nil {
		return fmt.Errorf("cannot save GitHub App identity")
	}
	a.mu.Lock()
	a.identity = v
	a.key = key
	a.mu.Unlock()
	return nil
}
