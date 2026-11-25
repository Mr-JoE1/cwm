package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// Entry types of a backup manifest.
const (
	TypeFile = "file"
	TypeDir  = "dir"
	TypeLink = "link"
)

// Root is one path the backup covers: a dir (walked without following
// links) or a single file. Exists is false when the path was absent, so a
// rollback knows to move whatever appeared there since.
type Root struct {
	Path    string   `json:"path"`
	Exists  bool     `json:"exists"`
	Exclude []string `json:"exclude,omitempty"` // slash paths relative to Path, never backed up nor rolled back
}

// Entry is one backed-up filesystem entry. Rel is slash-separated and
// relative to its root ("" = the root itself).
type Entry struct {
	Root   int    `json:"root"`
	Rel    string `json:"rel"`
	Type   string `json:"type"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Link   string `json:"link,omitempty"`
	// Skipped is why a file has no backup copy ("locked": another process
	// held it open through every retry, D101). A rollback leaves it alone.
	Skipped string `json:"skipped,omitempty"`
}

// SkippedLocked marks an entry another process kept locked.
const SkippedLocked = "locked"

// Manifest lists every backed-up path with its sha256 (SPEC-V0.8
// Migration step 1). Data is at <backup>/data/<root index>/<rel>.
type Manifest struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	// Incomplete is set while the backup is being written (D101): a
	// backup that never finished keeps it, and is never used to roll back.
	Incomplete bool    `json:"incomplete,omitempty"`
	PID        int     `json:"pid,omitempty"`
	Roots      []Root  `json:"roots"`
	Entries    []Entry `json:"entries"`
}

// ErrIncomplete means a backup dir holds no complete manifest.
var ErrIncomplete = errors.New("migrate: incomplete backup")

// ManifestName is the manifest file inside a backup dir.
const ManifestName = "manifest.json"

func dataPath(dir string, root int, rel string) string {
	return filepath.Join(dir, "data", strconv.Itoa(root), filepath.FromSlash(rel))
}

// holdsExcluded reports a dir (rel under root, "" = root itself) with an
// excluded path somewhere below it: a rollback descends into it instead
// of moving it aside whole, so the excluded path stays where it is.
func holdsExcluded(root, rel string, ex []string) bool {
	if len(ex) == 0 {
		return false
	}
	base := root
	if rel != "" {
		base = filepath.Join(root, filepath.FromSlash(rel))
	}
	found := false
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return fs.SkipAll
		}
		r, rerr := filepath.Rel(root, p)
		if rerr != nil || r == "." {
			return nil
		}
		if excluded(filepath.ToSlash(r), ex) {
			found = true
			return fs.SkipAll
		}
		if info, ierr := d.Info(); ierr == nil && isLink(info) && d.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
	return found
}

// excluded reports a slash path that a root's Exclude list covers. A
// plain entry covers that path and everything below it; a glob without a
// slash ("*.lock") matches an entry's name at any depth; a glob with a
// slash ("state/*.pid") matches the path or one of its parents.
func excluded(rel string, ex []string) bool {
	for _, e := range ex {
		if !strings.ContainsAny(e, "*?[") {
			if rel == e || strings.HasPrefix(rel, e+"/") {
				return true
			}
			continue
		}
		if !strings.Contains(e, "/") {
			for r := rel; r != "." && r != "" && r != "/"; r = path.Dir(r) {
				if ok, _ := path.Match(e, path.Base(r)); ok {
					return true
				}
			}
			continue
		}
		for r := rel; r != "." && r != "" && r != "/"; r = path.Dir(r) {
			if ok, _ := path.Match(e, r); ok {
				return true
			}
		}
	}
	return false
}

// isLink reports a symlink, or on Windows a junction (Go reports mount
// points as irregular entries).
func isLink(info fs.FileInfo) bool {
	return info.Mode()&fs.ModeSymlink != 0 || (runtime.GOOS == "windows" && info.Mode()&fs.ModeIrregular != 0)
}

// backupOpts tune a backup run (D101).
type backupOpts struct {
	// needed reports a path the migration reads or writes: if it stays
	// locked, the backup fails instead of skipping it.
	needed func(p string) bool
	// warn reports a file skipped because another process held it.
	warn func(p, holder string)
}

// Backup copies every root into dir and writes the manifest. Links are
// recorded, never followed: a login-only dir's links into the workspace
// must not copy the workspace twice.
func Backup(dir, id string, roots []Root, now time.Time) (*Manifest, error) {
	return backup(dir, id, roots, now, backupOpts{})
}

func backup(dir, id string, roots []Root, now time.Time, bo backupOpts) (*Manifest, error) {
	m := &Manifest{ID: id, Created: now.UTC(), Incomplete: true, PID: os.Getpid()}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("migrate: create backup %s: %w", dir, err)
	}
	// Written first: a run that dies half-way leaves a dir that says so.
	if err := writeJSON(filepath.Join(dir, ManifestName), m); err != nil {
		return nil, err
	}
	for i, r := range roots {
		info, err := os.Lstat(r.Path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("migrate: back up %s: %w", r.Path, err)
			}
			r.Exists = false
			m.Roots = append(m.Roots, r)
			continue
		}
		r.Exists = true
		m.Roots = append(m.Roots, r)
		if !info.IsDir() || isLink(info) {
			e, err := backupEntry(dir, i, "", r.Path, info, bo)
			if err != nil {
				return nil, err
			}
			m.Entries = append(m.Entries, e)
			continue
		}
		err = filepath.WalkDir(r.Path, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return fmt.Errorf("migrate: walk %s: %w", p, werr)
			}
			rel, err := filepath.Rel(r.Path, p)
			if err != nil {
				return fmt.Errorf("migrate: rel %s: %w", p, err)
			}
			rel = filepath.ToSlash(rel)
			if rel == "." {
				rel = ""
			}
			if rel != "" && excluded(rel, r.Exclude) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return fmt.Errorf("migrate: stat %s: %w", p, err)
			}
			e, err := backupEntry(dir, i, rel, p, info, bo)
			if err != nil {
				return err
			}
			m.Entries = append(m.Entries, e)
			if d.IsDir() && e.Type == TypeLink {
				return fs.SkipDir
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	m.Incomplete = false
	if err := writeJSON(filepath.Join(dir, ManifestName), m); err != nil {
		return nil, err
	}
	return m, nil
}

// Seams for the locked-file tests: how a backup opens a source file and
// waits between retries.
var (
	openForBackup = func(p string) (io.ReadCloser, error) { return os.Open(p) }
	backupSleep   = time.Sleep
)

// lockRetries is the backoff between attempts at a file another process
// holds open: about two seconds in all, then the file is skipped.
var lockRetries = []time.Duration{100 * time.Millisecond, 300 * time.Millisecond, 600 * time.Millisecond, 1000 * time.Millisecond}

// LockedError is a file the migration needs that another process keeps
// locked (Windows sharing violation).
type LockedError struct {
	Path   string
	Holder string
	Err    error
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("migrate: %s is locked by another process (likely %s) and this migration needs it — close that and run the migration again", e.Path, e.Holder)
}

func (e *LockedError) Unwrap() error { return e.Err }

// LikelyHolder names the program that most likely holds p open.
func LikelyHolder(p string) string {
	base := strings.ToLower(filepath.Base(p))
	switch {
	case base == "agent.lock" || base == "server.toml":
		return "the cwm agent — stop it with `cwm service stop`"
	case base == ".credentials.json" || base == ".claude.json" || strings.HasSuffix(base, ".jsonl"):
		return "Claude Code — close every claude session, the desktop app and the editor extension"
	}
	if claude, err := claudepaths.ClaudeDir(); err == nil && (p == claude || strings.HasPrefix(p, claude+string(filepath.Separator))) {
		return "Claude Code — close every claude session, the desktop app and the editor extension"
	}
	return "another program: an editor, Syncthing or antivirus"
}

func backupEntry(dir string, root int, rel, src string, info fs.FileInfo, bo backupOpts) (Entry, error) {
	e := Entry{Root: root, Rel: rel, Mode: uint32(info.Mode().Perm())}
	dst := dataPath(dir, root, rel)
	switch {
	case isLink(info):
		target, err := os.Readlink(src)
		if err != nil {
			return e, fmt.Errorf("migrate: readlink %s: %w", src, err)
		}
		e.Type, e.Link = TypeLink, target
	case info.IsDir():
		e.Type = TypeDir
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return e, fmt.Errorf("migrate: create %s: %w", dst, err)
		}
	case info.Mode().IsRegular():
		e.Type, e.Size = TypeFile, info.Size()
		sum, err := copyHashedRetry(src, dst)
		if err != nil {
			if !isLocked(err) {
				return e, err
			}
			holder := LikelyHolder(src)
			if bo.needed != nil && bo.needed(src) {
				return e, &LockedError{Path: src, Holder: holder, Err: err}
			}
			_ = os.Remove(dst)
			e.Skipped = SkippedLocked
			if bo.warn != nil {
				bo.warn(src, holder)
			}
			return e, nil
		}
		e.SHA256 = sum
	default:
		// Sockets, devices: nothing a rollback could restore. Recorded
		// as absent; the rollback leaves them alone.
		return e, nil
	}
	return e, nil
}

// copyHashedRetry is copyHashed through the backup opener, retried with
// a short backoff while another process holds src locked.
func copyHashedRetry(src, dst string) (string, error) {
	sum, err := copyFrom(openForBackup, src, dst)
	for _, d := range lockRetries {
		if err == nil || !isLocked(err) {
			break
		}
		backupSleep(d)
		sum, err = copyFrom(openForBackup, src, dst)
	}
	return sum, err
}

// copyHashed copies src to dst (parents created) and returns the sha256
// of what was copied.
func copyHashed(src, dst string) (string, error) {
	return copyFrom(func(p string) (io.ReadCloser, error) { return os.Open(p) }, src, dst)
}

func copyFrom(open func(string) (io.ReadCloser, error), src, dst string) (string, error) {
	in, err := open(src)
	if err != nil {
		return "", fmt.Errorf("migrate: open %s: %w", src, err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", fmt.Errorf("migrate: create %s: %w", filepath.Dir(dst), err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("migrate: create %s: %w", dst, err)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		out.Close()
		return "", fmt.Errorf("migrate: copy %s: %w", src, err)
	}
	if err := out.Close(); err != nil {
		return "", fmt.Errorf("migrate: write %s: %w", dst, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileSHA(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadManifest loads <dir>/manifest.json.
func ReadManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, fmt.Errorf("migrate: read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("migrate: parse manifest: %w", err)
	}
	if m.Incomplete {
		return nil, fmt.Errorf("%w: %s never finished — it cannot be rolled back to (remove it: cwm migrate clean)", ErrIncomplete, dir)
	}
	return &m, nil
}

// Restore makes every root of the manifest byte-identical to the backup:
// entries that appeared since are moved into trash (nothing is deleted),
// changed or missing ones are restored from the backup data. It returns
// how many paths it moved away and restored.
func Restore(dir string, m *Manifest, trash string) (moved, restored int, err error) {
	byRoot := map[int]map[string]Entry{}
	for _, e := range m.Entries {
		if e.Type == "" {
			continue
		}
		if byRoot[e.Root] == nil {
			byRoot[e.Root] = map[string]Entry{}
		}
		byRoot[e.Root][e.Rel] = e
	}
	toTrash := func(root int, rel, p string) error {
		dst := dataPath(trash, root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return fmt.Errorf("migrate: create %s: %w", filepath.Dir(dst), err)
		}
		if err := moveAside(p, dst); err != nil {
			return err
		}
		moved++
		return nil
	}
	for i, r := range m.Roots {
		want := byRoot[i]
		cur, lerr := os.Lstat(r.Path)
		// A root that did not exist but now holds excluded paths (the cwm
		// home holding its own backups) keeps those and loses the rest.
		keepShell := (!r.Exists || len(want) == 0) && lerr == nil && cur.IsDir() && !isLink(cur) && holdsExcluded(r.Path, "", r.Exclude)
		if (!r.Exists || len(want) == 0) && !keepShell {
			if lerr == nil {
				if err := toTrash(i, "", r.Path); err != nil {
					return moved, restored, err
				}
			}
			continue
		}
		// Pass 1: move away whatever the manifest does not know, or whose
		// type changed. Top-down, so a whole new subtree moves at once —
		// except a dir holding an excluded path, which is descended into.
		if keepShell || (lerr == nil && cur.IsDir() && !isLink(cur) && want[""].Type == TypeDir) {
			var extra []string
			werr := filepath.WalkDir(r.Path, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				rel, rerr := filepath.Rel(r.Path, p)
				if rerr != nil || rel == "." {
					return nil
				}
				rel = filepath.ToSlash(rel)
				if excluded(rel, r.Exclude) {
					if d.IsDir() {
						return fs.SkipDir
					}
					return nil
				}
				info, ierr := d.Info()
				if ierr != nil {
					return nil
				}
				e, ok := want[rel]
				if !ok && d.IsDir() && !isLink(info) && holdsExcluded(r.Path, rel, r.Exclude) {
					return nil
				}
				if !ok || e.Type != entryType(info) {
					extra = append(extra, rel)
					if d.IsDir() {
						return fs.SkipDir
					}
					return nil
				}
				if isLink(info) && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			})
			if werr != nil {
				return moved, restored, fmt.Errorf("migrate: scan %s: %w", r.Path, werr)
			}
			for _, rel := range extra {
				if err := toTrash(i, rel, filepath.Join(r.Path, filepath.FromSlash(rel))); err != nil {
					return moved, restored, err
				}
			}
		} else if lerr == nil && entryType(cur) != want[""].Type {
			if err := toTrash(i, "", r.Path); err != nil {
				return moved, restored, err
			}
		}
		// Pass 2: restore every entry, parents first.
		rels := make([]string, 0, len(want))
		for rel := range want {
			rels = append(rels, rel)
		}
		sort.Strings(rels)
		for _, rel := range rels {
			e := want[rel]
			p := r.Path
			if rel != "" {
				p = filepath.Join(r.Path, filepath.FromSlash(rel))
			}
			did, err := restoreEntry(dir, e, p)
			if err != nil {
				return moved, restored, err
			}
			if did {
				restored++
			}
		}
		// Dir modes last: a read-only dir would block restoring into it.
		for _, rel := range rels {
			if e := want[rel]; e.Type == TypeDir {
				p := filepath.Join(r.Path, filepath.FromSlash(rel))
				_ = os.Chmod(p, fs.FileMode(e.Mode))
			}
		}
	}
	return moved, restored, nil
}

func entryType(info fs.FileInfo) string {
	switch {
	case isLink(info):
		return TypeLink
	case info.IsDir():
		return TypeDir
	case info.Mode().IsRegular():
		return TypeFile
	}
	return ""
}

func restoreEntry(dir string, e Entry, p string) (bool, error) {
	if e.Skipped != "" {
		return false, nil // never backed up: whatever is there now stays
	}
	switch e.Type {
	case TypeDir:
		if info, err := os.Lstat(p); err == nil && info.IsDir() {
			return false, nil
		}
		if err := os.MkdirAll(p, 0o700); err != nil {
			return false, fmt.Errorf("migrate: restore %s: %w", p, err)
		}
		return true, nil
	case TypeLink:
		if got, err := os.Readlink(p); err == nil && got == e.Link {
			return false, nil
		}
		if _, err := os.Lstat(p); err == nil {
			if err := os.Remove(p); err != nil {
				return false, fmt.Errorf("migrate: replace link %s: %w", p, err)
			}
		}
		if err := makeLink(e.Link, p); err != nil {
			return false, fmt.Errorf("migrate: restore link %s: %w", p, err)
		}
		return true, nil
	case TypeFile:
		if sum, err := fileSHA(p); err == nil && sum == e.SHA256 {
			if info, err := os.Stat(p); err == nil && uint32(info.Mode().Perm()) != e.Mode {
				_ = os.Chmod(p, fs.FileMode(e.Mode))
			}
			return false, nil
		}
		src := dataPath(dir, e.Root, e.Rel)
		data, err := os.ReadFile(src)
		if err != nil {
			return false, fmt.Errorf("migrate: backup copy of %s: %w", p, err)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return false, fmt.Errorf("migrate: restore %s: %w", p, err)
		}
		if err := storage.WriteFileAtomic(p, data, fs.FileMode(e.Mode)); err != nil {
			return false, fmt.Errorf("migrate: restore %s: %w", p, err)
		}
		_ = os.Chmod(p, fs.FileMode(e.Mode))
		return true, nil
	}
	return false, nil
}

// makeLink recreates a link; on Windows a dir target that refuses a
// symlink becomes a junction again.
func makeLink(target, p string) error {
	err := os.Symlink(target, p)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	if info, serr := os.Stat(target); serr == nil && info.IsDir() {
		return junction(target, p)
	}
	return err
}

func writeJSON(p string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("migrate: encode %s: %w", filepath.Base(p), err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("migrate: create %s: %w", filepath.Dir(p), err)
	}
	if err := storage.WriteFileAtomic(p, data, 0o600); err != nil {
		return fmt.Errorf("migrate: write %s: %w", p, err)
	}
	return nil
}

// moveAside renames p to dst; across filesystems it copies (links kept as
// links) and then removes p, so the content still exists exactly once.
func moveAside(p, dst string) error {
	if err := os.Rename(p, dst); err == nil {
		return nil
	}
	err := filepath.WalkDir(p, func(src string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, err := filepath.Rel(p, src)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case isLink(info):
			target, err := os.Readlink(src)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(to+".link-target", []byte(target+"\n"), 0o600); err != nil {
				return err
			}
			if d.IsDir() {
				return fs.SkipDir
			}
		case d.IsDir():
			return os.MkdirAll(to, 0o700)
		case info.Mode().IsRegular():
			if _, err := copyHashed(src, to); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("migrate: move %s aside: %w", p, err)
	}
	if err := os.RemoveAll(p); err != nil {
		return fmt.Errorf("migrate: move %s aside: %w", p, err)
	}
	return nil
}
