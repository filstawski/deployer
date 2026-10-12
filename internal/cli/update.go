package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/client"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/spf13/cobra"
)

// waitOptions control how long-running commands wait.
type waitOptions struct {
	detach, follow bool
	timeout        time.Duration
}

func (a *app) updateCommand() *cobra.Command {
	var ref string
	var o waitOptions
	cmd := &cobra.Command{
		Use:   "update [NAME]",
		Short: "Rebuild a preview from the latest commit of its branch",
		Long: `Rebuild a preview from the latest pushed commit of its branch. It keeps its
name, URL, and lifetime, so links you already shared keep working.

Without a name, it updates the preview of the current branch, or of --ref. A
running preview keeps serving while the new version builds; switching takes a
few seconds. If the new version doesn't start, the previous one keeps running.

With a name, --ref moves that preview to another branch or commit.`,
		Example: `  deployer update
  deployer update design-a
  deployer update design-a --ref design/b`,
		Args:              maxArgs(1),
		ValidArgsFunction: a.completeDeployments(live),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ref != "" {
				if err := domain.ValidateRef(ref); err != nil {
					return err
				}
			}
			repo, _ := detectRepo(ctx, a.env.WorkDir) // nil outside a repository
			branch := ref
			if branch == "" && repo != nil {
				branch = repo.Branch
			}
			if len(args) == 0 && branch == "" && !a.canPrompt() {
				why := "outside a Git repository"
				if repo != nil {
					why = "when HEAD is detached"
				}
				return usage("deployer update needs a deployment name or --ref %s", why).WithHint("see your deployments with: deployer list")
			}
			c, err := a.client(ctx)
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			} else if name, err = a.previewOf(ctx, c, repo, branch); err != nil {
				return err
			}
			return a.update(ctx, c, name, ref, repo, o)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&ref, "ref", "r", "", "move the preview to this branch or full commit SHA")
	f.BoolVarP(&o.detach, "detach", "d", false, "return once the update is queued")
	f.BoolVarP(&o.follow, "follow", "f", false, "stream build output while waiting")
	f.DurationVar(&o.timeout, "timeout", 30*time.Minute, "how long to wait; the update continues if the wait ends")
	cmd.RegisterFlagCompletionFunc("ref", a.completeBranches)
	return cmd
}

// previewOf finds the preview to update when no name is given: the one
// deployed from branch, in the repository's project when there is one.
func (a *app) previewOf(ctx context.Context, c *client.Client, repo *localRepo, branch string) (string, error) {
	if branch == "" {
		return a.deploymentArg(ctx, c, nil, "update", live)
	}
	var opts client.ListOptions
	if repo != nil {
		project, found, err := a.findRepoProject(ctx, c, repo)
		if e, ok := domain.AsError(err); ok && e.Code == domain.CodeInvalidInput {
			return "", e.WithHint("name the preview: deployer update NAME")
		}
		if err != nil {
			return "", err
		}
		if !found {
			return "", domain.Errorf(domain.CodeNotFound, "this repository has no previews yet").WithHint("deploy it: deployer deploy")
		}
		opts.Project = project.Name
	}
	list, err := c.Deployments(ctx, opts)
	if err != nil {
		return "", err
	}
	var matches []domain.Deployment
	for _, d := range list {
		if d.Ref == branch {
			matches = append(matches, d)
		}
	}
	switch {
	case len(matches) == 1:
		return matches[0].Name, nil
	case len(matches) == 0:
		return "", domain.Errorf(domain.CodeNotFound, "%s has no preview to update", branch).
			WithHint("deploy it: deployer deploy --ref %s, or name a preview: deployer update NAME", branch)
	case !a.canPrompt():
		names := make([]string, len(matches))
		for n, d := range matches {
			names[n] = d.Name
		}
		return "", usage("%s has %d previews: %s", branch, len(matches), strings.Join(names, ", ")).
			WithHint("name one: deployer update %s", matches[0].Name)
	}
	options := make([]Option, len(matches))
	for n, d := range matches {
		options[n] = Option{Label: deploymentLabel(d), Value: d.Name}
	}
	choice := options[0].Value
	return choice, a.ask(Field{Title: "Which preview of " + branch + " do you want to update?", Options: options, Value: &choice})
}

