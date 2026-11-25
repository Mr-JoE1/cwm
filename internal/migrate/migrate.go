// Package migrate is `cwm migrate` (SPEC-V0.8 Migration, D83): it moves a
// v0.7.x layout, an existing ~/.claude or the other mode's root into the
// workspace root of the target mode in five steps — back up (with a
// sha256 manifest), plan, apply, verify, record — and rolls any
// migration back from its manifest byte for byte. Nothing is deleted:
// what a migration replaces stays in its backup, and the v0.7 launch dirs
// are moved into it.
package migrate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// Migration kinds.
const (
	KindV07    = "v0.7"   // a v0.7.x ~/.cwm layout
	KindAdopt  = "adopt"  // first run, an existing ~/.claude adopted in place
	KindFresh  = "fresh"  // first run, nothing to move
	KindSwitch = "switch" // a mode change between two v0.8 roots
	KindNone   = "none"   // the config already records the target mode
)

// Action kinds of a plan.
const (
	ActCopy       = "copy"        // dst absent: copy
	ActExtend     = "extend"      // dst is a prefix of src (a session continued): src replaces it
	ActConflict   = "conflict"    // both changed: src kept next to dst under a conflict name
	ActMergeLines = "merge-lines" // history.jsonl / repos.tsv: line union
	ActMergeJSON  = "merge-json"  // settings.json / mcp.json: deep merge, target wins
	ActVault      = "vault"       // a login saved into the vault
	ActImport     = "import"      // a login imported as a new account
	ActSwapIn     = "swap-in"     // the default account's login written to the live location
	ActMoveBackup = "move-backup" // a v0.7 launch dir moved into the backup
	ActProjects   = "projects"    // .claude.json project entries merged into the live one
	ActStIgnore   = "stignore"    // the allow-list .stignore written
)

// Action is one planned step.
type Action struct {
	Kind    string `json:"kind"`
	Src     string `json:"src,omitempty"`
	Dst     string `json:"dst,omitempty"`
	Account string `json:"account,omitempty"`
	Detail  string `json:"detail,omitempty"`

	// login paths (never serialised: plans show locations, not values)
	from, to claudepaths.Paths
	full     bool // vault: copy the whole .claude.json, not only identity keys
}

// Plan is the dry-run preview and the apply input.
type Plan struct {
	Kind      string   `json:"kind"`
	FromMode  string   `json:"from_mode"` // "" = pre-v0.8 config or none
	ToMode    string   `json:"to_mode"`
	Root      string   `json:"root"`
	Sources   []string `json:"sources"`
	Actions   []Action `json:"actions"`
	Skipped   int      `json:"skipped_identical"`
	Conflicts int      `json:"conflicts"`
	// Sessions is every session id found in the sources and the target:
	// verify proves each is present in the root afterwards.
	Sessions    []string `json:"sessions"`
	Default     string   `json:"default_account,omitempty"`
	BackupRoots []string `json:"backup_roots"`
	BackupBytes int64    `json:"backup_bytes"`
	Warnings    []string `json:"warnings,omitempty"`
	// Leftovers are backups an earlier run never finished (D101): the
	// apply removes them before it starts its own.
	Leftovers []string `json:"leftover_backups,omitempty"`

	roots     []Root
	newConfig *config.Config
}

// Options tune a migration.
type Options struct {
	To      string // target mode; "" = the config's mode, else capture
	Account string // name for a live login no account matches (default "default")
	Store   accounts.CredStore
	Now     func() time.Time
	Host    string
	// SyncFolder re-points the Syncthing folder at root (best effort;
	// nil = Syncthing not managed here).
	SyncFolder func(root string) error
	Progress   io.Writer
}

func (o *Options) defaults() {
	if o.Store == nil {
		o.Store = accounts.DefaultStore()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Host == "" {
		o.Host = storage.SanitizeHost(storage.HostName())
	}
	if o.Account == "" {
		o.Account = "default"
	}
	if o.Progress == nil {
		o.Progress = io.Discard
	}
}

// conflictDevice is the "device" of a migration conflict copy: seven
// upper-case characters, so Health lists it with the Syncthing conflicts
// and the dashboard's "pick one" action resolves it.
const conflictDevice = "MIGRATE"

func homeDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("migrate: home dir: %w", err)
	}
	return h, nil
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// nonEmptyDir reports a dir holding anything but a .stignore.
func nonEmptyDir(p string) bool {
	ents, err := os.ReadDir(p)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if e.Name() != ".stignore" {
			return true
		}
	}
	return false
}

