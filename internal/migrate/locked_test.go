package migrate

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// plainV07 is a v0.7 layout without links (so it runs on Windows too):
// one account "work", sessions in ~/.cwm/workspace, plus the runtime
// files a running agent and claude keep next to them.
func (w *world) plainV07(t *testing.T) {
	ws := filepath.Join(w.cwm, "workspace")
	write(t, filepath.Join(ws, "projects", "api", "s-1.jsonl"), `{"uuid":"a1"}`+"\n")
	write(t, filepath.Join(ws, "projects", "api", "s-2.jsonl"), `{"uuid":"a2"}`+"\n")
	write(t, filepath.Join(ws, "history.jsonl"), `{"display":"one","timestamp":1}`+"\n")
	write(t, filepath.Join(ws, "CLAUDE.md"), "# v07 memory\n")
	write(t, filepath.Join(w.cwm, "accounts", "work", ".credentials.json"), fakeWorkCreds)
	write(t, filepath.Join(w.cwm, "accounts", "work", ".claude.json"), identity("uuid-work", "work@example.com"))
	write(t, filepath.Join(w.cwm, "config.toml"), "version = 1\ndefault_account = \"work\"\n\n[[account]]\nname = \"work\"\ndir = \""+filepath.ToSlash(filepath.Join(w.cwm, "accounts", "work"))+"\"\n")
	// Runtime files: never backed up, never rolled back.
	write(t, filepath.Join(w.cwm, "cache", "stats.json"), strings.Repeat("x", 4096))
	write(t, filepath.Join(w.cwm, "server.toml"), "token = \"t\"\n")
	write(t, filepath.Join(w.cwm, "state", "agent.pid"), "1\n")
	write(t, filepath.Join(w.claude, "session-env", "abc", "env"), "X=1\n")
	write(t, filepath.Join(w.claude, "shell-snapshots", "snap.sh"), "true\n")
	write(t, filepath.Join(w.claude, ".credentials.lock"), "")
	write(t, filepath.Join(w.claude, "ide", "1234.lock"), "{}")
	write(t, filepath.Join(w.claude, "todos", "y.json"), "[]")
}

func TestExcludedPatterns(t *testing.T) {
	cases := map[string]bool{
		"backups": true, "backups/x": true, "cache/stats.json": true, "state/agent.lock": true,
		"state/agent.pid": true, "state/migrations.json": true, "state/x.json": false,
		"server.toml": true, "ide/1.lock": true, "a/b.lock/c": true, "lockfile": false,
		"workspace/session-env/x": true, "accounts/work/shell-snapshots/s.sh": true,
		"workspace/projects/a.jsonl": false, "config.toml": false,
	}
	for rel, want := range cases {
		if got := excluded(rel, CwmExcludes); got != want {
			t.Errorf("excluded(%q) = %v, want %v", rel, got, want)
		}
	}
	if !excluded("session-env/x/env", ClaudeExcludes) || excluded("projects/p/s.jsonl", ClaudeExcludes) {
		t.Error("claude excludes")
	}
}

// lockedOpener simulates Windows: opening any path that ends in one of
// names fails with a sharing violation.
func lockedOpener(t *testing.T, names ...string) *int {
	t.Helper()
	calls := 0
	prevOpen, prevSleep := openForBackup, backupSleep
	t.Cleanup(func() { openForBackup, backupSleep = prevOpen, prevSleep })
	backupSleep = func(time.Duration) {}
	openForBackup = func(p string) (io.ReadCloser, error) {
		for _, n := range names {
			if strings.HasSuffix(filepath.ToSlash(p), n) {
				calls++
				return nil, &fs.PathError{Op: "open", Path: p, Err: errSharingViolation}
			}
		}
		return os.Open(p)
	}
	return &calls
}

