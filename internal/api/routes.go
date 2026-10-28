package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/controller"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/github"
	"github.com/filstawski/deployer/internal/store"
)

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		req := &request{Request: r, id: requestID(r)}
		if err := s.Store.Ping(r.Context()); err != nil {
			s.writeError(w, req, domain.Errorf(domain.CodeUnavailable, "the database is unavailable"))
			return
		}
		s.write(w, req, http.StatusOK, map[string]string{"status": "ok"})
	})
	// Browser steps of GitHub setup authenticate with a single-use link.
	browser := func(w http.ResponseWriter, r *http.Request) { s.GitHub.ServeBrowser(w, r, s.activeAdmin) }
	s.mux.HandleFunc("GET "+github.SetupPath+"{state}", browser)
	s.mux.HandleFunc("GET "+github.CallbackPath, browser)
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.writeError(w, &request{Request: r, id: requestID(r)}, domain.Errorf(domain.CodeNotFound, "no API route for %s %s", r.Method, r.URL.Path))
	})

	s.handle("GET /v1/server", domain.ScopeServerRead, s.status)

	s.handle("GET /v1/projects", domain.ScopeProjectsRead, s.listProjects)
	s.handle("POST /v1/projects", domain.ScopeProjectsWrite, s.createProject)
	s.handle("GET /v1/projects/{project}", domain.ScopeProjectsRead, s.getProject)
	s.handle("DELETE /v1/projects/{project}", domain.ScopeProjectsWrite, s.deleteProject)
	s.handle("POST /v1/projects/{project}/verify", domain.ScopeProjectsWrite, s.verifyProject)
	s.handle("GET /v1/projects/{project}/branches", domain.ScopeProjectsRead, s.projectBranches)

	s.handle("GET /v1/deployments", domain.ScopeDeploymentsRead, s.listDeployments)
	s.handle("POST /v1/deployments", domain.ScopeDeploymentsWrite, s.createDeployment)
	s.handle("GET /v1/deployments/{deployment}", domain.ScopeDeploymentsRead, s.getDeployment)
	s.handle("DELETE /v1/deployments/{deployment}", domain.ScopeDeploymentsWrite, s.action(s.Controller.Destroy))
	s.handle("POST /v1/deployments/{deployment}/stop", domain.ScopeDeploymentsWrite, s.action(s.Controller.Stop))
	s.handle("POST /v1/deployments/{deployment}/start", domain.ScopeDeploymentsWrite, s.action(s.Controller.Start))
	s.handle("POST /v1/deployments/{deployment}/extend", domain.ScopeDeploymentsWrite, s.extendDeployment)
	s.handle("POST /v1/deployments/{deployment}/update", domain.ScopeDeploymentsWrite, s.updateDeployment)
	s.handle("GET /v1/deployments/{deployment}/logs", domain.ScopeDeploymentsRead, s.deploymentLogs)

	s.handle("GET /v1/operations/{operation}", "", s.getOperation)

	s.handle("GET /v1/tokens", domain.ScopeAdmin, s.listTokens)
	s.handle("POST /v1/tokens", domain.ScopeAdmin, s.createToken)
	s.handle("DELETE /v1/tokens/{token}", domain.ScopeAdmin, s.revokeToken)

	s.handle("GET /v1/github", domain.ScopeServerRead, s.githubStatus)
	s.handle("POST /v1/github/setup", domain.ScopeAdmin, s.githubSetup)
}

// Server ----------------------------------------------------------------

func (s *Server) status(r *request) (int, any, error) {
	live, err := s.Controller.Deployments(r.Context(), store.DeploymentFilter{})
	if err != nil {
		return 0, nil, err
	}
	limits := s.Config.Deployments
	return http.StatusOK, domain.ServerStatus{
		Version: s.Version, APIVersion: domain.APIVersion, Domain: s.Config.Domain, PreviewURL: s.Config.PreviewURL("{name}"), APIURL: s.Config.API.PublicURL,
		Deployments: domain.DeploymentsStatus{
			Live: len(live), Max: limits.MaxLive,
			DefaultTTL: int64(limits.DefaultTTL.D().Seconds()), MaxTTL: int64(limits.MaxTTL.D().Seconds()),
		},
		GitHub: s.GitHub.Configured(),
		Token:  r.token,
	}, nil
}

// Projects --------------------------------------------------------------

