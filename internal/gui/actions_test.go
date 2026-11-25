package gui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/gui/term"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
	"github.com/Mr-JoE1/cwm/internal/syncthing"
)

const fakeCwm = "/opt/cwm/bin/cwm"

// fakeLauncher records every argv it is asked to open; a real terminal is
// never spawned in tests.
type fakeLauncher struct {
	calls [][]string
	err   error
}

func (l *fakeLauncher) Open(argv []string) error {
	l.calls = append(l.calls, append([]string(nil), argv...))
	return l.err
}

// pairSync is a mutable in-memory syncthing for the pairing endpoint.
type pairSync struct {
	fakeSync
	cfg  *syncthing.Config
	puts int
}

func newPairSync() *pairSync {
	return &pairSync{cfg: &syncthing.Config{Devices: []syncthing.Device{{DeviceID: testMyID, Name: "self"}}}}
}

func (p *pairSync) GetConfig() (*syncthing.Config, error) {
	data, _ := json.Marshal(p.cfg)
	var c syncthing.Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (p *pairSync) PutConfig(c *syncthing.Config) error {
	p.cfg = c
	p.puts++
	return nil
}

// actionFixture is newFixture plus a fake launcher, a manifest listing
// alpha, and the reserved-name seam.
func actionFixture(t *testing.T, sync func() (syncengine.Client, error)) (*fixture, *fakeLauncher) {
	t.Helper()
	f := newFixture(t, sync)
	l := &fakeLauncher{}
	f.srv.opts.Launcher = l
	f.srv.opts.CwmPath = fakeCwm
	f.srv.opts.GOOS = "linux"
	f.srv.opts.Reserved = func(n string) bool { return n == "gui" || n == "list" }
	f.srv.opts.FindClaude = func() (claudebin.Result, error) {
		return claudebin.Result{Path: "/usr/bin/claude", Source: claudebin.SourcePath, OnPath: true}, nil
	}
	writeFile(t, filepath.Join(f.root, "workspace", "repos.tsv"), "alpha\t/srv/git/alpha.git\nbeta\t/srv/git/beta.git\n")
	return f, l
}

// post sends an authenticated, CSRF-carrying JSON POST unless mut
// overrides the request.
func (f *fixture) post(t *testing.T, target, body string, mut func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", target, strings.NewReader(body))
	req.Host = "127.0.0.1:47805"
	req.AddCookie(f.cookie())
	req.Header.Set(CSRFHeader, f.srv.csrf)
	req.Header.Set("Content-Type", "application/json")
	if mut != nil {
		mut(req)
	}
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("invalid JSON (%d): %v\n%s", rec.Code, err, rec.Body.String())
	}
}

var postEndpoints = map[string]string{
	"/api/init":            `{}`,
	"/api/accounts":        `{"name":"work"}`,
	"/api/sessions/resume": `{"project":"alpha","id":"11111111-aaaa"}`,
	"/api/pair":            `{"deviceID":"` + testPeerID + `","name":"laptop"}`,
}

func TestActionAuthCSRFAndBodyRules(t *testing.T) {
	for target, good := range postEndpoints {
		t.Run(target, func(t *testing.T) {
			f, l := actionFixture(t, func() (syncengine.Client, error) { return newPairSync(), nil })
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
				{"csrf is not the cookie name", good, func(r *http.Request) { r.Header.Set(CSRFHeader, f.srv.CookieName()) }, http.StatusForbidden},
				{"form content type", good, func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }, http.StatusUnsupportedMediaType},
				{"no content type", good, func(r *http.Request) { r.Header.Del("Content-Type") }, http.StatusUnsupportedMediaType},
				{"bad json", `{"name":`, nil, http.StatusBadRequest},
				{"not an object", `["x"]`, nil, http.StatusBadRequest},
				{"empty body", ``, nil, http.StatusBadRequest},
				{"unknown field", `{"zzz":1}`, nil, http.StatusBadRequest},
				{"trailing data", good + `{}`, nil, http.StatusBadRequest},
				{"oversized", `{"name":"` + strings.Repeat("a", maxBodyBytes) + `"}`, nil, http.StatusRequestEntityTooLarge},
			}
			for _, tc := range cases {
				rec := f.post(t, target, tc.body, tc.mut)
				if rec.Code != tc.want {
					t.Errorf("%s: code = %d, want %d\n%s", tc.name, rec.Code, tc.want, rec.Body.String())
				}
			}
			if len(l.calls) != 0 {
				t.Errorf("a rejected request reached the launcher: %v", l.calls)
			}
			// The raw launch token is accepted as CSRF too (scripted callers),
			// as is the charset-qualified JSON content type.
			rec := f.post(t, target, good, func(r *http.Request) {
				r.Header.Set(CSRFHeader, f.token)
				r.Header.Set("Content-Type", "application/json; charset=utf-8")
			})
			if rec.Code != http.StatusOK {
				t.Errorf("raw-token CSRF: code = %d\n%s", rec.Code, rec.Body.String())
			}
			if rec := f.do(t, "GET", target, func(r *http.Request) { r.AddCookie(f.cookie()) }); target != "/api/accounts" && rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("GET on POST-only %s = %d, want 405", target, rec.Code)
			}
		})
	}
}

