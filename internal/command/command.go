package command

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// Run never invokes a shell and bounds captured diagnostics. Kill the process
// group on cancellation so git/build helper subprocesses cannot outlive jobs.
func Run(ctx context.Context, dir string, env []string, log io.Writer, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	var b bytes.Buffer
	w := &LimitWriter{W: &b, Remaining: 1 << 20}
	var out io.Writer = w
	if log != nil {
		out = io.MultiWriter(w, log)
	}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	return b.String(), err
}

type LimitWriter struct {
	W         io.Writer
	Remaining int64
}

func (w *LimitWriter) Write(p []byte) (int, error) {
	n := len(p)
	if w.Remaining > 0 {
		keep := min(int64(n), w.Remaining)
		written, err := w.W.Write(p[:keep])
		w.Remaining -= int64(written)
		if err != nil {
			return 0, err
		}
	}
	return n, nil
}
