package execx

import "os/exec"

// killGroup leaves the default, killing only cmd itself. Only the CLI runs on
// Windows, and the git commands it runs don't start lasting helpers.
func killGroup(*exec.Cmd) {}
