//go:build windows

package syncengine

import (
	"os/exec"
	"syscall"
)

const (
	detachedProcess     = 0x00000008
	createNewProcessGrp = 0x00000200
)

// detach starts the child as a detached background process in a new process
// group so it survives cwm's exit and leaves the console alone.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess | createNewProcessGrp,
	}
}