// project resolves the {project} path value, hiding projects the token
// cannot access.
func (s *Server) project(r *request, ref string) (domain.Project, error) {
	p, err := s.Store.GetProject(r.Context(), ref)
	if err == nil && !r.token.CanAccess(p.ID) {
		err = domain.Errorf(domain.CodeNotFound, "project %q not found", ref)
	}
	return p, err
}

func (s *Server) listProjects(r *request) (int, any, error) {
	all, err := s.Store.ListProjects(r.Context())
	if err != nil {
		return 0, nil, err
	}
	visible := []domain.Project{}
	for _, p := range all {
		if r.token.CanAccess(p.ID) {
			visible = append(visible, p)
		}
	}
	return http.StatusOK, visible, nil
}

func (s *Server) createProject(r *request) (int, any, error) {
	if !r.token.CanAccess(domain.AllProjects) {
		return 0, nil, domain.Errorf(domain.CodeForbidden, "creating projects requires a token with access to all projects (*)")
	}
	var in domain.CreateProjectRequest
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	p, err := s.Store.CreateProject(r.Context(), r.token.ID, in, r.Header.Get("Idempotency-Key"))
	return http.StatusCreated, p, err
}

func (s *Server) getProject(r *request) (int, any, error) {
	p, err := s.project(r, r.PathValue("project"))
	return http.StatusOK, p, err
}

func (s *Server) deleteProject(r *request) (int, any, error) {
	p, err := s.project(r, r.PathValue("project"))
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, p, s.Store.DeleteProject(r.Context(), p.ID, r.token.ID)
}

func (s *Server) projectBranches(r *request) (int, any, error) {
	p, err := s.project(r, r.PathValue("project"))
	if err != nil {
		return 0, nil, err
	}
	branches, err := s.Controller.Branches(r.Context(), p)
	return http.StatusOK, branches, err
}

func (s *Server) verifyProject(r *request) (int, any, error) {
	p, err := s.project(r, r.PathValue("project"))
	if err != nil {
		return 0, nil, err
	}
	op, err := s.Controller.VerifyProject(r.Context(), r.token.ID, p, r.Header.Get("Idempotency-Key"))
	return http.StatusAccepted, op, err
}

// Deployments -----------------------------------------------------------

func (s *Server) deployment(r *request) (domain.Deployment, error) {
	ref := r.PathValue("deployment")
	d, err := s.Controller.Deployment(r.Context(), ref)
	if err == nil && !r.token.CanAccess(d.ProjectID) {
		err = domain.Errorf(domain.CodeNotFound, "deployment %q not found", ref)
	}
	return d, err
}

func (s *Server) listDeployments(r *request) (int, any, error) {
	filter := store.DeploymentFilter{IncludeDestroyed: r.URL.Query().Get("all") == "true"}
	if ref := r.URL.Query().Get("project"); ref != "" {
		p, err := s.project(r, ref)
		if err != nil {
			return 0, nil, err
		}
		filter.ProjectID = p.ID
	}
	all, err := s.Controller.Deployments(r.Context(), filter)
	if err != nil {
		return 0, nil, err
	}
	visible := []domain.Deployment{}
	for _, d := range all {
		if r.token.CanAccess(d.ProjectID) {
			visible = append(visible, d)
		}
	}
	return http.StatusOK, visible, nil
}

func (s *Server) createDeployment(r *request) (int, any, error) {
	var in domain.CreateDeploymentRequest
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	if in.Project == "" {
		return 0, nil, domain.Errorf(domain.CodeInvalidInput, "project is required")
	}
	p, err := s.project(r, in.Project)
	if err != nil {
		return 0, nil, err
	}
	resp, err := s.Controller.Deploy(r.Context(), r.token.ID, p, in, r.Header.Get("Idempotency-Key"))
	return http.StatusAccepted, resp, err
}

func (s *Server) getDeployment(r *request) (int, any, error) {
	d, err := s.deployment(r)
	return http.StatusOK, d, err
}

// action adapts a controller lifecycle method to a handler.
func (s *Server) action(fn func(ctx context.Context, actor, id, key string) (domain.Operation, error)) handler {
	return func(r *request) (int, any, error) {
		d, err := s.deployment(r)
		if err != nil {
			return 0, nil, err
		}
		op, err := fn(r.Context(), r.token.ID, d.ID, r.Header.Get("Idempotency-Key"))
		return http.StatusAccepted, op, err
	}
}

