package cli

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/client"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/spf13/cobra"
)

func (a *app) listCommand() *cobra.Command {
	var project string
	var all bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List deployments",
		Args:    exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			list, err := c.Deployments(cmd.Context(), client.ListOptions{Project: project, All: all})
			if err != nil {
				return err
			}
			return a.out.result(list, func() {
				if len(list) == 0 {
					a.out.line("No deployments. Create one with: deployer deploy")
					return
				}
				rows := make([][]string, len(list))
				for i, d := range list {
					rows[i] = []string{d.Name, d.Project, d.Ref, string(d.State), a.out.shortWhen(d.ExpiresAt), d.URL}
				}
				a.out.table([]string{"NAME", "PROJECT", "REF", "STATE", "EXPIRES", "URL"}, rows)
			})
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "only this project")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include destroyed deployments")
	return cmd
}

func (a *app) inspectCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "inspect NAME",
		Aliases: []string{"show"},
		Short:   "Show a deployment's details",
		Args:    exactArgs(1, "NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			d, err := c.Deployment(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.out.result(d, func() { a.printDeployment(d) })
		},
	}
}

func (a *app) printDeployment(d domain.Deployment) {
	pairs := [][2]string{
		{"Name", d.Name}, {"ID", d.ID}, {"Project", d.Project}, {"URL", d.URL},
		{"State", string(d.State)}, {"Ref", refAt(d)},
		{"Created", a.out.when(&d.CreatedAt)}, {"Ready", a.out.when(d.ReadyAt)},
		{"Expires", a.out.when(d.ExpiresAt)}, {"Removed", a.out.when(d.PurgeAt)},
		{"Error", errorText(d.Error)},
	}
	if d.ExpiresAt == nil && d.State.InProgress() {
		pairs = append(pairs, [2]string{"Lifetime", domain.FormatDuration(d.TTL()) + " once ready"})
	}
	a.out.fields(pairs)
	if d.Error != nil && d.Error.Hint != "" {
		a.out.line("\n%s", d.Error.Hint)
	}
}

func (a *app) logsCommand() *cobra.Command {
	var build, follow bool
	var lines int
	cmd := &cobra.Command{
		Use:   "logs NAME",
		Short: "Show a deployment's app or build output",
		Example: `  deployer logs design-a
  deployer logs design-a --build --follow`,
		Args: exactArgs(1, "NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if follow && !build {
				return usage("--follow works with --build")
			}
			ctx := cmd.Context()
			c, err := a.client(ctx)
			if err != nil {
				return err
			}
			if !follow {
				logs, err := c.Logs(ctx, args[0], client.LogsOptions{Build: build, Lines: lines})
				if err != nil {
					return err
				}
				return a.out.result(logs, func() { fmt.Fprint(a.env.Stdout, logs.Logs) })
			}
			return a.followBuild(ctx, c, args[0])
		},
	}
	cmd.Flags().BoolVar(&build, "build", false, "show the build log instead of the app's output")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing the build log until the build ends")
	cmd.Flags().IntVarP(&lines, "lines", "n", 200, "number of lines from the end")
	return cmd
}

func (a *app) followBuild(ctx context.Context, c *client.Client, ref string) error {
	var offset int64
	for {
		logs, err := c.Logs(ctx, ref, client.LogsOptions{Build: true, Offset: &offset})
		if err != nil && domain.CodeOf(err) != domain.CodeNotFound {
			return err
		}
		fmt.Fprint(a.env.Stdout, logs.Logs)
		if logs.Offset > 0 {
			offset = logs.Offset
		}
		d, err := c.Deployment(ctx, ref)
		if err != nil {
			return err
		}
		if d.State != domain.StateQueued && d.State != domain.StateBuilding && logs.Logs == "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

func (a *app) openCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "open NAME",
		Short: "Open a deployment in the browser",
		Args:  exactArgs(1, "NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			d, err := c.Deployment(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.env.OpenBrowser != nil && a.env.Interactive && !a.json {
				if err := a.env.OpenBrowser(d.URL); err != nil {
					a.progress("Could not open a browser: %v", err)
				}
			}
			return a.out.result(map[string]string{"url": d.URL}, func() { a.out.line("%s", d.URL) })
		},
	}
}

