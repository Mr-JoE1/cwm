//go:build !windows

package agent

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockFile opens p and takes a non-blocking exclusive flock on it.
func lockFile(p string) (*os.File, error) {
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("agent: open lock %s: %w", p, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("agent: lock %s: %w", p, err)
	}
	return f, nil
}

func unlockFile(f *os.File) error {
	uerr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	cerr := f.Close()
	return errors.Join(uerr, cerr)
}
