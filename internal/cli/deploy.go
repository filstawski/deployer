package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/client"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/spf13/cobra"
)

func (a *app) deployCommand() *cobra.Command {
	var ref, name, ttl, key string
	var detach, follow, yes bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "deploy [PROJECT]",
		Short: "Deploy a branch as a new preview",
		Long: `Deploy a branch or commit as a new, isolated preview.

Run inside a Git repository to deploy it: the project is found from the origin
remote and registered on first use. The server builds what is pushed to the Git
host, not your local files.

At a terminal, a short form asks for the branch, name, and lifetime, already
filled in with suggestions; press Enter to accept them. Flags answer their
questions in advance, and --yes skips the form.

The preview's URL is reserved immediately and shows a progress page until the
app passes its health check. Its lifetime starts at that point.`,
		Example: `  deployer deploy
  deployer deploy --ref design/a --name design-a --ttl 3d
  deployer deploy shop --ref main --detach --json`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := a.client(ctx)
			if err != nil {
				return err
			}
			req := domain.CreateDeploymentRequest{Ref: ref, Name: name}
			if ttl != "" {
				d, err := domain.ParseDuration(ttl)
				if err != nil {
					return usage("--ttl: %v", err)
				}
				req.TTLSeconds = int64(d.Seconds())
			}
			if len(args) == 1 {
				req.Project = args[0]
			}
			repo, _ := detectRepo(ctx, a.env.WorkDir) // nil outside a repository
			interactive := a.canPrompt() && !yes
			if err := a.resolveProject(ctx, c, &req, repo, interactive); err != nil {
				return err
			}
			switch {
			case interactive:
				if err := a.deployForm(ctx, c, &req, repo); err != nil {
					return err
				}
			case req.Ref == "" && repo == nil:
				return usage("--ref is required outside a Git repository")
			case req.Ref == "" && repo.Branch == "":
				return usage("HEAD is detached; choose what to deploy with --ref")
			case req.Ref == "":
				req.Ref = repo.Branch
			}
			a.warnLocalChanges(repo, req.Ref)
			if key == "" {
				key = "deploy-" + domain.RandomString(16)
			}
			resp, err := c.Deploy(ctx, req, key)
			if err != nil {
				return err
			}
			d := resp.Deployment
			st := a.out.st
			if detach {
				return a.out.result(resp, func() {
					if !st.enabled {
						a.out.line("Queued %s for %s", d.Name, d.Ref)
						a.out.fields([][2]string{{"  URL", d.URL}, {"  Deployment", d.ID}, {"  Operation", resp.Operation.ID}})
						a.out.line("\nFollow progress with: deployer operation wait %s", resp.Operation.ID)
						return
					}
					a.out.details(panel{
						title: "Queued " + d.Name, accent: colorBusy,
						lines:  []string{st.url(d.URL) + st.muted("  shows progress until it's ready")},
						fields: [][2]string{{"Ref", d.Ref}, {"Deployment", st.muted(d.ID)}, {"Operation", st.muted(resp.Operation.ID)}},
						notes:  []string{"Follow it with: " + st.command("deployer operation wait "+resp.Operation.ID)},
					})
				})
			}
			es := a.errs
			a.progress("Deploying %s as %s → %s", es.bold(d.Ref), es.bold(d.Name), es.url(d.URL))
			d, err = a.waitDeployment(ctx, c, resp.Operation, timeout, follow)
			if err != nil {
				return err
			}
			resp.Deployment = d
			resp.Operation, _ = c.Operation(ctx, resp.Operation.ID)
			return a.out.result(resp, func() {
				if !st.enabled {
					a.out.success("%s is live at %s", d.Name, d.URL)
					a.out.fields([][2]string{{"  Ref", refAt(d)}, {"  Expires", a.out.when(d.ExpiresAt)}})
					return
				}
				a.out.details(panel{
					title: st.ok("✓ ") + d.Name + " is live", accent: colorOK,
					lines: []string{st.url(d.URL)},
					fields: [][2]string{
						{"Ref", d.Ref + st.muted(" @ "+shortCommit(d.Commit))}, {"Project", d.Project},
						{"Expires", st.bold(a.out.when(d.ExpiresAt))},
					},
				})
			})
		},
	}
	f := cmd.Flags()
	f.StringVarP(&ref, "ref", "r", "", "branch or full commit SHA (default: the current branch)")
	f.StringVarP(&name, "name", "n", "", "preview name, used as <name>.<domain> (default: generated)")
	f.StringVar(&ttl, "ttl", "", "lifetime after the preview is ready, such as 4h or 3d (default: server setting)")
	f.BoolVarP(&yes, "yes", "y", false, "skip the form and use the defaults")
	f.BoolVarP(&detach, "detach", "d", false, "return once the deployment is queued")
	f.BoolVarP(&follow, "follow", "f", false, "stream build output while waiting")
	f.DurationVar(&timeout, "timeout", 30*time.Minute, "how long to wait; the deployment continues if the wait ends")
	f.StringVar(&key, "idempotency-key", "", "retrying with the same key returns the same deployment")
	return cmd
}

