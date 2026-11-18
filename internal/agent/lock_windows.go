//go:build windows

package agent

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// errSharingViolation is ERROR_SHARING_VIOLATION (32).
const errSharingViolation syscall.Errno = 32

// lockFile opens p with share mode 0: while this handle is open, every
// other CreateFile on p fails with a sharing violation. The handle (and so
// the lock) dies with the process.
func lockFile(p string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return nil, fmt.Errorf("agent: lock path %s: %w", p, err)
	}
	h, err := syscall.CreateFile(name,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, // no sharing: this is the lock
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		if errors.Is(err, errSharingViolation) || errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("agent: lock %s: %w", p, err)
	}
	return os.NewFile(uintptr(h), p), nil
}

func unlockFile(f *os.File) error { return f.Close() }
