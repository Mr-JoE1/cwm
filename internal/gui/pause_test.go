package gui

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/migrate"
)

func waitMigration(t *testing.T, f *fixture) migrationStatus {
	t.Helper()
	var st migrationStatus
	deadline := time.Now().Add(15 * time.Second)
	for {
		rec := f.do(t, "GET", "/api/migrate", func(r *http.Request) { r.AddCookie(f.cookie()) })
		decode(t, rec, &st)
		if !st.Running || time.Now().After(deadline) {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// D101: the dashboard's apply runs inside the agent, which holds
// agent.lock (exclusively, on Windows) the whole time.
func TestMigrateApplyWhileAgentRuns(t *testing.T) {
	f, _ := settingsFixture(t)
	f.srv.opts.Migrate = &migrate.Options{Store: accounts.FileStore{}, Host: "testhost"}
	lock, err := agent.Acquire(f.root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	writeFile(t, filepath.Join(f.root, "cache", "stats.json"), "{}")

	// `cwm migrate` holds the agent: the dashboard waits its turn.
	if rec := f.post(t, "/api/agent/pause", `{"holder":"cli-9","why":"cwm migrate"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("pause = %d %s", rec.Code, rec.Body.String())
	}
	rec := f.post(t, "/api/migrate/apply", `{"to":"capture"}`, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "cwm migrate") {
		t.Fatalf("apply while paused = %d %s", rec.Code, rec.Body.String())
	}
	rec = f.do(t, "GET", "/api/agent", func(r *http.Request) { r.AddCookie(f.cookie()) })
	if !strings.Contains(rec.Body.String(), `"paused_by":"cwm migrate"`) {
		t.Fatalf("agent info = %s", rec.Body.String())
	}
	if rec := f.post(t, "/api/agent/pause", `{"holder":"dashboard"}`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("a remote pause as the dashboard = %d", rec.Code)
	}
	if rec := f.post(t, "/api/agent/resume", `{"holder":"cli-9"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("resume = %d", rec.Code)
	}

	rec = f.post(t, "/api/migrate/apply", `{"to":"capture"}`, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("apply = %d %s", rec.Code, rec.Body.String())
	}
	st := waitMigration(t, f)
	if st.Running || st.Error != "" || st.Record == nil || st.Record.Status != migrate.StatusApplied {
		t.Fatalf("status = %+v", st)
	}
	if h, _ := f.srv.opts.Pauser.Paused(); h != "" {
		t.Fatalf("still paused by %s after the migration", h)
	}
	m, err := migrate.ReadManifest(filepath.Join(f.root, "backups", "migrate-"+st.Record.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Entries {
		if strings.HasSuffix(e.Rel, "agent.lock") || strings.HasPrefix(e.Rel, "cache") {
			t.Fatalf("%s backed up", e.Rel)
		}
	}
	if _, err := os.Stat(agent.LockPath(f.root)); err != nil {
		t.Fatalf("agent.lock gone: %v", err)
	}
}

// While the dashboard's own migration holds the tick, a CLI pause is
// refused at once (not queued behind the migration's lock).
func TestPauseRefusedDuringDashboardMigration(t *testing.T) {
	f, _ := settingsFixture(t)
	if err := f.srv.opts.Pauser.Pause(dashboardHolder, "the dashboard's migration", 0); err != nil {
		t.Fatal(err)
	}
	f.srv.actionMu.Lock() // what a running dashboard migration holds
	defer f.srv.actionMu.Unlock()
	done := make(chan int, 1)
	go func() { done <- f.post(t, "/api/agent/pause", `{"holder":"cli-1"}`, nil).Code }()
	select {
	case code := <-done:
		if code != http.StatusConflict {
			t.Fatalf("pause = %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pause waited for the migration's action lock")
	}
}