func (s *Server) extendDeployment(r *request) (int, any, error) {
	d, err := s.deployment(r)
	if err != nil {
		return 0, nil, err
	}
	var in domain.ExtendRequest
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	d, err = s.Controller.Extend(r.Context(), r.token.ID, d.ID, time.Duration(in.Seconds)*time.Second)
	return http.StatusOK, d, err
}

func (s *Server) updateDeployment(r *request) (int, any, error) {
	d, err := s.deployment(r)
	if err != nil {
		return 0, nil, err
	}
	var in domain.UpdateDeploymentRequest
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	op, err := s.Controller.Update(r.Context(), r.token.ID, d.ID, r.Header.Get("Idempotency-Key"), in)
	return http.StatusAccepted, op, err
}

func (s *Server) deploymentLogs(r *request) (int, any, error) {
	d, err := s.deployment(r)
	if err != nil {
		return 0, nil, err
	}
	q := r.URL.Query()
	req := controller.LogsRequest{Source: controller.LogsApp, Offset: -1}
	switch q.Get("source") {
	case "", "app":
	case "build":
		req.Source = controller.LogsBuild
	default:
		return 0, nil, domain.Errorf(domain.CodeInvalidInput, "source must be app or build")
	}
	if v := q.Get("lines"); v != "" {
		if req.Lines, err = strconv.Atoi(v); err != nil {
			return 0, nil, domain.Errorf(domain.CodeInvalidInput, "lines must be a number")
		}
	}
	if v := q.Get("offset"); v != "" {
		if req.Offset, err = strconv.ParseInt(v, 10, 64); err != nil || req.Offset < 0 {
			return 0, nil, domain.Errorf(domain.CodeInvalidInput, "offset must be a non-negative number")
		}
	}
	logs, err := s.Controller.Logs(r.Context(), d, req)
	return http.StatusOK, logs, err
}

// Operations ------------------------------------------------------------

func (s *Server) getOperation(r *request) (int, any, error) {
	id := r.PathValue("operation")
	op, err := s.Store.GetOperation(r.Context(), id)
	if err != nil {
		return 0, nil, err
	}
	need := domain.ScopeDeploymentsRead
	if op.DeploymentID == "" {
		need = domain.ScopeProjectsRead
	}
	if !r.token.Has(need) || !r.token.CanAccess(op.ProjectID) {
		return 0, nil, domain.Errorf(domain.CodeNotFound, "operation %q not found", id)
	}
	return http.StatusOK, op, nil
}

// Tokens ----------------------------------------------------------------

func (s *Server) listTokens(r *request) (int, any, error) {
	tokens, err := s.Store.ListTokens(r.Context())
	return http.StatusOK, tokens, err
}

func (s *Server) createToken(r *request) (int, any, error) {
	var in domain.CreateTokenRequest
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	if in.TTLSeconds < 0 {
		return 0, nil, domain.Errorf(domain.CodeInvalidInput, "ttl_seconds must not be negative")
	}
	tok, secret, err := s.Store.CreateToken(r.Context(), store.NewToken{
		Name: in.Name, Scopes: in.Scopes, Projects: in.Projects, TTL: time.Duration(in.TTLSeconds) * time.Second, CreatedBy: r.token.ID,
	})
	return http.StatusCreated, domain.CreateTokenResponse{Token: tok, Secret: secret}, err
}

func (s *Server) revokeToken(r *request) (int, any, error) {
	id := r.PathValue("token")
	if err := s.Store.RevokeToken(r.Context(), id, r.token.ID); err != nil {
		return 0, nil, err
	}
	tok, err := s.Store.GetToken(r.Context(), id)
	return http.StatusOK, tok, err
}

// GitHub ----------------------------------------------------------------

func (s *Server) githubStatus(r *request) (int, any, error) {
	status, err := s.GitHub.Status(r.Context())
	return http.StatusOK, status, err
}

func (s *Server) githubSetup(r *request) (int, any, error) {
	var in domain.GitHubSetupRequest
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	origin := strings.TrimRight(in.PublicURL, "/")
	if origin == "" {
		origin = s.Config.PublicAPIURL()
	}
	link, err := s.GitHub.BeginSetup(github.SetupRequest{Origin: origin, AppName: "Deployer " + s.Config.Domain, Organization: in.Organization, Actor: r.token.ID})
	return http.StatusCreated, link, err
}

func (s *Server) activeAdmin(ctx context.Context, tokenID string) bool {
	tok, err := s.Store.GetToken(ctx, tokenID)
	return err == nil && tok.Active(s.Store.Now()) && tok.Has(domain.ScopeAdmin)
}
