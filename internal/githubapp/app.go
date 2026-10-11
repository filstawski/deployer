// Package githubapp owns the server's GitHub App identity. Installation tokens
// are minted for one repository and are never persisted or returned by the API.
package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"deployer/internal/domain"
)

type identity struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	PEM  string `json:"pem"`
}
type App struct {
	mu       sync.Mutex
	path     string
	identity identity
	key      *rsa.PrivateKey
	pending  *setup
	http     *http.Client
	api      string
}
type Installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
	RepositorySelection string     `json:"repository_selection"`
	SuspendedAt         *time.Time `json:"suspended_at"`
}

var component = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,99}$`)

func Open(stateDir string) (*App, error) {
	a := &App{path: filepath.Join(stateDir, "github-app.json"), api: "https://api.github.com", http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	st, err := os.Lstat(a.path)
	if os.IsNotExist(err) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("GitHub App credentials must be an owner-only regular file")
	}
	b, err := os.ReadFile(a.path)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, &a.identity); err != nil {
		return nil, fmt.Errorf("invalid GitHub App credentials")
	}
	a.key, err = parseIdentity(a.identity)
	if err != nil {
		return nil, err
	}
	return a, nil
}
func parseIdentity(v identity) (*rsa.PrivateKey, error) {
	if v.ID <= 0 || !component.MatchString(v.Slug) {
		return nil, fmt.Errorf("invalid GitHub App identity")
	}
	block, _ := pem.Decode([]byte(v.PEM))
	if block == nil {
		return nil, fmt.Errorf("invalid GitHub App private key")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, k.Validate()
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err == nil {
		if rsaKey, ok := k.(*rsa.PrivateKey); ok {
			return rsaKey, rsaKey.Validate()
		}
	}
	return nil, fmt.Errorf("GitHub App requires an RSA private key")
}
func (a *App) Configured() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.key != nil }
func (a *App) InstallURL() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.key == nil {
		return "", domain.Fail("GITHUB_NOT_CONFIGURED", "an administrator must run deployer github setup first")
	}
	return "https://github.com/apps/" + a.identity.Slug + "/installations/new", nil
}
func (a *App) jwt() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.key == nil {
		return "", domain.Fail("GITHUB_NOT_CONFIGURED", "GitHub App is not configured")
	}
	now := time.Now()
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(5 * time.Minute).Unix(), "iss": a.identity.ID})
	raw := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(raw))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return raw + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// request deliberately discards GitHub error bodies: they can contain secrets.
func (a *App) request(ctx context.Context, method, path, token string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.api+path, body)
	if err != nil {
		return fmt.Errorf("invalid GitHub request")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return domain.Fail("GITHUB_UNAVAILABLE", "cannot reach GitHub; retry later")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return domain.Fail("GITHUB_NOT_INSTALLED", "GitHub App has no access to this repository; install it on the repository owner's account")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return domain.Fail("GITHUB_REQUEST_FAILED", fmt.Sprintf("GitHub returned HTTP %d; check App access and rate limits", resp.StatusCode))
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output); err != nil {
		return domain.Fail("GITHUB_REQUEST_FAILED", "GitHub returned an invalid response")
	}
	return nil
}
func (a *App) Installations(ctx context.Context) ([]Installation, error) {
	jwt, err := a.jwt()
	if err != nil {
		return nil, err
	}
	all := []Installation{}
	for page := 1; page <= 100; page++ {
		var batch []Installation
		if err = a.request(ctx, "GET", fmt.Sprintf("/app/installations?per_page=100&page=%d", page), jwt, nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, nil
		}
	}
	return nil, domain.Fail("GITHUB_REQUEST_FAILED", "too many installations to list")
}
func Repository(raw string) (string, string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return "", "", false
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git"), "/")
	if len(parts) != 2 || !component.MatchString(parts[0]) || !component.MatchString(parts[1]) || parts[1] == "." || parts[1] == ".." {
		return "", "", false
	}
	return parts[0], parts[1], true
}
func (a *App) Token(ctx context.Context, repository string) (string, error) {
	owner, repo, ok := Repository(repository)
	if !ok || !a.Configured() {
		return "", nil
	}
	jwt, err := a.jwt()
	if err != nil {
		return "", err
	}
	var installation Installation
	err = a.request(ctx, "GET", "/repos/"+owner+"/"+repo+"/installation", jwt, nil, &installation)
	// Public repositories remain usable without an installation. Git itself will
	// fail closed for a private repository when this unauthenticated fetch runs.
	if e, ok := err.(*domain.Error); ok && e.Code == "GITHUB_NOT_INSTALLED" {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if installation.ID <= 0 || installation.SuspendedAt != nil {
		return "", domain.Fail("GITHUB_REQUEST_FAILED", "GitHub App installation is unavailable or suspended")
	}
	var result struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	err = a.request(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", installation.ID), jwt, map[string]any{"repositories": []string{repo}, "permissions": map[string]string{"contents": "read"}}, &result)
	if err != nil {
		return "", err
	}
	if result.Token == "" || strings.ContainsAny(result.Token, "\r\n\x00") || !result.ExpiresAt.After(time.Now()) {
		return "", domain.Fail("GITHUB_REQUEST_FAILED", "GitHub returned an invalid installation token")
	}
	return result.Token, nil
}
