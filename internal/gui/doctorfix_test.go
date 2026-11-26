package gui

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/doctor"
)

// fixFixture is an isolated-mode v0.8 workspace whose .stignore is
// missing: the one thing `cwm doctor --fix` repairs that needs no login.
func fixFixture(t *testing.T) *fixture {
	t.Helper()
	f, _ := actionFixture(t, okSync)
	writeFile(t, filepath.Join(f.root, "config.toml"), "version = 1\nmode = \"isolated\"\n")
	f.srv.opts.Switcher = &accounts.Switcher{Store: accounts.FileStore{}, Running: func() []int { return nil }}
	return f
}

func (f *fixture) stignore() string { return filepath.Join(f.root, "workspace", ".stignore") }

func TestDoctorFixAllowList(t *testing.T) {
	f := fixFixture(t)
	for _, body := range []string{
		`{"checks":["ssh"]}`,
		`{"checks":["stignore","claude"]}`, // one bad name rejects the whole request
		`{"checks":["rm -rf ~"]}`,
		`{"checks":["cwm doctor --fix"]}`,
		`{"checks":[]}`,
		`{}`,
		`{"checks":["` + strings.Repeat(`stignore","`, maxFixChecks) + `stignore"]}`,
	} {
		for _, target := range []string{"/api/doctor/fix", "/api/doctor/fix?dry_run=1"} {
			if rec := f.post(t, target, body, nil); rec.Code != http.StatusBadRequest {
				t.Errorf("%s %s = %d, want 400\n%s", target, body, rec.Code, rec.Body.String())
			}
		}
	}
	if _, err := os.Stat(f.stignore()); err == nil {
		t.Fatal("a rejected request wrote the .stignore")
	}
	// Every allow-listed name is a real doctor check name the page shows.
	for name := range doctor.FixActions {
		if _, ok := map[string]bool{"stignore": true, "mcp-bridge": true, "cwm-hooks": true, "settings": true, "vault": true}[name]; !ok {
			t.Errorf("unexpected allow-listed check %q", name)
		}
	}
}

func TestDoctorFixNeedsCSRFAndCookie(t *testing.T) {
	f := fixFixture(t)
	body := `{"checks":["stignore"]}`
	if rec := f.post(t, "/api/doctor/fix", body, func(r *http.Request) { r.Header.Del(CSRFHeader) }); rec.Code != http.StatusForbidden {
		t.Errorf("no CSRF = %d, want 403", rec.Code)
	}
	if rec := f.post(t, "/api/doctor/fix", body, func(r *http.Request) { r.Header.Del("Cookie") }); rec.Code != http.StatusUnauthorized {
		t.Errorf("no cookie = %d, want 401", rec.Code)
	}
	if rec := f.do(t, "GET", "/api/doctor/fix", func(r *http.Request) { r.AddCookie(f.cookie()) }); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want 405", rec.Code)
	}
	if _, err := os.Stat(f.stignore()); err == nil {
		t.Fatal("a refused request wrote the .stignore")
	}
}

func TestDoctorFixDryRunThenApply(t *testing.T) {
	f := fixFixture(t)
	gen := f.srv.snapshot().Doctor

	rec := f.post(t, "/api/doctor/fix?dry_run=1", `{"checks":["stignore","stignore"]}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry run = %d %s", rec.Code, rec.Body.String())
	}
	var plan doctorFixResponse
	decode(t, rec, &plan)
	if !plan.DryRun || len(plan.Results) != 1 || plan.Results[0].Status != "planned" || !strings.Contains(plan.Results[0].Detail, ".stignore") || plan.Checks != nil {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := os.Stat(f.stignore()); err == nil {
		t.Fatal("the dry run wrote the .stignore")
	}
	f.srv.invalidateSnapshot()
	if f.srv.snapshot().Doctor != gen {
		t.Fatal("the dry run emitted a doctor event")
	}

	rec = f.post(t, "/api/doctor/fix", `{"checks":["stignore"]}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply = %d %s", rec.Code, rec.Body.String())
	}
	var res doctorFixResponse
	decode(t, rec, &res)
	if res.DryRun || len(res.Results) != 1 || res.Results[0] != (fixResult{Name: "stignore", Status: "ok", Detail: "fixed"}) {
		t.Fatalf("results = %+v", res.Results)
	}
	if _, err := os.Stat(f.stignore()); err != nil {
		t.Fatalf("the fix did not write the .stignore: %v", err)
	}
	if c := findCheck(res.Checks, "stignore"); c == nil || c.Status != doctor.StatusOK {
		t.Fatalf("fresh checks: stignore = %+v", c)
	}
	snap := f.srv.snapshot()
	if snap.Doctor != gen+1 {
		t.Fatalf("doctor generation = %d, want %d", snap.Doctor, gen+1)
	}
	if ch := (Snapshot{Doctor: gen}).changed(Snapshot{Doctor: snap.Doctor}); len(ch) != 1 || ch[0] != "doctor" {
		t.Fatalf("changed = %v, want [doctor]", ch)
	}
}

func TestDoctorFixSkipsAndPause(t *testing.T) {
	f := fixFixture(t)
	// features.hooks off: the pass leaves settings.json alone.
	writeFile(t, filepath.Join(f.root, "config.toml"), "version = 1\nmode = \"isolated\"\n[features]\nhooks = false\n")
	var res doctorFixResponse
	rec := f.post(t, "/api/doctor/fix?dry_run=1", `{"checks":["cwm-hooks","vault"]}`, nil)
	decode(t, rec, &res)
	if rec.Code != http.StatusOK || len(res.Results) != 2 || res.Results[0].Status != "skipped" || res.Results[1].Status != "skipped" ||
		!strings.Contains(res.Results[1].Detail, "no default account") {
		t.Fatalf("skips = %d %+v", rec.Code, res.Results)
	}

	// A pre-v0.8 config waits for the migration.
	writeFile(t, filepath.Join(f.root, "config.toml"), "version = 1\n")
	rec = f.post(t, "/api/doctor/fix", `{"checks":["stignore"]}`, nil)
	res = doctorFixResponse{}
	decode(t, rec, &res)
	if rec.Code != http.StatusOK || len(res.Results) != 1 || res.Results[0].Status != "skipped" || !strings.Contains(res.Results[0].Detail, "migrate") {
		t.Fatalf("pre-v0.8 = %d %+v", rec.Code, res.Results)
	}

	// A paused agent (a migration running) refuses to apply.
	writeFile(t, filepath.Join(f.root, "config.toml"), "version = 1\nmode = \"isolated\"\n")
	if err := f.srv.opts.Pauser.Pause("test", "a migration", time.Minute); err != nil {
		t.Fatal(err)
	}
	if rec := f.post(t, "/api/doctor/fix", `{"checks":["stignore"]}`, nil); rec.Code != http.StatusConflict {
		t.Fatalf("paused = %d, want 409\n%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(f.stignore()); err == nil {
		t.Fatal("a paused agent's fix wrote the .stignore")
	}
}
