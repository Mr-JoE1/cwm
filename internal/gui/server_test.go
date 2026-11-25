package gui

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/status"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
	"github.com/Mr-JoE1/cwm/internal/syncthing"
)

const testPort = 47805

var (
	testMyID   = strings.Repeat("A2B3C4D7EFGHJKLM", 3) + "NPQR"
	testPeerID = strings.Repeat("P7Q2R3S4T7UVWXYZ", 3) + "AB2C"
)

// fakeSync is a minimal in-memory syncengine.Client.
type fakeSync struct{}

func (fakeSync) Ping() error { return nil }
func (fakeSync) Status() (*syncthing.SystemStatus, error) {
	return &syncthing.SystemStatus{MyID: testMyID}, nil
}
func (fakeSync) GetConfig() (*syncthing.Config, error) {
	return &syncthing.Config{Devices: []syncthing.Device{
		{DeviceID: testMyID, Name: "self"},
		{DeviceID: testPeerID, Name: "laptop"},
	}}, nil
}
func (fakeSync) PutConfig(*syncthing.Config) error { return errors.New("read-only fake") }
func (fakeSync) Connections() (*syncthing.Connections, error) {
	return &syncthing.Connections{Connections: map[string]syncthing.DeviceConnection{
		testPeerID: {Connected: true, Address: "192.168.1.21:22000", InBytesTotal: 2048, OutBytesTotal: 4096},
	}}, nil
}
func (fakeSync) Completion(string, string) (*syncthing.Completion, error) {
	return &syncthing.Completion{Completion: 94.5}, nil
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	srv   *Server
	token string
	root  string
}

func newFixture(t *testing.T, sync func() (syncengine.Client, error)) *fixture {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CWM_HOME", root)    // keep every cwm path inside the sandbox
	t.Setenv("PATH", t.TempDir()) // never a real claude/ssh/git (doctor runs per request)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv(syncthing.EnvURL, "http://127.0.0.1:1")
	t.Setenv(syncthing.EnvAPIKey, "test")
	ws := filepath.Join(root, "workspace")
	writeFile(t, filepath.Join(ws, "projects", "alpha", "11111111-aaaa.jsonl"),
		`{"type":"user","message":{"content":"hello <script>alert(1)</script>"}}`+"\n")
	writeFile(t, filepath.Join(ws, "projects", "beta", "notes.txt"), "not a session\n")
	token, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{
		Version:    status.Version{Version: "v0.5.0-test", Commit: "abc123", OS: "linux", Arch: "amd64"},
		Layout:     storage.NewLayoutAt(root),
		ReposRoot:  filepath.Join(root, "repos-absent"),
		SyncClient: sync,
	}, testPort, token)
	if err != nil {
		t.Fatal(err)
	}
	waitStats(t, srv)
	return &fixture{srv: srv, token: token, root: root}
}

func okSync() (syncengine.Client, error) { return fakeSync{}, nil }

// waitStats makes the test's cleanup wait for a background stats scan:
// it writes <cwm home>/cache/stats.json, and the temp dir's removal must
// not race it (on Windows an open file even blocks the delete). Cleanups
// run last-in first-out, so this runs before the TempDir removal.
func waitStats(t *testing.T, srv *Server) {
	t.Cleanup(func() { srv.statsEngine().Wait() })
}

// do sends one request through the handler with a valid Host unless the
// caller overrides it.
func (f *fixture) do(t *testing.T, method, target string, mut func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Host = "127.0.0.1:47805"
	if mut != nil {
		mut(req)
	}
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)
	return rec
}

func (f *fixture) cookie() *http.Cookie {
	return &http.Cookie{Name: f.srv.CookieName(), Value: f.token}
}

func TestNewToken(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewToken()
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(a) {
		t.Errorf("token %q is not 32 bytes of hex", a)
	}
	if a == b {
		t.Error("two tokens are identical")
	}
	if _, err := New(Options{Layout: storage.NewLayoutAt(t.TempDir())}, testPort, "short"); err == nil {
		t.Error("New accepted a short token")
	}
}

