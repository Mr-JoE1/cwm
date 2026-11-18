//go:build windows

package agent

import "syscall"

// createNoWindow gives a console program a hidden console: the agent's own
// children (git, ...) inherit it instead of each popping a window.
const createNoWindow = 0x08000000

// DetachAttr is the SysProcAttr of a background agent process (see
// StartDetached).
func DetachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: createNoWindow | syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}
