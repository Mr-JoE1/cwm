package migrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/workspace"
)

// Record statuses.
const (
	StatusApplied      = "applied"
	StatusFailed       = "failed"
	StatusVerifyFailed = "verify-failed"
	StatusRolledBack   = "rolled-back"
)

// Record is one entry of ~/.cwm/state/migrations.json.
type Record struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind"`
	At             time.Time  `json:"at"`
	FromMode       string     `json:"from_mode"`
	ToMode         string     `json:"to_mode"`
	Root           string     `json:"root"`
	Backup         string     `json:"backup"`
	SessionsBefore int        `json:"sessions_before"`
	SessionsAfter  int        `json:"sessions_after"`
	Changed        int        `json:"changed"`
	Conflicts      int        `json:"conflicts"`
	Default        string     `json:"default_account,omitempty"`
	Status         string     `json:"status"`
	Error          string     `json:"error,omitempty"`
	Warnings       []string   `json:"warnings,omitempty"`
	RolledBackAt   *time.Time `json:"rolled_back_at,omitempty"`
}

// RecordsPath is ~/.cwm/state/migrations.json.
func RecordsPath(l *storage.Layout) string { return filepath.Join(l.StateDir(), "migrations.json") }

// Records loads every recorded migration, oldest first.
func Records(l *storage.Layout) ([]Record, error) {
	data, err := os.ReadFile(RecordsPath(l))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("migrate: read %s: %w", RecordsPath(l), err)
	}
	var out []Record
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("migrate: parse %s: %w", RecordsPath(l), err)
	}
	return out, nil
}

func saveRecord(l *storage.Layout, r Record) error {
	all, err := Records(l)
	if err != nil {
		return err
	}
	replaced := false
	for i := range all {
		if all[i].ID == r.ID {
			all[i], replaced = r, true
		}
	}
	if !replaced {
		all = append(all, r)
	}
	return writeJSON(RecordsPath(l), all)
}

