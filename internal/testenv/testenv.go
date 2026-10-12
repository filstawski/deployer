// Package testenv runs a complete Deployer server in-process for tests: the
// real store, controller, dispatcher, and API, with fake container runtime,
// gateway router, and Git source.
package testenv

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/filstawski/deployer/internal/api"
	"github.com/filstawski/deployer/internal/config"
	"github.com/filstawski/deployer/internal/controller"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/fakes"
	"github.com/filstawski/deployer/internal/github"
	"github.com/filstawski/deployer/internal/operations"
	"github.com/filstawski/deployer/internal/store"
)

// Env is a running test server.
type Env struct {
	Config     config.Server
	Store      *store.Store
	Controller *controller.Controller
	Runtime    *fakes.Runtime
	Router     *fakes.Router
	Source     *fakes.Source
	// Server serves the API; its URL is the API base URL.
	Server *httptest.Server
	// AdminToken is the secret of an admin token.
	AdminToken string
}

// New starts a server that is stopped when the test ends.
func New(t testing.TB) *Env {
	t.Helper()
	cfg := config.Defaults()
	cfg.Domain = "previews.test"
	cfg.StateDir = t.TempDir()
	s, err := store.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := &Env{Config: cfg, Store: s, Runtime: fakes.NewRuntime(), Router: fakes.NewRouter(), Source: fakes.NewSource()}
	e.Controller = controller.New(controller.Deps{Store: s, Config: cfg, Runtime: e.Runtime, Router: e.Router, Source: e.Source, Log: log, ProbeInterval: 10 * time.Millisecond})
	dispatcher := operations.New(s, log, operations.Options{PollInterval: 10 * time.Millisecond})
	e.Controller.Register(dispatcher)
	gh, err := github.Open(filepath.Join(cfg.StateDir, "github-app.json"))
	if err != nil {
		t.Fatal(err)
	}
	e.Server = httptest.NewServer(api.New(api.Deps{Store: s, Controller: e.Controller, GitHub: gh, Config: cfg, Log: log, Version: "v0.0.0-test"}).Handler())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { dispatcher.Run(ctx); close(done) }()
	t.Cleanup(func() {
		e.Server.Close()
		cancel()
		<-done
		s.Close()
	})
	e.AdminToken = e.Token(t, "admin", []domain.Scope{domain.ScopeAdmin}, domain.AllProjects)
	return e
}

// Token creates a token and returns its secret.
func (e *Env) Token(t testing.TB, name string, scopes []domain.Scope, projects ...string) string {
	t.Helper()
	_, secret, err := e.Store.CreateToken(context.Background(), store.NewToken{Name: name, Scopes: scopes, Projects: projects, CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return secret
}