// Detect names the migration a config needs to reach mode to.
func Detect(l *storage.Layout, cfg *config.Config, configExists bool, to string) (string, error) {
	claude, err := claudepaths.ClaudeDir()
	if err != nil {
		return "", err
	}
	switch {
	case cfg.Mode == to:
		return KindNone, nil
	case config.ValidMode(cfg.Mode):
		return KindSwitch, nil
	case configExists && (nonEmptyDir(l.IsolatedWorkspaceDir()) || nonEmptyDir(l.LaunchDir()) || len(cfg.Accounts) > 0):
		return KindV07, nil
	case to == config.ModeCapture && isDir(claude):
		return KindAdopt, nil
	case to == config.ModeIsolated && isDir(claude):
		// First run straight into isolated mode copies ~/.claude in.
		return KindSwitch, nil
	}
	return KindFresh, nil
}

// BuildPlan computes the whole migration without changing anything.
func BuildPlan(l *storage.Layout, cfg *config.Config, o Options) (*Plan, error) {
	o.defaults()
	to := o.To
	if to == "" {
		to = cfg.Mode
	}
	if to == "" {
		to = config.ModeCapture
	}
	if !config.ValidMode(to) {
		return nil, fmt.Errorf("migrate: invalid mode %q (want capture or isolated)", to)
	}
	_, statErr := os.Stat(l.ConfigPath())
	if statErr != nil {
		// No config.toml yet: Load handed back the defaults, whose mode
		// is not a recorded choice. This is a first run.
		first := *cfg
		first.Mode = ""
		cfg = &first
	}
	kind, err := Detect(l, cfg, statErr == nil, to)
	if err != nil {
		return nil, err
	}
	root, err := claudepaths.Root(l, to)
	if err != nil {
		return nil, err
	}
	p := &Plan{Kind: kind, FromMode: cfg.Mode, ToMode: to, Root: root}
	nc := *cfg
	nc.Accounts = append([]config.Account(nil), cfg.Accounts...)
	p.newConfig = &nc
	if kind == KindNone {
		return p, nil
	}

	claude, err := claudepaths.ClaudeDir()
	if err != nil {
		return nil, err
	}
	// Sources of synced items: the other root, and the real (unlinked)
	// items of v0.7 launch dirs (copy link mode, Windows fallbacks).
	other := l.IsolatedWorkspaceDir()
	if to == config.ModeIsolated {
		other = claude
	}
	if kind != KindAdopt && kind != KindFresh && isDir(other) && other != root {
		p.Sources = append(p.Sources, other)
	}
	launchDirs := v07LaunchDirs(l)
	p.Sources = append(p.Sources, launchDirs...)

	host := o.Host
	ids := map[string]bool{}
	collectSessions(root, ids)
	for _, src := range p.Sources {
		collectSessions(src, ids)
		planItems(p, src, root, host)
	}
	for id := range ids {
		p.Sessions = append(p.Sessions, id)
	}
	sort.Strings(p.Sessions)

	if err := planLogins(l, cfg, p, launchDirs, o); err != nil {
		return nil, err
	}
	if nonEmptyDir(l.LaunchDir()) {
		names := make([]string, 0, len(launchDirs))
		for _, d := range launchDirs {
			names = append(names, filepath.Base(d))
		}
		p.Actions = append(p.Actions, Action{Kind: ActMoveBackup, Src: l.LaunchDir(), Detail: "v0.7 launch dirs: " + strings.Join(names, ", ")})
	}
	p.Actions = append(p.Actions, Action{Kind: ActStIgnore, Dst: filepath.Join(root, ".stignore"), Detail: "allow-list from [sync]"})

	// What the backup covers: everything for a real move; for adopt and
	// fresh only what the apply writes (~/.claude itself is not touched).
	hj, err := claudepaths.HomeClaudeJSON()
	if err != nil {
		return nil, err
	}
	cwmRoot := Root{Path: l.Root, Exclude: CwmExcludes}
	if kind == KindAdopt || kind == KindFresh {
		p.roots = []Root{
			cwmRoot,
			{Path: hj},
			{Path: filepath.Join(claude, ".stignore")},
			{Path: filepath.Join(claude, ".credentials.json")},
		}
		if kind == KindFresh {
			p.roots = append(p.roots, Root{Path: claude, Exclude: ClaudeExcludes})
		}
	} else {
		p.roots = []Root{{Path: claude, Exclude: ClaudeExcludes}, {Path: hj}, cwmRoot}
		if !strings.HasPrefix(root, l.Root+string(filepath.Separator)) && root != claude {
			p.roots = append(p.roots, Root{Path: root, Exclude: ClaudeExcludes})
		}
	}
	for _, r := range p.roots {
		p.BackupRoots = append(p.BackupRoots, claudepaths.Tilde(r.Path))
		p.BackupBytes += treeSize(r)
	}
	for _, d := range IncompleteBackups(l) {
		p.Leftovers = append(p.Leftovers, claudepaths.Tilde(d))
	}
	nc.Mode = to
	return p, nil
}

