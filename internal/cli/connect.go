package cli

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"

	"github.com/filstawski/deployer/internal/client"
	"github.com/filstawski/deployer/internal/config"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/secretfile"
	"github.com/filstawski/deployer/internal/sshtunnel"
	"github.com/filstawski/deployer/internal/version"
)

// connection is a resolved server connection.
type connection struct {
	// Source describes where the connection came from, for status output.
	Source  string
	URL     string
	APIPort int
	Token   string
}

// resolveConnection picks the server to talk to, in order of precedence:
// DEPLOYER_URL with DEPLOYER_TOKEN or DEPLOYER_TOKEN_FILE; the --context flag
// or DEPLOYER_CONTEXT; the current context; and, as root on a Deployer server,
// the local administrator token.
func (a *app) resolveConnection() (connection, error) {
	env := a.env
	if u := env.Getenv("DEPLOYER_URL"); u != "" {
		token, err := tokenFromEnv(env)
		return connection{Source: "environment", URL: u, Token: token}, err
	}
	if env.Getenv("DEPLOYER_TOKEN") != "" || env.Getenv("DEPLOYER_TOKEN_FILE") != "" {
		return connection{}, domain.Errorf(domain.CodeInvalidInput, "DEPLOYER_TOKEN and DEPLOYER_TOKEN_FILE need DEPLOYER_URL")
	}
	saved, err := env.loadContexts()
	if err != nil {
		return connection{}, err
	}
	name := a.contextName
	if name == "" {
		name = env.Getenv("DEPLOYER_CONTEXT")
	}
	if name == "" {
		name = saved.Current
	}
	if name != "" {
		ctx, err := saved.get(name)
		if err != nil {
			return connection{}, err
		}
		token, err := secretfile.ReadToken(ctx.TokenFile)
		if err != nil {
			return connection{}, domain.Errorf(domain.CodeUnauthenticated, "cannot read the token for context %q: %v", name, err).
				WithHint("log in again: deployer login %s", ctx.URL)
		}
		return connection{Source: "context " + name, URL: ctx.URL, APIPort: ctx.APIPort, Token: token}, nil
	}
	if env.ServerConfig != "" {
		if cfg, err := config.LoadServer(env.ServerConfig); err == nil {
			if token, err := secretfile.ReadToken(cfg.AdminTokenPath()); err == nil {
				return connection{Source: "local server", URL: "http://" + cfg.API.Listen, Token: token}, nil
			}
		}
	}
	return connection{}, domain.Errorf(domain.CodeUnauthenticated, "not logged in to a Deployer server").
		WithHint("run: deployer login https://api.example.com")
}

func tokenFromEnv(env Env) (string, error) {
	if s := strings.TrimSpace(env.Getenv("DEPLOYER_TOKEN")); s != "" {
		if !domain.ValidTokenSecret(s) {
			return "", domain.Errorf(domain.CodeInvalidInput, "DEPLOYER_TOKEN is not a Deployer token (dpl_...)")
		}
		return s, nil
	}
	if path := env.Getenv("DEPLOYER_TOKEN_FILE"); path != "" {
		return secretfile.ReadToken(path)
	}
	return "", domain.Errorf(domain.CodeInvalidInput, "DEPLOYER_URL needs DEPLOYER_TOKEN or DEPLOYER_TOKEN_FILE")
}

// dial opens a client for a connection, starting an SSH tunnel for ssh://
// URLs. The returned function releases the tunnel.
func dial(ctx context.Context, conn connection) (*client.Client, func(), error) {
	opts := client.Options{}
	base := conn.URL
	closeFn := func() {}
	if strings.HasPrefix(conn.URL, "ssh://") {
		target, err := sshtunnel.Parse(conn.URL)
		if err != nil {
			return nil, nil, domain.Errorf(domain.CodeInvalidInput, "%v", err)
		}
		tunnel, err := sshtunnel.Open(ctx, target, conn.APIPort)
		if err != nil {
			return nil, nil, domain.Errorf(domain.CodeUnavailable, "%v", err)
		}
		opts.Transport, base, closeFn = tunnel.Transport, "http://deployer", tunnel.Close
	}
	c, err := client.New(base, conn.Token, opts)
	if err != nil {
		closeFn()
		return nil, nil, err
	}
	c.UserAgent = "deployer/" + version.Version
	return c, closeFn, nil
}

// client returns the client for this invocation, connecting on first use.
func (a *app) client(ctx context.Context) (*client.Client, error) {
	if a.api != nil {
		return a.api, nil
	}
	conn, err := a.resolveConnection()
	if err != nil {
		return nil, err
	}
	c, closeFn, err := dial(ctx, conn)
	if err != nil {
		return nil, err
	}
	a.api, a.conn = c, conn
	a.cleanup = append(a.cleanup, closeFn)
	return c, nil
}

// defaultContextName derives a context name from a server URL.
func defaultContextName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "default"
	}
	return u.Hostname()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}
