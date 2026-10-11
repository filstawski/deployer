package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"deployer/internal/command"
	"deployer/internal/domain"
)

var commitRE = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)

// Checkout supports public HTTPS source in this slice. Private deploy-key
// enrollment is deliberately separate from inheriting host SSH credentials.
func Checkout(ctx context.Context, repository, ref, pinned, destination string) (string, error) {
	if err := domain.ValidateRepository(repository); err != nil {
		return "", err
	}
	if !strings.HasPrefix(repository, "https://") {
		return "", domain.Fail("UNSUPPORTED_FEATURE", "source builds currently require a public HTTPS repository")
	}
	return checkout(ctx, repository, ref, pinned, destination, false)
}
func checkout(ctx context.Context, repository, ref, pinned, destination string, localTest bool) (string, error) {
	if err := domain.ValidateRef(ref); err != nil {
		return "", err
	}
	if pinned != "" && !commitRE.MatchString(pinned) {
		return "", fmt.Errorf("invalid recorded commit")
	}
	// destination is an installation-owned per-deployment workspace. Clear partial
	// checkouts before retries so untracked files cannot leak between revisions.
	if err := os.RemoveAll(destination); err != nil {
		return "", err
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return "", err
	}
	home := filepath.Join(filepath.Dir(destination), "git-home")
	if err := os.MkdirAll(home, 0700); err != nil {
		return "", err
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/usr/bin/false", "LC_ALL=C"}
	filePolicy := "never"
	if localTest {
		filePolicy = "always"
	}
	run := func(args ...string) (string, error) {
		prefix := []string{"-c", "credential.helper=", "-c", "http.followRedirects=false", "-c", "protocol.file.allow=" + filePolicy, "-c", "protocol.ext.allow=never", "-c", "core.hooksPath=/dev/null"}
		return command.Run(ctx, destination, env, nil, "git", append(prefix, args...)...)
	}
	if _, err := run("init", "--quiet"); err != nil {
		return "", domain.Fail("SOURCE_FAILED", "cannot initialize source workspace")
	}
	wanted := "refs/heads/" + ref
	if commitRE.MatchString(ref) {
		wanted = ref
	}
	if pinned != "" {
		wanted = pinned
	}
	if _, err := run("fetch", "--no-tags", "--depth=1", "--", repository, wanted); err != nil {
		return "", domain.Fail("SOURCE_FAILED", "cannot fetch requested branch/commit; check repository access and ref")
	}
	sha, err := run("rev-parse", "--verify", "FETCH_HEAD^{commit}")
	sha = strings.TrimSpace(sha)
	if err != nil || !commitRE.MatchString(sha) {
		return "", domain.Fail("SOURCE_FAILED", "Git did not return an exact commit")
	}
	if pinned != "" && sha != pinned {
		return "", domain.Fail("SOURCE_FAILED", "source no longer matches recorded commit")
	}
	if _, err := run("checkout", "--force", "--detach", sha); err != nil {
		return "", domain.Fail("SOURCE_FAILED", "cannot check out commit")
	}
	// Do not expand submodules or Git LFS implicitly. Avoid including repository
	// credentials/configuration in the build context, regardless of .dockerignore.
	if _, err := os.Stat(filepath.Join(destination, ".gitmodules")); err == nil {
		return "", domain.Fail("UNSUPPORTED_FEATURE", "submodules require explicit support")
	}
	if err := os.RemoveAll(filepath.Join(destination, ".git")); err != nil {
		return "", err
	}
	return sha, nil
}
