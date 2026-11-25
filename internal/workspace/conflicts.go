package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// conflictRe matches a Syncthing conflict copy:
// <base>.sync-conflict-<YYYYMMDD>-<HHMMSS>-<short device id><ext>.
var conflictRe = regexp.MustCompile(`^(.+)\.sync-conflict-(\d{8}-\d{6})-([A-Z0-9]{7})(\.[^.]*)?$`)

// HistoryFile is the prompt history the agent merges automatically.
const HistoryFile = "history.jsonl"

// ConflictCopy is one Syncthing conflict copy of a file.
type ConflictCopy struct {
	Name    string    `json:"name"` // path relative to the root, slash-separated
	Device  string    `json:"device"`
	When    string    `json:"when"` // Syncthing's YYYYMMDD-HHMMSS stamp
	ModTime time.Time `json:"mtime"`
	Size    int64     `json:"size"`
}

// Conflict is a file with at least one conflict copy next to it.
type Conflict struct {
	Path      string         `json:"path"` // the original, relative to the root
	Exists    bool           `json:"exists"`
	Copies    []ConflictCopy `json:"copies"`
	Mergeable bool           `json:"mergeable"` // "keep both merged" is offered
}

// ParseConflictName splits a conflict copy's base name into the original
// base name, the device short ID and the stamp; ok is false for any other
// name.
func ParseConflictName(name string) (orig, device, when string, ok bool) {
	m := conflictRe.FindStringSubmatch(name)
	if m == nil {
		return "", "", "", false
	}
	return m[1] + m[4], m[3], m[2], true
}

