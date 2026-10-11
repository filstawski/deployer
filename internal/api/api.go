package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"deployer/internal/config"
	"deployer/internal/deployments"
	"deployer/internal/domain"
	"deployer/internal/githubapp"
	"deployer/internal/store/sqlite"
	"deployer/internal/version"
)

type Server struct {
	GitHub      *githubapp.App
	Store       *sqlite.Store
	Config      config.Server
	Log         *slog.Logger
	Deployments *deployments.Service
}
type contextKey int

const principalKey contextKey = 0
const requestKey contextKey = 1

func principal(r *http.Request) domain.Token { return r.Context().Value(principalKey).(domain.Token) }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/github/setup", s.githubSetup)
	mux.HandleFunc("POST /v1/github/connect", s.github)
	mux.HandleFunc("GET /v1/github", s.github)
	mux.HandleFunc("GET /v1/server", s.status)
	mux.HandleFunc("POST /v1/deployments", s.deploy)
	mux.HandleFunc("GET /v1/deployments", s.deployments)
	mux.HandleFunc("GET /v1/deployments/{id}", s.deployment)
	mux.HandleFunc("GET /v1/deployments/{id}/logs", s.deploymentLogs)
	mux.HandleFunc("POST /v1/deployments/{id}/actions/{action}", s.deploymentAction)
	mux.HandleFunc("GET /v1/projects", s.projects)
	mux.HandleFunc("POST /v1/projects", s.createProject)
	mux.HandleFunc("GET /v1/projects/{id}", s.project)
	mux.HandleFunc("POST /v1/projects/{id}/verify", s.verify)
	mux.HandleFunc("GET /v1/operations/{id}", s.operation)
	mux.HandleFunc("GET /v1/tokens", s.tokens)
	mux.HandleFunc("POST /v1/tokens", s.issueToken)
	mux.HandleFunc("DELETE /v1/tokens/{id}", s.revoke)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.fail(w, r, domain.Fail("NOT_FOUND", "API route not found"))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		id := domain.ID("req")
		w.Header().Set("X-Request-ID", id)
		r = r.WithContext(context.WithValue(r.Context(), requestKey, id))
		if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
			if err := s.Store.DB.PingContext(r.Context()); err != nil {
				s.fail(w, r, domain.Fail("UNAVAILABLE", "server is not ready"))
				return
			}
			s.write(w, r, 200, map[string]string{"status": "ok"})
			return
		}
		if s.GitHub != nil && r.Method == "GET" && (r.URL.Path == "/v1/github/callback" || strings.HasPrefix(r.URL.Path, "/v1/github/setup/")) {
			s.GitHub.Browser(w, r, s.githubAdminActive)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || len(auth) > 256 {
			s.fail(w, r, domain.Fail("UNAUTHENTICATED", "a Deployer bearer token is required"))
			return
		}
		t, err := s.Store.Authenticate(r.Context(), strings.TrimPrefix(auth, "Bearer "))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), principalKey, t))
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) write(w http.ResponseWriter, r *http.Request, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(domain.Envelope{SchemaVersion: 1, Data: data, RequestID: r.Context().Value(requestKey).(string)})
}
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var e *domain.Error
	if !errors.As(err, &e) {
		s.Log.Error("request failed", "request_id", r.Context().Value(requestKey), "error", err)
		e = domain.Fail("INTERNAL_ERROR", "request failed; use the request ID to inspect server diagnostics")
	}
	status := http.StatusBadRequest
	switch e.Code {
	case "UNAUTHENTICATED":
		status = 401
		w.Header().Set("WWW-Authenticate", "Bearer")
	case "FORBIDDEN":
		status = 403
	case "NOT_FOUND":
		status = 404
	case "ALREADY_EXISTS", "IDEMPOTENCY_CONFLICT", "ALREADY_INITIALIZED", "INVALID_STATE", "OPERATION_IN_PROGRESS", "SUBDOMAIN_TAKEN":
		status = 409
	case "QUOTA_EXCEEDED":
		status = 429
	case "UNAVAILABLE":
		status = 503
	case "INTERNAL_ERROR":
		status = 500
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(domain.Envelope{SchemaVersion: 1, Error: e, RequestID: r.Context().Value(requestKey).(string)})
}
func (s *Server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil || len(bytes.TrimSpace(body)) == 0 || bytes.TrimSpace(body)[0] != '{' {
		s.fail(w, r, domain.Fail("INVALID_INPUT", "request must be a JSON object (maximum 64 KiB)"))
		return false
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		s.fail(w, r, domain.Fail("INVALID_INPUT", "request must be a valid JSON object with supported fields (maximum 64 KiB)"))
		return false
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		s.fail(w, r, domain.Fail("INVALID_INPUT", "request must contain exactly one JSON object"))
		return false
	}
	return true
}
func (s *Server) scope(w http.ResponseWriter, r *http.Request, scope string) bool {
	if !principal(r).Has(scope) {
		s.fail(w, r, domain.Fail("FORBIDDEN", "token does not grant "+scope))
		return false
	}
	return true
}
func (s *Server) lookup(w http.ResponseWriter, r *http.Request, scope string) (domain.Project, bool) {
	if !s.scope(w, r, scope) {
		return domain.Project{}, false
	}
	p, err := s.Store.Project(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return p, false
	}
	if !principal(r).AllowsProject(p.ID) {
		s.fail(w, r, domain.Fail("NOT_FOUND", "project not found"))
		return p, false
	}
	return p, true
}
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if !s.scope(w, r, "server:read") {
		return
	}
	runtime := "disabled"
	capabilities := []string{"projects", "scoped_tokens", "durable_repository_verification"}
	if s.GitHub != nil {
		capabilities = append(capabilities, "github_app")
	}
	if s.Deployments != nil {
		runtime = "docker"
		capabilities = append(capabilities, "dockerfile_previews", "deployment_lifecycle", "ttl")
	}
	s.write(w, r, 200, map[string]any{"name": "deployer", "version": version.Version, "api_version": 1, "base_domain": s.Config.BaseDomain, "capabilities": capabilities, "deployment_runtime": runtime})
}
func (s *Server) projects(w http.ResponseWriter, r *http.Request) {
	if !s.scope(w, r, "projects:read") {
		return
	}
	all, err := s.Store.Projects(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []domain.Project{}
	for _, p := range all {
		if principal(r).AllowsProject(p.ID) {
			out = append(out, p)
		}
	}
	s.write(w, r, 200, out)
}
func (s *Server) project(w http.ResponseWriter, r *http.Request) {
	p, ok := s.lookup(w, r, "projects:read")
	if ok {
		s.write(w, r, 200, p)
	}
}
func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	if !s.scope(w, r, "projects:write") {
		return
	}
	if !principal(r).AllowsProject("*") {
		s.fail(w, r, domain.Fail("FORBIDDEN", "creating projects requires the * project grant"))
		return
	}
	var in struct {
		Name       string `json:"name"`
		Repository string `json:"repository"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	p, err := s.Store.CreateProject(r.Context(), in.Name, in.Repository, principal(r).ID, r.Header.Get("Idempotency-Key"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, r, 201, p)
}
func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	p, ok := s.lookup(w, r, "projects:write")
	if !ok {
		return
	}
	var in struct{}
	if !s.decode(w, r, &in) {
		return
	}
	o, err := s.Store.EnqueueVerify(r.Context(), p.ID, principal(r).ID, r.Header.Get("Idempotency-Key"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, r, 202, o)
}
func (s *Server) operation(w http.ResponseWriter, r *http.Request) {
	if !principal(r).Has("projects:read") && !principal(r).Has("deployments:read") {
		s.fail(w, r, domain.Fail("FORBIDDEN", "operation reading requires projects:read or deployments:read"))
		return
	}
	o, err := s.Store.Operation(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !principal(r).AllowsProject(o.ProjectID) || o.DeploymentID != "" && !principal(r).Has("deployments:read") || o.DeploymentID == "" && !principal(r).Has("projects:read") {
		s.fail(w, r, domain.Fail("NOT_FOUND", "operation not found"))
		return
	}
	s.write(w, r, 200, o)
}
func (s *Server) tokens(w http.ResponseWriter, r *http.Request) {
	if !s.scope(w, r, "admin") {
		return
	}
	v, err := s.Store.Tokens(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, r, 200, v)
}
func (s *Server) issueToken(w http.ResponseWriter, r *http.Request) {
	if !s.scope(w, r, "admin") {
		return
	}
	var in sqlite.TokenInput
	if !s.decode(w, r, &in) {
		return
	}
	for _, id := range in.Projects {
		if id != "*" {
			p, err := s.Store.Project(r.Context(), id)
			if err != nil || p.ID != id {
				s.fail(w, r, domain.Fail("INVALID_INPUT", "unknown project ID in token grant"))
				return
			}
		}
	}
	t, secret, err := s.Store.IssueToken(r.Context(), in, principal(r).ID, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, r, 201, map[string]any{"token": t, "secret": secret})
}
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if !s.scope(w, r, "admin") {
		return
	}
	if err := s.Store.Revoke(r.Context(), r.PathValue("id"), principal(r).ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, r, 200, map[string]bool{"revoked": true})
}
