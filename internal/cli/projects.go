package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/config"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/spf13/cobra"
)

func (a *app) projectCommand() *cobra.Command {
	cmd := group("project", "Manage registered repositories", "projects")
	var repo string
	add := &cobra.Command{
		Use:     "add NAME --repo URL",
		Short:   "Register a repository",
		Long:    "Register a Git repository. You usually don't need this: deployer deploy registers the current repository automatically.",
		Example: "  deployer project add shop --repo https://github.com/acme/shop",
		Args:    exactArgs(1, "NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if repo == "" {
				return usage("--repo is required")
			}
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			p, err := c.CreateProject(cmd.Context(), domain.CreateProjectRequest{Name: args[0], Repository: repo}, "")
			if err != nil {
				return err
			}
			return a.out.result(p, func() { a.out.success("Registered %s (%s)", p.Name, p.Repository) })
		},
	}
	add.Flags().StringVar(&repo, "repo", "", "HTTPS Git URL; GitHub SSH remotes are converted")

	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List projects",
		Args:    exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			list, err := c.Projects(cmd.Context())
			if err != nil {
				return err
			}
			return a.out.result(list, func() {
				if len(list) == 0 {
					a.out.line("No projects. Run %s inside a repository to register it.", a.out.st.command("deployer deploy"))
					return
				}
				rows := make([][]string, len(list))
				for i, p := range list {
					rows[i] = []string{a.out.st.bold(p.Name), a.out.st.muted(p.ID), p.Repository}
				}
				a.out.table([]string{"NAME", "ID", "REPOSITORY"}, rows)
			})
		},
	}

	show := &cobra.Command{
		Use:               "show NAME",
		ValidArgsFunction: a.completeProjects,
		Short:             "Show a project",
		Args:              exactArgs(1, "NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			p, err := c.Project(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.out.result(p, func() {
				a.out.fields([][2]string{{"Name", p.Name}, {"ID", p.ID}, {"Repository", p.Repository}, {"Created", a.out.when(&p.CreatedAt)}})
			})
		},
	}

	verify := &cobra.Command{
		Use:               "verify NAME",
		ValidArgsFunction: a.completeProjects,
		Short:             "Check that the server can read a project's repository",
		Args:              exactArgs(1, "NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			op, err := c.VerifyProject(cmd.Context(), args[0], "")
			if err != nil {
				return err
			}
			if op, err = a.waitOperation(cmd.Context(), c, op.ID, 2*time.Minute); err != nil {
				return err
			}
			return a.out.result(op, func() { a.out.success("The server can read %s", args[0]) })
		},
	}

	var yes bool
	remove := &cobra.Command{
		Use:               "remove NAME",
		ValidArgsFunction: a.completeProjects,
		Aliases:           []string{"rm"},
		Short:             "Unregister a project without live deployments",
		Args:              exactArgs(1, "NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return usage("removing a project deletes its history; pass --yes")
			}
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			p, err := c.DeleteProject(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.out.result(p, func() { a.out.success("Removed project %s", p.Name) })
		},
	}
	remove.Flags().BoolVarP(&yes, "yes", "y", false, "confirm removal")
	cmd.AddCommand(add, list, show, verify, remove)
	return cmd
}

var exposePattern = regexp.MustCompile(`(?mi)^\s*EXPOSE\s+(\d+)`)

