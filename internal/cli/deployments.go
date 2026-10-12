package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
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
					a.out.line("No deployments. Create one with: %s", a.out.st.command("deployer deploy"))
					return
				}
				st := a.out.st
				// At a terminal, the project column is left out when every
				// deployment belongs to the same project, to save width.
				withProject := !st.enabled
				for _, d := range list {
					withProject = withProject || d.Project != list[0].Project
				}
				header := []string{"NAME", "PROJECT", "REF", "STATE", "EXPIRES", "URL"}
				if !withProject {
					header = slices.Delete(header, 1, 2)
				}
				rows := make([][]string, len(list))
				for i, d := range list {
					rows[i] = []string{st.bold(d.Name), d.Project, d.Ref, st.state(d.State), st.muted(a.out.shortWhen(d.ExpiresAt)), st.url(d.URL)}
					if !withProject {
						rows[i] = slices.Delete(rows[i], 1, 2)
					}
				}
				a.out.table(header, rows)
			})
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "only this project")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include destroyed deployments")
	cmd.RegisterFlagCompletionFunc("project", a.completeProjects)
	return cmd
}

// live keeps deployments that still exist.
func live(d domain.Deployment) bool { return d.State.Live() }

func (a *app) inspectCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "inspect [NAME]",
		Aliases:           []string{"show"},
		Short:             "Show a deployment's details",
		Args:              maxArgs(1),
		ValidArgsFunction: a.completeDeployments(nil),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.needsName(args, "inspect"); err != nil {
				return err
			}
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			ref, err := a.deploymentArg(cmd.Context(), c, args, "inspect", nil)
			if err != nil {
				return err
			}
			d, err := c.Deployment(cmd.Context(), ref)
			if err != nil {
				return err
			}
			return a.out.result(d, func() { a.printDeployment(d) })
		},
	}
}

func (a *app) printDeployment(d domain.Deployment) {
	st := a.out.st
	lifetime := ""
	if d.ExpiresAt == nil && d.State.InProgress() {
		lifetime = domain.FormatDuration(d.TTL()) + " once ready"
	}
	if !st.enabled {
		a.out.fields([][2]string{
			{"Name", d.Name}, {"ID", d.ID}, {"Project", d.Project}, {"URL", d.URL},
			{"State", string(d.State)}, {"Ref", refAt(d)},
			{"Created", a.out.when(&d.CreatedAt)}, {"Ready", a.out.when(d.ReadyAt)},
			{"Expires", a.out.when(d.ExpiresAt)}, {"Removed", a.out.when(d.PurgeAt)},
			{"Lifetime", lifetime}, {"Error", errorText(d.Error)},
		})
		if d.Error != nil && d.Error.Hint != "" {
			a.out.line("\n%s", d.Error.Hint)
		}
		return
	}
	p := panel{
		title: d.Name + "  " + st.state(d.State), accent: stateColor(d.State),
		lines: []string{st.url(d.URL)},
		fields: [][2]string{
			{"Ref", d.Ref + st.muted(" @ "+shortCommit(d.Commit))}, {"Project", d.Project},
			{"Created", a.out.when(&d.CreatedAt)}, {"Ready", a.out.when(d.ReadyAt)},
			{"Expires", st.bold(a.out.when(d.ExpiresAt))}, {"Lifetime", lifetime},
			{"Removed", st.muted(a.out.when(d.PurgeAt))}, {"ID", st.muted(d.ID)},
		},
	}
	if d.Commit == "" {
		p.fields[0][1] = d.Ref
	}
	if d.Error != nil {
		p.notes = append(p.notes, st.bad("✗ "+d.Error.Message)+st.muted(" ("+string(d.Error.Code)+")"))
		if d.Error.Hint != "" {
			p.notes = append(p.notes, st.muted("→ ")+d.Error.Hint)
		}
	}
	a.out.details(p)
}

