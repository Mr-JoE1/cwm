package pathmirror

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Env is what resolving a session's local repo needs. Every lookup is a
// field so tests (and the dashboard) need no git or network.
type Env struct {
	Root      string // the mode root (holds projects/)
	Paths     *RepoPaths
	Index     SessionIndex
	ReposRoot string                       // the canonical clone root (~/repos)
	Manifest  func(name string) string     // repos.tsv: name -> clone URL ("" = unknown)
	RemoteOf  func(dir string) string      // `git -C dir remote get-url origin` ("" = none)
	Stat      func(p string) (bool, error) // dir exists; nil = os.Stat
}

func (e *Env) isDir(p string) bool {
	if p == "" {
		return false
	}
	if e.Stat != nil {
		ok, _ := e.Stat(p)
		return ok
	}
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// Plan says where session id resumes on this machine.
type Plan struct {
	ID       string `json:"id"`
	Source   Copy   `json:"source"`
	SrcRepo  string `json:"srcRepo"` // the source copy's cwd (the other device's path)
	Remote   string `json:"remote,omitempty"`
	Name     string `json:"name,omitempty"` // repo dir name
	URL      string `json:"url,omitempty"`  // clone URL when the repo must be cloned
	Local    string `json:"local,omitempty"`
	Exists   bool   `json:"exists"` // Local is already a directory
	Diverged bool   `json:"diverged"`
	Copies   int    `json:"copies"`
}

// NeedsMirror reports that the session must be materialised under Local.
func (p Plan) NeedsMirror() bool {
	return p.Local != "" && FolderName(p.Local) != p.Source.Folder
}

// PlanResume finds the newest copy of id (the prefix rule) and this
// machine's path for its repo: the recorded remote -> path map, then
// <repos root>/<name> when its origin is the same repo, then the source
// path itself when it exists here, else a clone target under the repos
// root (Exists=false, URL set) when repos.tsv knows the URL.
func (e *Env) PlanResume(id string) (Plan, error) {
	copies, err := FindCopies(e.Root, id)
	if err != nil {
		return Plan{}, err
	}
	if len(copies) == 0 {
		return Plan{}, fmt.Errorf("session %s not found under %s", id, filepath.Join(e.Root, "projects"))
	}
	best, diverged := Pick(copies)
	p := Plan{ID: id, Source: best, SrcRepo: best.Cwd, Diverged: diverged, Copies: len(copies)}
	if p.SrcRepo == "" {
		return p, nil // no cwd recorded: nothing to map
	}
	p.Name = baseName(p.SrcRepo)
	p.Remote = e.Index.Remote[id]
	if p.Remote == "" && e.Manifest != nil {
		p.URL = e.Manifest(p.Name)
		p.Remote = NormalizeRemote(p.URL)
	} else if e.Manifest != nil {
		p.URL = e.Manifest(p.Name)
		if NormalizeRemote(p.URL) != p.Remote {
			p.URL = ""
		}
	}
	remoteOf := func(dir string) string {
		if e.RemoteOf == nil {
			return ""
		}
		return NormalizeRemote(e.RemoteOf(dir))
	}
	if p.Remote != "" && e.Paths != nil {
		if dir, ok := e.Paths.Lookup(p.Remote); ok {
			p.Local, p.Exists = dir, true
			return p, nil
		}
	}
	cand := ""
	if e.ReposRoot != "" && p.Name != "" {
		cand = filepath.Join(e.ReposRoot, p.Name)
		if e.isDir(cand) && (p.Remote == "" || remoteOf(cand) == p.Remote) {
			p.Local, p.Exists = cand, true
			return p, nil
		}
	}
	if e.isDir(p.SrcRepo) && (p.Remote == "" || remoteOf(p.SrcRepo) == p.Remote) {
		p.Local, p.Exists = p.SrcRepo, true // the same path on this machine
		return p, nil
	}
	if cand != "" && !e.isDir(cand) && p.URL != "" {
		p.Local = cand // clone on demand
	}
	return p, nil
}

func baseName(p string) string {
	p = strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Apply materialises the plan's session under its local repo folder.
func (e *Env) Apply(p Plan) (Result, error) {
	if !p.NeedsMirror() {
		return Result{Local: p.Source.Path, Source: p.Source.Path, Diverged: p.Diverged}, nil
	}
	return Materialise(e.Root, p.ID, p.SrcRepo, p.Local)
}

// Mirrorer is the agent tick's mirroring state: a rate limit and a
// size+mtime signature per session, so unchanged sessions are never
// read. Whether to write is decided by content (the prefix rule), never
// by mtime, so two devices cannot ping-pong a session.
type Mirrorer struct {
	MinInterval time.Duration
	last        time.Time
	sigs        map[string]string // session id -> signature of its copies
	cwds        map[string]string // copy path -> first cwd
}

// MirrorReport is one mirroring pass.
type MirrorReport struct {
	Ran      bool     `json:"ran"`
	Checked  int      `json:"checked"`
	Mirrored []string `json:"mirrored,omitempty"`
	Diverged []string `json:"diverged,omitempty"`
	Errors   []string `json:"errors,omitempty"`
}

type copyStat struct {
	folder, path string
	size         int64
	mtime        time.Time
}

// scanCopies groups every projects/*/<id>.jsonl by id without reading.
func scanCopies(root string) map[string][]copyStat {
	out := map[string][]copyStat{}
	dirs, err := os.ReadDir(filepath.Join(root, "projects"))
	if err != nil {
		return out
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(root, "projects", d.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			n := f.Name()
			if f.IsDir() || !strings.HasSuffix(n, ".jsonl") || strings.HasPrefix(n, "agent-") {
				continue
			}
			info, err := f.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			id := strings.TrimSuffix(n, ".jsonl")
			out[id] = append(out[id], copyStat{folder: d.Name(), path: filepath.Join(dir, n), size: info.Size(), mtime: info.ModTime()})
		}
	}
	return out
}

func signature(cs []copyStat) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = c.folder + ":" + strconv.FormatInt(c.size, 10) + ":" + strconv.FormatInt(c.mtime.UnixNano(), 10)
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// FirstCwd returns the first cwd value in a session file, reading only
// as far as needed.
func FirstCwd(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for n := 0; sc.Scan() && n < 200; n++ {
		ln := sc.Bytes()
		if !strings.Contains(string(ln), `"cwd"`) {
			continue
		}
		var li struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(ln, &li) == nil && li.Cwd != "" {
			return li.Cwd
		}
	}
	return ""
}

// Tick keeps every session whose repo exists here at another path
// mirrored under this machine's folder. It never clones and never
// touches a diverged local copy.
func (m *Mirrorer) Tick(e *Env, now time.Time) MirrorReport {
	var rep MirrorReport
	if m.MinInterval > 0 && !m.last.IsZero() && now.Sub(m.last) < m.MinInterval {
		return rep
	}
	m.last, rep.Ran = now, true
	if m.sigs == nil {
		m.sigs, m.cwds = map[string]string{}, map[string]string{}
	}
	all := scanCopies(e.Root)
	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		cs := all[id]
		sig := signature(cs)
		if m.sigs[id] == sig {
			continue
		}
		rep.Checked++
		if len(cs) == 1 { // the common case: decide from the first cwd alone
			cwd, ok := m.cwds[cs[0].path]
			if !ok {
				cwd = FirstCwd(cs[0].path)
				m.cwds[cs[0].path] = cwd
			}
			if cwd == "" || (FolderName(cwd) == cs[0].folder && e.isDir(cwd)) {
				m.sigs[id] = sig // made on this machine (or unmappable)
				continue
			}
		}
		p, err := e.PlanResume(id)
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if p.Local != "" && p.Exists && p.NeedsMirror() {
			res, err := e.Apply(p)
			if err != nil {
				rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", id, err))
				continue
			}
			if res.Wrote {
				rep.Mirrored = append(rep.Mirrored, id)
			}
			if res.Diverged {
				rep.Diverged = append(rep.Diverged, id)
			}
		} else if p.Diverged {
			rep.Diverged = append(rep.Diverged, id)
		}
		m.sigs[id] = signature(scanCopiesOf(cs, e.Root, id, p.Local))
	}
	return rep
}

// scanCopiesOf re-stats an id's copies (plus the one just written) so the
// next tick's signature includes our own write.
func scanCopiesOf(cs []copyStat, root, id, local string) []copyStat {
	paths := map[string]string{}
	for _, c := range cs {
		paths[c.path] = c.folder
	}
	if local != "" {
		f := FolderName(local)
		paths[filepath.Join(root, "projects", f, id+".jsonl")] = f
	}
	var out []copyStat
	for p, f := range paths {
		if info, err := os.Stat(p); err == nil {
			out = append(out, copyStat{folder: f, path: p, size: info.Size(), mtime: info.ModTime()})
		}
	}
	return out
}

// Diverged lists the sessions with copies that are not on one prefix
// chain — continued on two devices (the Health warning).
func Diverged(root string) ([]string, error) {
	var out []string
	for id, cs := range scanCopies(root) {
		if len(cs) < 2 {
			continue
		}
		copies, err := FindCopies(root, id)
		if err != nil {
			return nil, err
		}
		if _, div := Pick(copies); div {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Mirrored counts the sessions with more than one copy.
func Mirrored(root string) int {
	n := 0
	for _, cs := range scanCopies(root) {
		if len(cs) > 1 {
			n++
		}
	}
	return n
}