// lifecycleCommand builds stop, start, and destroy, which queue an operation
// and wait for it by default.
func (a *app) lifecycleCommand(use, short, long, done string, submit func(c *client.Client, ctx context.Context, ref, key string) (domain.Operation, error), confirm bool) *cobra.Command {
	var detach, yes bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   use + " NAME",
		Short: short,
		Long:  long,
		Args:  exactArgs(1, "NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := a.client(ctx)
			if err != nil {
				return err
			}
			if confirm && !yes {
				if err := a.confirm(ctx, c, args[0]); err != nil {
					return err
				}
			}
			op, err := submit(c, ctx, args[0], "cli-"+domain.RandomString(16))
			if err != nil {
				return err
			}
			if detach {
				return a.out.result(op, func() { a.out.line("Queued operation %s", op.ID) })
			}
			d, err := a.waitDeployment(ctx, c, op, timeout, false)
			if err != nil {
				return err
			}
			return a.out.result(d, func() { a.out.line("✓ %s %s", d.Name, done) })
		},
	}
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "return once the operation is queued")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "how long to wait")
	if confirm {
		cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	}
	return cmd
}

func (a *app) stopCommand() *cobra.Command {
	return a.lifecycleCommand("stop", "Stop a running deployment",
		"Stop a deployment's container. Visitors see an offline page. Its lifetime keeps running; start it again with deployer start.",
		"stopped", (*client.Client).Stop, false)
}

func (a *app) startCommand() *cobra.Command {
	return a.lifecycleCommand("start", "Start a stopped or failed deployment",
		"Start a deployment again from its built image, without rebuilding. An expired deployment must be extended first.",
		"is running", (*client.Client).Start, false)
}

func (a *app) destroyCommand() *cobra.Command {
	cmd := a.lifecycleCommand("destroy", "Remove a deployment and its resources",
		"Remove a deployment's container, network, image, and logs, and release its name. This cancels a build in progress.",
		"was destroyed", (*client.Client).Destroy, true)
	cmd.Aliases = []string{"rm"}
	return cmd
}

// confirm asks before destroying. Without a terminal, --yes is required.
func (a *app) confirm(ctx context.Context, c *client.Client, ref string) error {
	if !a.env.Interactive {
		return usage("destroying requires confirmation; pass --yes")
	}
	d, err := c.Deployment(ctx, ref)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.env.Stderr, "Destroy %s (%s)? [y/N] ", d.Name, d.URL)
	answer, _ := bufio.NewReader(a.env.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return domain.Errorf(domain.CodeClient, "cancelled")
	}
	return nil
}

func (a *app) extendCommand() *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:   "extend NAME --by DURATION",
		Short: "Lengthen a deployment's lifetime",
		Long: `Lengthen a deployment's lifetime. An expired deployment that has not been
removed yet gets a new lifetime starting now; start it again with deployer start.`,
		Example: "  deployer extend design-a --by 2d",
		Args:    exactArgs(1, "NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if by == "" {
				return usage("--by is required, such as --by 1d")
			}
			dur, err := domain.ParseDuration(by)
			if err != nil {
				return usage("--by: %v", err)
			}
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			d, err := c.Extend(cmd.Context(), args[0], dur)
			if err != nil {
				return err
			}
			return a.out.result(d, func() {
				if d.ExpiresAt == nil {
					a.out.line("✓ %s will run for %s once ready", d.Name, domain.FormatDuration(d.TTL()))
					return
				}
				a.out.line("✓ %s now expires %s", d.Name, a.out.when(d.ExpiresAt))
				if d.State == domain.StateStopped {
					a.out.line("Start it again with: deployer start %s", d.Name)
				}
			})
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "how much longer, such as 4h or 2d")
	return cmd
}

func (a *app) operationCommand() *cobra.Command {
	cmd := group("operation", "Inspect or wait for an operation", "op")
	show := &cobra.Command{
		Use:   "show ID",
		Short: "Show an operation",
		Args:  exactArgs(1, "ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			op, err := c.Operation(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.out.result(op, func() { a.printOperation(op) })
		},
	}
	var timeout time.Duration
	wait := &cobra.Command{
		Use:   "wait ID",
		Short: "Wait for an operation to finish",
		Args:  exactArgs(1, "ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			op, err := a.waitOperation(cmd.Context(), c, args[0], timeout)
			if err != nil {
				return err
			}
			return a.out.result(op, func() { a.printOperation(op) })
		},
	}
	wait.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "how long to wait")
	cmd.AddCommand(show, wait)
	return cmd
}

func (a *app) printOperation(op domain.Operation) {
	a.out.fields([][2]string{
		{"Operation", op.ID}, {"Kind", string(op.Kind)}, {"Status", string(op.Status)},
		{"Deployment", op.DeploymentID}, {"Started", a.out.when(op.StartedAt)}, {"Finished", a.out.when(op.FinishedAt)},
		{"Error", errorText(op.Error)},
	})
}
