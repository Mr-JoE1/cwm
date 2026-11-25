//go:build windows

package migrate

import (
	"errors"
	"syscall"
)

// errSharingViolation is ERROR_SHARING_VIOLATION (32): another process
// opened the file without sharing it (the agent's lock, a running claude).
const errSharingViolation = syscall.Errno(32)

// errLockViolation is ERROR_LOCK_VIOLATION (33): a byte-range lock.
const errLockViolation = syscall.Errno(33)

func isLocked(err error) bool {
	return errors.Is(err, errSharingViolation) || errors.Is(err, errLockViolation)
}
