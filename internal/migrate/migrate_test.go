package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/testsafe"
)

func TestMain(m *testing.M) { os.Exit(testsafe.Main(m)) }

// Obviously fake login material: never a real token anywhere.
const (
	fakeWorkCreds    = `{"claudeAiOauth":{"accessToken":"FAKE-WORK-TOKEN-OLD"}}`
	fakeWorkCredsNew = `{"claudeAiOauth":{"accessToken":"FAKE-WORK-TOKEN-REFRESHED"}}`
	fakeLiveCreds    = `{"claudeAiOauth":{"accessToken":"FAKE-LIVE-TOKEN"}}`
)

type world struct {
	home, claude, cwm string
	l                 *storage.Layout
}

func newWorld(t *testing.T) *world {
	t.Helper()
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		t.Setenv(k, home)
	}
	t.Setenv("CWM_HOME", filepath.Join(home, ".cwm"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	w := &world{home: home, claude: filepath.Join(home, ".claude"), cwm: filepath.Join(home, ".cwm")}
	w.l = storage.NewLayoutAt(w.cwm)
	return w
}

func write(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func identity(uuid, email string) string {
	return `{"oauthAccount":{"accountUuid":"` + uuid + `","emailAddress":"` + email + `"},"numStartups":3}`
}

// v07 builds a v0.7.x layout: ~/.cwm/workspace, accounts/work and an
// assembled launch/work whose credentials claude refreshed.
func (w *world) v07(t *testing.T) {
	ws := filepath.Join(w.cwm, "workspace")
	write(t, filepath.Join(ws, "projects", "api", "s-old.jsonl"), `{"uuid":"a1","cwd":"/r/api"}`+"\n")
	write(t, filepath.Join(ws, "projects", "api", "s-shared.jsonl"), `{"uuid":"b1"}`+"\n"+`{"uuid":"b2"}`+"\n")
	write(t, filepath.Join(ws, "projects", "api", "memory", "notes.md"), "v07 notes\n")
	write(t, filepath.Join(ws, "history.jsonl"), `{"display":"one","timestamp":1}`+"\n"+`{"display":"three","timestamp":3}`+"\n")
	write(t, filepath.Join(ws, "CLAUDE.md"), "# v07 memory\n")
	write(t, filepath.Join(ws, "settings.json"), `{"theme":"dark","permissions":{"allow":["Bash(ls)"]},"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"cwm hook session-start"}]}]}}`)
	write(t, filepath.Join(ws, "mcp.json"), `{"mcpServers":{"docs":{"command":"docs-mcp"}}}`)
	write(t, filepath.Join(ws, "repos.tsv"), "api\tgit@example.com:me/api.git\n")
	write(t, filepath.Join(ws, "todos", "x.json"), "[]")
	write(t, filepath.Join(w.cwm, "accounts", "work", ".credentials.json"), fakeWorkCreds)
	write(t, filepath.Join(w.cwm, "accounts", "work", ".claude.json"), identity("uuid-work", "work@example.com"))
	launch := filepath.Join(w.cwm, "launch", "work")
	if err := os.MkdirAll(launch, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, it := range []string{"projects", "history.jsonl", "CLAUDE.md"} {
		if err := os.Symlink(filepath.Join(ws, it), filepath.Join(launch, it)); err != nil {
			t.Skip("symlinks unavailable:", err)
		}
	}
	write(t, filepath.Join(launch, ".credentials.json"), fakeWorkCredsNew)
	write(t, filepath.Join(launch, ".claude.json"), `{"oauthAccount":{"accountUuid":"uuid-work","emailAddress":"work@example.com"},"projects":{"/r/api":{"hasTrustDialogAccepted":true}}}`)
	write(t, filepath.Join(launch, "statsig", "cache"), "x")
	write(t, filepath.Join(w.cwm, "config.toml"), "version = 1\ndefault_account = \"work\"\n\n[[account]]\nname = \"work\"\ndir = \""+filepath.ToSlash(filepath.Join(w.cwm, "accounts", "work"))+"\"\n")
}

// claudeDir builds an existing ~/.claude with a live login.
func (w *world) claudeDir(t *testing.T) {
	write(t, filepath.Join(w.claude, "projects", "-r-api", "s-live.jsonl"), `{"uuid":"c1"}`+"\n")
	write(t, filepath.Join(w.claude, "projects", "api", "s-shared.jsonl"), `{"uuid":"b1"}`+"\n")
	write(t, filepath.Join(w.claude, "history.jsonl"), `{"display":"two","timestamp":2}`+"\n"+`{"display":"one","timestamp":1}`+"\n")
	write(t, filepath.Join(w.claude, "CLAUDE.md"), "# live memory\n")
	write(t, filepath.Join(w.claude, "settings.json"), `{"theme":"light","permissions":{"allow":["Read"]}}`)
	write(t, filepath.Join(w.claude, ".credentials.json"), fakeLiveCreds)
	write(t, filepath.Join(w.claude, "todos", "y.json"), "[]")
	write(t, filepath.Join(w.home, ".claude.json"), identity("uuid-live", "me@example.com"))
}

// snapshot hashes every entry under the three migrated roots (links by
// target), minus what a rollback deliberately keeps: the backups, the
// logs and the migration record.
func (w *world) snapshot(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range []string{w.claude, filepath.Join(w.home, ".claude.json"), w.cwm} {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(w.home, p)
			rel = filepath.ToSlash(rel)
			if rel == ".cwm/backups" || rel == ".cwm/logs" {
				return fs.SkipDir
			}
			if rel == ".cwm/state/migrations.json" || rel == ".cwm/state" || rel == ".cwm" {
				return nil
			}
			info, _ := d.Info()
			switch {
			case info.Mode()&fs.ModeSymlink != 0:
				tgt, _ := os.Readlink(p)
				out[rel] = "link:" + tgt
			case d.IsDir():
				out[rel] = "dir"
			default:
				b, _ := os.ReadFile(p)
				s := sha256.Sum256(b)
				out[rel] = hex.EncodeToString(s[:]) + " " + info.Mode().Perm().String()
			}
			return nil
		})
	}
	return out
}

func sameSnapshot(t *testing.T, want, got map[string]string) {
	t.Helper()
	var diffs []string
	for k, v := range want {
		if got[k] != v {
			diffs = append(diffs, "changed/missing "+k)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			diffs = append(diffs, "extra "+k)
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		t.Fatalf("rollback is not byte-identical:\n%s", strings.Join(diffs, "\n"))
	}
}

func clock() func() time.Time {
	t0 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	n := 0
	return func() time.Time { n++; return t0.Add(time.Duration(n) * time.Second) }
}

func run(t *testing.T, w *world, to string) (*config.Config, *Plan, *Record) {
	t.Helper()
	cfg, err := config.Load(w.l.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	o := Options{To: to, Store: accounts.FileStore{}, Now: clock(), Host: "hosta"}
	p, err := BuildPlan(w.l, cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := Apply(w.l, cfg, p, o)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return cfg, p, rec
}

func TestMigrateV07ToCaptureAndRollback(t *testing.T) {
	w := newWorld(t)
	w.v07(t)
	before := w.snapshot(t)
	cfg, p, rec := run(t, w, "")
	if p.Kind != KindV07 || rec.ToMode != config.ModeCapture || cfg.Mode != config.ModeCapture {
		t.Fatalf("kind %s mode %s", p.Kind, cfg.Mode)
	}
	for _, f := range []string{"projects/api/s-old.jsonl", "projects/api/s-shared.jsonl", "projects/api/memory/notes.md", "history.jsonl", "CLAUDE.md", "settings.json", "mcp.json", "repos.tsv", ".stignore"} {
		if _, err := os.Stat(filepath.Join(w.claude, f)); err != nil {
			t.Errorf("%s not migrated: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(w.claude, "todos")); err == nil {
		t.Error("todos/ (not on the allow-list) was migrated")
	}
	if got := read(t, filepath.Join(w.claude, ".credentials.json")); got != fakeWorkCredsNew {
		t.Error("live credentials are not the refreshed launch-dir copy")
	}
	if got := read(t, filepath.Join(w.cwm, "accounts", "work", ".credentials.json")); got != fakeWorkCredsNew {
		t.Error("vault not refreshed from the launch dir")
	}
	id, err := accounts.ReadIdentity(filepath.Join(w.home, ".claude.json"))
	if err != nil || id.AccountUUID != "uuid-work" {
		t.Errorf("live identity = %+v %v", id, err)
	}
	if !strings.Contains(read(t, filepath.Join(w.home, ".claude.json")), "hasTrustDialogAccepted") {
		t.Error("project entries of the v0.7 .claude.json not carried over")
	}
	if _, err := os.Stat(w.l.LaunchDir()); err == nil {
		t.Error("launch dir not moved into the backup")
	}
	if _, err := os.Stat(filepath.Join(storage.ExpandTilde(rec.Backup), "moved", "launch", "work", "statsig", "cache")); err != nil {
		t.Errorf("launch dir content not in the backup: %v", err)
	}
	if rec.SessionsBefore != 2 || rec.SessionsAfter != 2 {
		t.Errorf("sessions %d -> %d", rec.SessionsBefore, rec.SessionsAfter)
	}
	m, err := ReadManifest(storage.ExpandTilde(rec.Backup))
	if err != nil {
		t.Fatal(err)
	}
	hashed := 0
	for _, e := range m.Entries {
		if e.Type == TypeFile && len(e.SHA256) == 64 {
			hashed++
		}
	}
	if hashed < 10 {
		t.Errorf("manifest hashes %d files", hashed)
	}
	// Rollback restores the exact v0.7 state.
	if _, err := Rollback(w.l, "", Options{Now: clock()}); err != nil {
		t.Fatal(err)
	}
	sameSnapshot(t, before, w.snapshot(t))
	recs, _ := Records(w.l)
	if len(recs) != 1 || recs[0].Status != StatusRolledBack {
		t.Errorf("records = %+v", recs)
	}
	if _, err := Rollback(w.l, "", Options{}); err == nil {
		t.Error("a second rollback found something to undo")
	}
}

func TestMigrateBothSourcesOverlap(t *testing.T) {
	w := newWorld(t)
	w.v07(t)
	w.claudeDir(t)
	before := w.snapshot(t)
	cfg, p, rec := run(t, w, config.ModeCapture)
	if p.Kind != KindV07 {
		t.Fatalf("kind %s", p.Kind)
	}
	// Every session of both sources is present; s-shared is a prefix
	// conflict resolved by extension (the v0.7 copy continued it).
	if rec.SessionsBefore != 3 || rec.SessionsAfter != 3 {
		t.Errorf("sessions %d -> %d", rec.SessionsBefore, rec.SessionsAfter)
	}
	if got := read(t, filepath.Join(w.claude, "projects", "api", "s-shared.jsonl")); !strings.Contains(got, "b2") {
		t.Errorf("shared session not extended: %q", got)
	}
	// History merged by line, ordered by timestamp, deduped.
	h := read(t, filepath.Join(w.claude, "history.jsonl"))
	if strings.Count(h, `"one"`) != 1 || strings.Index(h, `"one"`) > strings.Index(h, `"two"`) || strings.Index(h, `"two"`) > strings.Index(h, `"three"`) {
		t.Errorf("history = %q", h)
	}
	// CLAUDE.md: target kept, the source version waits as a conflict copy.
	if read(t, filepath.Join(w.claude, "CLAUDE.md")) != "# live memory\n" {
		t.Error("CLAUDE.md target overwritten")
	}
	cps, _ := filepath.Glob(filepath.Join(w.claude, "CLAUDE.sync-conflict-*-MIGRATE.md"))
	if len(cps) != 1 || read(t, cps[0]) != "# v07 memory\n" {
		t.Errorf("CLAUDE.md conflict copy = %v", cps)
	}
	// settings.json: deep merge, target wins scalars, arrays/hooks union.
	var s map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(w.claude, "settings.json"))), &s); err != nil {
		t.Fatal(err)
	}
	if s["theme"] != "light" || s["hooks"] == nil {
		t.Errorf("settings = %v", s)
	}
	if allow := s["permissions"].(map[string]any)["allow"].([]any); len(allow) != 2 {
		t.Errorf("permissions.allow = %v", allow)
	}
	// The login already live stays live and becomes an account.
	if read(t, filepath.Join(w.claude, ".credentials.json")) != fakeLiveCreds {
		t.Error("live login replaced")
	}
	if cfg.DefaultAccount != "default" {
		t.Errorf("default = %q", cfg.DefaultAccount)
	}
	if read(t, filepath.Join(w.cwm, "accounts", "default", ".credentials.json")) != fakeLiveCreds {
		t.Error("live login not imported into the vault")
	}
	if read(t, filepath.Join(w.cwm, "accounts", "work", ".credentials.json")) != fakeWorkCredsNew {
		t.Error("work vault not refreshed")
	}
	if !strings.Contains(read(t, filepath.Join(w.home, ".claude.json")), `"numStartups": 3`) && !strings.Contains(read(t, filepath.Join(w.home, ".claude.json")), `"numStartups":3`) {
		t.Error("non-identity key of ~/.claude.json lost")
	}
	if _, err := Rollback(w.l, rec.ID, Options{Now: clock()}); err != nil {
		t.Fatal(err)
	}
	sameSnapshot(t, before, w.snapshot(t))
}

func TestMigrateSessionConflictCopy(t *testing.T) {
	w := newWorld(t)
	w.v07(t)
	w.claudeDir(t)
	write(t, filepath.Join(w.claude, "projects", "api", "s-shared.jsonl"), `{"uuid":"zz"}`+"\n")
	_, p, _ := run(t, w, config.ModeCapture)
	if p.Conflicts < 1 {
		t.Errorf("conflicts = %d", p.Conflicts)
	}
	if got := read(t, filepath.Join(w.claude, "projects", "api", "s-shared.conflict-hosta.jsonl")); !strings.Contains(got, "b2") {
		t.Errorf("conflict copy = %q", got)
	}
	if got := read(t, filepath.Join(w.claude, "projects", "api", "s-shared.jsonl")); !strings.Contains(got, "zz") {
		t.Error("target session overwritten")
	}
}

func TestAdoptInPlace(t *testing.T) {
	w := newWorld(t)
	w.claudeDir(t)
	before := w.snapshot(t)
	claudeBefore := map[string]string{}
	for k, v := range before {
		if strings.HasPrefix(k, ".claude/") {
			claudeBefore[k] = v
		}
	}
	cfg, p, rec := run(t, w, "")
	if p.Kind != KindAdopt {
		t.Fatalf("kind %s", p.Kind)
	}
	after := w.snapshot(t)
	for k, v := range claudeBefore {
		if after[k] != v {
			t.Errorf("adopt changed %s", k)
		}
	}
	if _, err := os.Stat(filepath.Join(w.claude, ".stignore")); err != nil {
		t.Error("no .stignore written")
	}
	if cfg.Mode != config.ModeCapture || cfg.DefaultAccount != "default" || rec.Default != "default" {
		t.Errorf("mode %q default %q", cfg.Mode, cfg.DefaultAccount)
	}
	if a, ok := cfg.Account("default"); !ok || a.Login == nil || a.Login.Email != "me@example.com" {
		t.Errorf("account default = %+v", a)
	}
	for _, r := range p.BackupRoots {
		if r == "~/.claude" || r == filepath.Join("~", ".claude") {
			t.Error("adopt backs up the whole ~/.claude")
		}
	}
	if _, err := Rollback(w.l, "", Options{Now: clock()}); err != nil {
		t.Fatal(err)
	}
	sameSnapshot(t, before, w.snapshot(t))
}

func TestFreshMachine(t *testing.T) {
	w := newWorld(t)
	before := w.snapshot(t)
	cfg, p, rec := run(t, w, "")
	if p.Kind != KindFresh || cfg.Mode != config.ModeCapture || rec.SessionsBefore != 0 {
		t.Fatalf("kind %s mode %s", p.Kind, cfg.Mode)
	}
	if _, err := os.Stat(filepath.Join(w.claude, ".stignore")); err != nil {
		t.Error("fresh root has no .stignore")
	}
	if _, err := Rollback(w.l, "", Options{Now: clock()}); err != nil {
		t.Fatal(err)
	}
	sameSnapshot(t, before, w.snapshot(t))
	// Nothing to migrate once the mode is recorded.
	w2 := newWorld(t)
	run(t, w2, "")
	cfg2, _ := config.Load(w2.l.ConfigPath())
	p2, err := BuildPlan(w2.l, cfg2, Options{Store: accounts.FileStore{}})
	if err != nil || p2.Kind != KindNone {
		t.Errorf("second plan = %v %v", p2, err)
	}
	if _, err := Apply(w2.l, cfg2, p2, Options{}); err != ErrNothingToDo {
		t.Errorf("apply none = %v", err)
	}
}

func TestModeRoundTripKeepsSessions(t *testing.T) {
	w := newWorld(t)
	w.claudeDir(t)
	run(t, w, config.ModeCapture)
	want := SessionIDs(w.claude)
	cfg, p, rec := run(t, w, config.ModeIsolated)
	if p.Kind != KindSwitch || cfg.Mode != config.ModeIsolated {
		t.Fatalf("kind %s", p.Kind)
	}
	iso := w.l.IsolatedWorkspaceDir()
	if got := SessionIDs(iso); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("isolated sessions %v, want %v", got, want)
	}
	if rec.SessionsAfter != len(want) {
		t.Errorf("after = %d", rec.SessionsAfter)
	}
	// Isolated: the default's login sits in the isolated root, ~/.claude
	// is left as it was.
	live, _ := claudepaths.Live(w.l, config.ModeIsolated, "", "")
	if read(t, live.Credentials) != fakeLiveCreds {
		t.Error("isolated live login missing")
	}
	if read(t, filepath.Join(w.claude, ".credentials.json")) != fakeLiveCreds {
		t.Error("~/.claude login touched")
	}
	// A session made in isolated mode survives the way back.
	write(t, filepath.Join(iso, "projects", "x", "s-iso.jsonl"), `{"uuid":"i1"}`+"\n")
	cfg, _, _ = run(t, w, config.ModeCapture)
	if cfg.Mode != config.ModeCapture {
		t.Fatal(cfg.Mode)
	}
	got := SessionIDs(w.claude)
	if len(got) != len(want)+1 {
		t.Errorf("capture sessions after round trip = %v", got)
	}
	// Three applied migrations: rollback is newest first.
	recs, _ := Records(w.l)
	if len(recs) != 3 {
		t.Fatalf("records = %d", len(recs))
	}
	if _, err := Rollback(w.l, recs[0].ID, Options{}); err == nil {
		t.Error("rolled back an older migration first")
	}
}

func TestMergeJSON(t *testing.T) {
	d := map[string]any{"a": "t", "o": map[string]any{"x": 1.0}, "l": []any{"p"}}
	s := map[string]any{"a": "s", "b": true, "o": map[string]any{"x": 2.0, "y": 3.0}, "l": []any{"p", "q"}}
	MergeJSON(d, s)
	if d["a"] != "t" || d["b"] != true || d["o"].(map[string]any)["x"] != 1.0 || d["o"].(map[string]any)["y"] != 3.0 || len(d["l"].([]any)) != 2 {
		t.Errorf("merge = %v", d)
	}
}

func TestBackupRestoreLinksNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need developer mode")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "r")
	write(t, filepath.Join(dir, "big", "f"), strings.Repeat("x", 100))
	write(t, filepath.Join(root, "a.txt"), "a")
	if err := os.Symlink(filepath.Join(dir, "big"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	m, err := Backup(filepath.Join(dir, "bk"), "id", []Root{{Path: root}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Entries {
		if strings.HasPrefix(e.Rel, "link/") {
			t.Fatal("backup followed a link")
		}
	}
	// Mutate: change a file, drop the link, add a new file.
	write(t, filepath.Join(root, "a.txt"), "changed")
	_ = os.Remove(filepath.Join(root, "link"))
	write(t, filepath.Join(root, "new", "n.txt"), "n")
	moved, restored, err := Restore(filepath.Join(dir, "bk"), m, filepath.Join(dir, "trash"))
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 || restored != 2 {
		t.Errorf("moved %d restored %d", moved, restored)
	}
	if read(t, filepath.Join(root, "a.txt")) != "a" {
		t.Error("file not restored")
	}
	if tgt, err := os.Readlink(filepath.Join(root, "link")); err != nil || tgt != filepath.Join(dir, "big") {
		t.Error("link not restored")
	}
	if read(t, filepath.Join(dir, "trash", "data", "0", "new", "n.txt")) != "n" {
		t.Error("new file not moved aside")
	}
}
