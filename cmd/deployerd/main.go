// Command deployerd is the Deployer controller. It also runs the preview
// gateway inside the gateway container.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/filstawski/deployer/internal/config"
	"github.com/filstawski/deployer/internal/container/docker"
	"github.com/filstawski/deployer/internal/daemon"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/secretfile"
	"github.com/filstawski/deployer/internal/store"
	"github.com/filstawski/deployer/internal/version"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := root().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "deployerd:", err)
		os.Exit(1)
	}
}

func logger() *slog.Logger {
	if term.IsTerminal(int(os.Stderr.Fd())) {
		return slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, nil))
}

func root() *cobra.Command {
	root := &cobra.Command{
		Use:           "deployerd",
		Short:         "The Deployer controller",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	var configPath string

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Run the controller",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.LoadServer(configPath)
			if err != nil {
				return err
			}
			return daemon.Serve(cmd.Context(), cfg, logger())
		},
	}
	serve.Flags().StringVar(&configPath, "config", config.DefaultServerPath, "server configuration file")

	var gw daemon.GatewayOptions
	gateway := &cobra.Command{
		Use:    "gateway",
		Short:  "Run the preview gateway (inside its container)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return daemon.Gateway(cmd.Context(), gw, logger())
		},
	}
	gateway.Flags().StringVar(&gw.Listen, "listen", "0.0.0.0:8080", "visitor traffic address")
	gateway.Flags().StringVar(&gw.ControlListen, "control-listen", "0.0.0.0:8081", "control API address")
	gateway.Flags().StringVar(&gw.TokenFile, "token-file", "/run/deployer/gateway.token", "control API token file")
	gateway.Flags().StringVar(&gw.Scheme, "scheme", "https", "scheme visitors use, sent as X-Forwarded-Proto")

	var name, role, output string
	var ttl time.Duration
	tokenCreate := &cobra.Command{
		Use:   "create NAME --output FILE",
		Short: "Create an API token directly in the database (recovery)",
		Long: `Create an API token without the API, for example when every administrator
token has been lost. Run it as the service account, or stop the service first
if the database is busy.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name = args[0]
			cfg, err := config.LoadServer(configPath)
			if err != nil {
				return err
			}
			scopes, ok := domain.Roles[role]
			if !ok {
				return fmt.Errorf("unknown role %q", role)
			}
			st, err := store.Open(cfg.DatabasePath())
			if err != nil {
				return err
			}
			defer st.Close()
			tok, secret, err := st.CreateToken(cmd.Context(), store.NewToken{Name: name, Scopes: scopes, Projects: []string{domain.AllProjects}, TTL: ttl, CreatedBy: "deployerd"})
			if err != nil {
				return err
			}
			if err := secretfile.WriteNew(output, secret); err != nil {
				st.RevokeToken(cmd.Context(), tok.ID, "deployerd")
				return err
			}
			fmt.Printf("Created token %s; the secret is in %s\n", tok.ID, output)
			return nil
		},
	}
	tokenCreate.Flags().StringVar(&configPath, "config", config.DefaultServerPath, "server configuration file")
	tokenCreate.Flags().StringVar(&role, "role", "admin", "token role: admin, deployer, or viewer")
	tokenCreate.Flags().StringVar(&output, "output", "", "new file for the secret (required)")
	tokenCreate.Flags().DurationVar(&ttl, "ttl", 0, "lifetime; 0 never expires")
	tokenCreate.MarkFlagRequired("output")
	token := &cobra.Command{Use: "token", Short: "Manage API tokens offline"}
	token.AddCommand(tokenCreate)

	cleanup := &cobra.Command{
		Use:    "cleanup",
		Short:  "Remove every Docker resource of this installation (used by uninstall)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.LoadServer(configPath)
			if err != nil {
				return err
			}
			st, err := store.Open(cfg.DatabasePath())
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := st.InstallationID(cmd.Context())
			if err != nil {
				return err
			}
			return docker.New(docker.Options{InstallationID: id, StateDir: cfg.StateDir}).RemoveAll(cmd.Context())
		},
	}
	cleanup.Flags().StringVar(&configPath, "config", config.DefaultServerPath, "server configuration file")

	ver := &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run:   func(*cobra.Command, []string) { fmt.Println(version.Version) },
	}
	root.AddCommand(serve, gateway, token, cleanup, ver)
	return root
}
