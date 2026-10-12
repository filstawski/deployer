// Package execx runs external programs such as git and docker without a shell.
package execx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// MaxOutput bounds the output captured from a command.
const MaxOutput = 1 << 20

// Cmd describes a command to run.
type Cmd struct {
	Name string
	Args []string
	Dir  string
	// Env replaces the environment when non-nil.
	Env []string
	// Stdin is optional input.
	Stdin io.Reader
	// Log, if set, also receives combined output as it is produced.
	Log io.Writer
}

// Error is returned when a command exits unsuccessfully. Output holds the
// captured combined stdout and stderr.
type Error struct {
	Cmd    string
	Err    error
	Output string
}

func (e *Error) Error() string {
	out := strings.TrimSpace(e.Output)
	if len(out) > 2000 {
		out = "..." + out[len(out)-2000:]
	}
	if out == "" {
		return fmt.Sprintf("%s: %v", e.Cmd, e.Err)
	}
	return fmt.Sprintf("%s: %v: %s", e.Cmd, e.Err, out)
}

func (e *Error) Unwrap() error { return e.Err }

// Run executes c and returns its combined output, bounded to MaxOutput.
// Cancelling ctx kills the command's whole process group, so helpers spawned
// by git or docker cannot outlive the operation.
func Run(ctx context.Context, c Cmd) (string, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	cmd.Stdin = c.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	var buf bytes.Buffer
	var out io.Writer = &LimitedWriter{W: &buf, N: MaxOutput}
	if c.Log != nil {
		out = io.MultiWriter(out, c.Log)
	}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return buf.String(), &Error{Cmd: c.Name + " " + firstArg(c.Args), Err: err, Output: buf.String()}
	}
	return buf.String(), nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// LimitedWriter writes at most N bytes to W and silently discards the rest,
// so a noisy process cannot exhaust memory or disk.
type LimitedWriter struct {
	W io.Writer
	N int64
}

func (w *LimitedWriter) Write(p []byte) (int, error) {
	if w.N > 0 {
		keep := p
		if int64(len(keep)) > w.N {
			keep = keep[:w.N]
		}
		n, err := w.W.Write(keep)
		w.N -= int64(n)
		if err != nil {
			return n, err
		}
	}
	return len(p), nil
}