// resolveProject fills in the project: from the repository's origin remote,
// or by asking.
func (a *app) resolveProject(ctx context.Context, c *client.Client, req *domain.CreateDeploymentRequest, repo *localRepo, interactive bool) error {
	switch {
	case req.Project != "":
		return nil
	case repo != nil:
		p, err := a.projectForRepo(ctx, c, repo)
		req.Project = p.Name
		return err
	case !interactive:
		return usage("not inside a Git repository").WithHint("name the project and ref: deployer deploy PROJECT --ref BRANCH")
	}
	projects, err := c.Projects(ctx)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		return usage("no projects are registered yet").WithHint("run deployer deploy inside a clone of the repository")
	}
	options := make([]Option, len(projects))
	for n, p := range projects {
		options[n] = Option{Label: fmt.Sprintf("%-24s %s", p.Name, p.Repository), Value: p.Name}
	}
	req.Project = projects[0].Name
	return a.ask(Field{Title: "Project", Options: options, Value: &req.Project})
}

// deployForm asks for the branch, name, and lifetime that flags did not set,
// suggesting the current branch, a readable unused name, and the server's
// default lifetime.
func (a *app) deployForm(ctx context.Context, c *client.Client, req *domain.CreateDeploymentRequest, repo *localRepo) error {
	if req.Ref != "" && req.Name != "" && req.TTLSeconds != 0 {
		return nil
	}
	project, err := c.Project(ctx, req.Project)
	if err != nil {
		return err
	}
	status, err := c.Status(ctx)
	if err != nil {
		return err
	}
	deployments, err := c.Deployments(ctx, client.ListOptions{})
	if err != nil {
		return err
	}
	taken := map[string]bool{}
	for _, d := range deployments {
		taken[d.Name] = true
	}
	es := a.errs
	a.progress("%s  %s  %s", es.muted(fmt.Sprintf("%-*s", summaryWidth, "Project")), es.bold(project.Name), es.muted(project.Repository))

	var fields []Field
	branch := req.Ref
	if branch == "" {
		branch = "main"
		if repo != nil && repo.Branch != "" {
			branch = repo.Branch
		}
		if repo != nil {
			fields = append(fields, Field{Title: "Branch", Description: "Pushed branches, most recent first", Options: branchOptions(ctx, repo), Value: &branch})
		} else {
			fields = append(fields, Field{Title: "Branch", Description: "A branch or full commit SHA on the Git host", Value: &branch, Validate: validateWith(domain.ValidateRef)})
		}
	}
	name := req.Name
	if name == "" {
		fields = append(fields, Field{
			Title:       "Name",
			Description: "The preview's address: " + strings.ReplaceAll(status.PreviewURL, "{name}", "NAME"),
			Value:       &name,
			Suggest:     func() string { return suggestName(project.Name, branch, taken) },
			SuggestFrom: &branch,
			Validate: func(v string) error {
				if taken[v] {
					return fmt.Errorf("a live deployment is already named %s", v)
				}
				return validateWith(domain.ValidateDeploymentName)(v)
			},
		})
	}
	lifetime := ""
	if req.TTLSeconds == 0 {
		var options []Option
		options, lifetime = lifetimeOptions(status.Deployments)
		fields = append(fields, Field{Title: "Lifetime", Description: "How long it stays up once it's ready", Options: options, Value: &lifetime})
	}
	if err := a.ask(fields...); err != nil {
		return err
	}
	req.Ref, req.Name = branch, name
	if lifetime != "" {
		req.TTLSeconds, _ = strconv.ParseInt(lifetime, 10, 64)
	}
	return nil
}

// validateWith adapts a domain validator to a prompt, showing its message.
func validateWith(validate func(string) error) func(string) error {
	return func(v string) error {
		if e, ok := domain.AsError(validate(v)); ok {
			return errors.New(e.Message)
		}
		return nil
	}
}

// branchOptions lists the repository's pushed branches, most recently
// changed first, with the current branch at the top.
func branchOptions(ctx context.Context, repo *localRepo) []Option {
	var options []Option
	seen := map[string]bool{}
	add := func(branch, note string) {
		if branch != "" && !seen[branch] && len(options) < 40 {
			seen[branch] = true
			label := branch
			if note != "" {
				label += "  (" + note + ")"
			}
			options = append(options, Option{Label: label, Value: branch})
		}
	}
	remote := repo.remoteBranches(ctx)
	pushed := map[string]bool{}
	for _, b := range remote {
		pushed[b] = true
	}
	switch {
	case repo.Branch == "":
	case pushed[repo.Branch]:
		add(repo.Branch, "current")
	default:
		add(repo.Branch, "current, not pushed")
	}
	for _, b := range remote {
		add(b, "")
	}
	if len(options) == 0 {
		add("main", "")
	}
	return options
}