func (a *app) logsCommand() *cobra.Command {
	var build, follow bool
	var lines int
	cmd := &cobra.Command{
		Use:               "logs [NAME]",
		Short:             "Show a deployment's app or build output",
		ValidArgsFunction: a.completeDeployments(live),
		Example: `  deployer logs design-a
  deployer logs design-a --build --follow`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if follow && !build {
				return usage("--follow works with --build")
			}
			ctx := cmd.Context()
			if err := a.needsName(args, "logs"); err != nil {
				return err
			}
			c, err := a.client(ctx)
			if err != nil {
				return err
			}
			ref, err := a.deploymentArg(ctx, c, args, "see logs of", live)
			if err != nil {
				return err
			}
			if !follow {
				logs, err := c.Logs(ctx, ref, client.LogsOptions{Build: build, Lines: lines})
				if err != nil {
					return err
				}
				return a.out.result(logs, func() { fmt.Fprint(a.out.w, logs.Logs) })
			}
			return a.followBuild(ctx, c, ref)
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
		fmt.Fprint(a.out.w, logs.Logs)
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
		Use:               "open [NAME]",
		Short:             "Open a deployment in the browser",
		Args:              maxArgs(1),
		ValidArgsFunction: a.completeDeployments(live),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.needsName(args, "open"); err != nil {
				return err
			}
			c, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			ref, err := a.deploymentArg(cmd.Context(), c, args, "open", live)
			if err != nil {
				return err
			}
			d, err := c.Deployment(cmd.Context(), ref)
			if err != nil {
				return err
			}
			if a.env.OpenBrowser != nil && a.env.Interactive && !a.json {
				if err := a.env.OpenBrowser(d.URL); err != nil {
					a.warn("could not open a browser: %v", err)
				}
			}
			return a.out.result(map[string]string{"url": d.URL}, func() { a.out.line("%s", a.out.st.url(d.URL)) })
		},
	}
}

// lifecycle describes stop, start, or destroy, which queue an operation and
// wait for it by default.
type lifecycle struct {
	use, short, long string
	verb, done       string
	submit           func(c *client.Client, ctx context.Context, ref, key string) (domain.Operation, error)
	// keep selects the deployments offered when no name is given.
	keep func(domain.Deployment) bool
	// confirm asks before going ahead.
	confirm bool
}

func (a *app) lifecycleCommand(l lifecycle) *cobra.Command {
	var detach, yes bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:               l.use + " [NAME]",
		Short:             l.short,
		Long:              l.long,
		Args:              maxArgs(1),
		ValidArgsFunction: a.completeDeployments(l.keep),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := a.needsName(args, l.use); err != nil {
				return err
			}
			c, err := a.client(ctx)
			if err != nil {
				return err
			}
			ref, err := a.deploymentArg(ctx, c, args, l.verb, l.keep)
			if err != nil {
				return err
			}
			if l.confirm && !yes {
				if err := a.confirmDestroy(ctx, c, ref); err != nil {
					return err
				}
			}
			op, err := l.submit(c, ctx, ref, "cli-"+domain.RandomString(16))
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
			return a.out.result(d, func() { a.out.success("%s %s", a.out.st.bold(d.Name), l.done) })
		},
	}
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "return once the operation is queued")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "how long to wait")
	if l.confirm {
		cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	}
	return cmd
}

func (a *app) stopCommand() *cobra.Command {
	return a.lifecycleCommand(lifecycle{
		use: "stop", short: "Stop a running deployment", verb: "stop", done: "stopped",
		long:   "Stop a deployment's container. Visitors see an offline page. Its lifetime keeps running; start it again with deployer start.",
		submit: (*client.Client).Stop,
		keep:   func(d domain.Deployment) bool { return d.State == domain.StateRunning },
	})
}

