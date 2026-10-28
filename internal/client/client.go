// Package client is a typed client for the Deployer /v1 API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/domain"
)

// Client calls one Deployer server.
type Client struct {
	base  string
	token string
	http  *http.Client
	// UserAgent identifies the caller.
	UserAgent string
}

// Options configures a Client.
type Options struct {
	// Transport replaces the default transport, for example with an SSH
	// tunnel. Plain HTTP is then allowed because the tunnel encrypts traffic.
	Transport http.RoundTripper
	Timeout   time.Duration
}

// New returns a client for baseURL, such as https://api.example.com.
func New(baseURL, token string, opts Options) (*Client, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if err := ValidateURL(baseURL, opts.Transport != nil); err != nil {
		return nil, err
	}
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	return &Client{
		base:  baseURL,
		token: token,
		http: &http.Client{
			Transport: opts.Transport,
			Timeout:   opts.Timeout,
			// Never forward the token to another host.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		UserAgent: "deployer",
	}, nil
}

// ValidateURL checks a server URL. Plain HTTP is accepted only for loopback
// addresses, or when tunneled.
func ValidateURL(raw string, tunneled bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return domain.Errorf(domain.CodeInvalidInput, "server URL must look like https://api.example.com")
	}
	switch {
	case u.Scheme == "https", u.Scheme == "http" && tunneled:
		return nil
	case u.Scheme == "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || ip != nil && ip.IsLoopback() {
			return nil
		}
		return domain.Errorf(domain.CodeInvalidInput, "refusing to send a token over plain HTTP to %s; use https://", host)
	default:
		return domain.Errorf(domain.CodeInvalidInput, "server URL must start with https://")
	}
}

// BaseURL returns the server URL.
func (c *Client) BaseURL() string { return c.base }

// do performs a request and decodes the envelope's data into out. Requests
// that are safe to repeat are retried on connection failures and gateway
// errors: GETs, and writes that carry an idempotency key.
func (c *Client) do(ctx context.Context, method, path, key string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	retryable := method == http.MethodGet || key != ""
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		transient, err := c.once(ctx, method, path, key, body, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable || !transient || ctx.Err() != nil {
			break
		}
	}
	return lastErr
}

// once sends one request. It reports whether a failure may be temporary.
func (c *Client) once(ctx context.Context, method, path, key string, body []byte, out any) (bool, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		// A transport that explains its own failure, as the SSH tunnel does
		// when ssh fails, has already tried; say what it said.
		if e, ok := domain.AsError(err); ok {
			return false, e
		}
		return true, domain.Errorf(domain.CodeUnavailable, "cannot reach the Deployer API at %s: %v", c.base, unwrapURLError(err))
	}
	defer resp.Body.Close()
	transient := resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout
	var env domain.Envelope[json.RawMessage]
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&env); err != nil {
		return transient, domain.Errorf(domain.CodeUnavailable, "%s did not return a Deployer API response (HTTP %d); check the server URL", c.base, resp.StatusCode)
	}
	if env.Error != nil {
		return transient, env.Error
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return transient, domain.Errorf(domain.CodeUnavailable, "unexpected HTTP %d from %s", resp.StatusCode, c.base)
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return false, fmt.Errorf("decode response: %w", err)
		}
	}
	return false, nil
}

func unwrapURLError(err error) error {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err
	}
	return err
}

func esc(s string) string { return url.PathEscape(s) }

// Status returns server information.
func (c *Client) Status(ctx context.Context) (domain.ServerStatus, error) {
	var out domain.ServerStatus
	return out, c.do(ctx, "GET", "/v1/server", "", nil, &out)
}

// Projects lists projects.
func (c *Client) Projects(ctx context.Context) ([]domain.Project, error) {
	var out []domain.Project
	return out, c.do(ctx, "GET", "/v1/projects", "", nil, &out)
}

// Project returns a project by name or ID.
func (c *Client) Project(ctx context.Context, ref string) (domain.Project, error) {
	var out domain.Project
	return out, c.do(ctx, "GET", "/v1/projects/"+esc(ref), "", nil, &out)
}

// CreateProject registers a repository.
func (c *Client) CreateProject(ctx context.Context, req domain.CreateProjectRequest, key string) (domain.Project, error) {
	var out domain.Project
	return out, c.do(ctx, "POST", "/v1/projects", key, req, &out)
}

// DeleteProject removes a project without live deployments.
func (c *Client) DeleteProject(ctx context.Context, ref string) (domain.Project, error) {
	var out domain.Project
	return out, c.do(ctx, "DELETE", "/v1/projects/"+esc(ref), "", nil, &out)
}

// Branches lists a project's branches, default branch first.
func (c *Client) Branches(ctx context.Context, project string) ([]domain.Branch, error) {
	var out []domain.Branch
	return out, c.do(ctx, "GET", "/v1/projects/"+esc(project)+"/branches", "", nil, &out)
}

// VerifyProject queues a repository access check.
func (c *Client) VerifyProject(ctx context.Context, ref, key string) (domain.Operation, error) {
	var out domain.Operation
	return out, c.do(ctx, "POST", "/v1/projects/"+esc(ref)+"/verify", key, nil, &out)
}