func TestCSRFEndpointNeverEchoesToken(t *testing.T) {
	f := newFixture(t, okSync)
	if rec := f.do(t, "GET", "/api/csrf", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /api/csrf = %d", rec.Code)
	}
	rec := f.do(t, "GET", "/api/csrf", func(r *http.Request) { r.AddCookie(f.cookie()) })
	var body map[string]string
	decode(t, rec, &body)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(body["csrf"]) || body["csrf"] == f.token {
		t.Errorf("csrf = %q (must be a derived value, not the token)", body["csrf"])
	}
	if strings.Contains(rec.Body.String(), f.token) {
		t.Error("launch token appears in the /api/csrf body")
	}
	// Derived values differ per launch token.
	other, _ := NewToken()
	if deriveCSRF(other) == body["csrf"] {
		t.Error("derived CSRF does not depend on the token")
	}
}

func TestStateAndInit(t *testing.T) {
	f, _ := actionFixture(t, okSync)
	// A fresh cwm home: nothing initialized yet.
	f.srv.opts.Layout = storage.NewLayoutAt(filepath.Join(f.root, "fresh"))
	var st map[string]any
	getJSON(t, f, "/api/state", 200, &st)
	if st["initialized"] != false || st["paired"] != float64(1) || st["deviceID"] != testMyID || st["syncthing"] != true {
		t.Fatalf("state before init = %v", st)
	}
	if acc, ok := st["accounts"].([]any); !ok || len(acc) != 0 {
		t.Errorf("accounts must be [] before init: %v", st["accounts"])
	}

	for i := 0; i < 2; i++ { // idempotent
		rec := f.post(t, "/api/init", `{}`, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("init #%d = %d\n%s", i+1, rec.Code, rec.Body.String())
		}
		var res struct {
			Initialized bool             `json:"initialized"`
			Checks      []map[string]any `json:"checks"`
		}
		decode(t, rec, &res)
		if !res.Initialized || len(res.Checks) < 5 {
			t.Fatalf("init #%d result = %+v", i+1, res)
		}
	}
	getJSON(t, f, "/api/state", 200, &st)
	if st["initialized"] != true {
		t.Errorf("state after init = %v", st)
	}
	if _, err := os.Stat(filepath.Join(f.root, "fresh", "config.toml")); err != nil {
		t.Errorf("init did not write config.toml: %v", err)
	}
	// Init never takes options from the client (no migrate, no account).
	if rec := f.post(t, "/api/init", `{"migrate":true}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("init with migrate = %d, want 400", rec.Code)
	}
}

func TestStateWithoutSyncthing(t *testing.T) {
	f, _ := actionFixture(t, func() (syncengine.Client, error) { return nil, errors.New("not reachable") })
	rec := f.do(t, "GET", "/api/state", func(r *http.Request) { r.AddCookie(f.cookie()) })
	if rec.Code != 200 {
		t.Fatalf("state = %d", rec.Code)
	}
	var st map[string]any
	decode(t, rec, &st)
	if _, has := st["deviceID"]; has || st["syncthing"] != false || st["paired"] != float64(0) {
		t.Errorf("state without syncthing = %v", st)
	}
}

// seedAccount registers an account with an identity and a credentials
// file whose content must never leave the machine through the API.
func seedAccount(t *testing.T, root, name, email string) string {
	t.Helper()
	l := storage.NewLayoutAt(root)
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(l.AccountsDir(), name)
	writeFile(t, filepath.Join(dir, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"sk-ant-SECRET-`+name+`"}}`)
	if err := cfg.AddAccount(name, dir); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetAccountLogin(name, config.Login{Email: email, Plan: "max", AccountUUID: "uuid-" + name}); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(l.ConfigPath(), cfg); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAccountsIdentityOnly(t *testing.T) {
	f, _ := actionFixture(t, okSync)
	dir := seedAccount(t, f.root, "work", "me@example.com")
	var list []map[string]any
	getJSON(t, f, "/api/accounts", 200, &list)
	if len(list) != 1 || list[0]["name"] != "work" || list[0]["email"] != "me@example.com" || list[0]["default"] != true {
		t.Fatalf("accounts = %v", list)
	}
	for _, target := range []string{"/api/accounts", "/api/state"} {
		rec := f.do(t, "GET", target, func(r *http.Request) { r.AddCookie(f.cookie()) })
		body := rec.Body.String()
		for _, leak := range []string{"SECRET", "accessToken", dir, "uuid-work", f.token} {
			if strings.Contains(body, leak) {
				t.Errorf("%s leaks %q: %s", target, leak, body)
			}
		}
	}
}

func TestAccountAddLaunchesTerminal(t *testing.T) {
	f, l := actionFixture(t, okSync)
	rec := f.post(t, "/api/accounts", `{"name":"work-2"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("add = %d\n%s", rec.Code, rec.Body.String())
	}
	if len(l.calls) != 1 || strings.Join(l.calls[0], "|") != fakeCwm+"|--hold-on-error|account|add|work-2" {
		t.Fatalf("launcher argv = %q", l.calls)
	}
	var res launchResult
	decode(t, rec, &res)
	if !res.Launched || res.Command != fakeCwm+" account add work-2" {
		t.Errorf("result = %+v", res)
	}

	bad := []string{"", "../x", "..", "Work", "-x", "a_b", "a b", "a;rm -rf ~", "$(id)", "a`id`", "a|b", "a&b",
		"a\nb", "a/b", `a\b`, "123", strings.Repeat("a", 33), "ü"}
	for _, name := range bad {
		body, _ := json.Marshal(map[string]string{"name": name})
		if rec := f.post(t, "/api/accounts", string(body), nil); rec.Code != http.StatusBadRequest {
			t.Errorf("name %q: code = %d, want 400", name, rec.Code)
		}
	}
	if rec := f.post(t, "/api/accounts", `{"name":7}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("numeric name: %d", rec.Code)
	}
	if len(l.calls) != 1 {
		t.Errorf("invalid names reached the launcher: %q", l.calls[1:])
	}

	seedAccount(t, f.root, "taken", "t@example.com")
	if rec := f.post(t, "/api/accounts", `{"name":"taken"}`, nil); rec.Code != http.StatusConflict {
		t.Errorf("existing account: %d, want 409", rec.Code)
	}
}

func TestLaunchNoTerminal501(t *testing.T) {
	f, l := actionFixture(t, okSync)
	writeFile(t, filepath.Join(f.root, "config.toml"), "mode = \"isolated\"\n")
	l.err = fmt.Errorf("term: %w (headless)", term.ErrNoTerminal)
	cases := map[string]string{
		"/api/accounts":        fakeCwm + " account add work",
		"/api/sessions/resume": fakeCwm + " open alpha new --resume 11111111-aaaa",
	}
	for target, wantCmd := range cases {
		rec := f.post(t, target, postEndpoints[target], nil)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s: code = %d, want 501\n%s", target, rec.Code, rec.Body.String())
		}
		var e apiError
		decode(t, rec, &e)
		if e.Error != "no terminal found" || e.Command != wantCmd {
			t.Errorf("%s: 501 body = %+v, want command %q", target, e, wantCmd)
		}
	}

	// No launcher configured at all → the same 501.
	f.srv.opts.Launcher = nil
	if rec := f.post(t, "/api/accounts", `{"name":"work"}`, nil); rec.Code != http.StatusNotImplemented {
		t.Errorf("nil launcher: %d", rec.Code)
	}

	// Any other failure is a 500 that still carries the command.
	f.srv.opts.Launcher = &fakeLauncher{err: errors.New("exec format error")}
	rec := f.post(t, "/api/accounts", `{"name":"work"}`, nil)
	var e apiError
	decode(t, rec, &e)
	if rec.Code != http.StatusInternalServerError || e.Command == "" || !strings.Contains(e.Error, "exec format error") {
		t.Errorf("launch failure: %d %+v", rec.Code, e)
	}
}

