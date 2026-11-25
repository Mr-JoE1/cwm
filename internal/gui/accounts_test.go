package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/gui/term"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// accountsFixture is actionFixture with an initialised workspace and two
// accounts: "work" (default, logged in) and "home" (no login yet).
func accountsFixture(t *testing.T) (*fixture, *fakeLauncher, *storage.Layout) {
	t.Helper()
	f, l := actionFixture(t, okSync)
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	f.srv.opts.Reserved = func(n string) bool { return n == "gui" || n == "list" || n == "debug" }
	seedAccount(t, f.root, "work", "work@example.com")
	layout := storage.NewLayoutAt(f.root)
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.CreateSkeleton(layout, cfg, "home", nil); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(layout.ConfigPath(), cfg); err != nil {
		t.Fatal(err)
	}
	// D79: no real Keychain, no /proc scan.
	f.srv.opts.Switcher = &accounts.Switcher{Store: accounts.FileStore{}, Running: func() []int { return nil }}
	return f, l, layout
}

// vaultLogin gives an account a saved (obviously fake) login in its vault
// entry, which a default switch needs.
func vaultLogin(t *testing.T, layout *storage.Layout, name string) {
	t.Helper()
	writeFile(t, filepath.Join(layout.AccountsDir(), name, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"fake-test-token-`+name+`"}}`)
}

func getAccounts(t *testing.T, f *fixture) map[string]accountView {
	t.Helper()
	var list []accountView
	getJSON(t, f, "/api/accounts", http.StatusOK, &list)
	out := map[string]accountView{}
	for _, a := range list {
		out[a.Name] = a
	}
	return out
}

var accountActions = map[string]string{
	"/api/accounts/add":         `{"name":"fresh"}`,
	"/api/accounts/login":       `{"name":"home"}`,
	"/api/accounts/set-default": `{"name":"home"}`,
	"/api/accounts/remove":      `{"name":"home","also_delete_config_dir":false}`,
}

func TestAccountActionGates(t *testing.T) {
	for target, good := range accountActions {
		t.Run(target, func(t *testing.T) {
			f, l, layout := accountsFixture(t)
			vaultLogin(t, layout, "home")
			cases := []struct {
				name string
				body string
				mut  func(*http.Request)
				want int
			}{
				{"no auth", good, func(r *http.Request) { r.Header.Del("Cookie") }, http.StatusUnauthorized},
				{"bad host", good, func(r *http.Request) { r.Host = "evil.com:47805" }, http.StatusForbidden},
				{"no csrf", good, func(r *http.Request) { r.Header.Del(CSRFHeader) }, http.StatusForbidden},
				{"bad csrf", good, func(r *http.Request) { r.Header.Set(CSRFHeader, strings.Repeat("0", 64)) }, http.StatusForbidden},
				{"form content type", good, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, http.StatusUnsupportedMediaType},
				{"bad json", `{"name":`, nil, http.StatusBadRequest},
				{"unknown field", `{"name":"home","zzz":1}`, nil, http.StatusBadRequest},
				{"trailing data", good + `{}`, nil, http.StatusBadRequest},
				{"oversized", `{"name":"` + strings.Repeat("a", maxBodyBytes) + `"}`, nil, http.StatusRequestEntityTooLarge},
			}
			for _, tc := range cases {
				if rec := f.post(t, target, tc.body, tc.mut); rec.Code != tc.want {
					t.Errorf("%s: code = %d, want %d\n%s", tc.name, rec.Code, tc.want, rec.Body.String())
				}
			}
			if len(l.calls) != 0 {
				t.Errorf("a rejected request reached the launcher: %v", l.calls)
			}
			if rec := f.do(t, "GET", target, func(r *http.Request) { r.AddCookie(f.cookie()) }); rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("GET on POST-only %s = %d, want 405", target, rec.Code)
			}
			if rec := f.post(t, target, good, nil); rec.Code != http.StatusOK {
				t.Errorf("good request: %d\n%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAccountsList(t *testing.T) {
	f, _, layout := accountsFixture(t)
	if err := os.MkdirAll(filepath.Join(layout.LaunchDir(), "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	got := getAccounts(t, f)
	w, h := got["work"], got["home"]
	if !w.IsDefault || !w.Default || !w.HasRecordedLogin || w.LoginStatus != accounts.LoginDone || w.LastUsed == "" ||
		w.ConfigDirReady == nil || !*w.ConfigDirReady || w.Email != "work@example.com" {
		t.Errorf("work = %+v", w)
	}
	if h.IsDefault || h.HasRecordedLogin || h.LoginStatus != accounts.LoginNever || h.LastUsed != "" || h.ConfigDirReady != nil {
		t.Errorf("home = %+v", h)
	}
	// The raw JSON keeps the v0.5 fields and adds snake_case ones.
	rec := f.do(t, "GET", "/api/accounts", func(r *http.Request) { r.AddCookie(f.cookie()) })
	for _, k := range []string{`"name":"work"`, `"default":true`, `"is_default":true`, `"has_recorded_login":true`, `"login_status":"done"`, `"config_dir_ready":true`, `"last_used":"`} {
		if !strings.Contains(rec.Body.String(), k) {
			t.Errorf("GET /api/accounts lacks %s: %s", k, rec.Body.String())
		}
	}
	if strings.Contains(rec.Body.String(), layout.AccountsDir()) {
		t.Error("GET /api/accounts leaks the account dir")
	}
}

