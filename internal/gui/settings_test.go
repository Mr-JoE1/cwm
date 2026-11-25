package gui

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/migrate"
)

func (f *fixture) put(t *testing.T, target, body string, mut func(*http.Request)) *httptest.ResponseRecorder {
	return f.post(t, target, body, func(r *http.Request) {
		r.Method = http.MethodPut
		if mut != nil {
			mut(r)
		}
	})
}

func settingsFixture(t *testing.T) (*fixture, *[]string) {
	t.Helper()
	f, _ := actionFixture(t, okSync)
	writeFile(t, filepath.Join(f.root, "config.toml"), "version = 1\nmode = \"isolated\"\n")
	var calls []string
	f.srv.opts.SettingEffects = func(cfg *config.Config, key string, dry bool, out io.Writer) error {
		v, _ := cfg.Get(key)
		calls = append(calls, fmt.Sprintf("%s=%s dry=%v", key, v, dry))
		fmt.Fprintf(out, "  would change %s\n\n", key)
		return nil
	}
	return f, &calls
}

func TestSettingsGet(t *testing.T) {
	f, _ := settingsFixture(t)
	rec := f.do(t, "GET", "/api/settings", func(r *http.Request) { r.AddCookie(f.cookie()) })
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body.String())
	}
	var v settingsView
	decode(t, rec, &v)
	if v.Mode != "isolated" || v.NeedsMigration || v.Hash == "" || v.Live.Env == "" {
		t.Fatalf("view = %+v", v)
	}
	byKey := map[string]settingRow{}
	for _, k := range v.Keys {
		byKey[k.Name] = k
	}
	if len(v.Keys) != len(config.Keys()) {
		t.Fatalf("%d keys, want %d", len(v.Keys), len(config.Keys()))
	}
	if r := byKey["sync.sessions"]; r.Value != "true" || r.Group != "sync" || r.Help == "" {
		t.Errorf("sync.sessions = %+v", r)
	}
	if r := byKey["features.path_mirror"]; !r.NA || r.Group != "features" {
		t.Errorf("path_mirror in isolated mode = %+v, want n/a", r)
	}
	if byKey["mode"].Group != "mode" || byKey["display.default_tab"].Group != "display" || byKey["link_mode"].Group != "advanced" {
		t.Errorf("groups = %+v", byKey)
	}
}

func TestSettingsPut(t *testing.T) {
	f, calls := settingsFixture(t)
	cfgPath := filepath.Join(f.root, "config.toml")

	// CSRF is required, and PUT is refused on every other endpoint.
	if rec := f.put(t, "/api/settings", `{"key":"features.stats","value":"off"}`, func(r *http.Request) { r.Header.Del(CSRFHeader) }); rec.Code != http.StatusForbidden {
		t.Fatalf("no CSRF = %d", rec.Code)
	}
	if rec := f.put(t, "/api/accounts/add", `{"name":"x"}`, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT on a POST endpoint = %d", rec.Code)
	}
	if rec := f.post(t, "/api/settings", `{"key":"features.stats","value":"off"}`, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/settings = %d", rec.Code)
	}
	for body, want := range map[string]int{
		`{"key":"nope","value":"on"}`:                       http.StatusNotFound,
		`{"key":"features.stats","value":"maybe"}`:          http.StatusBadRequest,
		`{"key":"display.hidden_tabs","value":"sessions"}`:  http.StatusBadRequest, // the default tab
		`{"key":"mode","value":"capture"}`:                  http.StatusConflict,
		`{"key":"features.stats","value":"on","extra":1}`:   http.StatusBadRequest,
		`{"key":"display.default_tab","value":"settings2"}`: http.StatusBadRequest,
	} {
		if rec := f.put(t, "/api/settings", body, nil); rec.Code != want {
			t.Errorf("%s = %d, want %d (%s)", body, rec.Code, want, rec.Body.String())
		}
	}
	before, _ := os.ReadFile(cfgPath)
	if len(*calls) != 0 {
		t.Fatalf("effects ran for rejected writes: %v", *calls)
	}

	// dry_run describes, saves nothing.
	rec := f.put(t, "/api/settings", `{"key":"features.autostart","value":"off","dry_run":true}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry = %d %s", rec.Code, rec.Body.String())
	}
	var res settingsPutResult
	decode(t, rec, &res)
	if res.Saved || res.Value != "false" || !slices.Equal(res.Notes, []string{"would change features.autostart"}) {
		t.Fatalf("dry result = %+v", res)
	}
	if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
		t.Fatal("dry run changed config.toml")
	}

	rec = f.put(t, "/api/settings", `{"key":"features.stats","value":"off"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("put = %d %s", rec.Code, rec.Body.String())
	}
	decode(t, rec, &res)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Saved || res.Hash == "" || cfg.Features.Stats || cfg.Mode != "isolated" {
		t.Fatalf("after put: %+v, stats=%v mode=%q", res, cfg.Features.Stats, cfg.Mode)
	}
	if want := []string{"features.autostart=false dry=true", "features.stats=false dry=false"}; !slices.Equal(*calls, want) {
		t.Fatalf("effects = %v, want %v", *calls, want)
	}

	// The event snapshot notices the change.
	a := f.srv.snapshot()
	f.put(t, "/api/settings", `{"key":"sync.todos_never","value":"on"}`, nil) // unknown: 404, no change
	f.put(t, "/api/settings", `{"key":"sync.history","value":"off"}`, nil)
	b := f.srv.snapshot()
	if !slices.Contains(a.changed(b), "settings") {
		t.Fatalf("snapshot change = %v, want settings", a.changed(b))
	}

	// An effect failure reports that the value is saved.
	f.srv.opts.SettingEffects = func(*config.Config, string, bool, io.Writer) error { return fmt.Errorf("boom") }
	rec = f.put(t, "/api/settings", `{"key":"features.hooks","value":"off"}`, nil)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "is saved") {
		t.Fatalf("effect failure = %d %s", rec.Code, rec.Body.String())
	}
}

