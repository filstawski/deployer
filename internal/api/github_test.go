package api

import (
	"context"
	"deployer/internal/config"
	"deployer/internal/githubapp"
	"deployer/internal/store/sqlite"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestGitHubSetupRequiresAdminAndRevocationInvalidatesLink(t *testing.T) {
	s, _, admin := fixture(t)
	g, err := githubapp.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &Server{Store: s, Config: config.Defaults(), GitHub: g, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := a.Handler()
	_, laptop, err := s.IssueToken(context.Background(), sqlite.TokenInput{Name: "laptop", Scopes: []string{"projects:write"}, Projects: []string{"*"}, TTLSeconds: 3600}, "local", false)
	if err != nil {
		t.Fatal(err)
	}
	if w := request(h, "POST", "/v1/github/setup", laptop, `{}`); w.Code != 403 {
		t.Fatal("non-admin configured app", w.Code)
	}
	if w := request(h, "GET", "/v1/github", "", ""); w.Code != 401 {
		t.Fatal("unauthenticated installations", w.Code)
	}
	if w := request(h, "GET", "/v1/github/setup/invalid", "", ""); w.Code != 400 {
		t.Fatal("unexpected browser route", w.Code)
	}
	w := request(h, "POST", "/v1/github/setup", admin, `{"public_url":"https://api.example.com"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var response struct {
		Data githubapp.SetupLink `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &response)
	path := strings.TrimPrefix(response.Data.URL, "https://api.example.com")
	if w = request(h, "GET", path, "", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	token, err := s.Authenticate(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	s.Revoke(context.Background(), token.ID, "test")
	if w = request(h, "GET", path, "", ""); w.Code != 403 {
		t.Fatal("revoked admin link accepted", w.Code)
	}
}
