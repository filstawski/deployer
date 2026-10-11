package api

import (
	"deployer/internal/domain"
	"net/http"
)

func (s *Server) runtime(w http.ResponseWriter, r *http.Request) bool {
	if s.Deployments == nil {
		s.fail(w, r, domain.Fail("UNSUPPORTED_FEATURE", "enable the Docker runtime in server configuration"))
		return false
	}
	return true
}
func (s *Server) deploy(w http.ResponseWriter, r *http.Request) {
	if !s.scope(w, r, "deployments:write") || !s.runtime(w, r) {
		return
	}
	var in domain.DeployInput
	if !s.decode(w, r, &in) {
		return
	}
	p, err := s.Store.Project(r.Context(), in.Project)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !principal(r).AllowsProject(p.ID) {
		s.fail(w, r, domain.Fail("NOT_FOUND", "project not found"))
		return
	}
	o, err := s.Store.QueueDeploy(r.Context(), in, p, principal(r).ID, r.Header.Get("Idempotency-Key"), s.Config.BaseDomain, s.Config.PreviewPort(), s.Config.Runtime.MaxDeployments, s.Config.PreviewScheme())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// The durable queue is authoritative; reconciliation retries route publication.
	if err = s.Deployments.Publish(r.Context()); err != nil {
		s.Log.Error("queued route publication", "error", err)
	}
	s.write(w, r, 202, o)
}
func (s *Server) deployments(w http.ResponseWriter, r *http.Request) {
	if !s.scope(w, r, "deployments:read") {
		return
	}
	all, err := s.Store.Deployments(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []domain.Deployment{}
	for _, d := range all {
		if principal(r).AllowsProject(d.ProjectID) {
			d.Environment = nil
			out = append(out, d)
		}
	}
	s.write(w, r, 200, out)
}
func (s *Server) deploymentLookup(w http.ResponseWriter, r *http.Request, scope string) (domain.Deployment, bool) {
	if !s.scope(w, r, scope) {
		return domain.Deployment{}, false
	}
	d, err := s.Store.Deployment(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return d, false
	}
	if !principal(r).AllowsProject(d.ProjectID) {
		s.fail(w, r, domain.Fail("NOT_FOUND", "deployment not found"))
		return d, false
	}
	return d, true
}
func (s *Server) deployment(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deploymentLookup(w, r, "deployments:read")
	if ok {
		d.Environment = nil
		s.write(w, r, 200, d)
	}
}
func (s *Server) deploymentAction(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deploymentLookup(w, r, "deployments:write")
	if !ok || !s.runtime(w, r) {
		return
	}
	var in struct {
		Seconds int64 `json:"seconds"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	o, err := s.Deployments.Action(r.Context(), d.ID, r.PathValue("action"), principal(r).ID, r.Header.Get("Idempotency-Key"), in.Seconds)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, r, 202, o)
}
func (s *Server) deploymentLogs(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deploymentLookup(w, r, "deployments:read")
	if !ok || !s.runtime(w, r) {
		return
	}
	logs, err := s.Deployments.Logs(r.Context(), d, r.URL.Query().Get("build") == "true")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, r, 200, map[string]string{"logs": logs})
}