func TestAccountsAddCreatesSkeletonAndLaunchesLogin(t *testing.T) {
	f, l, layout := accountsFixture(t)
	rec := f.post(t, "/api/accounts/add", `{"name":"lab"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("add = %d\n%s", rec.Code, rec.Body.String())
	}
	if len(l.calls) != 1 || strings.Join(l.calls[0], "|") != fakeCwm+"|--hold-on-error|account|login|lab" {
		t.Fatalf("launcher argv = %q", l.calls)
	}
	var res accountLaunch
	decode(t, rec, &res)
	if !res.Launched || res.Command != fakeCwm+" account login lab" || res.Account != "lab" || res.LoginStatus != accounts.LoginInProgress {
		t.Errorf("result = %+v", res)
	}
	cfg, _ := config.Load(layout.ConfigPath())
	a, ok := cfg.Account("lab")
	if !ok || a.Login != nil || cfg.DefaultAccount != "work" {
		t.Fatalf("config after add: %+v (default %q)", a, cfg.DefaultAccount)
	}
	if fi, err := os.Stat(filepath.Join(layout.AccountsDir(), "lab")); err != nil || !fi.IsDir() || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700) {
		t.Errorf("account dir: %v %v", fi, err)
	}
	if st := getAccounts(t, f)["lab"]; st.LoginStatus != accounts.LoginInProgress {
		t.Errorf("lab status = %q, want in_progress", st.LoginStatus)
	}

	cases := map[string]int{
		"":                      http.StatusBadRequest,
		strings.Repeat("a", 33): http.StatusBadRequest,
		"a/b":                   http.StatusBadRequest,
		`a\b`:                   http.StatusBadRequest,
		"../x":                  http.StatusBadRequest,
		"gui":                   http.StatusBadRequest, // a cwm command
		"list":                  http.StatusBadRequest,
		"login":                 http.StatusBadRequest, // a `cwm account` action
		"Work":                  http.StatusBadRequest,
		"123":                   http.StatusBadRequest,
		"a;b":                   http.StatusBadRequest,
		"work":                  http.StatusConflict,
		"lab":                   http.StatusConflict,
		strings.Repeat("b", 32): http.StatusOK,
		"under_score":           http.StatusOK,
	}
	for name, want := range cases {
		body, _ := json.Marshal(map[string]string{"name": name})
		rec := f.post(t, "/api/accounts/add", string(body), nil)
		if rec.Code != want {
			t.Errorf("name %q: %d, want %d\n%s", name, rec.Code, want, rec.Body.String())
		}
	}
	var e apiError
	decode(t, f.post(t, "/api/accounts/add", `{"name":"lab"}`, nil), &e)
	if !strings.Contains(e.Error, "cwm account login lab") {
		t.Errorf("duplicate error should point at the login command: %q", e.Error)
	}
	if len(l.calls) != 3 {
		t.Errorf("launcher calls = %d, want 3 (lab + the two valid names)", len(l.calls))
	}
}

func TestAccountsAddNeedsInitAndClaude(t *testing.T) {
	f, l := actionFixture(t, okSync) // workspace dir, but no config.toml yet
	if rec := f.post(t, "/api/accounts/add", `{"name":"lab"}`, nil); rec.Code != http.StatusConflict {
		t.Errorf("uninitialised: %d, want 409", rec.Code)
	}
	f2, l2, layout := accountsFixture(t)
	f2.srv.opts.FindClaude = func() (claudebin.Result, error) { return claudebin.Result{}, &claudebin.NotFoundError{GOOS: "linux"} }
	rec := f2.post(t, "/api/accounts/add", `{"name":"lab"}`, nil)
	var e apiError
	decode(t, rec, &e)
	if rec.Code != http.StatusFailedDependency || e.Command != claudebin.InstallCommand("linux") {
		t.Fatalf("no claude: %d %+v", rec.Code, e)
	}
	if _, err := os.Stat(filepath.Join(layout.AccountsDir(), "lab")); !os.IsNotExist(err) {
		t.Errorf("a missing claude must leave nothing behind (stat err = %v)", err)
	}
	if cfg, _ := config.Load(layout.ConfigPath()); len(cfg.Accounts) != 2 {
		t.Errorf("accounts = %d, want unchanged 2", len(cfg.Accounts))
	}
	if len(l.calls)+len(l2.calls) != 0 {
		t.Error("no terminal may open")
	}
}

// The launcher is unavailable (headless, no emulator): 501 with the exact
// command to paste; the account exists with no login and no marker.
func TestAccountsAddNoTerminalGivesCommand(t *testing.T) {
	f, l, layout := accountsFixture(t)
	l.err = fmt.Errorf("term: %w (headless)", term.ErrNoTerminal)
	rec := f.post(t, "/api/accounts/add", `{"name":"lab"}`, nil)
	var e apiError
	decode(t, rec, &e)
	if rec.Code != http.StatusNotImplemented || e.Command != fakeCwm+" account login lab" {
		t.Fatalf("501: %d %+v", rec.Code, e)
	}
	if _, ok := accounts.ReadLoginState(layout, "lab"); ok {
		t.Error("nothing runs, so no in-progress marker may stay")
	}
	if st := getAccounts(t, f)["lab"]; st.LoginStatus != accounts.LoginNever {
		t.Errorf("lab = %q, want never", st.LoginStatus)
	}
	// Another launch failure marks the login failed, with the reason.
	l.err = fmt.Errorf("exec format error")
	f.post(t, "/api/accounts/login", `{"name":"lab"}`, nil)
	if st := getAccounts(t, f)["lab"]; st.LoginStatus != accounts.LoginFailed || !strings.Contains(st.LoginError, "exec format error") {
		t.Errorf("lab = %+v, want failed", st)
	}
}

func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestAccountsLoginStateTransitionsAndRetry(t *testing.T) {
	f, l, layout := accountsFixture(t)
	// The CLI in the window reported failure (what `cwm account login`
	// writes when claude exits non-zero).
	accounts.MarkLogin(layout, "home", accounts.LoginFailed, "cli", fmt.Errorf("accounts: claude auth login failed (exit status 1)"))
	if st := getAccounts(t, f)["home"]; st.LoginStatus != accounts.LoginFailed || !strings.Contains(st.LoginError, "exit status 1") {
		t.Fatalf("home = %+v", st)
	}
	// Retry works after a failure.
	if rec := f.post(t, "/api/accounts/login", `{"name":"home"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("retry = %d %s", rec.Code, rec.Body.String())
	}
	if len(l.calls) != 1 || strings.Join(l.calls[0], " ") != fakeCwm+" --hold-on-error account login home" {
		t.Fatalf("calls = %q", l.calls)
	}
	// The window was closed: its cwm process is gone → failed.
	now := time.Now().UTC()
	if err := accounts.WriteLoginState(layout, "home", accounts.LoginState{Status: accounts.LoginInProgress, Started: now, PID: exitedPID(t), By: "cli"}); err != nil {
		t.Fatal(err)
	}
	st := getAccounts(t, f)["home"]
	if st.LoginStatus != accounts.LoginFailed || !strings.Contains(st.LoginError, "closed") {
		t.Errorf("dead pid: %+v", st)
	}
	if stored, _ := accounts.ReadLoginState(layout, "home"); stored.Status != accounts.LoginFailed {
		t.Errorf("the agent must persist the transition, marker = %+v", stored)
	}
	// A login still running (this test process) refuses a second one.
	if err := accounts.WriteLoginState(layout, "home", accounts.LoginState{Status: accounts.LoginInProgress, Started: now, PID: os.Getpid(), By: "cli"}); err != nil {
		t.Fatal(err)
	}
	if rec := f.post(t, "/api/accounts/login", `{"name":"home"}`, nil); rec.Code != http.StatusConflict {
		t.Errorf("second login while running: %d, want 409", rec.Code)
	}
	// The login finishes: identity recorded, credentials written.
	cfg, _ := config.Load(layout.ConfigPath())
	_ = cfg.SetAccountLogin("home", config.Login{Email: "home@example.com"})
	if err := config.Save(layout.ConfigPath(), cfg); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(layout.AccountsDir(), "home", ".credentials.json"), "{}")
	if st := getAccounts(t, f)["home"]; st.LoginStatus != accounts.LoginDone || !st.HasRecordedLogin {
		t.Errorf("finished: %+v", st)
	}
	if rec := f.post(t, "/api/accounts/login", `{"name":"ghost"}`, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown: %d", rec.Code)
	}
}

