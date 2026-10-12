package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filstawski/deployer/internal/domain"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repository creates a repository with main and design/a branches.
func repository(t *testing.T) (url, mainCommit, designCommit string) {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "--quiet", "-b", "main")
	os.WriteFile(filepath.Join(dir, "app.txt"), []byte("main\n"), 0o644)
	run(t, dir, "add", ".")
	run(t, dir, "commit", "--quiet", "-m", "main")
	mainCommit = run(t, dir, "rev-parse", "HEAD")
	run(t, dir, "checkout", "--quiet", "-b", "design/a")
	os.WriteFile(filepath.Join(dir, "app.txt"), []byte("design\n"), 0o644)
	run(t, dir, "commit", "--quiet", "-am", "design")
	designCommit = run(t, dir, "rev-parse", "HEAD")
	return "file://" + dir, mainCommit, designCommit
}

func TestFetch(t *testing.T) {
	url, mainCommit, designCommit := repository(t)
	c := &Client{HomeDir: t.TempDir(), AllowLocal: true}
	ctx := context.Background()
	dest := filepath.Join(t.TempDir(), "source")

	commit, err := c.Fetch(ctx, url, "design/a", "", dest)
	if err != nil || commit != designCommit {
		t.Fatalf("Fetch = %s, %v", commit, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "app.txt")); string(b) != "design\n" {
		t.Fatalf("checkout content %q", b)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); !os.IsNotExist(err) {
		t.Fatal(".git left in the build context")
	}

	// A retry pinned to the first commit fails if the branch has moved.
	if _, err := c.Fetch(ctx, url, "design/a", mainCommit, dest); err != nil {
		t.Fatalf("pinned fetch of a reachable commit: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "app.txt")); string(b) != "main\n" {
		t.Fatal("pinned fetch did not check out the pinned commit")
	}

	_, err = c.Fetch(ctx, url, "missing", "", dest)
	if e, ok := domain.AsError(err); !ok || e.Code != domain.CodeSourceFailed || !strings.Contains(e.Hint, "git push") {
		t.Fatalf("missing branch error: %v", err)
	}

	head, err := c.Verify(ctx, url)
	if err != nil || head != designCommit {
		// HEAD of the source repository is the checked-out design/a branch.
		t.Fatalf("Verify = %s, %v", head, err)
	}
}

func TestLocalRepositoriesRejectedInProduction(t *testing.T) {
	url, _, _ := repository(t)
	c := &Client{HomeDir: t.TempDir()}
	if _, err := c.Fetch(context.Background(), url, "main", "", filepath.Join(t.TempDir(), "s")); err == nil {
		t.Fatal("file:// repository fetched without AllowLocal")
	}
}

type staticCredentials struct{ seen string }

func (s *staticCredentials) Credentials(_ context.Context, repo string) (string, string, error) {
	s.seen = repo
	return "x-access-token", "secret-token", nil
}

func TestCredentialsStayOutOfArguments(t *testing.T) {
	creds := &staticCredentials{}
	c := &Client{HomeDir: t.TempDir(), Credentials: creds}
	env, err := c.env(context.Background(), "https://github.com/o/r.git")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	if creds.seen != "https://github.com/o/r.git" || !strings.Contains(joined, "GIT_CONFIG_KEY_0=http.https://github.com/o/r.git.extraheader") {
		t.Fatalf("credentials not scoped to the repository: %s", joined)
	}
	for _, arg := range c.baseArgs() {
		if strings.Contains(arg, "secret") {
			t.Fatal("credential leaked into arguments")
		}
	}
}
