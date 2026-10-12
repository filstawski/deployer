package cli

import (
	"context"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/client"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/secretfile"
	"github.com/spf13/cobra"
)

func (a *app) tokenCommand() *cobra.Command {
	cmd := group("token", "Manage API tokens (admin)", "tokens")
	var role, scopes, ttl, output string
	var projects []string
	create := &cobra.Command{
		Use:   "create NAME",
		Short: "Create an API token",
		Long: `Create an API token. The secret is shown once; pass it to deployer login on
the machine that will use it.

Roles:
  deployer  deploy and manage previews, register projects (default)
  viewer    read-only access to projects and deployments
  admin     everything, including tokens and GitHub setup`,
		Example: `  deployer token create laptop
  deployer token create ci --project shop --ttl 30d
  deployer token create readonly --role viewer --output viewer.token`,
		Args: exactArgs(1, "NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			req := domain.CreateTokenRequest{Name: args[0], Scopes: domain.Roles[role]}
			if scopes != "" {
				var err error
				if req.Scopes, err = domain.ParseScopes(scopes); err != nil {
					return usage("--scopes: %v", err)
				}
			} else if req.Scopes == nil {
				return usage("unknown role %q (choose %s)", role, strings.Join(domain.RoleNames(), ", "))
			}
			if ttl != "never" {
				d, err := domain.ParseDuration(ttl)
				if err != nil {
					return usage("--ttl: %v", err)
				}
				req.TTLSeconds = int64(d.Seconds())
			}
			c, err := a.client(ctx)
			if err != nil {
				return err
			}
			if req.Projects, err = projectIDs(ctx, c, projects); err != nil {
				return err
			}
			if output != "" && fileExists(output) {
				return usage("%s already exists", output)
			}
			created, err := c.CreateToken(ctx, req)
			if err != nil {
				return err
			}
			if output != "" {
				if err := secretfile.WriteNew(output, created.Secret); err != nil {
					c.RevokeToken(ctx, created.Token.ID)
					return err
				}
				created.Secret = ""
			}
			return a.out.result(created, func() {
				a.out.line("✓ Created token %s (%s)", created.Token.Name, created.Token.ID)
				if output != "" {
					a.out.line("  Saved to %s (mode 600)", output)
					return
				}
				a.out.line("\n  %s\n", created.Secret)
				a.out.line("This secret is shown once. Use it with: deployer login %s", a.loginURL(ctx, c))
			})
		},
	}
	f := create.Flags()
	f.StringVar(&role, "role", "deployer", "token role: deployer, viewer, or admin")
	f.StringVar(&scopes, "scopes", "", "comma-separated scopes instead of a role")
	f.StringSliceVarP(&projects, "project", "p", nil, "limit the token to these projects (default: all)")
	f.StringVar(&ttl, "ttl", "90d", `lifetime, such as 30d, or "never"`)
	f.StringVarP(&output, "output", "o", "", "write the secret to this new file instead of printing it")

	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List API tokens",
		Args:    exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			tokens, err := c.Tokens(cmd.Context())
			if err != nil {
				return err
			}
			return a.out.result(tokens, func() {
				rows := make([][]string, len(tokens))
				now := a.env.Now()
				for i, t := range tokens {
					state := "active"
					switch {
					case t.RevokedAt != nil:
						state = "revoked"
					case !t.Active(now):
						state = "expired"
					}
					rows[i] = []string{t.ID, t.Name, domain.FormatScopes(t.Scopes), strings.Join(t.Projects, ","), expiry(t.ExpiresAt, a.out), state}
				}
				a.out.table([]string{"ID", "NAME", "SCOPES", "PROJECTS", "EXPIRES", "STATE"}, rows)
			})
		},
	}

	revoke := &cobra.Command{
		Use:   "revoke ID",
		Short: "Revoke an API token immediately",
		Args:  exactArgs(1, "ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			t, err := c.RevokeToken(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.out.result(t, func() { a.out.line("✓ Revoked %s (%s)", t.Name, t.ID) })
		},
	}
	cmd.AddCommand(create, list, revoke)
	return cmd
}

func expiry(t *time.Time, p *printer) string {
	if t == nil {
		return "never"
	}
	return p.shortWhen(t)
}

// projectIDs resolves project names to IDs for token grants.
func projectIDs(ctx context.Context, c *client.Client, refs []string) ([]string, error) {
	if len(refs) == 0 {
		return []string{domain.AllProjects}, nil
	}
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref == domain.AllProjects {
			ids = append(ids, ref)
			continue
		}
		p, err := c.Project(ctx, ref)
		if err != nil {
			return nil, err
		}
		ids = append(ids, p.ID)
	}
	return ids, nil
}

// loginURL suggests the URL to log in with a token created on this server.
func (a *app) loginURL(ctx context.Context, c *client.Client) string {
	if strings.HasPrefix(c.BaseURL(), "https://") {
		return c.BaseURL()
	}
	if s, err := c.Status(ctx); err == nil && s.APIURL != "" {
		return s.APIURL
	}
	return "ssh://root@THIS-SERVER"
}
