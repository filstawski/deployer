//go:build !windows

package execx

import (
	"os/exec"
	"syscall"
)

// killGroup makes cancelling cmd kill its whole process group, so helpers
// spawned by git or docker cannot outlive the operation.
func killGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
