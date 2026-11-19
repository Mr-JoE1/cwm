//go:build !windows

package accounts

import (
	"errors"
	"syscall"
)

// processAlive reports whether pid still runs: signal 0 probes without
// delivering anything; EPERM means it exists under another user.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