func (a *app) initCommand() *cobra.Command {
	var port int
	var dockerfile, health string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a deployer.yaml for the current repository",
		Long: `Create a deployer.yaml in the current directory. The port is taken from the
Dockerfile's EXPOSE instruction unless --port is given.`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := filepath.Join(a.env.WorkDir, config.RecipeFile)
			if fileExists(path) {
				return usage("%s already exists", config.RecipeFile).WithHint("check it with: deployer validate")
			}
			df, err := os.ReadFile(filepath.Join(a.env.WorkDir, dockerfile))
			if errors.Is(err, os.ErrNotExist) {
				return usage("no %s found; Deployer builds your app from a Dockerfile", dockerfile).
					WithHint("add a Dockerfile, or point to one with --dockerfile")
			}
			if err != nil {
				return err
			}
			if port == 0 {
				if m := exposePattern.FindSubmatch(df); m != nil {
					port, _ = strconv.Atoi(string(m[1]))
				}
			}
			if a.canPrompt() {
				if err := a.askRecipe(cmd, &port, &health); err != nil {
					return err
				}
			}
			if port == 0 {
				return usage("could not find the app's port in %s", dockerfile).WithHint("pass it with --port 3000")
			}
			// Recipes use forward slashes on every platform.
			recipe := fmt.Sprintf(recipeTemplate, filepath.ToSlash(dockerfile), port, health)
			if _, err := config.ParseRecipe([]byte(recipe)); err != nil {
				return usage("%v", err)
			}
			if err := os.WriteFile(path, []byte(recipe), 0o644); err != nil {
				return err
			}
			return a.out.result(map[string]string{"path": path}, func() {
				st := a.out.st
				a.out.success("Created %s (port %d, health check %s)", config.RecipeFile, port, health)
				a.out.line("\nNext:\n  1. Make sure the app listens on %s and answers %s\n  2. Commit and push %s\n  3. Run %s",
					st.bold(fmt.Sprintf("0.0.0.0:%d", port)), st.bold(health), st.bold(config.RecipeFile), st.command("deployer deploy"))
			})
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "port the app listens on inside the container")
	cmd.Flags().StringVar(&dockerfile, "dockerfile", "Dockerfile", "path of the Dockerfile")
	cmd.Flags().StringVar(&health, "health", "/health", "path that returns a success status once the app is ready")
	return cmd
}

// askRecipe asks for the port and health check path that flags did not set,
// suggesting what was found in the Dockerfile.
func (a *app) askRecipe(cmd *cobra.Command, port *int, health *string) error {
	var fields []Field
	portText := ""
	if !cmd.Flags().Changed("port") {
		if *port != 0 {
			portText = strconv.Itoa(*port)
		}
		fields = append(fields, Field{
			Title: "Port", Description: "The port your app listens on inside the container", Value: &portText,
			Validate: func(v string) error {
				if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 65535 {
					return fmt.Errorf("enter a port between 1 and 65535")
				}
				return nil
			},
		})
	}
	if !cmd.Flags().Changed("health") {
		fields = append(fields, Field{
			Title: "Health check", Description: "A path that answers once the app is ready; / works for static sites", Value: health,
			Validate: func(v string) error {
				if !strings.HasPrefix(v, "/") {
					return fmt.Errorf("start the path with /")
				}
				return nil
			},
		})
	}
	if len(fields) == 0 {
		return nil
	}
	if err := a.ask(fields...); err != nil {
		return err
	}
	if portText != "" {
		*port, _ = strconv.Atoi(portText)
	}
	return nil
}

const recipeTemplate = `# yaml-language-server: $schema=https://raw.githubusercontent.com/filstawski/deployer/main/schemas/deployer.schema.json
# Deployer project recipe: https://github.com/filstawski/deployer/blob/main/docs/projects.md
version: 1
source:
  dockerfile: %s
  context: .
http:
  port: %d
  healthcheck:
    path: %s
    timeout: 90s
# environment:
#   NODE_ENV: production
`

func (a *app) validateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [DIR]",
		Short: "Check a repository's deployer.yaml",
		Args:  maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := a.env.WorkDir
			if len(args) == 1 {
				dir = args[0]
			}
			r, err := config.LoadRecipe(dir)
			if err != nil {
				return usage("%v", err)
			}
			summary := map[string]any{"valid": true, "dockerfile": r.Source.Dockerfile, "context": r.Source.Context,
				"port": r.HTTP.Port, "health_path": r.HTTP.Healthcheck.Path, "health_timeout": r.HTTP.Healthcheck.Timeout.D().String()}
			return a.out.result(summary, func() {
				a.out.success("%s is valid", config.RecipeFile)
				a.out.fields([][2]string{{"  Dockerfile", r.Source.Dockerfile}, {"  Context", r.Source.Context},
					{"  Port", strconv.Itoa(r.HTTP.Port)}, {"  Health check", r.HTTP.Healthcheck.Path + " within " + domain.FormatDuration(r.HTTP.Healthcheck.Timeout.D())}})
			})
		},
	}
}
