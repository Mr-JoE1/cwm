package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// WriteFileAtomic durably replaces path's content: write a temp file in the
// SAME directory (atomic rename requires same filesystem), fsync it, rename.
// Readers never observe a torn file. Callers fsync'd before rename matter:
// a crash after rename must not resurrect the old content.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("storage: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("storage: write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("storage: fsync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("storage: close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("storage: chmod %s: %w", tmpName, err)
	}
	if err := renameRetry(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("storage: rename %s -> %s: %w", tmpName, path, err)
	}
	return nil
}

// renameRetry is os.Rename, retried briefly on Windows: there a rename
// over a file another process or goroutine has open (the agent hashing
// config.toml for its change feed, an antivirus scan, Syncthing) fails
// with "Access denied" until that handle closes. Elsewhere it is one
// plain rename.
func renameRetry(from, to string) error {
	err := os.Rename(from, to)
	if runtime.GOOS != "windows" {
		return err
	}
	for i := 1; err != nil && os.IsPermission(err) && i <= 10; i++ {
		time.Sleep(time.Duration(i*15) * time.Millisecond)
		err = os.Rename(from, to)
	}
	return err
}