func TestMigrateEndpoints(t *testing.T) {
	f, _ := settingsFixture(t)
	f.srv.opts.Migrate = &migrate.Options{Store: accounts.FileStore{}, Host: "testhost"}
	sync := 0
	f.srv.opts.Migrate.SyncFolder = func(string) error { sync++; return nil }
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	if rec := f.post(t, "/api/migrate/preview", `{"to":"elsewhere"}`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad mode = %d", rec.Code)
	}
	rec := f.post(t, "/api/migrate/preview", `{"to":"capture"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", rec.Code, rec.Body.String())
	}
	var pv struct {
		Kind     string         `json:"kind"`
		ToMode   string         `json:"to_mode"`
		Root     string         `json:"root"`
		Sessions []string       `json:"sessions"`
		Counts   map[string]int `json:"counts"`
		Actions  []struct {
			Src string `json:"src"`
		} `json:"actions"`
	}
	decode(t, rec, &pv)
	if pv.ToMode != "capture" || pv.Root != filepath.Join("~", ".claude") || len(pv.Sessions) != 1 || len(pv.Counts) == 0 {
		t.Fatalf("preview = %+v", pv)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "projects")); !os.IsNotExist(err) {
		t.Fatal("preview changed the target root")
	}

	rec = f.post(t, "/api/migrate/apply", `{"to":"capture"}`, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("apply = %d %s", rec.Code, rec.Body.String())
	}
	var st migrationStatus
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec = f.do(t, "GET", "/api/migrate", func(r *http.Request) { r.AddCookie(f.cookie()) })
		decode(t, rec, &st)
		if !st.Running || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.Running || st.Error != "" || st.Record == nil || st.Mode != "capture" || len(st.Lines) == 0 {
		t.Fatalf("status = %+v", st)
	}
	if st.Record.SessionsAfter != 1 || st.Record.Backup == "" || sync != 1 {
		t.Fatalf("record = %+v (sync folder calls %d)", st.Record, sync)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "projects", "alpha", "11111111-aaaa.jsonl")); err != nil {
		t.Fatalf("session not in the capture root: %v", err)
	}
	// The server follows the new root.
	if got := f.srv.opts.Layout.WorkspaceDir(); got != filepath.Join(home, ".claude") {
		t.Fatalf("layout root = %s", got)
	}
	for _, l := range st.Lines {
		if strings.Contains(l, home) {
			t.Fatalf("progress line with a full home path: %q", l)
		}
	}
}

func TestAccountsRunAndRunningNow(t *testing.T) {
	f, l := actionFixture(t, okSync)
	writeFile(t, filepath.Join(f.root, "config.toml"), "version = 1\nmode = \"capture\"\ndefault_account = \"work\"\n\n[[account]]\nname = \"work\"\ndir = \"x\"\n\n[[account]]\nname = \"home\"\ndir = \"y\"\n")
	loginDir := claudepaths.LoginDir(f.srv.opts.Layout, "home")
	f.srv.opts.Switcher = &accounts.Switcher{Procs: func() []accounts.ClaudeProc {
		return []accounts.ClaudeProc{{PID: 10, EnvKnown: true}, {PID: 11, EnvKnown: true, ConfigDir: "/elsewhere"}}
	}}
	var list []accountView
	rec := f.do(t, "GET", "/api/accounts", func(r *http.Request) { r.AddCookie(f.cookie()) })
	decode(t, rec, &list)
	if len(list) != 2 || list[0].RunningNow == nil || !*list[0].RunningNow || list[0].Parallel || !list[1].Parallel || *list[1].RunningNow {
		t.Fatalf("accounts = %+v", list)
	}
	f.srv.opts.Switcher.Procs = func() []accounts.ClaudeProc {
		return []accounts.ClaudeProc{{PID: 12, EnvKnown: true, ConfigDir: loginDir + string(filepath.Separator)}}
	}
	rec = f.do(t, "GET", "/api/accounts", func(r *http.Request) { r.AddCookie(f.cookie()) })
	decode(t, rec, &list)
	if *list[0].RunningNow || !*list[1].RunningNow {
		t.Fatalf("parallel attribution: %+v %+v", *list[0].RunningNow, *list[1].RunningNow)
	}
	// Unknown environment (macOS, Windows): no claim either way.
	f.srv.opts.Switcher.Procs = func() []accounts.ClaudeProc { return []accounts.ClaudeProc{{PID: 13}} }
	rec = f.do(t, "GET", "/api/accounts", func(r *http.Request) { r.AddCookie(f.cookie()) })
	if strings.Contains(rec.Body.String(), "running_now") {
		t.Fatalf("running_now reported without a readable environment: %s", rec.Body.String())
	}

	if rec := f.post(t, "/api/accounts/run", `{"name":"nobody"}`, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown = %d", rec.Code)
	}
	rec = f.post(t, "/api/accounts/run", `{"name":"home"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("run = %d %s", rec.Code, rec.Body.String())
	}
	last := l.calls[len(l.calls)-1]
	if !slices.Equal(last[len(last)-3:], []string{"run", "--account", "home"}) {
		t.Fatalf("argv = %v", last)
	}
}
