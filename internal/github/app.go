// Package github connects the server to GitHub through a GitHub App that the
// administrator creates once. The App's private key stays on the server; each
// fetch uses a short-lived, read-only token scoped to a single repository.
package github

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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/filstawski/deployer/internal/domain"
)

// identity is the App registration saved after setup.
type identity struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	PEM  string `json:"pem"`
}

// App is the server's GitHub App. Its zero configuration is valid: an
// unconfigured App provides anonymous access, which works for public
// repositories.
type App struct {
	path string
	// API and Web are GitHub's base URLs; tests point them at fakes.
	API  string
	Web  string
	HTTP *http.Client

	mu      sync.Mutex
	id      *identity
	key     *rsa.PrivateKey
	pending *setup
}

// Open loads the App identity stored at path, if any.
func Open(path string) (*App, error) {
	a := &App{
		path: path,
		API:  "https://api.github.com",
		Web:  "https://github.com",
		HTTP: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s must be a regular file readable only by its owner", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var id identity
	if err := json.Unmarshal(data, &id); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	key, err := parseKey(id)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	a.id, a.key = &id, key
	return a, nil
}

func parseKey(id identity) (*rsa.PrivateKey, error) {
	if id.ID <= 0 || id.Slug == "" {
		return nil, fmt.Errorf("invalid GitHub App identity")
	}
	block, _ := pem.Decode([]byte(id.PEM))
	if block == nil {
		return nil, fmt.Errorf("invalid GitHub App private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if key, ok := parsed.(*rsa.PrivateKey); err == nil && ok {
		return key, nil
	}
	return nil, fmt.Errorf("GitHub App private key must be RSA")
}

// Configured reports whether setup has been completed.
func (a *App) Configured() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.id != nil
}

// Status describes the App and, when configured, where it is installed.
func (a *App) Status(ctx context.Context) (domain.GitHubStatus, error) {
	a.mu.Lock()
	id := a.id
	a.mu.Unlock()
	if id == nil {
		return domain.GitHubStatus{}, nil
	}
	status := domain.GitHubStatus{Configured: true, App: id.Slug, InstallURL: a.installURL(id.Slug)}
	installs, err := a.installations(ctx)
	status.Installations = installs
	return status, err
}

// InstallURL returns the page where an account owner installs the App and
// selects repositories.
func (a *App) InstallURL() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.id == nil {
		return "", notConfigured()
	}
	return a.installURL(a.id.Slug), nil
}

func (a *App) installURL(slug string) string { return a.Web + "/apps/" + slug + "/installations/new" }

func notConfigured() error {
	return domain.Errorf(domain.CodeGitHubNotConfigured, "GitHub is not connected to this server").
		WithHint("an administrator runs: deployer github setup")
}

// Credentials returns a read-only token for one github.com repository. It
// returns no credentials for other hosts, when the App is not configured, or
// when the App is not installed for the repository, so public repositories
// keep working anonymously.
func (a *App) Credentials(ctx context.Context, repository string) (string, string, error) {
	owner, repo, ok := domain.GitHubRepository(repository)
	if !ok || !a.Configured() {
		return "", "", nil
	}
	var installation struct {
		ID          int64      `json:"id"`
		SuspendedAt *time.Time `json:"suspended_at"`
	}
	err := a.call(ctx, http.MethodGet, "/repos/"+owner+"/"+repo+"/installation", nil, &installation)
	if e, ok := domain.AsError(err); ok && e.Code == domain.CodeNotFound {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if installation.SuspendedAt != nil {
		return "", "", domain.Errorf(domain.CodeGitHubFailed, "the GitHub App installation for %s is suspended", owner)
	}
	var token struct {
		Token string `json:"token"`
	}
	body := map[string]any{"repositories": []string{repo}, "permissions": map[string]string{"contents": "read"}}
	if err := a.call(ctx, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", installation.ID), body, &token); err != nil {
		return "", "", err
	}
	if token.Token == "" {
		return "", "", domain.Errorf(domain.CodeGitHubFailed, "GitHub returned an empty installation token")
	}
	return "x-access-token", token.Token, nil
}

func (a *App) installations(ctx context.Context) ([]domain.GitHubInstallation, error) {
	out := []domain.GitHubInstallation{}
	for page := 1; page <= 10; page++ {
		var batch []struct {
			ID      int64 `json:"id"`
			Account struct {
				Login string `json:"login"`
			} `json:"account"`
			Selection   string     `json:"repository_selection"`
			SuspendedAt *time.Time `json:"suspended_at"`
		}
		if err := a.call(ctx, http.MethodGet, fmt.Sprintf("/app/installations?per_page=100&page=%d", page), nil, &batch); err != nil {
			return out, err
		}
		for _, i := range batch {
			out = append(out, domain.GitHubInstallation{ID: i.ID, Account: i.Account.Login, Selection: i.Selection, Suspended: i.SuspendedAt != nil})
		}
		if len(batch) < 100 {
			break
		}
	}
	return out, nil
}

// call makes an App-authenticated API request. GitHub's error bodies are not
// returned because they can echo request details.
func (a *App) call(ctx context.Context, method, path string, in, out any) error {
	jwt, err := a.jwt()
	if err != nil {
		return err
	}
	return a.request(ctx, method, path, "Bearer "+jwt, in, out)
}

func (a *App) request(ctx context.Context, method, path, auth string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.API+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return domain.Errorf(domain.CodeGitHubFailed, "cannot reach GitHub")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return domain.Errorf(domain.CodeNotFound, "GitHub resource not found")
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return domain.Errorf(domain.CodeGitHubFailed, "GitHub returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return domain.Errorf(domain.CodeGitHubFailed, "GitHub returned an invalid response")
	}
	return nil
}

// jwt returns a short-lived App JSON Web Token signed with RS256.
func (a *App) jwt() (string, error) {
	a.mu.Lock()
	id, key := a.id, a.key
	a.mu.Unlock()
	if id == nil {
		return "", notConfigured()
	}
	now := time.Now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]int64{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": id.ID})
	signed := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// save stores a new identity without replacing an existing one.
func (a *App) save(id identity) error {
	key, err := parseKey(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(id)
	f, err := os.OpenFile(a.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("cannot save GitHub App credentials: %w", err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(a.path)
		return err
	}
	a.mu.Lock()
	a.id, a.key = &id, key
	a.mu.Unlock()
	return nil
}
