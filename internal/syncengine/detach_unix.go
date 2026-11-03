//go:build !windows

package syncengine

import (
	"os/exec"
	"syscall"
)

// detach puts the child into its own session (new process group, no
// controlling terminal) so it survives cwm's exit.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
