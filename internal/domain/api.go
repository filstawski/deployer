package domain

import "time"

// APIVersion is the version of the HTTP API served under /v1.
const APIVersion = 1

// Envelope wraps every API response body.
type Envelope[T any] struct {
	Data      T      `json:"data,omitzero"`
	Error     *Error `json:"error,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// ServerStatus describes a running server, returned by GET /v1/server.
type ServerStatus struct {
	Version    string `json:"version"`
	APIVersion int    `json:"api_version"`
	Domain     string `json:"domain"`
	// PreviewURL shows where previews are served, with {name} for the
	// deployment name, such as https://{name}.example.com.
	PreviewURL string `json:"preview_url"`
	// APIURL is the public HTTPS address of this API, when it has one.
	APIURL      string            `json:"api_url,omitempty"`
	Deployments DeploymentsStatus `json:"deployments"`
	GitHub      bool              `json:"github_configured"`
	// Token describes the token that made the request.
	Token Token `json:"token"`
}

// DeploymentsStatus summarizes deployment capacity.
type DeploymentsStatus struct {
	Live       int   `json:"live"`
	Max        int   `json:"max"`
	DefaultTTL int64 `json:"default_ttl_seconds"`
	MaxTTL     int64 `json:"max_ttl_seconds"`
}

// CreateProjectRequest is the body of POST /v1/projects.
type CreateProjectRequest struct {
	Name       string `json:"name"`
	Repository string `json:"repository"`
}

// CreateDeploymentRequest is the body of POST /v1/deployments.
type CreateDeploymentRequest struct {
	// Project is a project name or ID.
	Project string `json:"project"`
	Ref     string `json:"ref"`
	// Name is the preview's subdomain label; generated when empty.
	Name string `json:"name,omitempty"`
	// TTLSeconds is the lifetime after readiness; the server default when 0.
	TTLSeconds int64 `json:"ttl_seconds,omitempty"`
}

// CreateDeploymentResponse is returned by POST /v1/deployments. The
// deployment's URL is reserved immediately and shows a progress page until
// the operation completes.
type CreateDeploymentResponse struct {
	Deployment Deployment `json:"deployment"`
	Operation  Operation  `json:"operation"`
}

// ExtendRequest is the body of POST /v1/deployments/{id}/extend.
type ExtendRequest struct {
	Seconds int64 `json:"seconds"`
}

// LogsResponse is returned by GET /v1/deployments/{id}/logs. For build logs,
// Offset is the byte position to request next to follow the log.
type LogsResponse struct {
	Logs   string `json:"logs"`
	Offset int64  `json:"offset,omitempty"`
}

// CreateTokenRequest is the body of POST /v1/tokens.
type CreateTokenRequest struct {
	Name     string   `json:"name"`
	Scopes   []Scope  `json:"scopes"`
	Projects []string `json:"projects"`
	// TTLSeconds is the token lifetime; 0 means the token never expires.
	TTLSeconds int64 `json:"ttl_seconds,omitempty"`
}

// CreateTokenResponse carries a new token's secret, which is shown only once.
type CreateTokenResponse struct {
	Token  Token  `json:"token"`
	Secret string `json:"secret"`
}

// GitHubStatus is returned by GET /v1/github.
type GitHubStatus struct {
	Configured    bool                 `json:"configured"`
	App           string               `json:"app,omitempty"`
	InstallURL    string               `json:"install_url,omitempty"`
	Installations []GitHubInstallation `json:"installations,omitempty"`
}

// GitHubInstallation is an account where the GitHub App is installed.
type GitHubInstallation struct {
	ID        int64  `json:"id"`
	Account   string `json:"account"`
	Selection string `json:"repository_selection"`
	Suspended bool   `json:"suspended"`
}

// GitHubSetupRequest is the body of POST /v1/github/setup.
type GitHubSetupRequest struct {
	// PublicURL is the HTTPS origin where GitHub can reach this API.
	PublicURL    string `json:"public_url,omitempty"`
	Organization string `json:"organization,omitempty"`
}

// GitHubSetupResponse links to the browser page that creates the GitHub App.
type GitHubSetupResponse struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}
