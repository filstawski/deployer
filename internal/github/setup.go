package github

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/webui"
)

// Browser routes, served without a bearer token. Only a capability link
// minted by an administrator can start or complete setup.
const (
	SetupPath    = "/v1/github/setup/"
	CallbackPath = "/v1/github/callback"
	setupCookie  = "deployer_github_setup"
	setupTTL     = 10 * time.Minute
)

var (
	codePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	orgPattern  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,38})$`)
)

type setup struct {
	state, origin, name, organization, actor string
	browser                                  string
	expires                                  time.Time
	completing                               bool
}

// SetupRequest starts the GitHub App creation flow.
type SetupRequest struct {
	// Origin is the public HTTPS origin of the API, which GitHub redirects to.
	Origin string
	// AppName pre-fills the App name; GitHub requires names to be unique.
	AppName      string
	Organization string
	// Actor is the ID of the administrator token that started setup.
	Actor string
}

// BeginSetup returns a single-use link that opens the App creation page.
// Starting again replaces an unfinished setup.
func (a *App) BeginSetup(req SetupRequest) (domain.GitHubSetupResponse, error) {
	u, err := url.Parse(req.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || strings.Trim(u.Path, "/") != "" || u.RawQuery != "" {
		return domain.GitHubSetupResponse{}, domain.Errorf(domain.CodeInvalidInput, "public URL must be an HTTPS origin, such as https://api.example.com")
	}
	if req.Organization != "" && !orgPattern.MatchString(req.Organization) {
		return domain.GitHubSetupResponse{}, domain.Errorf(domain.CodeInvalidInput, "invalid GitHub organization %q", req.Organization)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.id != nil {
		return domain.GitHubSetupResponse{}, domain.Errorf(domain.CodeAlreadyExists, "GitHub is already connected (App %s)", a.id.Slug).
			WithHint("to grant access to more repositories, run: deployer github connect")
	}
	if a.pending != nil && a.pending.completing {
		return domain.GitHubSetupResponse{}, domain.Errorf(domain.CodeBusy, "GitHub setup is completing; try again shortly")
	}
	origin := strings.TrimRight(req.Origin, "/")
	name := req.AppName
	if len(name) > 34 {
		name = name[:34]
	}
	p := &setup{state: domain.RandomString(32), origin: origin, name: name, organization: req.Organization, actor: req.Actor, expires: time.Now().Add(setupTTL)}
	a.pending = p
	return domain.GitHubSetupResponse{URL: origin + SetupPath + p.state, ExpiresAt: p.expires}, nil
}

// ServeBrowser handles the setup page and GitHub's callback. adminActive
// reports whether the token that started setup is still an active admin token.
func (a *App) ServeBrowser(w http.ResponseWriter, r *http.Request, adminActive func(ctx context.Context, tokenID string) bool) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", webui.CSP(a.Web))
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	callback := r.URL.Path == CallbackPath
	state := strings.TrimPrefix(r.URL.Path, SetupPath)
	if callback {
		state = r.URL.Query().Get("state")
	}

	a.mu.Lock()
	p := a.pending
	if p == nil || p.completing || a.id != nil || time.Now().After(p.expires) || subtle.ConstantTimeCompare([]byte(state), []byte(p.state)) != 1 {
		a.mu.Unlock()
		page(w, http.StatusBadRequest, webui.IconWarn, "This link has expired", "Start again on the server with: deployer github setup")
		return
	}
	if !adminActive(r.Context(), p.actor) {
		a.pending = nil
		a.mu.Unlock()
		page(w, http.StatusForbidden, webui.IconError, "Setup is no longer authorized", "The administrator token that started setup was revoked or expired.")
		return
	}
	if !callback {
		// The first browser to open the link owns the setup. Others, such as
		// someone the link leaked to, are refused.
		if p.browser == "" {
			p.browser = domain.RandomString(32)
		} else if cookie, err := r.Cookie(setupCookie); err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(p.browser)) != 1 {
			a.mu.Unlock()
			page(w, http.StatusConflict, webui.IconWarn, "This link was already opened", "Setup links work in one browser only. Start again with: deployer github setup")
			return
		}
		browser := p.browser
		a.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: setupCookie, Value: browser, Path: "/v1/github/", MaxAge: int(setupTTL.Seconds()), HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		a.renderCreatePage(w, p)
		return
	}
	cookie, err := r.Cookie(setupCookie)
	code := r.URL.Query().Get("code")
	if err != nil || p.browser == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(p.browser)) != 1 || !codePattern.MatchString(code) {
		a.mu.Unlock()
		page(w, http.StatusBadRequest, webui.IconWarn, "Open the setup link in this browser", "GitHub returned to a different browser than the one that started setup. Start again with: deployer github setup")
		return
	}
	p.completing = true
	a.mu.Unlock()

	err = a.complete(r.Context(), code)
	a.mu.Lock()
	a.pending = nil
	a.mu.Unlock()
	if err != nil {
		page(w, http.StatusBadGateway, webui.IconError, "GitHub setup didn't finish", "GitHub may have created the App anyway. Check github.com/settings/apps, delete any unused App, and run deployer github setup again.")
		return
	}
	link, _ := a.InstallURL()
	http.Redirect(w, r, link, http.StatusSeeOther)
}

func (a *App) complete(ctx context.Context, code string) error {
	var id identity
	if err := a.request(ctx, http.MethodPost, "/app-manifests/"+code+"/conversions", "", nil, &id); err != nil {
		return err
	}
	return a.save(id)
}

var createForm = template.Must(template.New("form").Parse(`<ul>
<li>The App can only <strong>read repository contents</strong>.</li>
<li>You choose which repositories it can access.</li>
<li>Its private key stays on your Deployer server.</li>
</ul>
<form method="post" action="{{.Action}}">
<input type="hidden" name="manifest" value="{{.Manifest}}">
<button type="submit">Create GitHub App</button>
</form>`))

func (a *App) renderCreatePage(w http.ResponseWriter, p *setup) {
	action := a.Web + "/settings/apps/new?state=" + url.QueryEscape(p.state)
	if p.organization != "" {
		action = a.Web + "/organizations/" + p.organization + "/settings/apps/new?state=" + url.QueryEscape(p.state)
	}
	manifest, _ := json.Marshal(map[string]any{
		"name":                p.name,
		"url":                 p.origin,
		"redirect_url":        p.origin + CallbackPath,
		"public":              true, // Lets other accounts install it on their repositories.
		"hook_attributes":     map[string]any{"url": p.origin + "/v1/github/events", "active": false},
		"default_permissions": map[string]string{"contents": "read", "metadata": "read"},
		"default_events":      []string{},
	})
	var body bytes.Buffer
	createForm.Execute(&body, map[string]string{"Action": action, "Manifest": string(manifest)})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	webui.Render(w, webui.Page{
		Title:   "Connect GitHub to Deployer",
		Message: "Create a GitHub App that your Deployer server uses to fetch private repositories.",
		Body:    template.HTML(body.String()),
		Footer:  "This link expires " + p.expires.UTC().Format("15:04 MST") + ".",
	})
}

func page(w http.ResponseWriter, status int, icon webui.Icon, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	webui.Render(w, webui.Page{Title: title, Message: message, Icon: icon})
}
