package workspace

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// SharedItems are the workspace-root items a login-only dir links to
// (D80): every allow-list item Claude Code itself reads. They are shared
// between accounts whatever the [sync] switches say — those decide what
// crosses devices, not what crosses accounts. mcp.json and repos.tsv are
// cwm's own files, which claude never reads, so they are not linked.
func SharedItems() []config.SyncItem {
	var out []config.SyncItem
	for _, it := range config.SyncItems {
		if it.Path == "mcp.json" || it.Path == "repos.tsv" {
			continue
		}
		out = append(out, it)
	}
	return out
}

// LoginDirResult reports what EnsureLoginDir did.
type LoginDirResult struct {
	Dir string
	// Relinked lists items whose link was missing, wrong or broken.
	Relinked []string
	// Merged lists diverged files merged back into the workspace root
	// before they were re-linked (a Windows hardlink broken by an atomic
	// rename, or a file claude wrote before the link existed).
	Merged []string
	// Backup holds whatever was replaced ("" = nothing).
	Backup string
}

// EnsureLoginDir prepares account's login-only dir ~/.cwm/accounts/<name>
// (D80): it keeps the account's own .credentials.json and .claude.json and
// links every shared item to the workspace root, which it never stores
// data of its own for. It is idempotent and re-verifies every link, so
// every `cwm run` and agent tick repairs what an atomic rename broke.
func EnsureLoginDir(l *storage.Layout, cfg *config.Config, account string) (LoginDirResult, error) {
	return EnsureLoginDirWith(l, cfg, account, OSLinker{})
}

// EnsureLoginDirWith is EnsureLoginDir with an injected Linker.
func EnsureLoginDirWith(l *storage.Layout, cfg *config.Config, account string, lk Linker) (LoginDirResult, error) {
	var res LoginDirResult
	if _, ok := cfg.Account(account); !ok {
		return res, fmt.Errorf("workspace: unknown account %q (see: cwm account list)", account)
	}
	root, err := claudepaths.Root(l, cfg.EffectiveMode())
	if err != nil {
		return res, err
	}
	dir := claudepaths.LoginDir(l, account)
	res.Dir = dir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return res, fmt.Errorf("workspace: create %s: %w", dir, err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return res, fmt.Errorf("workspace: create %s: %w", root, err)
	}
	backupOnce := func() (string, error) {
		if res.Backup == "" {
			res.Backup = l.BackupDirPath("login-" + storage.SanitizedLabel(account))
			if err := os.MkdirAll(res.Backup, 0o700); err != nil {
				return "", fmt.Errorf("workspace: create backup %s: %w", res.Backup, err)
			}
		}
		return res.Backup, nil
	}
	for _, it := range SharedItems() {
		src := filepath.Join(root, it.Path)
		if err := ensureTarget(src, it); err != nil {
			return res, err
		}
		changed, merged, err := linkItem(lk, cfg.LinkMode, filepath.Join(dir, it.Path), src, it, backupOnce)
		if err != nil {
			return res, err
		}
		if changed {
			res.Relinked = append(res.Relinked, it.Path)
		}
		if merged {
			res.Merged = append(res.Merged, it.Path)
		}
	}
	return res, nil
}

// ensureTarget creates a missing shared item in the root so a link has
// something to point at: claude writing into a dangling link would fail,
// and a real file in the login dir would split the data.
func ensureTarget(src string, it config.SyncItem) error {
	if _, err := os.Lstat(src); err == nil {
		return nil
	}
	if it.Dir {
		if err := os.MkdirAll(src, 0o755); err != nil {
			return fmt.Errorf("workspace: create %s: %w", src, err)
		}
		return nil
	}
	body := []byte{}
	if it.Path == "settings.json" {
		body = []byte("{}\n") // an empty settings.json is not valid JSON
	}
	if err := os.WriteFile(src, body, 0o644); err != nil {
		return fmt.Errorf("workspace: create %s: %w", src, err)
	}
	return nil
}