func TestAuthMatrix(t *testing.T) {
	f := newFixture(t, okSync)
	bad := strings.Repeat("0", 64)
	cases := []struct {
		name   string
		method string
		target string
		mut    func(*http.Request)
		want   int
	}{
		{"no token", "GET", "/api/version", nil, http.StatusUnauthorized},
		{"bad bearer", "GET", "/api/version", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+bad) }, http.StatusUnauthorized},
		{"bad cookie", "GET", "/api/version", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: f.srv.CookieName(), Value: bad}) }, http.StatusUnauthorized},
		{"cookie for another port", "GET", "/api/version", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "cwm_gui_47800", Value: f.token}) }, http.StatusUnauthorized},
		{"token in query is not api auth", "GET", "/api/version?t=" + f.token, nil, http.StatusUnauthorized},
		{"basic auth scheme", "GET", "/api/version", func(r *http.Request) { r.Header.Set("Authorization", "Basic "+f.token) }, http.StatusUnauthorized},
		{"cookie ok", "GET", "/api/version", func(r *http.Request) { r.AddCookie(f.cookie()) }, http.StatusOK},
		{"bearer ok", "GET", "/api/version", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.token) }, http.StatusOK},
		{"bad host", "GET", "/api/version", func(r *http.Request) {
			r.Host = "evil.com"
			r.Header.Set("Authorization", "Bearer "+f.token)
		}, http.StatusForbidden},
		{"rebinding host with right port", "GET", "/api/version", func(r *http.Request) {
			r.Host = "evil.com:47805"
			r.Header.Set("Authorization", "Bearer "+f.token)
		}, http.StatusForbidden},
		{"loopback host wrong port", "GET", "/api/version", func(r *http.Request) {
			r.Host = "127.0.0.1:47806"
			r.AddCookie(f.cookie())
		}, http.StatusForbidden},
		{"host without port", "GET", "/api/version", func(r *http.Request) {
			r.Host = "localhost"
			r.AddCookie(f.cookie())
		}, http.StatusForbidden},
		{"localhost host ok", "GET", "/api/version", func(r *http.Request) {
			r.Host = "localhost:47805"
			r.AddCookie(f.cookie())
		}, http.StatusOK},
		{"bad host on static", "GET", "/", func(r *http.Request) { r.Host = "evil.com" }, http.StatusForbidden},
		{"post without auth", "POST", "/api/version", nil, http.StatusUnauthorized},
		{"post without csrf", "POST", "/api/version", func(r *http.Request) { r.AddCookie(f.cookie()) }, http.StatusForbidden},
		{"post bearer without csrf", "POST", "/api/version", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.token) }, http.StatusForbidden},
		{"post bad csrf", "POST", "/api/version", func(r *http.Request) {
			r.AddCookie(f.cookie())
			r.Header.Set(CSRFHeader, bad)
		}, http.StatusForbidden},
		{"post with csrf on read-only endpoint", "POST", "/api/version", func(r *http.Request) {
			r.AddCookie(f.cookie())
			r.Header.Set(CSRFHeader, f.token)
		}, http.StatusMethodNotAllowed},
		{"put is not allowed", "PUT", "/api/version", func(r *http.Request) {
			r.AddCookie(f.cookie())
			r.Header.Set(CSRFHeader, f.token)
		}, http.StatusMethodNotAllowed},
		{"unknown endpoint authed", "GET", "/api/nope", func(r *http.Request) { r.AddCookie(f.cookie()) }, http.StatusNotFound},
		{"unknown endpoint unauthed", "GET", "/api/nope", nil, http.StatusUnauthorized},
		{"static post", "POST", "/", nil, http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(t, tc.method, tc.target, tc.mut)
			if rec.Code != tc.want {
				t.Fatalf("%s %s: code = %d, want %d\n%s", tc.method, tc.target, rec.Code, tc.want, rec.Body.String())
			}
			// Past the Host gate, every /api/* answer (errors included) is JSON.
			if strings.HasPrefix(tc.target, "/api/") && !strings.Contains(tc.name, "host") {
				if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
					t.Errorf("api response content-type = %q, want application/json", ct)
				}
			}
		})
	}
}

