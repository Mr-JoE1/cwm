//go:build windows

package claudebin

import (
	"os/exec"
	"syscall"
)

// createNoWindow keeps probes (npm.cmd, claude --version) started from the
// windowless agent from flashing a console.
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
