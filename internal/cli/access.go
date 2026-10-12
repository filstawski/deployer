package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/secretfile"
	"github.com/filstawski/deployer/internal/version"
	"github.com/spf13/cobra"
)

func (a *app) loginCommand() *cobra.Command {
	var name string
	var stdin bool
	var apiPort int
	cmd := &cobra.Command{
		Use:   "login URL",
		Short: "Connect to a Deployer server",
		Long: `Connect to a Deployer server and save it as a context. You are prompted for
an API token; an administrator creates one with deployer token create.

URL is the server's HTTPS API, such as https://api.example.com, or an SSH
destination such as ssh://root@server.example.com to reach a server whose API
is not exposed publicly.`,
		Example: `  deployer login https://api.example.com
  echo "$DEPLOYER_TOKEN" | deployer login https://api.example.com --token-stdin
  deployer login ssh://root@server.example.com`,
		Args: exactArgs(1, "URL"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			url := strings.TrimRight(args[0], "/")
			if name == "" {
				name = defaultContextName(url)
			}
			if !contextName.MatchString(name) {
				return usage("invalid context name %q", name)
			}
			token, err := a.readToken(stdin)
			if err != nil {
				return err
			}
			conn := connection{URL: url, APIPort: apiPort, Token: token}
			c, closeFn, err := dial(ctx, conn)
			if err != nil {
				return err
			}
			defer closeFn()
			status, err := c.Status(ctx)
			if err != nil {
				return err
			}
			path := a.env.tokenPath(name)
			if err := secretfile.Replace(path, token); err != nil {
				return err
			}
			saved, err := a.env.loadContexts()
			if err != nil {
				return err
			}
			saved.Contexts[name] = &savedContext{URL: url, APIPort: apiPort, TokenFile: path}
			saved.Current = name
			if err := a.env.saveContexts(saved); err != nil {
				return err
			}
			return a.out.result(map[string]any{"context": name, "url": url, "server": status}, func() {
				a.out.line("✓ Logged in to %s as %s (context %q)", url, status.Token.Name, name)
				a.out.line("  Previews are served at %s", strings.ReplaceAll(status.PreviewURL, "{name}", "NAME"))
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "context name (default: the server's hostname)")
	cmd.Flags().BoolVar(&stdin, "token-stdin", false, "read the token from standard input")
	cmd.Flags().IntVar(&apiPort, "api-port", 0, "server's loopback API port, for ssh:// URLs (default 7420)")
	return cmd
}

func (a *app) readToken(stdin bool) (string, error) {
	var token string
	switch {
	case stdin:
		line, err := bufio.NewReader(a.env.Stdin).ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		token = line
	case a.env.Interactive && a.env.ReadSecret != nil:
		var err error
		if token, err = a.env.ReadSecret("API token: "); err != nil {
			return "", err
		}
	default:
		return "", usage("no terminal to prompt for a token").WithHint("pipe it in: deployer login URL --token-stdin")
	}
	token = strings.TrimSpace(token)
	if !domain.ValidTokenSecret(token) {
		return "", usage("that is not a Deployer API token (they start with dpl_)")
	}
	return token, nil
}

func (a *app) logoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout [CONTEXT]",
		Short: "Forget a saved server and its token",
		Args:  maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			saved, err := a.env.loadContexts()
			if err != nil {
				return err
			}
			name := saved.Current
			if len(args) == 1 {
				name = args[0]
			}
			if name == "" {
				return usage("not logged in")
			}
			ctx, err := saved.get(name)
			if err != nil {
				return err
			}
			delete(saved.Contexts, name)
			if saved.Current == name {
				saved.Current = ""
				if names := saved.names(); len(names) > 0 {
					saved.Current = names[0]
				}
			}
			if err := a.env.saveContexts(saved); err != nil {
				return err
			}
			os.Remove(ctx.TokenFile)
			return a.out.result(map[string]string{"removed": name, "current": saved.Current}, func() {
				a.out.line("✓ Logged out of %s", name)
				if saved.Current != "" {
					a.out.line("  Current context is now %s", saved.Current)
				}
			})
		},
	}
}

func (a *app) contextCommand() *cobra.Command {
	cmd := group("context", "Switch between saved servers")
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List saved servers",
		Args:    exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			saved, err := a.env.loadContexts()
			if err != nil {
				return err
			}
			return a.out.result(saved, func() {
				if len(saved.Contexts) == 0 {
					a.out.line("No saved servers. Connect with: deployer login https://api.example.com")
					return
				}
				var rows [][]string
				for _, name := range saved.names() {
					current := ""
					if name == saved.Current {
						current = "*"
					}
					rows = append(rows, []string{current, name, saved.Contexts[name].URL})
				}
				a.out.table([]string{"", "NAME", "URL"}, rows)
			})
		},
	}
	use := &cobra.Command{
		Use:   "use NAME",
		Short: "Make a saved server the current one",
		Args:  exactArgs(1, "NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			saved, err := a.env.loadContexts()
			if err != nil {
				return err
			}
			if _, err := saved.get(args[0]); err != nil {
				return err
			}
			saved.Current = args[0]
			if err := a.env.saveContexts(saved); err != nil {
				return err
			}
			return a.out.result(map[string]string{"current": args[0]}, func() { a.out.line("✓ Using %s", args[0]) })
		},
	}
	cmd.AddCommand(list, use)
	return cmd
}

func (a *app) statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the server you are connected to",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			s, err := c.Status(cmd.Context())
			if err != nil {
				return err
			}
			return a.out.result(s, func() {
				github := "not connected (deployer github setup)"
				if s.GitHub {
					github = "connected"
				}
				a.out.fields([][2]string{
					{"Server", c.BaseURL()}, {"Using", a.conn.Source},
					{"Version", fmt.Sprintf("%s (CLI %s)", s.Version, version.Version)},
					{"Previews", strings.ReplaceAll(s.PreviewURL, "{name}", "NAME")},
					{"Deployments", fmt.Sprintf("%d of %d", s.Deployments.Live, s.Deployments.Max)},
					{"Lifetime", fmt.Sprintf("%s by default, up to %s", domain.FormatDuration(time.Duration(s.Deployments.DefaultTTL)*time.Second), domain.FormatDuration(time.Duration(s.Deployments.MaxTTL)*time.Second))},
					{"GitHub", github},
					{"Token", tokenSummary(s.Token, a.out)},
				})
				if s.Version != version.Version {
					a.out.line("\nThe CLI and server versions differ. Upgrade the older one with: deployer upgrade")
				}
			})
		},
	}
}

func tokenSummary(t domain.Token, p *printer) string {
	s := t.Name + " (" + domain.FormatScopes(t.Scopes) + "; projects " + strings.Join(t.Projects, ",")
	if t.ExpiresAt != nil {
		s += "; expires " + relative(*t.ExpiresAt, p.now())
	}
	return s + ")"
}

func (a *app) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := map[string]string{"version": version.Version}
			return a.out.result(v, func() { a.out.line("deployer %s", version.Version) })
		},
	}
}