func TestTokenExchangeSetsCookieAndRedirects(t *testing.T) {
	f := newFixture(t, okSync)
	rec := f.do(t, "GET", "/?t="+f.token, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want / (token stripped)", loc)
	}
	res := rec.Result()
	var c *http.Cookie
	for _, ck := range res.Cookies() {
		if ck.Name == f.srv.CookieName() {
			c = ck
		}
	}
	if c == nil {
		t.Fatalf("no %s cookie set; headers: %v", f.srv.CookieName(), rec.Header())
	}
	if c.Value != f.token || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Errorf("cookie = %+v, want token value, HttpOnly, SameSite=Strict, Path=/", c)
	}

	// The cookie then authenticates the API.
	rec = f.do(t, "GET", "/api/version", func(r *http.Request) { r.AddCookie(c) })
	if rec.Code != http.StatusOK {
		t.Fatalf("cookie from exchange rejected: %d", rec.Code)
	}

	// A wrong token never sets a cookie.
	rec = f.do(t, "GET", "/?t="+strings.Repeat("f", 64), nil)
	if rec.Code != http.StatusUnauthorized || len(rec.Result().Cookies()) != 0 {
		t.Errorf("bad token exchange: code = %d cookies = %v, want 401 and none", rec.Code, rec.Result().Cookies())
	}
}

func TestSecurityHeaders(t *testing.T) {
	f := newFixture(t, okSync)
	for _, target := range []string{"/", "/api/version"} {
		rec := f.do(t, "GET", target, func(r *http.Request) { r.AddCookie(f.cookie()) })
		h := rec.Header()
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("X-Frame-Options") != "DENY" ||
			h.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(h.Get("Content-Security-Policy"), "script-src 'self'") {
			t.Errorf("%s: missing security headers: %v", target, h)
		}
	}
	rec := f.do(t, "GET", "/api/version", func(r *http.Request) { r.AddCookie(f.cookie()) })
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("api responses must be no-store")
	}
}

const bugReportURL = "https://github.com/Mr-JoE1/cwm/issues/new?template=bug_report.yml"