func TestAccountsSetDefaultAndRemove(t *testing.T) {
	f, _, layout := accountsFixture(t)
	if rec := f.post(t, "/api/accounts/set-default", `{"name":"home"}`, nil); rec.Code != http.StatusConflict {
		t.Errorf("set-default to an account without a saved login: %d, want 409", rec.Code)
	}
	vaultLogin(t, layout, "home")
	if rec := f.post(t, "/api/accounts/set-default", `{"name":"ghost"}`, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown default: %d", rec.Code)
	}
	if rec := f.post(t, "/api/accounts/set-default", `{"name":"home"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("set-default: %d", rec.Code)
	}
	if cfg, _ := config.Load(layout.ConfigPath()); cfg.DefaultAccount != "home" {
		t.Fatalf("default = %q", cfg.DefaultAccount)
	}
	if err := os.MkdirAll(filepath.Join(layout.LaunchDir(), "home"), 0o700); err != nil {
		t.Fatal(err)
	}
	accounts.MarkLogin(layout, "home", accounts.LoginFailed, "cli", fmt.Errorf("boom"))

	// Remove the default with its dirs: backup first, default promoted.
	rec := f.post(t, "/api/accounts/remove", `{"name":"home","also_delete_config_dir":true}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body.String())
	}
	var res accountRemoveResult
	decode(t, rec, &res)
	if res.Removed != "home" || res.Backup == "" || !res.DeletedConfigDir || res.Default != "work" {
		t.Errorf("result = %+v", res)
	}
	if fi, err := os.Stat(res.Backup); err != nil || !fi.IsDir() {
		t.Errorf("backup %s: %v", res.Backup, err)
	}
	for _, gone := range []string{filepath.Join(layout.AccountsDir(), "home"), filepath.Join(layout.LaunchDir(), "home"), accounts.LoginStatePath(layout, "home")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be gone (err = %v)", gone, err)
		}
	}
	// The last account is refused, with nothing touched.
	rec = f.post(t, "/api/accounts/remove", `{"name":"work","also_delete_config_dir":true}`, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("last account: %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(layout.AccountsDir(), "work")); err != nil {
		t.Errorf("refused remove touched the account dir: %v", err)
	}
	if rec := f.post(t, "/api/accounts/remove", `{"name":"ghost"}`, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown: %d", rec.Code)
	}
	// Without the flag the dir stays (the v0.5 CLI semantics).
	cfg, _ := config.Load(layout.ConfigPath())
	_ = accounts.CreateSkeleton(layout, cfg, "keep", nil)
	_ = config.Save(layout.ConfigPath(), cfg)
	rec = f.post(t, "/api/accounts/remove", `{"name":"keep"}`, nil)
	decode(t, rec, &res)
	if rec.Code != http.StatusOK || res.DeletedConfigDir {
		t.Errorf("remove keep: %d %+v", rec.Code, res)
	}
	if _, err := os.Stat(filepath.Join(layout.AccountsDir(), "keep")); err != nil {
		t.Errorf("dir must stay without also_delete_config_dir: %v", err)
	}
}

func TestEventsSnapshotCarriesLoginState(t *testing.T) {
	f, _, layout := accountsFixture(t)
	a := f.srv.snapshot()
	accounts.MarkLogin(layout, "home", accounts.LoginFailed, "cli", fmt.Errorf("x"))
	f.srv.snapAt = time.Time{}
	b := f.srv.snapshot()
	if ch := a.changed(b); len(ch) != 1 || ch[0] != "accounts" {
		t.Errorf("changed = %v, want [accounts] (logins %v → %v)", ch, a.Logins, b.Logins)
	}
}
