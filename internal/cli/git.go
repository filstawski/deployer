package cli

import (
	"context"
	"path"
	"strconv"
	"strings"

	"github.com/filstawski/deployer/internal/execx"
)

// localRepo describes the Git repository in the working directory.
type localRepo struct {
	Root   string
	Origin string
	// Branch is empty for a detached HEAD.
	Branch string
	Dirty  bool
	// Unpushed is the number of commits ahead of the upstream branch, or -1
	// when the branch has no upstream.
	Unpushed int
}

func detectRepo(ctx context.Context, dir string) (*localRepo, error) {
	git := func(args ...string) (string, error) {
		out, err := execx.Run(ctx, execx.Cmd{Name: "git", Args: append([]string{"-C", dir}, args...)})
		return strings.TrimSpace(out), err
	}
	root, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	r := &localRepo{Root: root, Unpushed: -1}
	r.Origin, _ = git("remote", "get-url", "origin")
	r.Branch, _ = git("symbolic-ref", "--short", "-q", "HEAD")
	if out, err := git("status", "--porcelain"); err == nil {
		r.Dirty = out != ""
	}
	if out, err := git("rev-list", "--count", "@{upstream}..HEAD"); err == nil {
		r.Unpushed, _ = strconv.Atoi(out)
	}
	return r, nil
}

// repoName returns the last path element of a repository URL without .git.
func repoName(repository string) string {
	return strings.TrimSuffix(path.Base(repository), ".git")
}
