package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/gateway"
)

// GatewayOptions configures the gateway process.
type GatewayOptions struct {
	Listen        string
	ControlListen string
	TokenFile     string
	Scheme        string
}

// Gateway runs the preview gateway until ctx is cancelled. It normally runs
// inside the gateway container started by Serve.
func Gateway(ctx context.Context, opts GatewayOptions, log *slog.Logger) error {
	// The file is bind-mounted read-only; Docker may not preserve its mode.
	raw, err := os.ReadFile(opts.TokenFile)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(raw))
	if !domain.ValidTokenSecret(token) {
		return fmt.Errorf("%s does not contain a control token", opts.TokenFile)
	}
	if opts.Scheme != "http" && opts.Scheme != "https" {
		return fmt.Errorf("--scheme must be http or https")
	}
	g := gateway.New(gateway.Options{ControlToken: token, PublicScheme: opts.Scheme})
	public := &http.Server{Addr: opts.Listen, Handler: g.Public(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	control := &http.Server{Addr: opts.ControlListen, Handler: g.Control(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
	errs := make(chan error, 2)
	for _, s := range []*http.Server{public, control} {
		go func() { errs <- s.ListenAndServe() }()
	}
	log.Info("gateway listening", "public", opts.Listen, "control", opts.ControlListen)
	select {
	case <-ctx.Done():
		err = nil
	case err = <-errs:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	public.Shutdown(shutdown)
	control.Shutdown(shutdown)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