func treeSize(r Root) int64 {
	var n int64
	_ = filepath.WalkDir(r.Path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(r.Path, p)
		if rel != "." && excluded(filepath.ToSlash(rel), r.Exclude) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// What a backup never copies and a rollback never touches (D101): files
// the running agent or a running claude holds open (on Windows an
// exclusive handle fails every read with a sharing violation), runtime
// state that must outlive a rollback (server.toml: the dashboard token
// and port), and what is rebuilt on demand (cache/: the stats cache).
var (
	CwmExcludes = []string{
		"backups", "logs", "cache", "state/migrations.json",
		"state/agent.lock", "state/*.pid", "state/*.sock", "server.toml", "*.lock",
		// claude's per-process runtime dirs inside an isolated root or a
		// login-only dir
		"workspace/session-env", "workspace/shell-snapshots",
		"accounts/*/session-env", "accounts/*/shell-snapshots",
		"launch/*/session-env", "launch/*/shell-snapshots",
	}
	ClaudeExcludes = []string{"*.lock", "session-env", "shell-snapshots"}
)

// v07LaunchDirs lists ~/.cwm/launch/<account> dirs.
func v07LaunchDirs(l *storage.Layout) []string {
	ents, err := os.ReadDir(l.LaunchDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, filepath.Join(l.LaunchDir(), e.Name()))
		}
	}
	return out
}