// offerUpdate asks, before the deploy form, whether to update an existing
// preview of the branch instead of creating another one. It returns the
// preview to update, or "" to create a new one.
func (a *app) offerUpdate(ctx context.Context, c *client.Client, project, branch string) (string, error) {
	if branch == "" {
		return "", nil
	}
	list, err := c.Deployments(ctx, client.ListOptions{Project: project})
	if err != nil {
		return "", err
	}
	var options []Option
	for _, d := range list {
		if d.Ref == branch && !d.ExpiredAt(a.env.Now()) {
			options = append(options, Option{Label: fmt.Sprintf("Update %s  (%s, %s)", d.Name, d.State, d.Commit[:min(7, len(d.Commit))]), Value: d.Name})
		}
	}
	if len(options) == 0 {
		return "", nil
	}
	options = append(options, Option{Label: "Create a new preview", Value: ""})
	choice := options[0].Value
	err = a.ask(Field{
		Title:       branch + " already has a preview. Update it?",
		Description: "Updating rebuilds it from the latest pushed commit and keeps its URL.",
		Options:     options, Value: &choice,
	})
	return choice, err
}

// update moves a deployment to the latest commit of its ref, or to ref, and
// waits for it unless o.detach is set.
func (a *app) update(ctx context.Context, c *client.Client, name, ref string, repo *localRepo, o waitOptions) error {
	d, err := c.Deployment(ctx, name)
	if err != nil {
		return err
	}
	a.warnLocalChanges(repo, cmp.Or(ref, d.Ref))
	op, err := c.Update(ctx, d.Name, domain.UpdateDeploymentRequest{Ref: ref}, "update-"+domain.RandomString(16))
	if e, ok := domain.AsError(err); ok && e.Code == domain.CodeNotFound && strings.HasPrefix(e.Message, "no API route") {
		return domain.Errorf(domain.CodeUnsupported, "this server is too old to update previews").
			WithHint("an administrator upgrades it by running deployer upgrade on the server")
	}
	if err != nil {
		return err
	}
	resp := domain.CreateDeploymentResponse{Deployment: d, Operation: op}
	st := a.out.st
	if o.detach {
		return a.out.result(resp, func() {
			if !st.enabled {
				a.out.line("Queued an update of %s", d.Name)
				a.out.line("\nFollow progress with: deployer operation wait %s", op.ID)
				return
			}
			a.out.details(panel{
				title: "Queued an update of " + d.Name, accent: colorBusy,
				lines: []string{st.url(d.URL)},
				notes: []string{"Follow it with: " + st.command("deployer operation wait "+op.ID)},
			})
		})
	}
	es := a.errs
	a.progress("Updating %s from %s → %s", es.bold(d.Name), es.bold(refAt(d)), es.url(d.URL))
	if d, err = a.waitDeployment(ctx, c, op, o.timeout, o.follow); err != nil {
		return err
	}
	if op, err = c.Operation(ctx, op.ID); err != nil {
		return err
	}
	resp.Deployment, resp.Operation = d, op
	var result domain.UpdateResult
	json.Unmarshal(op.Result, &result)
	return a.out.result(resp, func() {
		title, note := d.Name+" is updated", ""
		if !result.Changed {
			title = d.Name + " is already up to date"
			note = "Push new commits to " + d.Ref + ", then run deployer update again."
		}
		if !st.enabled {
			a.out.success("%s: %s", title, d.URL)
			a.out.fields([][2]string{{"  Ref", refAt(d)}, {"  Previous", previous(result)}, {"  Expires", a.out.when(d.ExpiresAt)}})
			return
		}
		p := panel{
			title: st.ok("✓ ") + title, accent: colorOK,
			lines: []string{st.url(d.URL)},
			fields: [][2]string{
				{"Ref", styledRef(st, d)}, {"Previous", st.muted(previous(result))},
				{"Expires", st.bold(a.out.when(d.ExpiresAt))},
			},
		}
		if note != "" {
			p.notes = []string{st.muted(note)}
		}
		a.out.details(p)
	})
}

// previous names the commit an update replaced, if it replaced one.
func previous(r domain.UpdateResult) string {
	if !r.Changed || r.PreviousCommit == "" {
		return ""
	}
	return shortCommit(r.PreviousCommit)
}
