//go:build windows

package claudeopen

import (
	"os/exec"
	"syscall"
)

// createNoWindow keeps an editor CLI (code.cmd) started from the
// windowless agent from flashing a console.
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
