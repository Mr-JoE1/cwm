// Package pathmirror keeps sessions resumable across devices whose clones
// of the same repo sit at different paths (SPEC-V0.8 "Cross-device
// resume", D91). Claude Code files a session under projects/<folder name
// of the cwd>, so a session made at C:\Users\me\projects\api is invisible
// to claude at /home/me/projects/api. pathmirror computes the folder name
// exactly as Claude Code does and materialises a local copy of a session
// under this machine's folder, rewriting cwd, extended by the prefix rule
// and never modifying the source copy.
package pathmirror

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

// maxFolderName is Claude Code's limit before it truncates and hashes.
const maxFolderName = 200

// FolderName is the projects/ folder Claude Code (2.1.319, SPEC-V0.8
// research notes) uses for path: every character outside [a-zA-Z0-9]
// becomes "-"; a result over 200 characters is cut to 200 and suffixed
// with "-" and the base-36 absolute value of a 32-bit string hash of the
// ORIGINAL path. JavaScript strings are UTF-16, so non-BMP characters
// count (and become "-") twice.
func FolderName(path string) string {
	units := utf16.Encode([]rune(path))
	out := make([]byte, len(units))
	for i, u := range units {
		switch {
		case u >= 'a' && u <= 'z', u >= 'A' && u <= 'Z', u >= '0' && u <= '9':
			out[i] = byte(u)
		default:
			out[i] = '-'
		}
	}
	if len(out) <= maxFolderName {
		return string(out)
	}
	return string(out[:maxFolderName]) + "-" + strconv.FormatInt(abs64(int64(javaHash(units))), 36)
}

// javaHash is `h = (h<<5) - h + c | 0` over UTF-16 code units.
func javaHash(units []uint16) int32 {
	var h int32
	for _, u := range units {
		h = (h << 5) - h + int32(u)
	}
	return h
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// NormalizeRemote turns a git remote URL into a repo identity:
// host/path, host lower-cased, scheme, user and ".git" stripped, so the
// ssh and https remotes of one repo compare equal.
func NormalizeRemote(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		if at := strings.LastIndex(strings.SplitN(u, "/", 2)[0], "@"); at >= 0 {
			u = u[at+1:]
		}
		host, rest, _ := strings.Cut(u, "/")
		if h, _, ok := strings.Cut(host, ":"); ok { // drop a port
			host = h
		}
		u = strings.ToLower(host) + "/" + rest
	} else if at := strings.Index(u, "@"); at >= 0 && strings.Contains(u[at:], ":") {
		// scp-like: git@host:path
		host, rest, _ := strings.Cut(u[at+1:], ":")
		u = strings.ToLower(host) + "/" + rest
	} else if host, rest, ok := strings.Cut(u, ":"); ok && !strings.Contains(host, "/") && len(host) > 1 {
		u = strings.ToLower(host) + "/" + rest
	}
	u = strings.TrimSuffix(strings.TrimRight(u, "/"), ".git")
	return strings.TrimRight(u, "/")
}

// Copy is one copy of a session file found under projects/.
type Copy struct {
	Folder  string    `json:"folder"` // the projects/ sub-folder
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
	Lines   int       `json:"lines"`
	Cwd     string    `json:"cwd,omitempty"` // first cwd value in the file
	Last    string    `json:"last,omitempty"`

	keys []string
}

// FindCopies lists every projects/*/<id>.jsonl under root.
func FindCopies(root, id string) ([]Copy, error) {
	dirs, err := os.ReadDir(filepath.Join(root, "projects"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("pathmirror: read projects: %w", err)
	}
	var out []Copy
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		p := filepath.Join(root, "projects", d.Name(), id+".jsonl")
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		c := Copy{Folder: d.Name(), Path: p, Size: info.Size(), ModTime: info.ModTime()}
		if err := c.load(); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Folder < out[j].Folder })
	return out, nil
}

type lineInfo struct {
	UUID      string          `json:"uuid"`
	Cwd       *string         `json:"cwd"`
	Timestamp json.RawMessage `json:"timestamp"`
}

func (c *Copy) load() error {
	lines, err := readLines(c.Path)
	if err != nil {
		return err
	}
	c.Lines = len(lines)
	c.keys = make([]string, len(lines))
	for i, ln := range lines {
		var li lineInfo
		_ = json.Unmarshal([]byte(ln), &li)
		if c.Cwd == "" && li.Cwd != nil {
			c.Cwd = *li.Cwd
		}
		if len(li.Timestamp) > 0 {
			var ts string
			if json.Unmarshal(li.Timestamp, &ts) == nil {
				c.Last = ts
			}
		}
		c.keys[i] = lineKey(ln, li)
	}
	return nil
}

// lineKey identifies a line independently of the cwd rewrite: its uuid,
// else the hash of the line with cwd removed.
func lineKey(ln string, li lineInfo) string {
	if li.UUID != "" {
		return "u:" + li.UUID
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(ln), &m) == nil {
		delete(m, "cwd")
		if b, err := json.Marshal(m); err == nil {
			ln = string(b)
		}
	}
	s := sha256.Sum256([]byte(ln))
	return "h:" + hex.EncodeToString(s[:8])
}

func readLines(p string) ([]string, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("pathmirror: open %s: %w", p, err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 256<<20)
	for sc.Scan() {
		if ln := sc.Text(); strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("pathmirror: read %s: %w", p, err)
	}
	return out, nil
}