// FindConflicts lists every Syncthing conflict copy inside the synced
// items of root (only what syncs can conflict), grouped by original file
// and sorted by path. Plugin caches are skipped.
func FindConflicts(root string, cfg *config.Config) ([]Conflict, error) {
	byPath := map[string]*Conflict{}
	add := func(full string, info fs.FileInfo) {
		orig, dev, when, ok := ParseConflictName(info.Name())
		if !ok {
			return
		}
		relDir, err := filepath.Rel(root, filepath.Dir(full))
		if err != nil {
			return
		}
		rel := filepath.ToSlash(filepath.Join(relDir, orig))
		c := byPath[rel]
		if c == nil {
			_, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
			c = &Conflict{Path: rel, Exists: statErr == nil, Mergeable: mergeable(rel)}
			byPath[rel] = c
		}
		name := filepath.ToSlash(filepath.Join(relDir, info.Name()))
		c.Copies = append(c.Copies, ConflictCopy{Name: name, Device: dev, When: when, ModTime: info.ModTime().UTC(), Size: info.Size()})
	}
	// Top-level conflict copies of the synced files.
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("workspace: read %s: %w", root, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil {
			add(filepath.Join(root, e.Name()), info)
		}
	}
	for _, it := range config.SyncItems {
		if !it.Dir || (cfg != nil && !cfg.SyncEnabled(it.Key)) {
			continue
		}
		dir := filepath.Join(root, it.Path)
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == dir && errors.Is(err, fs.ErrNotExist) {
					return fs.SkipAll
				}
				return nil // unreadable corners never fail the scan
			}
			if d.IsDir() {
				if d.Name() == "cache" && it.Path == "plugins" {
					return fs.SkipDir
				}
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			if info, err := d.Info(); err == nil {
				add(p, info)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("workspace: scan %s: %w", dir, err)
		}
	}
	out := make([]Conflict, 0, len(byPath))
	for _, c := range byPath {
		sort.Slice(c.Copies, func(i, j int) bool { return c.Copies[i].Name < c.Copies[j].Name })
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// mergeable: line-union is safe for text, not for JSON documents.
func mergeable(rel string) bool {
	return !strings.EqualFold(filepath.Ext(rel), ".json")
}

// ConflictBackupDir is where merged or losing versions go:
// <cwm>/backups/conflicts/<stamp>/.
func ConflictBackupDir(l *storage.Layout, now time.Time) string {
	return filepath.Join(l.BackupsDir(), "conflicts", now.UTC().Format("20060102-150405"))
}

// MergeHistoryConflicts folds every history.sync-conflict-*.jsonl in the
// workspace root into history.jsonl (dedupe by exact line, ordered by the
// "timestamp" field when present; lines without one keep their place
// after their predecessor) and moves each merged copy into
// backups/conflicts/. It returns the number of copies merged; zero copies
// is a no-op that writes nothing.
func MergeHistoryConflicts(l *storage.Layout, now time.Time) (int, error) {
	root := l.WorkspaceDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("workspace: read %s: %w", root, err)
	}
	var copies []string
	for _, e := range entries {
		if orig, _, _, ok := ParseConflictName(e.Name()); ok && orig == HistoryFile && !e.IsDir() {
			copies = append(copies, filepath.Join(root, e.Name()))
		}
	}
	if len(copies) == 0 {
		return 0, nil
	}
	sort.Strings(copies)
	dst := filepath.Join(root, HistoryFile)
	if err := mergeHistory(dst, copies...); err != nil {
		return 0, err
	}
	bdir := ConflictBackupDir(l, now)
	for _, c := range copies {
		if err := moveInto(c, bdir); err != nil {
			return 0, err
		}
	}
	return len(copies), nil
}

// mergeHistory writes dst = the deduplicated, timestamp-ordered union of
// dst and the extra files.
func mergeHistory(dst string, extra ...string) error {
	have, err := readLines(dst)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("workspace: read %s: %w", dst, err)
	}
	type line struct {
		text string
		ts   float64
		seq  int
	}
	seen := map[string]bool{}
	var all []line
	var last float64
	push := func(lines []string) {
		last = 0
		for _, ln := range lines {
			if ln == "" || seen[ln] {
				continue
			}
			seen[ln] = true
			if ts, ok := lineTimestamp(ln); ok {
				last = ts
			}
			all = append(all, line{text: ln, ts: last, seq: len(all)})
		}
	}
	push(have)
	for _, x := range extra {
		lines, err := readLines(x)
		if err != nil {
			return fmt.Errorf("workspace: read %s: %w", x, err)
		}
		push(lines)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].ts != all[j].ts {
			return all[i].ts < all[j].ts
		}
		return all[i].seq < all[j].seq
	})
	var b bytes.Buffer
	for _, ln := range all {
		b.WriteString(ln.text)
		b.WriteByte('\n')
	}
	if old, err := os.ReadFile(dst); err == nil && bytes.Equal(old, b.Bytes()) {
		return nil
	}
	if err := storage.WriteFileAtomic(dst, b.Bytes(), 0o600); err != nil {
		return fmt.Errorf("workspace: write %s: %w", dst, err)
	}
	return nil
}

// lineTimestamp reads a numeric or RFC 3339 "timestamp" field of a JSON
// line.
func lineTimestamp(ln string) (float64, bool) {
	var v struct {
		Timestamp json.RawMessage `json:"timestamp"`
	}
	if json.Unmarshal([]byte(ln), &v) != nil || len(v.Timestamp) == 0 {
		return 0, false
	}
	var n float64
	if json.Unmarshal(v.Timestamp, &n) == nil {
		return n, true
	}
	var s string
	if json.Unmarshal(v.Timestamp, &s) == nil {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return float64(t.UnixMilli()), true
		}
	}
	return 0, false
}

// Resolve choices besides a copy's name.
const (
	ResolveKeepCurrent = "current" // keep the file as it is; copies go to backups
	ResolveMerge       = "merge"   // keep both: line-union into the file
)