// linkItem makes dst a link to src. A correct link (or an intact
// hardlink) is left alone; a wrong link is replaced; a real entry is
// merged back into src first, then backed up and replaced.
func linkItem(lk Linker, linkMode, dst, src string, it config.SyncItem, backupOnce func() (string, error)) (changed, merged bool, err error) {
	fi, lerr := os.Lstat(dst)
	switch {
	case lerr != nil && !os.IsNotExist(lerr):
		return false, false, fmt.Errorf("workspace: stat %s: %w", dst, lerr)
	case lerr == nil && fi.Mode()&os.ModeSymlink != 0:
		got, err := os.Readlink(dst)
		if err == nil && sameLinkTarget(src, got) {
			return false, false, nil
		}
		if err := os.Remove(dst); err != nil {
			return false, false, fmt.Errorf("workspace: remove %s: %w", dst, err)
		}
	case lerr == nil:
		if !it.Dir && fi.Mode().IsRegular() {
			if si, err := os.Stat(src); err == nil && os.SameFile(fi, si) {
				return false, false, nil // intact hardlink
			}
		}
		if isJunctionTo(dst, src) {
			return false, false, nil
		}
		if err := mergeBack(dst, src, it, backupOnce); err != nil {
			return false, false, err
		}
		merged = true
		b, err := backupOnce()
		if err != nil {
			return false, false, err
		}
		if err := storage.BackupEntry(dst, b); err != nil {
			return false, false, err
		}
		if err := os.RemoveAll(dst); err != nil {
			return false, false, fmt.Errorf("workspace: remove %s: %w", dst, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, false, fmt.Errorf("workspace: create %s: %w", filepath.Dir(dst), err)
	}
	if err := place(lk, linkKind(runtime.GOOS, it.Dir, linkMode), src, dst); err != nil {
		return false, false, fmt.Errorf("workspace: link %s -> %s: %w", dst, src, err)
	}
	return true, merged, nil
}

// isJunctionTo reports a Windows junction that already points at src
// (os.Readlink of a junction may fail on some Windows versions; SameFile
// of the resolved dirs is the robust test).
func isJunctionTo(dst, src string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	a, err1 := os.Stat(dst)
	b, err2 := os.Stat(src)
	return err1 == nil && err2 == nil && a.IsDir() && os.SameFile(a, b)
}

// mergeBack folds a diverged real entry into the workspace root:
// history.jsonl by line (exact-line dedupe, appended in order), other
// files newest-wins (the loser stays in the backup), dirs file by file
// without overwriting what the root already has.
func mergeBack(dst, src string, it config.SyncItem, backupOnce func() (string, error)) error {
	if it.Dir {
		return filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(dst, p)
			if err != nil {
				return err
			}
			target := filepath.Join(src, rel)
			if _, err := os.Lstat(target); err == nil {
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("workspace: create %s: %w", filepath.Dir(target), err)
			}
			return storage.CopyFile(p, target)
		})
	}
	if it.Path == "history.jsonl" {
		return MergeLines(src, dst)
	}
	a, err1 := os.Stat(dst)
	b, err2 := os.Stat(src)
	if err1 != nil {
		return nil
	}
	if err2 != nil || a.ModTime().After(b.ModTime()) {
		if err2 == nil {
			bk, err := backupOnce()
			if err != nil {
				return err
			}
			if err := storage.CopyFile(src, filepath.Join(bk, "root-"+filepath.Base(src))); err != nil {
				return fmt.Errorf("workspace: back up %s: %w", src, err)
			}
		}
		data, err := os.ReadFile(dst)
		if err != nil {
			return fmt.Errorf("workspace: read %s: %w", dst, err)
		}
		return storage.WriteFileAtomic(src, data, 0o644)
	}
	return nil
}

// MergeLines appends to dst every line of each extra file that dst does
// not hold yet (exact-line dedupe), keeping dst's order and then the extra
// files' order. A trailing newline is kept.
func MergeLines(dst string, extra ...string) error {
	have, err := readLines(dst)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("workspace: read %s: %w", dst, err)
	}
	seen := map[string]bool{}
	for _, ln := range have {
		seen[ln] = true
	}
	out := append([]string{}, have...)
	for _, x := range extra {
		lines, err := readLines(x)
		if err != nil {
			return fmt.Errorf("workspace: read %s: %w", x, err)
		}
		for _, ln := range lines {
			if !seen[ln] {
				seen[ln] = true
				out = append(out, ln)
			}
		}
	}
	if len(out) == len(have) {
		return nil
	}
	var b bytes.Buffer
	for _, ln := range out {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	return storage.WriteFileAtomic(dst, b.Bytes(), 0o644)
}

func readLines(p string) ([]string, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for sc.Scan() {
		if ln := sc.Text(); ln != "" {
			out = append(out, ln)
		}
	}
	return out, sc.Err()
}
