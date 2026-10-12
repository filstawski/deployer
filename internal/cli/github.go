package cli

import (
	"strings"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/spf13/cobra"
)

func (a *app) githubCommand() *cobra.Command {
	cmd := group("github", "Connect private GitHub repositories")
	cmd.Long = `Connect this server to GitHub through a GitHub App, so it can fetch private
repositories. An administrator creates the App once with deployer github setup;
anyone can then install it on the accounts and repositories to deploy.`
	var publicURL, org string
	var noBrowser bool
	setup := &cobra.Command{
		Use:   "setup",
		Short: "Create the server's GitHub App (admin, once)",
		Long: `Create the GitHub App this server uses to read private repositories. This
prints a single-use link that opens a page with a "Create GitHub App" button;
it must be opened in a browser within 10 minutes. GitHub then sends you back to
the server, which stores the App's key and forwards you to choose repositories.

The server's API must be reachable at --public-url over HTTPS for the callback.`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			link, err := c.GitHubSetup(cmd.Context(), domain.GitHubSetupRequest{PublicURL: publicURL, Organization: org})
			if err != nil {
				return err
			}
			a.maybeOpen(link.URL, noBrowser)
			return a.out.result(link, func() {
				a.out.line("Open this link in your browser within 10 minutes:\n\n  %s\n", link.URL)
				a.out.line("1. Click \"Create GitHub App\" and confirm on GitHub (rename it if the name is taken).")
				a.out.line("2. Choose the accounts and repositories Deployer may read.")
				a.out.line("Check the result with: deployer github status")
			})
		},
	}
	setup.Flags().StringVar(&publicURL, "public-url", "", "HTTPS origin of this server's API (default: https://api.<domain>)")
	setup.Flags().StringVar(&org, "org", "", "create the App under this GitHub organization")
	setup.Flags().BoolVar(&noBrowser, "no-browser", false, "print the link without opening a browser")

	var noBrowserConnect bool
	connect := &cobra.Command{
		Use:   "connect",
		Short: "Grant access to more accounts or repositories",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			status, err := c.GitHub(cmd.Context())
			if err != nil {
				return err
			}
			if !status.Configured {
				return domain.Errorf(domain.CodeGitHubNotConfigured, "GitHub is not connected to this server").WithHint("an administrator runs: deployer github setup")
			}
			a.maybeOpen(status.InstallURL, noBrowserConnect)
			return a.out.result(map[string]string{"url": status.InstallURL}, func() {
				a.out.line("Install the GitHub App on the account that owns the repository:\n\n  %s\n", status.InstallURL)
				a.out.line("To change an existing installation, use Configure on that page.")
			})
		},
	}
	connect.Flags().BoolVar(&noBrowserConnect, "no-browser", false, "print the link without opening a browser")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show where the GitHub App is installed",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			s, err := c.GitHub(cmd.Context())
			if err != nil {
				return err
			}
			return a.out.result(s, func() {
				if !s.Configured {
					a.out.line("GitHub is not connected. Public repositories still work.")
					a.out.line("To deploy private repositories, an administrator runs: deployer github setup")
					return
				}
				a.out.fields([][2]string{{"App", s.App}, {"Install", s.InstallURL}})
				if len(s.Installations) == 0 {
					a.out.line("\nNot installed on any account yet. Run: deployer github connect")
					return
				}
				a.out.line("")
				rows := make([][]string, len(s.Installations))
				for i, in := range s.Installations {
					state := a.out.st.ok("active")
					if in.Suspended {
						state = a.out.st.warn("suspended")
					}
					rows[i] = []string{a.out.st.bold(in.Account), strings.ReplaceAll(in.Selection, "_", " "), state}
				}
				a.out.table([]string{"ACCOUNT", "REPOSITORIES", "STATE"}, rows)
			})
		},
	}
	cmd.AddCommand(setup, connect, status)
	return cmd
}

func (a *app) maybeOpen(url string, disabled bool) {
	if disabled || a.json || !a.env.Interactive || a.env.OpenBrowser == nil {
		return
	}
	if err := a.env.OpenBrowser(url); err != nil {
		a.progress("Could not open a browser: %v", err)
	}
}