// Apply runs a plan: back up, apply, verify, record. cfg is updated to
// the migrated config on success. l is re-pointed at the target root.
func Apply(l *storage.Layout, cfg *config.Config, p *Plan, o Options) (*Record, error) {
	o.defaults()
	if p.Kind == KindNone {
		return nil, ErrNothingToDo
	}
	now := o.Now().UTC()
	id := now.Format("20060102T150405Z")
	dir := filepath.Join(l.BackupsDir(), "migrate-"+id)
	for n := 2; exists(dir); n++ {
		dir = filepath.Join(l.BackupsDir(), fmt.Sprintf("migrate-%s-%d", id, n))
		id = strings.TrimPrefix(filepath.Base(dir), "migrate-")
	}
	rec := Record{ID: id, Kind: p.Kind, At: now, FromMode: p.FromMode, ToMode: p.ToMode,
		Root: claudepaths.Tilde(p.Root), Backup: claudepaths.Tilde(dir),
		SessionsBefore: len(p.Sessions), Conflicts: p.Conflicts, Default: p.Default}
	say := func(format string, a ...any) { fmt.Fprintf(o.Progress, format+"\n", a...) }

	// 0. Leftovers of runs that died while backing up (D101).
	if gone, err := RemoveIncomplete(l); err != nil {
		say("backup: %v", err)
	} else {
		for _, d := range gone {
			say("backup: removed the incomplete backup %s (an earlier run never finished it)", claudepaths.Tilde(d))
		}
	}

	// 1. Back up.
	say("backup: %s", claudepaths.Tilde(dir))
	m, err := backup(dir, id, p.roots, now, backupOpts{
		needed: p.needs(),
		warn: func(path, holder string) {
			w := "not backed up (locked by " + holder + "): " + claudepaths.Tilde(path)
			rec.Warnings = append(rec.Warnings, w)
			say("backup: warning: %s", w)
		},
	})
	if err != nil {
		// Nothing was applied: the partial copy is worthless, and must
		// never look like a rollback target.
		if rerr := os.RemoveAll(dir); rerr != nil {
			return nil, fmt.Errorf("%w (the partial backup %s is marked incomplete; remove it: cwm migrate clean)", err, claudepaths.Tilde(dir))
		}
		return nil, fmt.Errorf("%w (nothing changed; the partial backup was removed)", err)
	}
	skipped := 0
	for _, e := range m.Entries {
		if e.Skipped != "" {
			skipped++
		}
	}
	if skipped > 0 {
		say("backup: %d entries, %d locked file(s) skipped, manifest with sha256 written", len(m.Entries), skipped)
	} else {
		say("backup: %d entries, manifest with sha256 written", len(m.Entries))
	}
	fail := func(status string, err error) (*Record, error) {
		rec.Status, rec.Error = status, err.Error()
		_ = saveRecord(l, rec)
		return &rec, fmt.Errorf("%w (undo it: cwm migrate rollback %s)", err, id)
	}

	// 3. Apply.
	nc := p.newConfig
	if err := os.MkdirAll(p.Root, 0o755); err != nil {
		return fail(StatusFailed, fmt.Errorf("migrate: create %s: %w", p.Root, err))
	}
	for _, a := range p.Actions {
		if err := applyAction(l, nc, a, dir, o); err != nil {
			return fail(StatusFailed, err)
		}
		if a.Kind != ActStIgnore {
			rec.Changed++
		}
		say("%s: %s", a.Kind, describe(a))
	}
	if p.Default != "" {
		if err := nc.SetDefaultAccount(p.Default); err != nil {
			return fail(StatusFailed, fmt.Errorf("migrate: %w", err))
		}
	}

	// 4. Verify.
	have := map[string]bool{}
	for _, s := range SessionIDs(p.Root) {
		have[s] = true
	}
	rec.SessionsAfter = len(have)
	var missing []string
	for _, s := range p.Sessions {
		if !have[s] {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fail(StatusVerifyFailed, fmt.Errorf("migrate: verify: %d session(s) missing from %s (first: %s)", len(missing), claudepaths.Tilde(p.Root), missing[0]))
	}
	say("verify: %d of %d sessions present in %s", len(p.Sessions), len(p.Sessions), claudepaths.Tilde(p.Root))
	if o.SyncFolder != nil {
		if err := o.SyncFolder(p.Root); err != nil {
			rec.Warnings = append(rec.Warnings, "Syncthing folder path not updated: "+err.Error())
			say("verify: Syncthing folder path not updated (%v) — run `cwm pair` once Syncthing is up", err)
		} else {
			say("verify: Syncthing folder cwm-workspace -> %s", claudepaths.Tilde(p.Root))
		}
	}

	// 5. Record.
	nc.Mode = p.ToMode
	if err := config.Save(l.ConfigPath(), nc); err != nil {
		return fail(StatusFailed, fmt.Errorf("migrate: %w", err))
	}
	*cfg = *nc
	if err := claudepaths.Apply(l, cfg); err != nil {
		return fail(StatusFailed, err)
	}
	rec.Status = StatusApplied
	if err := saveRecord(l, rec); err != nil {
		return &rec, err
	}
	say("record: mode = %s, %s", p.ToMode, claudepaths.Tilde(RecordsPath(l)))
	return &rec, nil
}

// needs reports the paths the plan reads or writes, for the backup: one
// of them locked is a hard error, anything else locked is skipped.
func (p *Plan) needs() func(string) bool {
	files := map[string]bool{}
	var dirs []string
	add := func(x string) {
		if x != "" {
			files[filepath.Clean(x)] = true
		}
	}
	for _, a := range p.Actions {
		if a.Kind == ActMoveBackup {
			dirs = append(dirs, filepath.Clean(a.Src)+string(filepath.Separator))
			continue
		}
		if a.Kind != ActVault && a.Kind != ActImport && a.Kind != ActSwapIn {
			add(a.Src)
			add(a.Dst)
		}
		for _, x := range []string{a.from.Credentials, a.from.ClaudeJSON, a.to.Credentials, a.to.ClaudeJSON} {
			add(x)
		}
	}
	return func(x string) bool {
		x = filepath.Clean(x)
		if files[x] {
			return true
		}
		for _, d := range dirs {
			if strings.HasPrefix(x, d) {
				return true
			}
		}
		return false
	}
}

func describe(a Action) string {
	switch {
	case a.Account != "":
		return a.Account + " (" + a.Src + " -> " + a.Dst + ")"
	case a.Src != "" && a.Dst != "":
		return claudepaths.Tilde(a.Src) + " -> " + claudepaths.Tilde(a.Dst)
	case a.Src != "":
		return claudepaths.Tilde(a.Src)
	}
	return claudepaths.Tilde(a.Dst)
}

func applyAction(l *storage.Layout, nc *config.Config, a Action, backup string, o Options) error {
	switch a.Kind {
	case ActCopy, ActExtend, ActConflict:
		return copyKeepTime(a.Src, a.Dst)
	case ActMergeLines:
		if filepath.Base(a.Dst) == workspace.HistoryFile {
			return workspace.MergeHistory(a.Dst, a.Src)
		}
		return workspace.MergeLines(a.Dst, a.Src)
	case ActMergeJSON:
		return mergeJSONFiles(a.Src, a.Dst)
	case ActVault, ActImport, ActSwapIn:
		return moveLogin(l, nc, a, o)
	case ActProjects:
		return mergeProjects(a.Src, a.Dst)
	case ActMoveBackup:
		dst := filepath.Join(backup, "moved", filepath.Base(a.Src))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return fmt.Errorf("migrate: create %s: %w", filepath.Dir(dst), err)
		}
		if err := os.Rename(a.Src, dst); err != nil {
			return fmt.Errorf("migrate: move %s into the backup: %w", a.Src, err)
		}
		return nil
	case ActStIgnore:
		probe := storage.NewLayoutAt(l.Root)
		probe.SetWorkspaceDir(filepath.Dir(a.Dst))
		if _, err := workspace.WriteStIgnore(probe, nc); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("migrate: unknown action %q", a.Kind)
}

func copyKeepTime(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("migrate: create %s: %w", filepath.Dir(dst), err)
	}
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("migrate: stat %s: %w", src, err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		if isLocked(err) {
			return &LockedError{Path: src, Holder: LikelyHolder(src), Err: err}
		}
		return fmt.Errorf("migrate: read %s: %w", src, err)
	}
	if err := storage.WriteFileAtomic(dst, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("migrate: write %s: %w", dst, err)
	}
	// Session lists sort by mtime: a copied session keeps its place.
	_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
	return nil
}