// isPrefix reports a's keys as a prefix of b's.
func isPrefix(a, b []string) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Pick chooses the copy to resume (the prefix rule): the one every other
// copy is a prefix of. Copies that are not on one chain have diverged:
// Pick then returns the copy whose last entry is newest and diverged=true.
func Pick(copies []Copy) (best Copy, diverged bool) {
	if len(copies) == 0 {
		return Copy{}, false
	}
	best = copies[0]
	for _, c := range copies[1:] {
		switch {
		case isPrefix(best.keys, c.keys):
			best = c
		case isPrefix(c.keys, best.keys):
		default:
			diverged = true
		}
	}
	if !diverged {
		return best, false
	}
	// Check every pair against the chosen best; pick by newest entry.
	newest := copies[0]
	for _, c := range copies[1:] {
		if c.Last > newest.Last || (c.Last == newest.Last && c.ModTime.After(newest.ModTime)) {
			newest = c
		}
	}
	return newest, true
}

// Result says what Materialise did.
type Result struct {
	Local    string `json:"local"`  // the local copy's path
	Source   string `json:"source"` // the copy it was made from
	Wrote    bool   `json:"wrote"`
	Diverged bool   `json:"diverged"`
	Sidecars int    `json:"sidecars"`
}

// Materialise makes root/projects/<FolderName(localRepo)>/<id>.jsonl the
// newest version of session id, every cwd equal to or under srcRepo
// rewritten to the same place under localRepo. A local copy that
// diverged from the newest is never overwritten (Result.Diverged). The
// source copy is never modified; writes are atomic.
func Materialise(root, id, srcRepo, localRepo string) (Result, error) {
	copies, err := FindCopies(root, id)
	if err != nil {
		return Result{}, err
	}
	if len(copies) == 0 {
		return Result{}, fmt.Errorf("pathmirror: session %s not found", id)
	}
	folder := FolderName(localRepo)
	res := Result{Local: filepath.Join(root, "projects", folder, id+".jsonl")}
	src, diverged := Pick(copies)
	res.Source, res.Diverged = src.Path, diverged
	var local *Copy
	for i := range copies {
		if copies[i].Folder == folder {
			local = &copies[i]
		}
	}
	if local != nil {
		if local.Path == src.Path || isPrefix(src.keys, local.keys) {
			return res, nil // already the newest (or ahead)
		}
		if !isPrefix(local.keys, src.keys) {
			res.Diverged = true
			return res, nil // both continued: keep both, Health warns
		}
	}
	if err := rewriteFile(src.Path, res.Local, srcRepo, localRepo); err != nil {
		return res, err
	}
	res.Wrote = true
	n, err := copySidecars(filepath.Join(filepath.Dir(src.Path), id), filepath.Join(root, "projects", folder, id), srcRepo, localRepo)
	res.Sidecars = n
	return res, err
}

// RewriteCwd maps p from under srcRepo to under localRepo (both cleaned,
// either separator style); other paths are returned unchanged.
func RewriteCwd(p, srcRepo, localRepo string) string {
	if srcRepo == "" || p == "" {
		return p
	}
	norm := func(s string) string { return strings.TrimRight(strings.ReplaceAll(s, `\`, "/"), "/") }
	np, ns := norm(p), norm(srcRepo)
	eq := func(x, y string) bool {
		if looksWindows(srcRepo) { // Windows paths compare case-insensitively
			return strings.EqualFold(x, y)
		}
		return x == y
	}
	if len(np) < len(ns) || !eq(np[:len(ns)], ns) || (len(np) > len(ns) && np[len(ns)] != '/') {
		return p
	}
	rest := np[len(ns):]
	if rest == "" {
		return localRepo
	}
	return filepath.Join(localRepo, filepath.FromSlash(strings.TrimPrefix(rest, "/")))
}

func looksWindows(p string) bool {
	return len(p) >= 2 && p[1] == ':' || strings.Contains(p, `\`)
}

func rewriteLine(ln, srcRepo, localRepo string) string {
	if !strings.Contains(ln, `"cwd"`) {
		return ln
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(ln), &m) != nil {
		return ln
	}
	raw, ok := m["cwd"]
	if !ok {
		return ln
	}
	var cwd string
	if json.Unmarshal(raw, &cwd) != nil {
		return ln
	}
	nc := RewriteCwd(cwd, srcRepo, localRepo)
	if nc == cwd {
		return ln
	}
	b, _ := json.Marshal(nc)
	m["cwd"] = b
	out, err := json.Marshal(m)
	if err != nil {
		return ln
	}
	return string(out)
}

func rewriteFile(src, dst, srcRepo, localRepo string) error {
	lines, err := readLines(src)
	if err != nil {
		return err
	}
	var b bytes.Buffer
	for _, ln := range lines {
		b.WriteString(rewriteLine(ln, srcRepo, localRepo))
		b.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("pathmirror: create %s: %w", filepath.Dir(dst), err)
	}
	if err := storage.WriteFileAtomic(dst, b.Bytes(), 0o600); err != nil {
		return fmt.Errorf("pathmirror: write %s: %w", dst, err)
	}
	return nil
}

// copySidecars mirrors the session's sidecar dir (subagents, tool
// results): .jsonl files with the cwd rewrite, other files as they are.
func copySidecars(src, dst, srcRepo, localRepo string) (int, error) {
	if info, err := os.Stat(src); err != nil || !info.IsDir() || src == dst {
		return 0, nil
	}
	n := 0
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return nil
		}
		to := filepath.Join(dst, rel)
		if strings.HasSuffix(p, ".jsonl") {
			if err := rewriteFile(p, to, srcRepo, localRepo); err != nil {
				return err
			}
		} else {
			if old, err := os.ReadFile(to); err == nil {
				if cur, err := os.ReadFile(p); err == nil && bytes.Equal(old, cur) {
					return nil
				}
			}
			if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
				return fmt.Errorf("pathmirror: create %s: %w", filepath.Dir(to), err)
			}
			if err := storage.CopyFile(p, to); err != nil {
				return err
			}
		}
		n++
		return nil
	})
	return n, err
}