func (a *app) startCommand() *cobra.Command {
	return a.lifecycleCommand(lifecycle{
		use: "start", short: "Start a stopped or failed deployment", verb: "start", done: "is running",
		long:   "Start a deployment again from its built image, without rebuilding. An expired deployment must be extended first.",
		submit: (*client.Client).Start,
		keep: func(d domain.Deployment) bool {
			return (d.State == domain.StateStopped || d.State == domain.StateFailed) && d.Image != "" && !d.ExpiredAt(time.Now())
		},
	})
}

func (a *app) destroyCommand() *cobra.Command {
	cmd := a.lifecycleCommand(lifecycle{
		use: "destroy", short: "Remove a deployment and its resources", verb: "destroy", done: "was destroyed",
		long:    "Remove a deployment's container, network, image, and logs, and release its name. This cancels a build in progress.",
		submit:  (*client.Client).Destroy,
		keep:    live,
		confirm: true,
	})
	cmd.Aliases = []string{"rm"}
	return cmd
}

// confirmDestroy asks before destroying. Without a terminal, --yes is
// required.
func (a *app) confirmDestroy(ctx context.Context, c *client.Client, ref string) error {
	if !a.canPrompt() {
		return usage("destroying needs confirmation; pass --yes")
	}
	d, err := c.Deployment(ctx, ref)
	if err != nil {
		return err
	}
	ok, err := a.confirmPrompt("Destroy "+d.Name+"?", d.URL+" and everything in it will be removed. This can't be undone.")
	if err != nil {
		return err
	}
	if !ok {
		return domain.Errorf(domain.CodeClient, "cancelled; %s was not destroyed", d.Name)
	}
	return nil
}

// durations are the common lifetimes offered in prompts, as far as the
// server's maximum allows.
var durations = []time.Duration{time.Hour, 4 * time.Hour, 24 * time.Hour, 72 * time.Hour, 168 * time.Hour, 336 * time.Hour, 720 * time.Hour}

// durationLabel names a duration for people: "4 hours", "3 days", "2 weeks".
func durationLabel(d time.Duration) string {
	unit := func(n time.Duration, name string) string {
		if n == 1 {
			return "1 " + name
		}
		return fmt.Sprintf("%d %ss", n, name)
	}
	const day, week = 24 * time.Hour, 7 * 24 * time.Hour
	switch {
	case d%week == 0 && d/week <= 4:
		return unit(d/week, "week")
	case d%day == 0:
		return unit(d/day, "day")
	case d%time.Hour == 0:
		return unit(d/time.Hour, "hour")
	default:
		return domain.FormatDuration(d)
	}
}

// durationOptions offers the common durations up to limit, in order, plus
// the durations in marks, labeled with their mark. Values are seconds.
func durationOptions(limit time.Duration, marks map[time.Duration]string) []Option {
	set := map[time.Duration]bool{}
	for d := range marks {
		set[d] = true
	}
	for _, d := range durations {
		if d <= limit {
			set[d] = true
		}
	}
	sorted := slices.Sorted(maps.Keys(set))
	options := make([]Option, 0, len(sorted))
	for _, d := range sorted {
		label := durationLabel(d)
		if mark := marks[d]; mark != "" {
			label += " (" + mark + ")"
		}
		options = append(options, Option{Label: label, Value: strconv.FormatInt(int64(d.Seconds()), 10)})
	}
	return options
}

