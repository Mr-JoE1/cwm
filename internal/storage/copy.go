// Copy and backup helpers shared by the account and launch-dir mutations:
// every pre-mutation backup funnels through here so restore semantics stay
// in one place.
package storage

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CopyFile copies a regular file, preserving permissions. dst's parent dir
// must exist.
func CopyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("storage: read %s: %w", src, err)
	}
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("storage: stat %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("storage: write %s: %w", dst, err)
	}
	return nil
}

// CopyTree recursively copies directory src to dst (which must not yet
// exist). Symlinks and special files are skipped: backups only need real
// content.
func CopyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("storage: stat %s: %w", src, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("storage: %s is not a directory", src)
	}
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("storage: walk %s: %w", path, err)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("storage: rel %s %s: %w", src, path, err)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			perm := fs.FileMode(0o755)
			if di, err := d.Info(); err == nil {
				perm = di.Mode().Perm()
			}
			return os.MkdirAll(target, perm)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return CopyFile(path, target)
	})
	if err != nil {
		return fmt.Errorf("storage: copy tree %s -> %s: %w", src, dst, err)
	}
	return nil
}

// BackupDirPath returns a fresh backups/<timestamp>-<label> path. The path
// is unique (a -2, -3... suffix is appended on same-second collisions) but
// NOT created; callers make it only when they actually have something to
// back up.
func (l *Layout) BackupDirPath(label string) string {
	ts := time.Now().UTC().Format("20060102T150405Z")
	p := filepath.Join(l.BackupsDir(), ts+"-"+label)
	for n := 2; ; n++ {
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			return p
		}
		p = filepath.Join(l.BackupsDir(), fmt.Sprintf("%s-%s-%d", ts, label, n))
	}
}

// BackupEntry copies one filesystem entry (file, dir or symlink) into
// backupDir, preserving its name. A symlink is recorded as a
// <name>.link-target text file holding its target, so a wrong link can be
// reconstructed after a replace.
func BackupEntry(entry, backupDir string) error {
	fi, err := os.Lstat(entry)
	if err != nil {
		return fmt.Errorf("storage: stat %s: %w", entry, err)
	}
	base := filepath.Base(entry)
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(entry)
		if err != nil {
			return fmt.Errorf("storage: readlink %s: %w", entry, err)
		}
		if err := os.WriteFile(filepath.Join(backupDir, base+".link-target"), []byte(target+"\n"), 0o644); err != nil {
			return fmt.Errorf("storage: write link target: %w", err)
		}
		return nil
	}
	if fi.IsDir() {
		return CopyTree(entry, filepath.Join(backupDir, base))
	}
	return CopyFile(entry, filepath.Join(backupDir, base))
}

// SanitizedLabel turns an account/item name into a safe path element for
// backup dir names (defensive: names are already validated, but backups
// must never escape on hostile input).
func SanitizedLabel(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, s)
}
