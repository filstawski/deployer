// Package git fetches application source for builds. Every git process runs
// with an isolated configuration: no host credential helpers, hooks, or
// redirects, and HTTPS as the only allowed transport.
package git

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/execx"
)

// Credentials supplies HTTPS credentials for a repository, such as a GitHub
// App installation token. Empty strings mean anonymous access.
type Credentials interface {
	Credentials(ctx context.Context, repository string) (username, password string, err error)
}

// Client runs git on behalf of the controller.
type Client struct {
	// HomeDir is an empty directory used as git's HOME.
	HomeDir string
	// Credentials is optional.
	Credentials Credentials
	// AllowLocal permits file:// repositories. It exists for tests.
	AllowLocal bool
}

// Fetch materializes one commit of repository into dest, which is replaced.
// ref is a branch name or commit SHA; when pinned is set, the fetched commit
// must equal it, so retries build exactly the commit recorded first. The
// checkout's .git directory is removed so it never reaches the build context.
func (c *Client) Fetch(ctx context.Context, repository, ref, pinned, dest string) (string, error) {
	if err := domain.ValidateRef(ref); err != nil {
		return "", err
	}
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	for _, dir := range []string{dest, c.HomeDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
	}
	env, err := c.env(ctx, repository)
	if err != nil {
		return "", err
	}
	git := func(args ...string) (string, error) {
		return execx.Run(ctx, execx.Cmd{Name: "git", Args: append(c.baseArgs(), args...), Dir: dest, Env: env})
	}
	if _, err := git("init", "--quiet"); err != nil {
		return "", err
	}
	want := "refs/heads/" + ref
	switch {
	case pinned != "":
		want = pinned
	case domain.IsCommitSHA(ref):
		want = ref
	}
	if out, err := git("fetch", "--quiet", "--no-tags", "--depth=1", "--", repository, want); err != nil {
		return "", classify(ctx, out, ref)
	}
	out, err := git("rev-parse", "--verify", "FETCH_HEAD^{commit}")
	commit := strings.TrimSpace(out)
	if err != nil || !domain.IsCommitSHA(commit) {
		return "", domain.Errorf(domain.CodeSourceFailed, "git did not resolve %q to a commit", ref)
	}
	if pinned != "" && commit != pinned {
		return "", domain.Errorf(domain.CodeSourceFailed, "fetched commit %s does not match the recorded commit %s", commit, pinned)
	}
	if _, err := git("checkout", "--quiet", "--force", "--detach", commit); err != nil {
		return "", domain.Errorf(domain.CodeSourceFailed, "git could not check out %s", commit[:12])
	}
	if err := os.RemoveAll(filepath.Join(dest, ".git")); err != nil {
		return "", err
	}
	return commit, nil
}

// Verify checks that the repository is readable and returns its HEAD commit.
func (c *Client) Verify(ctx context.Context, repository string) (string, error) {
	if err := os.MkdirAll(c.HomeDir, 0o700); err != nil {
		return "", err
	}
	env, err := c.env(ctx, repository)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := execx.Run(ctx, execx.Cmd{Name: "git", Args: append(c.baseArgs(), "ls-remote", "--exit-code", "--", repository, "HEAD"), Dir: c.HomeDir, Env: env})
	if err != nil {
		return "", classify(ctx, out, "HEAD")
	}
	fields := strings.Fields(out)
	if len(fields) < 2 || !domain.IsCommitSHA(fields[0]) {
		return "", domain.Errorf(domain.CodeSourceFailed, "repository has no default branch commit")
	}
	return fields[0], nil
}

// Branches lists the repository's branches: the default branch first, then
// the others by name.
func (c *Client) Branches(ctx context.Context, repository string) ([]domain.Branch, error) {
	if err := os.MkdirAll(c.HomeDir, 0o700); err != nil {
		return nil, err
	}
	env, err := c.env(ctx, repository)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	args := append(c.baseArgs(), "ls-remote", "--symref", "--", repository, "HEAD", "refs/heads/*")
	out, err := execx.Run(ctx, execx.Cmd{Name: "git", Args: args, Dir: c.HomeDir, Env: env})
	if err != nil {
		return nil, classify(ctx, out, "HEAD")
	}
	return parseBranches(out), nil
}

// parseBranches reads `git ls-remote --symref` output: "ref: refs/heads/main
// HEAD" names the default branch, and "<sha> refs/heads/<name>" each branch.
func parseBranches(out string) []domain.Branch {
	var head string
	branches := []domain.Branch{}
	lines := strings.Split(out, "\n")
	// Output is capped (execx.MaxOutput); a last line without its newline may
	// be cut off mid-name.
	lines = lines[:len(lines)-1]
	for _, line := range lines {
		left, right, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		if target, ok := strings.CutPrefix(left, "ref: refs/heads/"); ok && right == "HEAD" {
			head = target
			continue
		}
		if name, ok := strings.CutPrefix(right, "refs/heads/"); ok && domain.IsCommitSHA(left) {
			branches = append(branches, domain.Branch{Name: name, Commit: left})
		}
	}
	for i := range branches {
		branches[i].Default = branches[i].Name == head
	}
	slices.SortStableFunc(branches, func(a, b domain.Branch) int {
		if a.Default != b.Default {
			if a.Default {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return branches
}

func (c *Client) baseArgs() []string {
	args := []string{
		"-c", "credential.helper=", "-c", "core.hooksPath=/dev/null", "-c", "http.followRedirects=false",
		"-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "advice.detachedHead=false",
	}
	if c.AllowLocal {
		args = append(args, "-c", "protocol.file.allow=always")
	}
	return args
}

// env returns git's environment. Credentials are scoped to the exact
// repository URL through an extra HTTP header, so they never appear in
// command arguments, URLs, files, or the build context.
func (c *Client) env(ctx context.Context, repository string) ([]string, error) {
	env := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + c.HomeDir, "LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "GIT_LFS_SKIP_SMUDGE=1",
	}
	if c.Credentials == nil {
		return env, nil
	}
	user, password, err := c.Credentials.Credentials(ctx, repository)
	if err != nil || password == "" {
		return env, err
	}
	header := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
	return append(env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http."+repository+".extraheader", "GIT_CONFIG_VALUE_0="+header), nil
}

// classify turns git's output into an actionable error without echoing it,
// since it can include URLs and server messages.
func classify(ctx context.Context, output, ref string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return domain.Errorf(domain.CodeTimeout, "git timed out")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	out := strings.ToLower(output)
	switch {
	case strings.Contains(out, "couldn't find remote ref"), strings.Contains(out, "not our ref"):
		return domain.Errorf(domain.CodeSourceFailed, "%q was not found in the repository", ref).
			WithHint("push the branch first: git push -u origin %s", ref)
	case strings.Contains(out, "authentication"), strings.Contains(out, "could not read username"),
		strings.Contains(out, "terminal prompts disabled"), strings.Contains(out, "repository not found"),
		strings.Contains(out, "403"), strings.Contains(out, "401"):
		return domain.Errorf(domain.CodeSourceFailed, "the repository is private or does not exist").
			WithHint("for private GitHub repositories, connect GitHub: deployer github connect")
	case strings.Contains(out, "could not resolve host"), strings.Contains(out, "connection"):
		return domain.Errorf(domain.CodeSourceFailed, "the server could not reach the Git host")
	default:
		return domain.Errorf(domain.CodeSourceFailed, "git fetch failed; check the repository URL and ref")
	}
}