func (a *app) extendCommand() *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:   "extend [NAME] [--by DURATION]",
		Short: "Lengthen a deployment's lifetime",
		Long: `Lengthen a deployment's lifetime. An expired deployment that has not been
removed yet gets a new lifetime starting now; start it again with deployer start.`,
		Example:           "  deployer extend design-a --by 2d",
		Args:              maxArgs(1),
		ValidArgsFunction: a.completeDeployments(live),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if by == "" && !a.canPrompt() {
				return usage("--by is required, such as --by 1d")
			}
			var dur time.Duration
			if by != "" {
				var err error
				if dur, err = domain.ParseDuration(by); err != nil {
					return usage("--by: %v", err)
				}
			}
			if err := a.needsName(args, "extend"); err != nil {
				return err
			}
			c, err := a.client(ctx)
			if err != nil {
				return err
			}
			ref, err := a.deploymentArg(ctx, c, args, "extend", live)
			if err != nil {
				return err
			}
			if by == "" {
				if dur, err = a.askExtension(ctx, c, ref); err != nil {
					return err
				}
			}
			d, err := c.Extend(ctx, ref, dur)
			if err != nil {
				return err
			}
			return a.out.result(d, func() {
				st := a.out.st
				if d.ExpiresAt == nil {
					a.out.success("%s will run for %s once ready", st.bold(d.Name), domain.FormatDuration(d.TTL()))
					return
				}
				a.out.success("%s now expires %s", st.bold(d.Name), st.bold(a.out.when(d.ExpiresAt)))
				if d.State == domain.StateStopped {
					a.out.line("  Start it again with: %s", st.command("deployer start "+d.Name))
				}
			})
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "how much longer, such as 4h or 2d")
	cmd.RegisterFlagCompletionFunc("by", completeFixed("1h", "4h", "1d", "3d", "7d", "30d"))
	return cmd
}

// askExtension offers extensions up to what the server's maximum lifetime
// still allows for the deployment.
func (a *app) askExtension(ctx context.Context, c *client.Client, ref string) (time.Duration, error) {
	status, err := c.Status(ctx)
	if err != nil {
		return 0, err
	}
	d, err := c.Deployment(ctx, ref)
	if err != nil {
		return 0, err
	}
	limit := time.Duration(status.Deployments.MaxTTL) * time.Second
	headroom := limit - d.TTL()
	if d.ExpiresAt != nil {
		headroom = limit - max(time.Until(*d.ExpiresAt), 0)
	}
	headroom = headroom.Truncate(time.Minute)
	if headroom < time.Minute {
		return 0, domain.Errorf(domain.CodeInvalidInput, "%s already has the longest lifetime this server allows (%s)", d.Name, domain.FormatDuration(limit)).
			WithHint("an administrator can raise deployments.max_ttl in /etc/deployer/server.yaml")
	}
	options := durationOptions(headroom, map[time.Duration]string{headroom: "as long as allowed"})
	choice := options[len(options)-1].Value
	for _, o := range options {
		if o.Value == "86400" {
			choice = o.Value
		}
	}
	if err := a.ask(Field{Title: "Extend " + d.Name + " by how long?", Options: options, Value: &choice}); err != nil {
		return 0, err
	}
	seconds, _ := strconv.ParseInt(choice, 10, 64)
	return time.Duration(seconds) * time.Second, nil
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
	st := a.out.st
	status := string(op.Status)
	accent := colorBusy
	switch op.Status {
	case domain.OpSucceeded:
		status, accent = st.ok("✓ "+status), colorOK
	case domain.OpFailed, domain.OpCancelled:
		status, accent = st.bad("✗ "+status), colorBad
	default:
		status = st.warn("◐ " + status)
	}
	p := panel{
		title: op.ID, accent: accent,
		fields: [][2]string{
			{"Operation", op.ID}, {"Kind", string(op.Kind)}, {"Status", status},
			{"Deployment", op.DeploymentID}, {"Started", a.out.when(op.StartedAt)}, {"Finished", a.out.when(op.FinishedAt)},
			{"Error", errorText(op.Error)},
		},
	}
	if st.enabled {
		p.fields = p.fields[1:]
		if op.Error != nil {
			p.fields = p.fields[:len(p.fields)-1]
			p.notes = []string{st.bad("✗ " + op.Error.Message)}
			if op.Error.Hint != "" {
				p.notes = append(p.notes, st.muted("→ ")+op.Error.Hint)
			}
		}
	}
	a.out.details(p)
}