func TestStaticDashboard(t *testing.T) {
	f := newFixture(t, okSync)
	rec := f.do(t, "GET", "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rec.Code)
	}
	body := rec.Body.String()
	// v0.7.2 (D67, D70): a tmux window list, "<k>:<name>"; WIP is "handoff".
	for i, tab := range []string{"sessions", "projects", "devices", "handoff", "health", "accounts", "stats"} {
		k := strconv.Itoa(i + 1)
		if !strings.Contains(body, `role="tab"`) || !strings.Contains(body, `<span class="tk" aria-hidden="true">`+k+`:</span>`+tab+`<`) {
			t.Errorf("dashboard missing tab %s:%s", k, tab)
		}
	}
	for _, gone := range []string{"Ctrl-C", ">WIP<", "bug-icon"} {
		if strings.Contains(body, gone) {
			t.Errorf("index.html still contains %q", gone)
		}
	}
	for _, want := range []string{`class="statusline"`, `id="palette"`, `class="tmux-prefix"`, `~/cwm/</span>sessions</h1>`} {
		if !strings.Contains(body, want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	for _, asset := range []string{"/app.js", "/app.css", "/theme.js", "/bg.js", "/favicon.svg", "/favicon-16.svg"} {
		if rec := f.do(t, "GET", asset, nil); rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Errorf("GET %s = %d (%d bytes)", asset, rec.Code, rec.Body.Len())
		}
		if !strings.Contains(body, `"`+asset+`"`) {
			t.Errorf("index.html does not reference %s", asset)
		}
	}
	// v0.8: eight tabs, shortcuts 1-8 (D65, D95).
	if n := strings.Count(body, `role="tab"`); n != 8 || !strings.Contains(body, `aria-keyshortcuts="8"`) || !strings.Contains(body, "<kbd>1</kbd>–<kbd>8</kbd>") {
		t.Errorf("dashboard has %d tabs (want 8 with keys 1-8 in the help)", n)
	}
	// The 16 px favicon is its own hand-tuned file (D66).
	if !strings.Contains(body, `href="/favicon-16.svg" type="image/svg+xml" sizes="16x16"`) || !strings.Contains(body, `href="/favicon.svg" type="image/svg+xml" sizes="any"`) {
		t.Error("index.html must link favicon-16.svg (16x16) and favicon.svg (any)")
	}
	if rec := f.do(t, "GET", "/favicon.svg", nil); !strings.HasPrefix(rec.Header().Get("Content-Type"), "image/svg+xml") {
		t.Errorf("favicon.svg Content-Type = %q", rec.Header().Get("Content-Type"))
	}
	// Every embedded asset (v0.7: theme.js, bg.js, favicon.svg too): no
	// external CDN / remote resources, no HTML-parsing sinks in scripts,
	// and the whole UI stays under its D71 budget: 200KB raw, < 60KB gzip.
	// Every absolute URL is either the SVG namespace or a GitHub link of
	// this project (opened by the user with target=_blank, never loaded).
	external := regexp.MustCompile(`(?i)(src|href)\s*=\s*["']?(https?:)?//|url\(\s*["']?(https?:)?//`)
	sinks := regexp.MustCompile(`innerHTML|outerHTML|insertAdjacentHTML|document\.write|eval\(|new Function`)
	anyURL := regexp.MustCompile(`https?://[^\s"'<>)]*`)
	total, gz := 0, 0
	err := fs.WalkDir(webFS, "web", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := webFS.ReadFile(name)
		if err != nil {
			return err
		}
		total += len(data)
		var zb bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&zb, gzip.BestCompression)
		_, _ = zw.Write(data)
		_ = zw.Close()
		gz += zb.Len()
		for _, u := range anyURL.FindAllString(string(data), -1) {
			if u != "http://www.w3.org/2000/svg" && !strings.HasPrefix(u, "https://github.com/Mr-JoE1") {
				t.Errorf("%s contains a foreign URL %q", name, u)
			}
		}
		// One navigation link is allowed (D52): the bug-report form,
		// opened by the user's click, never loaded by the page.
		// v0.7.2: the About view adds the release notes and the profile.
		nav := regexp.MustCompile(`href="https://github\.com/Mr-JoE1/[^"]*" target="_blank" rel="noopener noreferrer"`)
		if external.Match(nav.ReplaceAll(data, nil)) || strings.Contains(string(data), "@import") {
			t.Errorf("%s references an external resource", name)
		}
		if strings.HasSuffix(name, ".js") && sinks.Match(data) {
			t.Errorf("%s uses an HTML-parsing sink (%s); build DOM nodes and use textContent", name, sinks.Find(data))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if total > 200<<10 {
		t.Errorf("embedded web assets = %d bytes, budget is 200KB (D71)", total)
	}
	if gz >= 60<<10 {
		t.Errorf("embedded web assets = %d bytes gzipped, budget is < 60KB (D71)", gz)
	}
	// The pre-paint theme script must be a blocking <head> script (no
	// defer/async) so the first paint already has the right theme; inline
	// scripts are impossible under the CSP.
	if !strings.Contains(body, `<script src="/theme.js"></script>`) || strings.Index(body, "/theme.js") > strings.Index(body, "</head>") {
		t.Error("theme.js must be a synchronous script in <head>")
	}
	if regexp.MustCompile(`<script>|\sstyle\s*=`).MatchString(body) {
		t.Error("index.html has an inline script or style attribute (blocked by the CSP)")
	}
}

func getJSON(t *testing.T, f *fixture, target string, wantCode int, v any) {
	t.Helper()
	rec := f.do(t, "GET", target, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.token) })
	if rec.Code != wantCode {
		t.Fatalf("GET %s = %d, want %d\n%s", target, rec.Code, wantCode, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("GET %s: invalid JSON: %v\n%s", target, err, rec.Body.String())
	}
}

func TestAPIShapes(t *testing.T) {
	f := newFixture(t, okSync)

	var ver map[string]string
	getJSON(t, f, "/api/version", 200, &ver)
	if ver["version"] != "v0.5.0-test" || ver["commit"] != "abc123" || ver["os"] == "" || ver["arch"] == "" {
		t.Errorf("version = %v", ver)
	}

	var projects []map[string]any
	getJSON(t, f, "/api/projects", 200, &projects)
	if len(projects) != 2 || projects[0]["name"] != "alpha" || projects[0]["sessions"] != float64(1) {
		t.Fatalf("projects = %v", projects)
	}
	if _, ok := projects[0]["modified"].(string); !ok {
		t.Errorf("alpha.modified missing: %v", projects[0])
	}
	if _, ok := projects[1]["modified"]; ok {
		t.Errorf("beta has no sessions; modified must be omitted: %v", projects[1])
	}

	var sess []map[string]any
	getJSON(t, f, "/api/sessions", 200, &sess)
	if len(sess) != 1 {
		t.Fatalf("sessions = %v", sess)
	}
	for _, k := range []string{"project", "id", "firstPrompt", "summary", "messages", "branch", "modified"} {
		if _, ok := sess[0][k]; !ok {
			t.Errorf("session missing key %q: %v", k, sess[0])
		}
	}
	if sess[0]["firstPrompt"] != "hello <script>alert(1)</script>" {
		t.Errorf("firstPrompt = %v (raw text must round-trip; the UI escapes)", sess[0]["firstPrompt"])
	}
	getJSON(t, f, "/api/sessions?project=nope", 200, &sess)
	if len(sess) != 0 {
		t.Errorf("filtered sessions = %v, want []", sess)
	}
	var apiErr map[string]string
	getJSON(t, f, "/api/sessions?limit=x", 400, &apiErr)
	if apiErr["error"] == "" {
		t.Errorf("bad limit: error body = %v", apiErr)
	}

	var devices []map[string]any
	getJSON(t, f, "/api/devices", 200, &devices)
	if len(devices) != 1 || devices[0]["id"] != testPeerID || devices[0]["name"] != "laptop" ||
		devices[0]["connected"] != true || devices[0]["completion"] != 94.5 || devices[0]["inBytes"] != float64(2048) {
		t.Errorf("devices = %v", devices)
	}

	var wipRows []map[string]any
	getJSON(t, f, "/api/wip", 200, &wipRows)
	if wipRows == nil || len(wipRows) != 0 {
		t.Errorf("wip = %v, want [] (absent repos root)", wipRows)
	}

	var checks []map[string]any
	getJSON(t, f, "/api/doctor", 200, &checks)
	// 9 on Windows: the shell-profile check is unix-only (v0.6.2, D58).
	if len(checks) < 9 {
		t.Fatalf("doctor checks = %v", checks)
	}
	names := map[string]bool{}
	for _, c := range checks {
		names[c["name"].(string)] = true
		switch c["status"] {
		case "ok", "warn", "fail":
		default:
			t.Errorf("check %v has bad status", c)
		}
		if c["detail"] == "" {
			t.Errorf("check %v has no detail", c)
		}
	}
	if names["ssh"] {
		t.Error("the GUI must never run the SSH probe (D9)")
	}
	if !names["workspace"] || !names["syncthing"] {
		t.Errorf("doctor names = %v", names)
	}
}

func TestAPIEmptyListsAreArrays(t *testing.T) {
	f := newFixture(t, okSync)
	if err := os.RemoveAll(filepath.Join(f.root, "workspace", "projects")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/api/projects", "/api/sessions", "/api/wip"} {
		rec := f.do(t, "GET", target, func(r *http.Request) { r.AddCookie(f.cookie()) })
		if got := strings.TrimSpace(rec.Body.String()); rec.Code != 200 || got != "[]" {
			t.Errorf("GET %s = %d %q, want 200 []", target, rec.Code, got)
		}
	}
}

func TestAPIErrors(t *testing.T) {
	f := newFixture(t, func() (syncengine.Client, error) {
		return nil, errors.New("syncthing API not reachable")
	})
	// v0.6.2 (D58): Syncthing not set up is a state, not an error: 200,
	// a not_configured body, and nothing in the debug bundle's error ring.
	var apiErr map[string]string
	getJSON(t, f, "/api/devices", http.StatusOK, &apiErr)
	if apiErr["status"] != "not_configured" || !strings.Contains(apiErr["error"], "not reachable") || !strings.Contains(apiErr["hint"], "cwm pair --start") {
		t.Errorf("devices body = %v", apiErr)
	}
	if n := len(f.srv.apiErrors.list()); n != 0 {
		t.Errorf("not_configured devices recorded %d API error(s)", n)
	}
	nilSync := newFixture(t, nil)
	getJSON(t, nilSync, "/api/devices", http.StatusOK, &apiErr)
	if apiErr["status"] != "not_configured" {
		t.Errorf("no sync client: %v", apiErr)
	}

	if err := os.RemoveAll(filepath.Join(f.root, "workspace")); err != nil {
		t.Fatal(err)
	}
	getJSON(t, f, "/api/projects", http.StatusConflict, &apiErr)
	if !strings.Contains(apiErr["error"], "cwm init") {
		t.Errorf("projects without workspace = %v, want init hint", apiErr)
	}
}

func TestListenLoopbackOnly(t *testing.T) {
	ln, err := Listen(0)
	if err != nil {
		t.Skipf("default port range busy on this machine: %v", err)
	}
	defer ln.Close()
	host := ln.Addr().String()
	if !strings.HasPrefix(host, "127.0.0.1:") {
		t.Errorf("listener bound to %s, want 127.0.0.1 only", host)
	}
	if p := Port(ln); p < DefaultPortFirst || p > DefaultPortLast {
		t.Errorf("port %d outside default range", p)
	}
	// An explicit busy port is an error, never a silent fallback.
	if ln2, err := Listen(Port(ln)); err == nil {
		ln2.Close()
		t.Error("Listen on a busy explicit port succeeded")
	}
	if _, err := Listen(70000); err == nil {
		t.Error("Listen accepted port 70000")
	}
}

func TestServeGracefulShutdown(t *testing.T) {
	f := newFixture(t, okSync)
	ln, err := Listen(0)
	if err != nil {
		t.Skipf("default port range busy: %v", err)
	}
	srv, err := New(f.srv.opts, Port(ln), f.token)
	if err != nil {
		t.Fatal(err)
	}
	waitStats(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, srv) }()

	// Real loopback round trip (no external network).
	req, _ := http.NewRequest("GET", "http://"+ln.Addr().String()+"/api/version", nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), "v0.5.0-test") {
		t.Errorf("live /api/version = %d %s", res.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve after cancel = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not shut down after cancel")
	}
}

func TestBrowserCommand(t *testing.T) {
	url := "http://127.0.0.1:47800/?t=abc"
	cases := map[string][]string{
		"linux":   {"xdg-open", url},
		"freebsd": {"xdg-open", url},
		"darwin":  {"open", url},
		"windows": {"cmd", "/c", "start", "", url},
	}
	for goos, want := range cases {
		name, args := browserCommand(goos, url)
		got := append([]string{name}, args...)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: %q, want %q", goos, got, want)
		}
	}
}