func moveLogin(l *storage.Layout, nc *config.Config, a Action, o Options) error {
	st := o.Store
	creds, err := st.Read(a.from)
	if err != nil {
		return fmt.Errorf("migrate: read %s's login from %s: %w", a.Account, st.Location(a.from), err)
	}
	if err := os.MkdirAll(filepath.Dir(a.to.Credentials), 0o700); err != nil {
		return fmt.Errorf("migrate: create %s: %w", filepath.Dir(a.to.Credentials), err)
	}
	if err := st.Write(a.to, creds); err != nil {
		return fmt.Errorf("migrate: write %s's login to %s: %w", a.Account, st.Location(a.to), err)
	}
	if exists(a.from.ClaudeJSON) {
		if a.full {
			if err := copyKeepTime(a.from.ClaudeJSON, a.to.ClaudeJSON); err != nil {
				return err
			}
		} else if err := accounts.MergeIdentity(a.from.ClaudeJSON, a.to.ClaudeJSON); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if a.Kind == ActImport {
		if _, ok := nc.Account(a.Account); !ok {
			if err := nc.AddAccount(a.Account, claudepaths.LoginDir(l, a.Account)); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}
		}
		if id, err := accounts.ReadIdentity(a.to.ClaudeJSON); err == nil {
			_ = nc.SetAccountLogin(a.Account, config.Login{Email: id.Email, AccountUUID: id.AccountUUID,
				OrganizationName: id.OrganizationName, Plan: id.Plan})
		}
	}
	return nil
}

