//go:build !windows

package agent

import "syscall"

// DetachAttr is the SysProcAttr of a background agent process (see
// StartDetached).
func DetachAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