// ListOptions filters Deployments.
type ListOptions struct {
	Project string
	// All includes destroyed deployments.
	All bool
}

// Deployments lists deployments, newest first.
func (c *Client) Deployments(ctx context.Context, opts ListOptions) ([]domain.Deployment, error) {
	q := url.Values{}
	if opts.Project != "" {
		q.Set("project", opts.Project)
	}
	if opts.All {
		q.Set("all", "true")
	}
	path := "/v1/deployments"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out []domain.Deployment
	return out, c.do(ctx, "GET", path, "", nil, &out)
}

// Deployment returns a deployment by name or ID.
func (c *Client) Deployment(ctx context.Context, ref string) (domain.Deployment, error) {
	var out domain.Deployment
	return out, c.do(ctx, "GET", "/v1/deployments/"+esc(ref), "", nil, &out)
}

// Deploy creates a deployment.
func (c *Client) Deploy(ctx context.Context, req domain.CreateDeploymentRequest, key string) (domain.CreateDeploymentResponse, error) {
	var out domain.CreateDeploymentResponse
	return out, c.do(ctx, "POST", "/v1/deployments", key, req, &out)
}

// Stop queues stopping a deployment.
func (c *Client) Stop(ctx context.Context, ref, key string) (domain.Operation, error) {
	var out domain.Operation
	return out, c.do(ctx, "POST", "/v1/deployments/"+esc(ref)+"/stop", key, nil, &out)
}

// Start queues starting a deployment.
func (c *Client) Start(ctx context.Context, ref, key string) (domain.Operation, error) {
	var out domain.Operation
	return out, c.do(ctx, "POST", "/v1/deployments/"+esc(ref)+"/start", key, nil, &out)
}

// Destroy queues destroying a deployment.
func (c *Client) Destroy(ctx context.Context, ref, key string) (domain.Operation, error) {
	var out domain.Operation
	return out, c.do(ctx, "DELETE", "/v1/deployments/"+esc(ref), key, nil, &out)
}

// Update queues moving a deployment to the latest commit of its ref, or of
// req.Ref.
func (c *Client) Update(ctx context.Context, ref string, req domain.UpdateDeploymentRequest, key string) (domain.Operation, error) {
	var out domain.Operation
	return out, c.do(ctx, "POST", "/v1/deployments/"+esc(ref)+"/update", key, req, &out)
}

// Extend lengthens a deployment's lease.
func (c *Client) Extend(ctx context.Context, ref string, by time.Duration) (domain.Deployment, error) {
	var out domain.Deployment
	return out, c.do(ctx, "POST", "/v1/deployments/"+esc(ref)+"/extend", "", domain.ExtendRequest{Seconds: int64(by.Seconds())}, &out)
}

// LogsOptions selects deployment output.
type LogsOptions struct {
	// Build selects the build log instead of the app's output.
	Build bool
	Lines int
	// Offset, if set, reads the build log from a byte offset.
	Offset *int64
}

// Logs returns app or build output.
func (c *Client) Logs(ctx context.Context, ref string, opts LogsOptions) (domain.LogsResponse, error) {
	q := url.Values{"source": {"app"}}
	if opts.Build {
		q.Set("source", "build")
	}
	if opts.Lines > 0 {
		q.Set("lines", strconv.Itoa(opts.Lines))
	}
	if opts.Offset != nil {
		q.Set("offset", strconv.FormatInt(*opts.Offset, 10))
	}
	var out domain.LogsResponse
	return out, c.do(ctx, "GET", "/v1/deployments/"+esc(ref)+"/logs?"+q.Encode(), "", nil, &out)
}

// Operation returns an operation.
func (c *Client) Operation(ctx context.Context, id string) (domain.Operation, error) {
	var out domain.Operation
	return out, c.do(ctx, "GET", "/v1/operations/"+esc(id), "", nil, &out)
}

// Tokens lists API tokens.
func (c *Client) Tokens(ctx context.Context) ([]domain.Token, error) {
	var out []domain.Token
	return out, c.do(ctx, "GET", "/v1/tokens", "", nil, &out)
}

// CreateToken creates an API token.
func (c *Client) CreateToken(ctx context.Context, req domain.CreateTokenRequest) (domain.CreateTokenResponse, error) {
	var out domain.CreateTokenResponse
	return out, c.do(ctx, "POST", "/v1/tokens", "", req, &out)
}

// RevokeToken revokes an API token.
func (c *Client) RevokeToken(ctx context.Context, id string) (domain.Token, error) {
	var out domain.Token
	return out, c.do(ctx, "DELETE", "/v1/tokens/"+esc(id), "", nil, &out)
}

// GitHub returns the GitHub connection status.
func (c *Client) GitHub(ctx context.Context) (domain.GitHubStatus, error) {
	var out domain.GitHubStatus
	return out, c.do(ctx, "GET", "/v1/github", "", nil, &out)
}

// GitHubSetup starts creating the server's GitHub App.
func (c *Client) GitHubSetup(ctx context.Context, req domain.GitHubSetupRequest) (domain.GitHubSetupResponse, error) {
	var out domain.GitHubSetupResponse
	return out, c.do(ctx, "POST", "/v1/github/setup", "", req, &out)
}