// readJSONObject decodes a JSON object keeping numbers exact.
func readJSONObject(p string) (map[string]any, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("migrate: %s is not a JSON object: %w", p, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// MergeJSON deep-merges src into dst: keys missing from dst are added,
// objects merge recursively, arrays become the union (dst order first,
// so hook matchers and permission lists from both survive), and on a
// scalar conflict dst — the target — wins.
func MergeJSON(dst, src map[string]any) {
	for k, sv := range src {
		dv, ok := dst[k]
		if !ok {
			dst[k] = sv
			continue
		}
		switch d := dv.(type) {
		case map[string]any:
			if s, ok := sv.(map[string]any); ok {
				MergeJSON(d, s)
			}
		case []any:
			if s, ok := sv.([]any); ok {
				dst[k] = unionArrays(d, s)
			}
		}
	}
}

func unionArrays(d, s []any) []any {
	key := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	seen := map[string]bool{}
	out := make([]any, 0, len(d)+len(s))
	for _, v := range append(append([]any{}, d...), s...) {
		if k := key(v); !seen[k] {
			seen[k] = true
			out = append(out, v)
		}
	}
	return out
}

func mergeJSONFiles(src, dst string) error {
	s, err := readJSONObject(src)
	if err != nil {
		return err
	}
	d, err := readJSONObject(dst)
	if err != nil {
		return err
	}
	MergeJSON(d, s)
	return writeJSONFile(dst, d)
}

func writeJSONFile(p string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("migrate: encode %s: %w", p, err)
	}
	perm := os.FileMode(0o644)
	if info, err := os.Stat(p); err == nil {
		perm = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("migrate: create %s: %w", filepath.Dir(p), err)
	}
	if err := storage.WriteFileAtomic(p, append(data, '\n'), perm); err != nil {
		return fmt.Errorf("migrate: write %s: %w", p, err)
	}
	return nil
}

// mergeProjects adds the per-project entries of src's .claude.json
// (trust, allowed tools) that dst lacks; no other key of dst changes.
func mergeProjects(src, dst string) error {
	read := func(p string) (map[string]json.RawMessage, error) {
		top := map[string]json.RawMessage{}
		data, err := os.ReadFile(p)
		if os.IsNotExist(err) || (err == nil && len(bytes.TrimSpace(data)) == 0) {
			return top, nil
		}
		if err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", p, err)
		}
		if err := json.Unmarshal(data, &top); err != nil {
			return nil, fmt.Errorf("migrate: %s is not a JSON object: %w", p, err)
		}
		if top == nil {
			top = map[string]json.RawMessage{}
		}
		return top, nil
	}
	s, err := read(src)
	if err != nil {
		return err
	}
	d, err := read(dst)
	if err != nil {
		return err
	}
	var sp, dp map[string]json.RawMessage
	if raw, ok := s["projects"]; !ok || json.Unmarshal(raw, &sp) != nil || len(sp) == 0 {
		return nil
	}
	if raw, ok := d["projects"]; ok {
		if err := json.Unmarshal(raw, &dp); err != nil {
			return nil // not ours to repair
		}
	}
	if dp == nil {
		dp = map[string]json.RawMessage{}
	}
	added := 0
	for k, v := range sp {
		if _, ok := dp[k]; !ok {
			dp[k] = v
			added++
		}
	}
	if added == 0 {
		return nil
	}
	raw, err := json.Marshal(dp)
	if err != nil {
		return fmt.Errorf("migrate: encode projects: %w", err)
	}
	d["projects"] = raw
	return writeJSONFile(dst, d)
}

// Rollback restores the backup of migration id ("" = the newest applied
// one) byte for byte. Only the newest migration still in effect can be
// rolled back, so restores never interleave.
func Rollback(l *storage.Layout, id string, o Options) (*Record, error) {
	o.defaults()
	all, err := Records(l)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Status == StatusRolledBack {
			continue
		}
		if id == "" || all[i].ID == id {
			idx = i
			break
		}
		if all[i].Status == StatusApplied {
			return nil, fmt.Errorf("migrate: roll back %s first (newer migrations go first)", all[i].ID)
		}
	}
	if idx < 0 {
		if id == "" {
			return nil, fmt.Errorf("migrate: no migration to roll back")
		}
		return nil, fmt.Errorf("migrate: migration %q not found or already rolled back", id)
	}
	rec := all[idx]
	dir := storage.ExpandTilde(rec.Backup)
	m, err := ReadManifest(dir)
	if err != nil {
		return nil, err
	}
	now := o.Now().UTC()
	trash := filepath.Join(l.BackupsDir(), "rollback-"+rec.ID+"-"+now.Format("20060102T150405Z"))
	fmt.Fprintf(o.Progress, "rollback: restoring %s from %s\n", rec.ID, claudepaths.Tilde(dir))
	moved, restored, err := Restore(dir, m, trash)
	if err != nil {
		return &rec, err
	}
	fmt.Fprintf(o.Progress, "rollback: %d path(s) restored, %d moved aside to %s\n", restored, moved, claudepaths.Tilde(trash))
	rec.Status = StatusRolledBack
	rec.RolledBackAt = &now
	if err := saveRecord(l, rec); err != nil {
		return &rec, err
	}
	return &rec, nil
}

func junction(target, link string) error { return workspace.OSLinker{}.Junction(target, link) }

// FirstRun is the installer's and the dashboard's first-run path: with no
// config.toml yet it plans and applies the adopt-in-place (an existing
// ~/.claude) or fresh migration and records it, so even the first run can
// be rolled back. With a config present it does nothing (nil record): an
// existing setup is never migrated without a review.
func FirstRun(l *storage.Layout, o Options) (*Record, error) {
	if _, err := os.Stat(l.ConfigPath()); err == nil {
		return nil, nil
	}
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		return nil, err
	}
	p, err := BuildPlan(l, cfg, o)
	if err != nil {
		return nil, err
	}
	if p.Kind == KindNone {
		return nil, nil
	}
	return Apply(l, cfg, p, o)
}