// ResolveConflict settles the conflict of the file rel (relative to the
// root): choice is ResolveKeepCurrent, ResolveMerge, or the Name of one of
// its copies, which then replaces the file. Every losing or merged version
// is moved into backups/conflicts/<stamp>/ (nothing is deleted); it
// returns that backup dir.
func ResolveConflict(l *storage.Layout, cfg *config.Config, rel, choice string, now time.Time) (string, error) {
	root := l.WorkspaceDir()
	all, err := FindConflicts(root, cfg)
	if err != nil {
		return "", err
	}
	var c *Conflict
	for i := range all {
		if all[i].Path == rel {
			c = &all[i]
		}
	}
	if c == nil {
		return "", fmt.Errorf("workspace: no conflict copies of %s", rel)
	}
	target := filepath.Join(root, filepath.FromSlash(c.Path))
	bdir := ConflictBackupDir(l, now)
	copyPath := func(cc ConflictCopy) string { return filepath.Join(root, filepath.FromSlash(cc.Name)) }
	switch choice {
	case ResolveKeepCurrent:
		if !c.Exists {
			return "", fmt.Errorf("workspace: %s does not exist; pick one of its copies", rel)
		}
	case ResolveMerge:
		if !c.Mergeable {
			return "", fmt.Errorf("workspace: %s is a JSON document; pick one version instead of merging", rel)
		}
		var paths []string
		for _, cc := range c.Copies {
			paths = append(paths, copyPath(cc))
		}
		if c.Exists {
			if err := backupCopy(target, bdir, filepath.Dir(c.Path)); err != nil {
				return "", err
			}
		}
		if c.Path == HistoryFile || strings.HasSuffix(c.Path, ".jsonl") {
			err = mergeHistory(target, paths...)
		} else {
			err = MergeLines(target, paths...)
		}
		if err != nil {
			return "", err
		}
	default:
		var win *ConflictCopy
		for i := range c.Copies {
			if c.Copies[i].Name == choice {
				win = &c.Copies[i]
			}
		}
		if win == nil {
			return "", fmt.Errorf("workspace: %q is not a version of %s (want %s, %s or a copy name)", choice, rel, ResolveKeepCurrent, ResolveMerge)
		}
		if c.Exists {
			if err := moveInto(target, filepath.Join(bdir, filepath.Dir(filepath.FromSlash(c.Path)))); err != nil {
				return "", err
			}
		}
		data, err := os.ReadFile(copyPath(*win))
		if err != nil {
			return "", fmt.Errorf("workspace: read %s: %w", win.Name, err)
		}
		if err := storage.WriteFileAtomic(target, data, 0o644); err != nil {
			return "", fmt.Errorf("workspace: write %s: %w", target, err)
		}
	}
	for _, cc := range c.Copies {
		if err := moveInto(copyPath(cc), filepath.Join(bdir, filepath.Dir(filepath.FromSlash(cc.Name)))); err != nil {
			return "", err
		}
	}
	return bdir, nil
}

// backupCopy copies src (kept in place) into dir/sub.
func backupCopy(src, dir, sub string) error {
	dst := filepath.Join(dir, filepath.FromSlash(sub), filepath.Base(src))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("workspace: create %s: %w", filepath.Dir(dst), err)
	}
	if err := storage.CopyFile(src, dst); err != nil {
		return fmt.Errorf("workspace: back up %s: %w", src, err)
	}
	return nil
}

// moveInto moves src into dir (rename, else copy then remove), never
// overwriting: a taken name gets a numeric suffix.
func moveInto(src, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("workspace: create %s: %w", dir, err)
	}
	dst := filepath.Join(dir, filepath.Base(src))
	for i := 1; ; i++ {
		if _, err := os.Lstat(dst); os.IsNotExist(err) {
			break
		}
		dst = filepath.Join(dir, fmt.Sprintf("%s.%d", filepath.Base(src), i))
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := storage.CopyFile(src, dst); err != nil {
		return fmt.Errorf("workspace: move %s: %w", src, err)
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("workspace: remove %s after copying it: %w", src, err)
	}
	return nil
}

// MergeHistory folds extra history files into dst with the conflict-merge
// rule (exact-line dedupe, ordered by timestamp); the migration uses it.
func MergeHistory(dst string, extra ...string) error { return mergeHistory(dst, extra...) }
