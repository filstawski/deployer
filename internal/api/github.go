package api

import (
	"context"
	"deployer/internal/domain"
	"net/http"
	"time"
)

func (s *Server) githubAdminActive(ctx context.Context, id string) bool {
	tokens, err := s.Store.Tokens(ctx)
	if err != nil {
		return false
	}
	for _, t := range tokens {
		if t.ID == id {
			return t.Has("admin") && !t.Revoked && (t.ExpiresAt == nil || t.ExpiresAt.After(time.Now()))
		}
	}
	return false
}
func (s *Server) githubSetup(w http.ResponseWriter, r *http.Request) {
	if !s.scope(w, r, "admin") {
		return
	}
	var in struct {
		PublicURL    string `json:"public_url"`
		Organization string `json:"organization"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	if s.GitHub == nil {
		s.fail(w, r, domain.Fail("UNAVAILABLE", "GitHub integration is unavailable"))
		return
	}
	if in.PublicURL == "" {
		in.PublicURL = "https://api." + s.Config.BaseDomain
	}
	result, err := s.GitHub.Begin(in.PublicURL, in.Organization, principal(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, r, 201, result)
}
func (s *Server) github(w http.ResponseWriter, r *http.Request) {
	if !s.scope(w, r, "projects:write") {
		return
	}
	if !principal(r).AllowsProject("*") {
		s.fail(w, r, domain.Fail("FORBIDDEN", "GitHub connections require the * project grant"))
		return
	}
	if s.GitHub == nil {
		s.fail(w, r, domain.Fail("UNAVAILABLE", "GitHub integration is unavailable"))
		return
	}
	link, err := s.GitHub.InstallURL()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if r.Method == "POST" {
		s.write(w, r, 200, map[string]string{"url": link})
		return
	}
	installations, err := s.GitHub.Installations(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, r, 200, map[string]any{"url": link, "installations": installations})
}