// SessionIDs lists the session ids under root/projects/*/<id>.jsonl
// (migration conflict copies excluded).
func SessionIDs(root string) []string {
	m := map[string]bool{}
	collectSessions(root, m)
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func collectSessions(root string, ids map[string]bool) {
	projects := filepath.Join(root, "projects")
	if info, err := os.Lstat(projects); err != nil || isLink(info) {
		return // a launch dir's link: the target root is counted already
	}
	dirs, err := os.ReadDir(projects)
	if err != nil {
		return
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(projects, d.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			n := f.Name()
			if f.Type().IsRegular() && strings.HasSuffix(n, ".jsonl") && !strings.Contains(n, ".conflict-") {
				ids[strings.TrimSuffix(n, ".jsonl")] = true
			}
		}
	}
}

// planItems plans every allow-list item of src into root. Switches that
// are off still migrate: they decide what crosses devices, not what this
// machine keeps.
func planItems(p *Plan, src, root, host string) {
	seen := map[string]bool{}
	for _, it := range config.SyncItems {
		if seen[it.Path] {
			continue
		}
		seen[it.Path] = true
		s := filepath.Join(src, it.Path)
		info, err := os.Lstat(s)
		if err != nil || isLink(info) {
			continue // absent, or a launch dir's link back into the root
		}
		d := filepath.Join(root, it.Path)
		if it.Dir {
			if !info.IsDir() {
				continue
			}
			_ = filepath.WalkDir(s, func(path string, de fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if de.IsDir() && it.Path == "plugins" && de.Name() == "cache" {
					return fs.SkipDir
				}
				if !de.Type().IsRegular() {
					return nil
				}
				rel, rerr := filepath.Rel(s, path)
				if rerr != nil {
					return nil
				}
				planFile(p, path, filepath.Join(d, rel), it.Path == "projects", host)
				return nil
			})
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		switch it.Path {
		case "history.jsonl", "repos.tsv":
			planLines(p, s, d)
		case "settings.json", "mcp.json":
			planJSON(p, s, d, host)
		default: // CLAUDE.md
			planFile(p, s, d, false, host)
		}
	}
}

func sameFile(a, b string) bool {
	x, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	y, err := os.ReadFile(b)
	return err == nil && bytes.Equal(x, y)
}

func planFile(p *Plan, src, dst string, session bool, host string) {
	info, err := os.Stat(dst)
	switch {
	case err != nil:
		p.Actions = append(p.Actions, Action{Kind: ActCopy, Src: src, Dst: dst})
		return
	case info.IsDir():
		p.Conflicts++
		p.Actions = append(p.Actions, Action{Kind: ActConflict, Src: src, Dst: conflictName(dst, session, host), Detail: "a directory is in the way"})
		return
	case info.Size() == 0:
		p.Actions = append(p.Actions, Action{Kind: ActExtend, Src: src, Dst: dst, Detail: "empty placeholder"})
		return
	}
	if sameFile(src, dst) {
		p.Skipped++
		return
	}
	if session && strings.HasSuffix(dst, ".jsonl") {
		s, _ := os.ReadFile(src)
		d, _ := os.ReadFile(dst)
		if bytes.HasPrefix(d, s) {
			p.Skipped++ // the target already continued this session
			return
		}
		if bytes.HasPrefix(s, d) {
			p.Actions = append(p.Actions, Action{Kind: ActExtend, Src: src, Dst: dst, Detail: "session continued in the source"})
			return
		}
	}
	p.Conflicts++
	p.Actions = append(p.Actions, Action{Kind: ActConflict, Src: src, Dst: conflictName(dst, session, host), Detail: "kept next to " + filepath.Base(dst)})
}

// conflictName is where a differing source version is kept: sessions as
// <id>.conflict-<host>.jsonl (spec), every other file as a conflict copy
// Health lists with a "pick one" action.
func conflictName(dst string, session bool, host string) string {
	ext := filepath.Ext(dst)
	base := strings.TrimSuffix(dst, ext)
	if session && ext == ".jsonl" {
		return base + ".conflict-" + host + ext
	}
	return base + ".sync-conflict-" + time.Now().UTC().Format("20060102-150405") + "-" + conflictDevice + ext
}

func planLines(p *Plan, src, dst string) {
	if info, err := os.Stat(dst); err != nil || info.Size() == 0 {
		p.Actions = append(p.Actions, Action{Kind: ActCopy, Src: src, Dst: dst})
		return
	}
	if sameFile(src, dst) {
		p.Skipped++
		return
	}
	p.Actions = append(p.Actions, Action{Kind: ActMergeLines, Src: src, Dst: dst})
}

func planJSON(p *Plan, src, dst, host string) {
	if info, err := os.Stat(dst); err != nil || info.Size() == 0 {
		p.Actions = append(p.Actions, Action{Kind: ActCopy, Src: src, Dst: dst})
		return
	}
	if sameFile(src, dst) {
		p.Skipped++
		return
	}
	if _, err := readJSONObject(src); err != nil {
		p.Conflicts++
		p.Actions = append(p.Actions, Action{Kind: ActConflict, Src: src, Dst: conflictName(dst, false, host), Detail: "source is not valid JSON"})
		return
	}
	if _, err := readJSONObject(dst); err != nil {
		p.Conflicts++
		p.Actions = append(p.Actions, Action{Kind: ActConflict, Src: src, Dst: conflictName(dst, false, host), Detail: "target is not valid JSON"})
		return
	}
	p.Actions = append(p.Actions, Action{Kind: ActMergeJSON, Src: src, Dst: dst, Detail: "deep merge, target wins, hooks union"})
}

// launchPaths addresses a v0.7 launch dir as claude saw it there.
func launchPaths(dir string) claudepaths.Paths {
	return claudepaths.Paths{
		ConfigDir: dir, Env: dir,
		ClaudeJSON:  filepath.Join(dir, claudepaths.ClaudeJSONName),
		Credentials: filepath.Join(dir, claudepaths.CredentialsName),
	}
}

// planLogins plans the credential moves (SPEC-V0.8 Accounts): refresh the
// vault from fresher v0.7 launch copies and from the old mode's live
// login, then make the target live location hold the default's login.
// A login already live at the target stays live: plain `claude` keeps the
// account it had.
func planLogins(l *storage.Layout, cfg *config.Config, p *Plan, launchDirs []string, o Options) error {
	st := o.Store
	vault := func(name string) (claudepaths.Paths, error) { return accounts.VaultPaths(l, p.ToMode, name) }
	for _, d := range launchDirs {
		name := filepath.Base(d)
		if _, ok := cfg.Account(name); !ok {
			continue
		}
		from := launchPaths(d)
		v, err := vault(name)
		if err != nil {
			return err
		}
		cur, err := st.Read(from)
		if err != nil {
			continue
		}
		if old, err := st.Read(v); err == nil && bytes.Equal(old, cur) && sameFile(from.ClaudeJSON, v.ClaudeJSON) {
			continue
		}
		p.Actions = append(p.Actions, Action{Kind: ActVault, Account: name, Src: st.Location(from), Dst: st.Location(v),
			Detail: "the launch dir's copy is the one claude refreshed", from: from, to: v, full: true})
	}
	def := cfg.DefaultAccount
	var fromLive *claudepaths.Paths
	if config.ValidMode(cfg.Mode) && cfg.Mode != p.ToMode {
		fl, err := claudepaths.Live(l, cfg.Mode, "", "")
		if err != nil {
			return err
		}
		if _, err := st.Read(fl); err == nil {
			fromLive = &fl
			if def != "" {
				v, err := vault(def)
				if err != nil {
					return err
				}
				p.Actions = append(p.Actions, Action{Kind: ActVault, Account: def, Src: st.Location(fl), Dst: st.Location(v),
					Detail: "save the " + cfg.Mode + " login back", from: fl, to: v})
			}
		}
	} else if cfg.NeedsMigration() && def != "" {
		// v0.7: the default's launch dir held the live .claude.json:
		// carry its project entries (trust, allowed tools) over.
		if lj := launchPaths(filepath.Join(l.LaunchDir(), def)).ClaudeJSON; exists(lj) {
			fl := launchPaths(filepath.Join(l.LaunchDir(), def))
			fromLive = &fl
		}
	}
	to, err := claudepaths.Live(l, p.ToMode, "", "")
	if err != nil {
		return err
	}
	if _, err := st.Read(to); err == nil {
		name := matchAccount(l, cfg, to.ClaudeJSON, p.ToMode)
		if name == "" {
			name = uniqueName(cfg, o.Account)
			v, err := vault(name)
			if err != nil {
				return err
			}
			p.Actions = append(p.Actions, Action{Kind: ActImport, Account: name, Src: st.Location(to), Dst: st.Location(v),
				Detail: "the live login becomes account " + name, from: to, to: v})
		} else if fromLive != nil && name == def && config.ValidMode(cfg.Mode) {
			// The same login live in both roots: the mode in use holds the
			// fresher tokens.
			v, err := vault(name)
			if err != nil {
				return err
			}
			p.Actions = append(p.Actions, Action{Kind: ActSwapIn, Account: name, Src: st.Location(v), Dst: st.Location(to), from: v, to: to})
		} else {
			v, err := vault(name)
			if err != nil {
				return err
			}
			p.Actions = append(p.Actions, Action{Kind: ActVault, Account: name, Src: st.Location(to), Dst: st.Location(v),
				Detail: "the live login is the freshest", from: to, to: v})
		}
		p.Default = name
	} else if def != "" {
		v, err := vault(def)
		if err != nil {
			return err
		}
		p.Actions = append(p.Actions, Action{Kind: ActSwapIn, Account: def, Src: st.Location(v), Dst: st.Location(to), from: v, to: to})
		p.Default = def
	}
	if fromLive != nil && exists(fromLive.ClaudeJSON) && fromLive.ClaudeJSON != to.ClaudeJSON {
		p.Actions = append(p.Actions, Action{Kind: ActProjects, Src: fromLive.ClaudeJSON, Dst: to.ClaudeJSON,
			Detail: "project entries absent from the target"})
	}
	return nil
}

// matchAccount finds the configured account whose recorded identity is
// the login in claudeJSON.
func matchAccount(l *storage.Layout, cfg *config.Config, claudeJSON, mode string) string {
	id, err := accounts.ReadIdentity(claudeJSON)
	if err != nil || (id.AccountUUID == "" && id.Email == "") {
		return ""
	}
	same := func(x accounts.Identity) bool {
		if id.AccountUUID != "" && x.AccountUUID != "" {
			return id.AccountUUID == x.AccountUUID
		}
		return id.Email != "" && strings.EqualFold(id.Email, x.Email)
	}
	for _, a := range cfg.Accounts {
		if a.Login != nil && same(accounts.Identity{Email: a.Login.Email, AccountUUID: a.Login.AccountUUID}) {
			return a.Name
		}
		v, err := accounts.VaultPaths(l, mode, a.Name)
		if err != nil {
			continue
		}
		if x, err := accounts.ReadIdentity(v.ClaudeJSON); err == nil && same(x) {
			return a.Name
		}
	}
	return ""
}

func uniqueName(cfg *config.Config, base string) string {
	if _, taken := cfg.Account(base); !taken {
		return base
	}
	for n := 2; ; n++ {
		c := fmt.Sprintf("%s-%d", base, n)
		if _, taken := cfg.Account(c); !taken {
			return c
		}
	}
}

// ErrNothingToDo means the config already records the target mode.
var ErrNothingToDo = errors.New("migrate: nothing to migrate")
