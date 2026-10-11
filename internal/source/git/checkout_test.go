package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deployer/internal/command"
)

func TestCheckoutPinsCommitAndIsolatesWorkspaces(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := command.Run(ctx, repo, nil, nil, "git", args...)
		if err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
		return strings.TrimSpace(out)
	}
	run("init", "--initial-branch=main")
	run("config", "user.email", "test@example.invalid")
	run("config", "user.name", "Test")
	commit := func(body string) string {
		t.Helper()
		os.WriteFile(filepath.Join(repo, "revision"), []byte(body), 0600)
		run("add", "revision")
		run("commit", "-m", body)
		return run("rev-parse", "HEAD")
	}
	first := commit("first")
	dest := filepath.Join(t.TempDir(), "source")
	got, err := checkout(ctx, repo, "main", "", dest, true)
	if err != nil || got != first {
		t.Fatalf("initial checkout: %s %v", got, err)
	}
	commit("second")
	os.WriteFile(filepath.Join(dest, "stray"), []byte("must disappear"), 0600)
	got, err = checkout(ctx, repo, "main", first, dest, true)
	if err != nil || got != first {
		t.Fatalf("pin moved: %s %v", got, err)
	}
	b, _ := os.ReadFile(filepath.Join(dest, "revision"))
	if string(b) != "first" {
		t.Fatal("checkout followed branch instead of pinned commit")
	}
	if _, err = os.Stat(filepath.Join(dest, "stray")); !os.IsNotExist(err) {
		t.Fatal("leftover file survived retry")
	}
	if _, err = os.Stat(filepath.Join(dest, ".git")); !os.IsNotExist(err) {
		t.Fatal("Git config was included in build context")
	}
	if _, err = Checkout(ctx, repo, "main", "", dest); err == nil {
		t.Fatal("production checkout accepted local repository")
	}
}
