package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrLocked means another agent holds the single-instance lock.
var ErrLocked = errors.New("agent: another cwm agent is running")

// LockPath is <root>/state/agent.lock.
func LockPath(root string) string { return filepath.Join(root, "state", "agent.lock") }

// Lock is the held single-instance lock. The OS releases it when the
// process exits, however it exits, so a crashed agent never leaves a stale
// lock behind.
type Lock struct {
	f *os.File
}

// Acquire takes the single-instance lock without blocking; ErrLocked when
// another process holds it.
func Acquire(root string) (*Lock, error) {
	p := LockPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, fmt.Errorf("agent: create %s: %w", filepath.Dir(p), err)
	}
	f, err := lockFile(p)
	if err != nil {
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Release drops the lock (idempotent).
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := unlockFile(l.f)
	l.f = nil
	if err != nil {
		return fmt.Errorf("agent: release lock: %w", err)
	}
	return nil
}
