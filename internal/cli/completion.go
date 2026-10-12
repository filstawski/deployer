package cli

import (
	"context"
	"time"

	"github.com/filstawski/deployer/internal/client"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/spf13/cobra"
)

// Shell completion asks the server for names, so it needs a login. It gives
// up quickly rather than stall the shell.
const completionTimeout = 3 * time.Second

// completeDeployments completes a command's first argument with the names of
// deployments that keep accepts.
func (a *app) completeDeployments(keep func(domain.Deployment) bool) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
		defer cancel()
		c, err := a.client(ctx)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		list, err := c.Deployments(ctx, client.ListOptions{})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []cobra.Completion
		for _, d := range list {
			if keep == nil || keep(d) {
				out = append(out, cobra.CompletionWithDesc(d.Name, string(d.State)+" · "+d.Ref))
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeProjects completes project names, as an argument or a flag value.
func (a *app) completeProjects(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 && cmd.ValidArgsFunction != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()
	c, err := a.client(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	projects, err := c.Projects(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]cobra.Completion, len(projects))
	for n, p := range projects {
		out[n] = cobra.CompletionWithDesc(p.Name, p.Repository)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeBranches completes --ref with the repository's pushed branches.
func (a *app) completeBranches(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	repo, err := detectRepo(cmd.Context(), a.env.WorkDir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, o := range branchOptions(cmd.Context(), repo) {
		out = append(out, o.Value)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeFixed completes a flag with fixed choices.
func completeFixed(choices ...string) cobra.CompletionFunc {
	return func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return choices, cobra.ShellCompDirectiveNoFileComp
	}
}
