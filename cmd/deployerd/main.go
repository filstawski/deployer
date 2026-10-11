package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"deployer/internal/api"
	"deployer/internal/config"
	"deployer/internal/credentials"
	"deployer/internal/deployments"
	"deployer/internal/operations"
	"deployer/internal/processlock"
	docker "deployer/internal/runtime/docker"
	git "deployer/internal/source/git"
	"deployer/internal/store/sqlite"
	"deployer/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "deployerd:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Println("deployerd serve --config FILE\ndeployerd token bootstrap|create --config FILE --output TOKEN_FILE [--name operator --scopes admin --projects '*' --ttl 720h]\ndeployerd version")
		return nil
	}
	if args[0] == "version" {
		fmt.Println(version.Version)
		return nil
	}
	mode := args[0]
	rest := args[1:]
	if mode == "token" {
		if len(rest) == 0 {
			return fmt.Errorf("token requires bootstrap or create")
		}
		mode = "token-" + rest[0]
		rest = rest[1:]
	}
	if mode != "serve" && mode != "token-bootstrap" && mode != "token-create" {
		return fmt.Errorf("unknown command %q", mode)
	}
	f := flag.NewFlagSet(mode, flag.ContinueOnError)
	path := f.String("config", "", "server YAML configuration")
	output := f.String("output", "", "new owner-only token file")
	name := f.String("name", "operator", "token name")
	scopes := f.String("scopes", "admin", "comma-separated scopes")
	projects := f.String("projects", "*", "comma-separated project IDs")
	ttl := f.Duration("ttl", 720*time.Hour, "token lifetime (maximum 8760h)")
	if err := f.Parse(rest); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	if mode != "serve" {
		file, err := credentials.Create(*output)
		if err != nil {
			return err
		}
		defer file.Close()
		success := false
		defer func() {
			if !success {
				os.Remove(*output)
			}
		}()
		s, err := sqlite.Open(c.StateDir)
		if err != nil {
			return err
		}
		defer s.Close()
		t, secret, err := s.IssueToken(context.Background(), sqlite.TokenInput{Name: *name, Scopes: strings.Split(*scopes, ","), Projects: strings.Split(*projects, ","), TTLSeconds: int64(ttl.Seconds())}, "local-administrator", mode == "token-bootstrap")
		if err != nil {
			return err
		}
		if err = credentials.Write(file, secret); err != nil {
			_ = s.Revoke(context.Background(), t.ID, "local-administrator")
			return err
		}
		success = true
		fmt.Printf("Token %s saved to %s (expires %s)\n", t.ID, *output, t.ExpiresAt.Format(time.RFC3339))
		return nil
	}
	lock, err := processlock.Acquire(c.StateDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	s, err := sqlite.Open(c.StateDir)
	if err != nil {
		return err
	}
	defer s.Close()
	tokens, err := s.Tokens(context.Background())
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return fmt.Errorf("initialize access first with deployerd token bootstrap --config FILE --output TOKEN_FILE")
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var service *deployments.Service
	if c.Runtime.Enabled {
		id, err := s.InstallationID()
		if err != nil {
			return err
		}
		runtime := docker.New(c, id)
		setup, done := context.WithTimeout(ctx, 5*time.Minute)
		err = runtime.Ensure(setup)
		done()
		if err != nil {
			return err
		}
		all, e := s.Deployments(ctx)
		if e != nil {
			return e
		}
		for _, d := range all {
			if next := c.PreviewURL(d.Hostname); d.URL != next {
				d.URL = next
				if e = s.SaveDeployment(ctx, d); e != nil {
					return e
				}
			}
			if d.State != "destroyed" {
				if e = runtime.Attach(ctx, d); e != nil {
					return e
				}
			}
		}
		service = &deployments.Service{Store: s, Config: c, Runtime: runtime, Fetch: git.Checkout, Log: log}
		if err = service.Publish(ctx); err != nil {
			return err
		}
	}
	a := &api.Server{Store: s, Config: c, Log: log, Deployments: service}
	server := &http.Server{Addr: c.Listen, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	worker := &operations.Worker{Store: s, Verify: git.Verify(c.StateDir), Log: log}
	if service != nil {
		worker.Handle = service.Handle
		worker.Failed = service.Failed
		workers.Add(1)
		go func() { defer workers.Done(); service.Run(ctx) }()
	}
	workers.Add(1)
	go func() { defer workers.Done(); errs <- worker.Run(ctx) }()
	go func() { errs <- server.Serve(listener) }()
	log.Info("Deployer API listening", "address", c.Listen, "version", version.Version, "state_dir", c.StateDir)
	select {
	case <-ctx.Done():
	case err = <-errs:
	}
	cancel()
	shutdownCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	_ = server.Shutdown(shutdownCtx)
	workers.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