func TestBackupSkipsLockedFileAndRollsBack(t *testing.T) {
	w := newWorld(t)
	w.plainV07(t)
	write(t, filepath.Join(w.claude, "debug", "latest.txt"), "held open by claude\n")
	calls := lockedOpener(t, "debug/latest.txt")
	cfg, err := config.Load(w.l.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	o := Options{Store: accounts.FileStore{}, Now: clock(), Host: "hosta"}
	p, err := BuildPlan(w.l, cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	before := w.snapshot(t)
	rec, err := Apply(w.l, cfg, p, o)
	if err != nil {
		t.Fatalf("a locked file outside the plan aborted the migration: %v", err)
	}
	if *calls != 1+len(lockRetries) {
		t.Errorf("open attempts = %d, want %d", *calls, 1+len(lockRetries))
	}
	if len(rec.Warnings) != 1 || !strings.Contains(rec.Warnings[0], "latest.txt") || !strings.Contains(rec.Warnings[0], "locked") {
		t.Errorf("warnings = %q", rec.Warnings)
	}
	m, err := ReadManifest(storage.ExpandTilde(rec.Backup))
	if err != nil {
		t.Fatal(err)
	}
	var skipped []string
	for _, e := range m.Entries {
		if e.Skipped != "" {
			skipped = append(skipped, e.Rel+"="+e.Skipped)
		}
		for _, bad := range []string{"agent.pid", "cache/stats.json", "server.toml", "session-env", "shell-snapshots", ".lock"} {
			if strings.Contains(e.Rel, bad) {
				t.Errorf("%s is in the backup", e.Rel)
			}
		}
	}
	if strings.Join(skipped, ",") != "debug/latest.txt=locked" {
		t.Errorf("skipped = %v", skipped)
	}
	// Rollback restores the rest and leaves the locked file (and the
	// runtime files) where they are.
	write(t, filepath.Join(w.cwm, "cache", "stats.json"), "rebuilt")
	if _, err := Rollback(w.l, "", Options{Now: clock()}); err != nil {
		t.Fatal(err)
	}
	after := w.snapshot(t)
	delete(before, ".cwm/cache/stats.json")
	delete(after, ".cwm/cache/stats.json")
	sameSnapshot(t, before, after)
	if read(t, filepath.Join(w.claude, "debug", "latest.txt")) != "held open by claude\n" {
		t.Error("the skipped file was touched")
	}
}

func TestBackupLockedPlannedFileIsHardError(t *testing.T) {
	w := newWorld(t)
	w.plainV07(t)
	lockedOpener(t, "workspace/CLAUDE.md")
	cfg, err := config.Load(w.l.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	o := Options{Store: accounts.FileStore{}, Now: clock(), Host: "hosta"}
	p, err := BuildPlan(w.l, cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(w.l, cfg, p, o)
	var le *LockedError
	if !errors.As(err, &le) || !strings.Contains(err.Error(), "CLAUDE.md") || !strings.Contains(err.Error(), "likely") || !strings.Contains(err.Error(), "nothing changed") {
		t.Fatalf("err = %v", err)
	}
	if left, _ := os.ReadDir(w.l.BackupsDir()); len(left) != 0 {
		t.Errorf("partial backup left behind: %v", left)
	}
	if recs, _ := Records(w.l); len(recs) != 0 {
		t.Errorf("a failed backup was recorded: %+v", recs)
	}
	if _, err := os.Stat(filepath.Join(w.claude, "CLAUDE.md")); err == nil {
		t.Error("the apply ran after a failed backup")
	}
}

func TestPlanBackupSizeExcludesRuntime(t *testing.T) {
	w := newWorld(t)
	w.plainV07(t)
	cfg, _ := config.Load(w.l.ConfigPath())
	p, err := BuildPlan(w.l, cfg, Options{Store: accounts.FileStore{}, Host: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if p.BackupBytes <= 0 || p.BackupBytes >= 4096 {
		t.Errorf("backup bytes = %d (the 4 KB stats cache must not count)", p.BackupBytes)
	}
}

func TestIncompleteBackupsAreCleanedAndNeverRolledBack(t *testing.T) {
	w := newWorld(t)
	w.plainV07(t)
	// A pre-v0.8.3 run that died while backing up: data, no manifest.
	old := filepath.Join(w.l.BackupsDir(), "migrate-20261125T111444Z")
	write(t, filepath.Join(old, "data", "0", "x"), "partial")
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(old, past, past)
	// A v0.8.3 run that died: manifest still marked incomplete, pid gone.
	dead := filepath.Join(w.l.BackupsDir(), "migrate-20261125T120000Z")
	if err := writeJSON(filepath.Join(dead, ManifestName), Manifest{ID: "20261125T120000Z", Incomplete: true, PID: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	// A finished one (an older version wrote no "incomplete" key).
	done := filepath.Join(w.l.BackupsDir(), "migrate-20261101T000000Z")
	write(t, filepath.Join(done, ManifestName), `{"id":"20261101T000000Z","roots":[],"entries":[]}`)

	left := IncompleteBackups(w.l)
	if len(left) != 2 || left[0] != old || left[1] != dead {
		t.Fatalf("incomplete = %v", left)
	}
	if _, err := ReadManifest(dead); !errors.Is(err, ErrIncomplete) {
		t.Errorf("ReadManifest(incomplete) = %v", err)
	}
	cfg, _ := config.Load(w.l.ConfigPath())
	o := Options{Store: accounts.FileStore{}, Now: clock(), Host: "hosta"}
	p, err := BuildPlan(w.l, cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Leftovers) != 2 {
		t.Errorf("plan leftovers = %v", p.Leftovers)
	}
	var progress strings.Builder
	o.Progress = &progress
	if _, err := Apply(w.l, cfg, p, o); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{old, dead} {
		if _, err := os.Stat(d); err == nil {
			t.Errorf("%s not cleaned up", d)
		}
	}
	if _, err := os.Stat(done); err != nil {
		t.Error("a complete backup was removed")
	}
	if !strings.Contains(progress.String(), "removed the incomplete backup") {
		t.Errorf("progress = %s", progress.String())
	}
}
