//go:build windows

package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// holdExclusive opens p with share mode 0, as the agent does with its
// lock: every other open of p fails with a sharing violation.
func holdExclusive(t *testing.T, p string) syscall.Handle {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.CloseHandle(h) })
	return h
}

// The failure of the first real Windows migration: the running agent's
// agent.lock (an exclusive handle) aborted the backup.
func TestWindowsMigrateWhileAgentHoldsLock(t *testing.T) {
	w := newWorld(t)
	w.plainV07(t)
	lock, err := agent.Acquire(w.cwm)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := os.ReadFile(agent.LockPath(w.cwm)); err == nil || !isLocked(err) {
		t.Fatalf("reading the held agent.lock: %v (want a sharing violation)", err)
	}
	// A file a running claude holds that the plan does not touch: skipped.
	held := filepath.Join(w.claude, "debug", "latest.txt")
	write(t, held, "log\n")
	holdExclusive(t, held)
	prev := backupSleep
	backupSleep = func(time.Duration) {}
	defer func() { backupSleep = prev }()

	cfg, err := config.Load(w.l.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	o := Options{Store: accounts.FileStore{}, Now: clock(), Host: "hosta"}
	p, err := BuildPlan(w.l, cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := Apply(w.l, cfg, p, o)
	if err != nil {
		t.Fatalf("migration with the agent's lock held: %v", err)
	}
	if rec.SessionsAfter != 2 || len(rec.Warnings) != 1 || !strings.Contains(rec.Warnings[0], "latest.txt") {
		t.Fatalf("record = %+v", rec)
	}
	m, err := ReadManifest(storage.ExpandTilde(rec.Backup))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Entries {
		if strings.HasSuffix(e.Rel, "agent.lock") {
			t.Fatalf("agent.lock in the manifest: %+v", e)
		}
	}
	// Rollback while the lock is still held.
	if _, err := Rollback(w.l, "", Options{Now: clock()}); err != nil {
		t.Fatalf("rollback with the agent's lock held: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.cwm, "workspace", "projects", "api", "s-1.jsonl")); err != nil {
		t.Errorf("rollback did not restore the v0.7 workspace: %v", err)
	}
}

// A file the plan must read, held exclusively: a hard error naming it.
func TestWindowsLockedPlannedFile(t *testing.T) {
	w := newWorld(t)
	w.plainV07(t)
	holdExclusive(t, filepath.Join(w.cwm, "workspace", "CLAUDE.md"))
	prev := backupSleep
	backupSleep = func(time.Duration) {}
	defer func() { backupSleep = prev }()
	cfg, _ := config.Load(w.l.ConfigPath())
	o := Options{Store: accounts.FileStore{}, Now: clock(), Host: "hosta"}
	p, err := BuildPlan(w.l, cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(w.l, cfg, p, o)
	var le *LockedError
	if !errors.As(err, &le) || !strings.Contains(err.Error(), "CLAUDE.md") {
		t.Fatalf("err = %v", err)
	}
	if left := IncompleteBackups(w.l); len(left) != 0 {
		t.Errorf("incomplete backups left: %v", left)
	}
}
