// Package daemon wires the controller and gateway processes together.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/filstawski/deployer/internal/api"
	"github.com/filstawski/deployer/internal/config"
	"github.com/filstawski/deployer/internal/container/docker"
	"github.com/filstawski/deployer/internal/controller"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/git"
	"github.com/filstawski/deployer/internal/github"
	"github.com/filstawski/deployer/internal/operations"
	"github.com/filstawski/deployer/internal/secretfile"
	"github.com/filstawski/deployer/internal/store"
	"github.com/filstawski/deployer/internal/version"
)

// Serve runs the controller until ctx is cancelled: the API, the operation
// dispatcher, and reconciliation, with the gateway and builder containers.
func Serve(ctx context.Context, cfg config.Server, log *slog.Logger) error {
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return err
	}
	unlock, err := lockStateDir(cfg.StateDir)
	if err != nil {
		return err
	}
	defer unlock()

	st, err := store.Open(cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer st.Close()
	if err := ensureAdminToken(ctx, st, cfg.AdminTokenPath(), log); err != nil {
		return err
	}
	installation, err := st.InstallationID(ctx)
	if err != nil {
		return err
	}
	gh, err := github.Open(filepath.Join(cfg.StateDir, "github-app.json"))
	if err != nil {
		return err
	}

	d := cfg.Deployments
	runtime := docker.New(docker.Options{
		InstallationID: installation,
		StateDir:       cfg.StateDir,
		Limits:         docker.Limits{CPUs: d.CPUs, MemoryMB: d.MemoryMB, PIDs: d.PIDs},
		Egress:         d.Egress,
		Gateway: docker.GatewayOptions{
			Image: cfg.Gateway.Image, Listen: cfg.Gateway.Listen, ControlListen: cfg.Gateway.ControlListen, PublicScheme: cfg.Previews.Scheme,
		},
	})
	setup, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	log.Info("starting gateway and build containers")
	router, err := runtime.EnsureGateway(setup)
	if err != nil {
		return err
	}
	if err := runtime.EnsureBuilder(setup); err != nil {
		return err
	}

	ctrl := controller.New(controller.Deps{
		Store: st, Config: cfg, Runtime: runtime, Router: router, Log: log, Source: newSource(cfg, gh),
	})
	// Builds wait for a build slot inside their operation. A deployment has
	// at most one create operation, so sizing for every live deployment plus
	// headroom keeps waiting builds from blocking stops, destroys, and checks.
	dispatcher := operations.New(st, log, operations.Options{Concurrency: cfg.Deployments.MaxLive + 8})
	ctrl.Register(dispatcher)
	if err := ctrl.Boot(ctx); err != nil {
		return err
	}

	server := &http.Server{
		Handler:           api.New(api.Deps{Store: st, Controller: ctrl, GitHub: gh, Config: cfg, Log: log, Version: version.Version}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	listener, err := net.Listen("tcp", cfg.API.Listen)
	if err != nil {
		return err
	}

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := dispatcher.Run(ctx); err != nil {
			log.Error("dispatcher stopped", "error", err)
			stop()
		}
	})
	wg.Go(func() { ctrl.Run(ctx, 5*time.Second, runtime) })
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	log.Info("Deployer is running", "version", version.Version, "api", cfg.API.Listen, "previews", cfg.PreviewURL("NAME"))

	select {
	case <-ctx.Done():
		err = nil
	case err = <-serveErr:
	}
	log.Info("shutting down")
	shutdown, done := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer done()
	server.Shutdown(shutdown)
	stop()
	wg.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// newSource returns the Git source; end-to-end tests replace it.
var newSource = func(cfg config.Server, gh *github.App) controller.Source {
	return &git.Client{HomeDir: filepath.Join(cfg.StateDir, "git-home"), Credentials: gh}
}

// ensureAdminToken creates an administrator token on first start and saves
// its secret to path, readable only by the service account (and root).
func ensureAdminToken(ctx context.Context, st *store.Store, path string, log *slog.Logger) error {
	active, err := st.CountActiveTokens(ctx)
	if err != nil || active > 0 {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		log.Warn("no active API tokens; create one with deployerd token create", "path", path)
		return nil
	}
	tok, secret, err := st.CreateToken(ctx, store.NewToken{Name: "admin", Scopes: []domain.Scope{domain.ScopeAdmin}, Projects: []string{domain.AllProjects}, CreatedBy: "deployerd"})
	if err != nil {
		return err
	}
	if err := secretfile.WriteNew(path, secret); err != nil {
		st.RevokeToken(ctx, tok.ID, "deployerd")
		return err
	}
	log.Info("created the administrator token", "token", tok.ID, "path", path)
	return nil
}

// lockStateDir ensures that only one controller uses a state directory.
func lockStateDir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, "deployerd.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another deployerd is already using %s", dir)
	}
	return func() { f.Close() }, nil
}