// suggestName proposes a readable name that no live deployment uses, such as
// "shop-feature-login".
func suggestName(project, branch string, taken map[string]bool) string {
	if domain.IsCommitSHA(branch) {
		branch = branch[:7]
	}
	base := domain.Slug(project+"-"+branch, 63)
	if base == "" || domain.ValidateDeploymentName(base) != nil {
		base = "preview"
	}
	name := base
	for n := 2; taken[name]; n++ {
		suffix := "-" + strconv.Itoa(n)
		name = strings.TrimRight(base[:min(len(base), 63-len(suffix))], "-") + suffix
	}
	return name
}

// lifetimeOptions offers lifetimes up to the server's maximum, and returns
// the server default's value.
func lifetimeOptions(s domain.DeploymentsStatus) ([]Option, string) {
	def := time.Duration(s.DefaultTTL) * time.Second
	limit := time.Duration(s.MaxTTL) * time.Second
	var options []Option
	added := false
	for _, o := range durations {
		if !added && def <= o.d {
			if def != o.d {
				options = append(options, Option{Label: domain.FormatDuration(def) + " (default)", Value: strconv.FormatInt(s.DefaultTTL, 10)})
			}
			added = true
		}
		if o.d > limit {
			break
		}
		label := o.label
		if o.d == def {
			label += " (default)"
		}
		options = append(options, Option{Label: label, Value: strconv.FormatInt(int64(o.d.Seconds()), 10)})
	}
	if !added {
		options = append(options, Option{Label: domain.FormatDuration(def) + " (default)", Value: strconv.FormatInt(s.DefaultTTL, 10)})
	}
	return options, strconv.FormatInt(s.DefaultTTL, 10)
}

// warnLocalChanges points out local work that deploying ref won't include,
// when ref is the checked-out branch.
func (a *app) warnLocalChanges(repo *localRepo, ref string) {
	if repo == nil || ref != repo.Branch {
		return
	}
	switch {
	case repo.Unpushed < 0:
		a.warn("%s has no upstream branch; the server deploys what is pushed (git push -u origin %s)", repo.Branch, repo.Branch)
	case repo.Unpushed > 0:
		a.warn("%d local commit(s) on %s are not pushed and will not be deployed", repo.Unpushed, repo.Branch)
	}
	if repo.Dirty {
		a.warn("uncommitted changes are not deployed")
	}
}

// projectForRepo returns the project registered for the repository's origin
// remote, registering one if there is none.
func (a *app) projectForRepo(ctx context.Context, c *client.Client, repo *localRepo) (domain.Project, error) {
	if repo.Origin == "" {
		return domain.Project{}, usage("the repository has no origin remote").WithHint("name the project: deployer deploy PROJECT")
	}
	url, err := domain.NormalizeRepository(repo.Origin)
	if err != nil {
		return domain.Project{}, usage("origin %q cannot be used: %v", repo.Origin, err).
			WithHint("register the project with an HTTPS URL: deployer project add NAME --repo https://...")
	}
	projects, err := c.Projects(ctx)
	if err != nil {
		return domain.Project{}, err
	}
	var matches []domain.Project
	for _, p := range projects {
		if p.Repository == url {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
	default:
		return domain.Project{}, usage("%d projects use %s", len(matches), url).WithHint("name one: deployer deploy %s", matches[0].Name)
	}

	// Prefer the repository name; fall back to owner-repo, then a suffix.
	candidates := []string{domain.Slug(repoName(url), domain.MaxProjectNameLength)}
	if owner, name, ok := domain.GitHubRepository(url); ok {
		candidates = append(candidates, domain.Slug(owner+"-"+name, domain.MaxProjectNameLength))
	}
	candidates = append(candidates, domain.Slug(repoName(url), domain.MaxProjectNameLength-6)+"-"+domain.RandomString(5))
	sum := sha256.Sum256([]byte(url))
	key := "project-" + hex.EncodeToString(sum[:8])
	for _, name := range candidates {
		if name == "" {
			continue
		}
		p, err := c.CreateProject(ctx, domain.CreateProjectRequest{Name: name, Repository: url}, key+"-"+name)
		if domain.CodeOf(err) == domain.CodeAlreadyExists {
			continue
		}
		if err == nil {
			a.done("Registered project %s for %s", p.Name, p.Repository)
		}
		return p, err
	}
	return domain.Project{}, fmt.Errorf("could not choose a project name for %s", url)
}
