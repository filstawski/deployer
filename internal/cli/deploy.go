package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/filstawski/deployer/internal/client"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/spf13/cobra"
)

func (a *app) deployCommand() *cobra.Command {
	var ref, name, ttl, key string
	var detach, follow bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "deploy [PROJECT]",
		Short: "Deploy a branch as a new preview",
		Long: `Deploy a branch or commit as a new, isolated preview.

Run inside a Git repository to deploy its current branch: the project is found
from the origin remote, and registered on first use. The server builds what is
pushed to the Git host, not your local files.

The preview's URL is reserved immediately and shows a progress page until the
app passes its health check. Its lifetime (--ttl) starts at that point.`,
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
			if err := a.fillFromRepo(ctx, c, &req); err != nil {
				return err
			}
			if key == "" {
				key = "deploy-" + domain.RandomString(16)
			}
			resp, err := c.Deploy(ctx, req, key)
			if err != nil {
				return err
			}
			d := resp.Deployment
			if detach {
				return a.out.result(resp, func() {
					a.out.line("Queued %s for %s", d.Name, d.Ref)
					a.out.fields([][2]string{{"  URL", d.URL}, {"  Deployment", d.ID}, {"  Operation", resp.Operation.ID}})
					a.out.line("\nFollow progress with: deployer operation wait %s", resp.Operation.ID)
				})
			}
			a.progress("Deploying %s as %s → %s", d.Ref, d.Name, d.URL)
			d, err = a.waitDeployment(ctx, c, resp.Operation, timeout, follow)
			if err != nil {
				return err
			}
			resp.Deployment = d
			resp.Operation, _ = c.Operation(ctx, resp.Operation.ID)
			return a.out.result(resp, func() {
				a.out.line("✓ %s is live at %s", d.Name, d.URL)
				a.out.fields([][2]string{{"  Ref", refAt(d)}, {"  Expires", a.out.when(d.ExpiresAt)}})
			})
		},
	}
	f := cmd.Flags()
	f.StringVarP(&ref, "ref", "r", "", "branch or full commit SHA (default: the current branch)")
	f.StringVarP(&name, "name", "n", "", "preview name, used as <name>.<domain> (default: generated)")
	f.StringVar(&ttl, "ttl", "", "lifetime after the preview is ready, such as 4h or 3d (default: server setting)")
	f.BoolVarP(&detach, "detach", "d", false, "return once the deployment is queued")
	f.BoolVarP(&follow, "follow", "f", false, "stream build output while waiting")
	f.DurationVar(&timeout, "timeout", 30*time.Minute, "how long to wait; the deployment continues if the wait ends")
	f.StringVar(&key, "idempotency-key", "", "retrying with the same key returns the same deployment")
	return cmd
}

// fillFromRepo completes a deploy request from the Git repository in the
// working directory: the project from the origin remote and the ref from
// the current branch.
func (a *app) fillFromRepo(ctx context.Context, c *client.Client, req *domain.CreateDeploymentRequest) error {
	if req.Project != "" && req.Ref != "" {
		return nil
	}
	repo, err := detectRepo(ctx, a.env.WorkDir)
	if err != nil {
		if req.Project == "" {
			return usage("not inside a Git repository").WithHint("name the project and ref: deployer deploy PROJECT --ref BRANCH")
		}
		return usage("--ref is required outside a Git repository")
	}
	if req.Project == "" {
		p, err := a.projectForRepo(ctx, c, repo)
		if err != nil {
			return err
		}
		req.Project = p.Name
	}
	if req.Ref == "" {
		if repo.Branch == "" {
			return usage("HEAD is detached; choose what to deploy with --ref")
		}
		req.Ref = repo.Branch
		switch {
		case repo.Unpushed < 0:
			a.progress("Warning: %s has no upstream branch; the server deploys what is pushed (git push -u origin %s)", repo.Branch, repo.Branch)
		case repo.Unpushed > 0:
			a.progress("Warning: %d local commit(s) on %s are not pushed and will not be deployed", repo.Unpushed, repo.Branch)
		}
		if repo.Dirty {
			a.progress("Warning: uncommitted changes are not deployed")
		}
	}
	return nil
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
			a.progress("Registered project %s for %s", p.Name, p.Repository)
		}
		return p, err
	}
	return domain.Project{}, fmt.Errorf("could not choose a project name for %s", url)
}
