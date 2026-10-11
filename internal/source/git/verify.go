package git

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"deployer/internal/domain"
)

// Verify uses the same repository-scoped authentication as source checkout.
func Verify(stateDir string, providers ...TokenProvider) func(context.Context, domain.Project) (any, error) {
	return func(ctx context.Context, p domain.Project) (any, error) {
		if !strings.HasPrefix(p.Repository, "https://") {
			return nil, domain.Fail("UNSUPPORTED_FEATURE", "verification requires an HTTPS repository")
		}
		home := filepath.Join(stateDir, "git-home")
		if err := os.MkdirAll(home, 0700); err != nil {
			return nil, err
		}
		jobCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(jobCtx, "git", "-c", "credential.helper=", "-c", "http.followRedirects=false", "-c", "protocol.file.allow=never", "-c", "protocol.ext.allow=never", "ls-remote", "--exit-code", "--", p.Repository, "HEAD")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/usr/bin/false", "LC_ALL=C"}
		auth, err := authentication(jobCtx, p.Repository, providers)
		if err != nil {
			return nil, err
		}
		cmd.Env = append(cmd.Env, auth...)
		cmd.Dir = home
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return nil
			}
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		cmd.WaitDelay = time.Second
		var output bytes.Buffer
		cmd.Stdout = &limitedWriter{Buffer: &output, Limit: 4096}
		if err := cmd.Run(); err != nil {
			if jobCtx.Err() != nil {
				return nil, domain.Fail("OPERATION_TIMEOUT", "repository verification timed out")
			}
			return nil, domain.Fail("VERIFICATION_FAILED", "Git could not read the repository HEAD; check the HTTPS URL and GitHub App repository access")
		}
		fields := strings.Fields(output.String())
		if len(fields) != 2 || fields[1] != "HEAD" || (len(fields[0]) != 40 && len(fields[0]) != 64) {
			return nil, domain.Fail("VERIFICATION_FAILED", "repository has no readable default-branch commit")
		}
		return map[string]any{"verified": true, "head_commit": fields[0]}, nil
	}
}

type limitedWriter struct {
	Buffer *bytes.Buffer
	Limit  int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if w.Buffer.Len() < w.Limit {
		keep := min(len(p), w.Limit-w.Buffer.Len())
		w.Buffer.Write(p[:keep])
	}
	return n, nil
}
