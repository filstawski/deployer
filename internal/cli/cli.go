// Package cli implements the deployer command-line interface.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/colorprofile"
	"github.com/filstawski/deployer/internal/client"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/spf13/cobra"
)

// Exit codes are part of the CLI's contract with scripts and agents.
const (
	ExitOK          = 0
	ExitError       = 1 // The request or operation failed.
	ExitUsage       = 2 // Invalid command line or input.
	ExitAuth        = 3 // Missing, invalid, or insufficient credentials.
	ExitWaitTimeout = 4 // Stopped waiting; the operation continues.
	ExitNotFound    = 5 // The named resource does not exist.
)

// app holds the state of one invocation.
type app struct {
	env         Env
	json        bool
	quiet       bool
	contextName string
	api         *client.Client
	conn        connection
	cleanup     []func()
	out         *printer
	// stderr carries progress, warnings, and errors; errs styles them.
	stderr io.Writer
	errs   styles
}

// Run executes the CLI and returns its exit code.
func Run(ctx context.Context, env Env, args []string) int {
	a := &app{env: env}
	// The writers strip colors where the output is not a color terminal, or
	// NO_COLOR is set.
	stdout := colorprofile.NewWriter(env.Stdout, env.Environ)
	a.stderr = colorprofile.NewWriter(env.Stderr, env.Environ)
	a.out = &printer{w: stdout, now: env.Now}
	defer func() {
		for _, fn := range a.cleanup {
			fn()
		}
	}()
	root := a.rootCommand()
	root.SetArgs(args)
	root.SetIn(env.Stdin)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	var code exitCode
	if errors.As(err, &code) {
		return int(code)
	}
	if _, ok := domain.AsError(err); !ok && strings.HasPrefix(err.Error(), "unknown command") {
		err = usage("%s", strings.TrimSpace(err.Error())).WithHint("see: deployer --help")
	}
	return a.fail(err)
}

// group returns a command that only holds subcommands. Run bare, it shows its
// help; given an unknown subcommand, it fails with a usage error instead of
// printing help and succeeding.
func group(use, short string, aliases ...string) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Aliases: aliases,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usage("unknown command %q for %q", args[0], cmd.CommandPath()).WithHint("see: %s --help", cmd.CommandPath())
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
}

func (a *app) rootCommand() *cobra.Command {
	cobra.EnableCommandSorting = false // List commands in workflow order.
	root := &cobra.Command{
		Use:   "deployer",
		Short: "Deploy Git branches as temporary previews",
		Long: `Deployer turns a Git branch into an isolated preview at its own HTTPS URL,
and removes it when its lifetime ends.

Get started:
  deployer login https://api.example.com   Connect to your Deployer server
  deployer deploy                          Deploy the current branch
  deployer update                          Rebuild its preview after you push
  deployer list                            See your previews

Working with an AI agent? deployer skill prints a guide written for it.`,
		SilenceUsage:      true,
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			a.out.json = a.json
			a.out.width = a.env.Width
			a.out.st = styles{enabled: a.env.Terminal && !a.json, links: a.env.supportsLinks()}
			a.errs = styles{enabled: a.env.Interactive && !a.json}
		},
	}
	root.PersistentFlags().BoolVar(&a.json, "json", false, "print machine-readable JSON to stdout")
	root.PersistentFlags().BoolVarP(&a.quiet, "quiet", "q", false, "do not print progress to stderr")
	root.PersistentFlags().StringVar(&a.contextName, "context", "", "use a saved server context (default: the current one)")
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return domain.Errorf(domain.CodeInvalidInput, "%v", err).WithHint("see: %s --help", cmd.CommandPath())
	})

	root.AddGroup(
		&cobra.Group{ID: "deploy", Title: "Deployments:"},
		&cobra.Group{ID: "project", Title: "Projects:"},
		&cobra.Group{ID: "access", Title: "Access:"},
		&cobra.Group{ID: "server", Title: "Server administration:"},
	)
	add := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			root.AddCommand(c)
		}
	}
	add("deploy", a.deployCommand(), a.updateCommand(), a.listCommand(), a.inspectCommand(), a.logsCommand(), a.openCommand(),
		a.stopCommand(), a.startCommand(), a.extendCommand(), a.destroyCommand(), a.operationCommand())
	add("project", a.projectCommand(), a.initCommand(), a.validateCommand())
	add("access", a.loginCommand(), a.logoutCommand(), a.contextCommand(), a.statusCommand(), a.tokenCommand(), a.githubCommand())
	add("server", a.serverCommand())
	root.AddCommand(a.skillCommand(), a.upgradeCommand(), a.versionCommand())
	return root
}

// fail reports err and returns its exit code. In JSON mode the error is
// written to stdout as {"error": {...}} so that callers parse one stream.
func (a *app) fail(err error) int {
	e, ok := domain.AsError(err)
	if !ok {
		if errors.Is(err, context.Canceled) {
			e = domain.Errorf(domain.CodeClient, "interrupted")
		} else {
			e = &domain.Error{Code: domain.CodeClient, Message: err.Error()}
		}
	}
	switch {
	case a.json:
		printJSON(a.out.w, map[string]any{"error": e})
	case a.errs.enabled:
		p := panel{title: a.errs.bad("✗ ") + "Error", accent: colorBad, lines: []string{e.Message}}
		if e.Hint != "" {
			p.notes = []string{a.errs.muted("Hint: ") + e.Hint}
		}
		fmt.Fprintln(a.stderr, p.render(a.errs, a.env.Width))
	default:
		fmt.Fprintf(a.stderr, "Error: %s\n", e.Message)
		if e.Hint != "" {
			fmt.Fprintf(a.stderr, "Hint:  %s\n", e.Hint)
		}
	}
	switch e.Code {
	case domain.CodeInvalidInput:
		return ExitUsage
	case domain.CodeUnauthenticated, domain.CodeForbidden:
		return ExitAuth
	case domain.CodeWaitTimeout:
		return ExitWaitTimeout
	case domain.CodeNotFound:
		return ExitNotFound
	default:
		return ExitError
	}
}

// exitCode ends the CLI with a status without printing anything, for
// commands that delegated to another process, which reported already.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// usage returns an INVALID_INPUT error for command-line mistakes.
func usage(format string, args ...any) *domain.Error {
	return domain.Errorf(domain.CodeInvalidInput, format, args...)
}

// progress prints a status line to stderr unless output is quiet.
func (a *app) progress(format string, args ...any) {
	if !a.quiet {
		fmt.Fprintf(a.stderr, format+"\n", args...)
	}
}

// warn prints a warning to stderr unless output is quiet.
func (a *app) warn(format string, args ...any) {
	a.progress("%s %s", a.errs.warn("▲ Warning:"), fmt.Sprintf(format, args...))
}

// done prints a completed step to stderr unless output is quiet.
func (a *app) done(format string, args ...any) {
	a.progress("%s %s", a.errs.ok("✓"), fmt.Sprintf(format, args...))
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run()
	default:
		return exec.Command("xdg-open", url).Run()
	}
}

// exactArgs is cobra.ExactArgs with an actionable usage error.
func exactArgs(n int, names ...string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usage("%s expects %s", cmd.CommandPath(), strings.Join(names, " ")).WithHint("see: %s --help", cmd.CommandPath())
		}
		return nil
	}
}

// maxArgs is cobra.MaximumNArgs with an actionable usage error.
func maxArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return usage("%s takes at most %d argument(s)", cmd.CommandPath(), n).WithHint("see: %s --help", cmd.CommandPath())
		}
		return nil
	}
}