func TestResumeLooksUpSession(t *testing.T) {
	f, l := actionFixture(t, okSync)
	// Isolated mode: pinned folders, the D20 passthrough (capture mode is
	// TestResumeCaptureModeFollowsTheSession).
	writeFile(t, filepath.Join(f.root, "config.toml"), "mode = \"isolated\"\n")
	ws := filepath.Join(f.root, "workspace")
	writeFile(t, filepath.Join(ws, "projects", "alpha", "agent-12345678.jsonl"), "{}\n")
	writeFile(t, filepath.Join(ws, "projects", "beta", "22222222-bbbb.jsonl"), "{}\n")
	writeFile(t, filepath.Join(ws, "projects", "gamma", "33333333-cccc.jsonl"), "{}\n") // not in the manifest

	rec := f.post(t, "/api/sessions/resume", `{"project":"alpha","id":"11111111-aaaa"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resume = %d\n%s", rec.Code, rec.Body.String())
	}
	// The D20 passthrough, exactly as `cwm sessions --pick` launches it,
	// behind the global hold flag (D49) that keeps a failed window open.
	want := fakeCwm + "|--hold-on-error|open|alpha|new|--resume|11111111-aaaa"
	if len(l.calls) != 1 || strings.Join(l.calls[0], "|") != want {
		t.Fatalf("launcher argv = %q, want %s", l.calls, want)
	}

	cases := []struct {
		body string
		want int
	}{
		{`{"project":"alpha","id":"99999999-ffff"}`, http.StatusNotFound},  // unknown id
		{`{"project":"alpha","id":"22222222-bbbb"}`, http.StatusNotFound},  // id of another project
		{`{"project":"alpha","id":"agent-12345678"}`, http.StatusNotFound}, // subagents are not sessions
		{`{"project":"gamma","id":"33333333-cccc"}`, http.StatusNotFound},  // not in the manifest
		{`{"project":"nope","id":"11111111-aaaa"}`, http.StatusNotFound},   // unknown project
		{`{"project":"../workspace/projects/alpha","id":"11111111-aaaa"}`, http.StatusBadRequest},
		{`{"project":"alpha/..","id":"11111111-aaaa"}`, http.StatusBadRequest},
		{`{"project":"..","id":"11111111-aaaa"}`, http.StatusBadRequest},
		{`{"project":".alpha","id":"11111111-aaaa"}`, http.StatusBadRequest},
		{`{"project":"--help","id":"11111111-aaaa"}`, http.StatusBadRequest},
		{`{"project":"alpha;id","id":"11111111-aaaa"}`, http.StatusBadRequest},
		{`{"project":"$(reboot)","id":"11111111-aaaa"}`, http.StatusBadRequest},
		{`{"project":"gui","id":"11111111-aaaa"}`, http.StatusBadRequest}, // reserved
		{`{"project":"","id":"11111111-aaaa"}`, http.StatusBadRequest},
		{`{"project":"alpha","id":""}`, http.StatusBadRequest},
		{`{"project":"alpha","id":"../../../etc/passwd"}`, http.StatusBadRequest},
		{`{"project":"alpha","id":"11111111-aaaa; rm -rf ~"}`, http.StatusBadRequest},
		{`{"project":"alpha","id":"--dangerously-skip-permissions"}`, http.StatusBadRequest},
		{`{"project":"alpha","id":"x y"}`, http.StatusBadRequest},
		{`{"project":"alpha"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		if rec := f.post(t, "/api/sessions/resume", tc.body, nil); rec.Code != tc.want {
			t.Errorf("%s: code = %d, want %d\n%s", tc.body, rec.Code, tc.want, rec.Body.String())
		}
	}
	if len(l.calls) != 1 {
		t.Errorf("rejected resumes reached the launcher: %q", l.calls[1:])
	}

	if err := os.RemoveAll(ws); err != nil {
		t.Fatal(err)
	}
	if rec := f.post(t, "/api/sessions/resume", postEndpoints["/api/sessions/resume"], nil); rec.Code != http.StatusConflict {
		t.Errorf("resume without workspace = %d, want 409", rec.Code)
	}
}

func TestPairEndpoint(t *testing.T) {
	ps := newPairSync()
	f, l := actionFixture(t, func() (syncengine.Client, error) { return ps, nil })
	rec := f.post(t, "/api/pair", `{"deviceID":"`+testPeerID+`","name":"Work laptop"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("pair = %d\n%s", rec.Code, rec.Body.String())
	}
	var res map[string]any
	decode(t, rec, &res)
	if res["deviceID"] != testMyID || res["peerID"] != testPeerID || res["changed"] != true ||
		res["addedDevice"] != true || res["sharedFolder"] != true {
		t.Errorf("pair result = %v", res)
	}
	if ps.puts != 1 || len(ps.cfg.Devices) != 2 || ps.cfg.Devices[1].Name != "Work laptop" || len(ps.cfg.Folders) != 1 {
		t.Errorf("syncthing config after pair = %+v (puts %d)", ps.cfg, ps.puts)
	}
	// Second run: nothing to do.
	rec = f.post(t, "/api/pair", `{"deviceID":"`+testPeerID+`"}`, nil)
	decode(t, rec, &res)
	if rec.Code != 200 || res["changed"] != false || ps.puts != 1 {
		t.Errorf("re-pair = %d %v puts %d", rec.Code, res, ps.puts)
	}

	bad := []string{
		`{"deviceID":"garbage"}`,
		`{"deviceID":""}`,
		`{"deviceID":"` + strings.ToLower(testPeerID) + `"}`,
		`{"deviceID":"` + testPeerID[:7] + `-` + testPeerID[7:] + `"}`,
		`{"deviceID":" ` + testPeerID + `"}`,
		`{"deviceID":"` + testPeerID[:50] + `;rm -rf ~"}`,
		`{"deviceID":"` + testMyID + `"}`, // this device
		`{"deviceID":"` + testPeerID + `","name":"../x"}`,
		`{"deviceID":"` + testPeerID + `","name":"a;b"}`,
		`{"deviceID":"` + testPeerID + `","name":"$(id)"}`,
		`{"deviceID":"` + testPeerID + `","name":"` + strings.Repeat("n", 65) + `"}`,
		`{"deviceID":"` + testPeerID + `","name":" lead"}`,
	}
	for _, body := range bad {
		if rec := f.post(t, "/api/pair", body, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400\n%s", body, rec.Code, rec.Body.String())
		}
	}
	if ps.puts != 1 || len(l.calls) != 0 {
		t.Errorf("invalid pairs changed state: puts %d, launches %v", ps.puts, l.calls)
	}
}

func TestPairEndpointUnavailable(t *testing.T) {
	f, _ := actionFixture(t, func() (syncengine.Client, error) {
		return nil, errors.New("syncthing API not reachable at http://127.0.0.1:1")
	})
	rec := f.post(t, "/api/pair", postEndpoints["/api/pair"], nil)
	var e apiError
	decode(t, rec, &e)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(e.Error, "not reachable") {
		t.Errorf("unreachable: %d %+v", rec.Code, e)
	}
	f.srv.opts.SyncClient = nil
	if rec := f.post(t, "/api/pair", postEndpoints["/api/pair"], nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no sync seam: %d", rec.Code)
	}
	if err := os.RemoveAll(filepath.Join(f.root, "workspace")); err != nil {
		t.Fatal(err)
	}
	if rec := f.post(t, "/api/pair", postEndpoints["/api/pair"], nil); rec.Code != http.StatusConflict {
		t.Errorf("no workspace: %d, want 409", rec.Code)
	}
}

func TestDashboardHasActions(t *testing.T) {
	html, _ := webFS.ReadFile("web/index.html")
	js, _ := webFS.ReadFile("web/app.js")
	for _, want := range []string{`id="wizard"`, `id="pair-form"`, `id="pair-device-id"`} {
		if !bytes.Contains(html, []byte(want)) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	for _, want := range []string{`data-debug-copy`, `id="debug-dialog"`, `id="debug-text"`, `id="debug-select"`, bugReportURL} {
		if !bytes.Contains(html, []byte(want)) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	if n := bytes.Count(html, []byte("data-debug-copy")); n < 2 {
		t.Errorf("Copy debug logs must be in the header/footer and on the Health tab, found %d", n)
	}
	for _, want := range []string{"/api/debug", "navigator.clipboard.writeText", `execCommand("copy")`, "Debug logs copied (", "pathHint", "424"} {
		if !bytes.Contains(js, []byte(want)) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	for _, want := range []string{"/api/state", "/api/init", "/api/accounts", "/api/sessions/resume", "/api/pair", "/api/csrf", CSRFHeader, `"resume"`} {
		if !bytes.Contains(js, []byte(want)) {
			t.Errorf("app.js lacks %s", want)
		}
	}
}

// D49: with no claude anywhere, Add account answers 424 with the install
// command instead of opening a terminal that fails and closes.
func TestAccountAddWithoutClaude424(t *testing.T) {
	f, l := actionFixture(t, okSync)
	f.srv.opts.FindClaude = func() (claudebin.Result, error) {
		return claudebin.Result{}, &claudebin.NotFoundError{GOOS: "windows"}
	}
	f.srv.opts.GOOS = "windows"
	rec := f.post(t, "/api/accounts", `{"name":"work"}`, nil)
	if rec.Code != http.StatusFailedDependency {
		t.Fatalf("code = %d, want 424\n%s", rec.Code, rec.Body.String())
	}
	var e apiError
	decode(t, rec, &e)
	if e.Command != "irm https://claude.ai/install.ps1 | iex" || !strings.Contains(e.Error, "not installed") {
		t.Errorf("424 body = %+v", e)
	}
	if len(l.calls) != 0 {
		t.Errorf("no terminal may open without claude: %q", l.calls)
	}
}

func TestStateReportsClaude(t *testing.T) {
	f, _ := actionFixture(t, okSync)
	calls := 0
	f.srv.opts.FindClaude = func() (claudebin.Result, error) {
		calls++
		return claudebin.Result{Path: "/home/u/.local/bin/claude", Source: claudebin.SourceDiscovered}, nil
	}
	var st struct {
		Claude struct {
			Found    bool   `json:"found"`
			Path     string `json:"path"`
			OnPath   bool   `json:"onPath"`
			PathHint string `json:"pathHint"`
			Install  string `json:"install"`
		} `json:"claude"`
	}
	getJSON(t, f, "/api/state", 200, &st)
	c := st.Claude
	if !c.Found || c.Path != "/home/u/.local/bin/claude" || c.OnPath ||
		c.PathHint != claudebin.PathHint("linux", filepath.Dir("/home/u/.local/bin/claude")) || c.Install == "" {
		t.Errorf("claude state = %+v", c)
	}
	getJSON(t, f, "/api/state", 200, &st)
	if calls != 1 {
		t.Errorf("resolver ran %d times for two polls, want 1 (cached)", calls)
	}
	// The precheck never trusts the cache.
	f.post(t, "/api/accounts", `{"name":"work"}`, nil)
	if calls != 2 {
		t.Errorf("precheck must re-resolve: calls = %d", calls)
	}
}

// Capture mode: the project is Claude Code's path-derived folder (it may
// start with "-", never reaches the argv) and the resume is `cwm sessions
// resume <id>`, which follows the session to this machine's clone (D91).
func TestResumeCaptureModeFollowsTheSession(t *testing.T) {
	f, l := actionFixture(t, okSync)
	writeFile(t, filepath.Join(f.root, "config.toml"), "mode = \"capture\"\n")
	ws := filepath.Join(f.root, "workspace")
	writeFile(t, filepath.Join(ws, "projects", "-home-a-projects-api", "44444444-dddd.jsonl"), "{}\n")
	rec := f.post(t, "/api/sessions/resume", `{"project":"-home-a-projects-api","id":"44444444-dddd"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resume = %d\n%s", rec.Code, rec.Body.String())
	}
	want := fakeCwm + "|--hold-on-error|sessions|resume|44444444-dddd"
	if len(l.calls) != 1 || strings.Join(l.calls[0], "|") != want {
		t.Fatalf("launcher argv = %q, want %s", l.calls, want)
	}
	for _, body := range []string{
		`{"project":"../x","id":"44444444-dddd"}`,
		`{"project":"-home-a-projects-api","id":"99999999-ffff"}`,
	} {
		if rec := f.post(t, "/api/sessions/resume", body, nil); rec.Code == http.StatusOK {
			t.Errorf("%s: accepted", body)
		}
	}
}
