package gui

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/status"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

func TestHostAllowList(t *testing.T) {
	f := newFixture(t, okSync)
	f.srv.opts.AllowHosts = []string{"cwm.example.lan", "proxy.example:443", "[fd00::5]"}
	f.srv.opts.Bind = "192.168.1.20"
	cases := map[string]int{
		"127.0.0.1:47805":       http.StatusOK,
		"localhost:47805":       http.StatusOK,
		"192.168.1.20:47805":    http.StatusOK, // concrete bind IP
		"192.168.1.20:47806":    http.StatusForbidden,
		"cwm.example.lan:47805": http.StatusOK, // bare allow-host + our port
		"cwm.example.lan":       http.StatusForbidden,
		"cwm.example.lan:80":    http.StatusForbidden,
		"proxy.example:443":     http.StatusOK, // host:port matches exactly
		"proxy.example:47805":   http.StatusForbidden,
		"[fd00::5]:47805":       http.StatusOK,
		"evil.com:47805":        http.StatusForbidden,
	}
	for host, want := range cases {
		rec := f.do(t, "GET", "/api/version", func(r *http.Request) {
			r.Host = host
			r.Header.Set("Authorization", "Bearer "+f.token)
		})
		if rec.Code != want {
			t.Errorf("Host %q: code = %d, want %d", host, rec.Code, want)
		}
	}
	// A wildcard bind is not itself an allowed Host.
	f.srv.opts.Bind = "0.0.0.0"
	if rec := f.do(t, "GET", "/healthz", func(r *http.Request) { r.Host = "0.0.0.0:47805" }); rec.Code != http.StatusForbidden {
		t.Errorf("0.0.0.0 Host accepted: %d", rec.Code)
	}
}

func TestPersistentCookie(t *testing.T) {
	f := newFixture(t, okSync)
	rec := f.do(t, "GET", "/?t="+f.token, nil)
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].MaxAge != 0 {
		t.Fatalf("v0.5 mode cookie = %+v, want a session cookie", c)
	}
	f.srv.opts.PersistentCookie = true
	rec = f.do(t, "GET", "/?t="+f.token, nil)
	c := rec.Result().Cookies()
	if len(c) != 1 || c[0].MaxAge != 30*24*3600 || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("agent mode cookie = %+v", c)
	}
}

func TestHealthzLoginAndAgentInfo(t *testing.T) {
	f := newFixture(t, okSync)
	rec := f.do(t, "GET", "/healthz", nil)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"app":"cwm","ok":true}` {
		t.Errorf("/healthz = %d %s", rec.Code, rec.Body)
	}
	rec = f.do(t, "GET", "/login", nil)
	body := rec.Body.String()
	csp := rec.Header().Get("Content-Security-Policy")
	if rec.Code != http.StatusOK || !strings.Contains(body, `<form method="get" action="/">`) || !strings.Contains(body, `name="t"`) ||
		strings.Contains(body, "<script") || !strings.Contains(csp, "form-action 'self'") || !strings.Contains(csp, loginStyleHash) {
		t.Errorf("/login = %d csp=%q\n%s", rec.Code, csp, body)
	}
	if strings.Contains(body, f.token) {
		t.Error("/login leaks the token")
	}
	if rec := f.do(t, "GET", "/api/agent", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("/api/agent unauthenticated = %d", rec.Code)
	}
	var info agent.Info
	getJSON(t, f, "/api/agent", http.StatusOK, &info)
	if !info.Agent || info.PID != os.Getpid() || info.Port != testPort || info.Bind != "127.0.0.1" || info.Version != "v0.5.0-test" || info.Started.IsZero() {
		t.Errorf("/api/agent = %+v", info)
	}
}

func TestSetTokenRevokesOldCredentials(t *testing.T) {
	f := newFixture(t, okSync)
	oldCookie := f.cookie()
	newTok := strings.Repeat("e", 64)
	if err := f.srv.SetToken("short"); err == nil {
		t.Error("SetToken accepted a short token")
	}
	if err := f.srv.SetToken(newTok); err != nil {
		t.Fatal(err)
	}
	if f.srv.Token() != newTok || !strings.HasSuffix(f.srv.URL(), "?t="+newTok) {
		t.Errorf("Token/URL not updated: %s", f.srv.URL())
	}
	for name, mut := range map[string]func(*http.Request){
		"old cookie": func(r *http.Request) { r.AddCookie(oldCookie) },
		"old bearer": func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.token) },
	} {
		if rec := f.do(t, "GET", "/api/version", mut); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s after rotation = %d, want 401", name, rec.Code)
		}
	}
	if rec := f.do(t, "GET", "/api/version", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+newTok) }); rec.Code != http.StatusOK {
		t.Errorf("new bearer = %d", rec.Code)
	}
	var csrf map[string]string
	f.token = newTok
	getJSON(t, f, "/api/csrf", http.StatusOK, &csrf)
	if csrf["csrf"] != deriveCSRF(newTok) {
		t.Error("CSRF value not re-derived from the new token")
	}
}

func TestSnapshotChanged(t *testing.T) {
	a := Snapshot{Accounts: []string{"main"}}
	b := Snapshot{Initialized: true, Accounts: []string{"main", "work"}, Projects: 1, Sessions: 2}
	if got := strings.Join(a.changed(b), ","); got != "initialized,accounts,projects,sessions" {
		t.Errorf("changed = %s", got)
	}
	if got := a.changed(a); len(got) != 0 {
		t.Errorf("no change reported %v", got)
	}
}

// sseEvent is one parsed Server-Sent Event.
type sseEvent struct {
	name string
	data map[string]any
}

// startLive serves the fixture's server on a real loopback port (SSE
// needs real streaming) and returns its base URL.
func startLive(t *testing.T, opts Options, token string) (*Server, string, context.CancelFunc) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := Port(ln)
	srv, err := New(opts, port, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, srv) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("Serve did not shut down within 3s with an open SSE stream")
		}
	})
	return srv, "http://127.0.0.1:" + strconv.Itoa(port), cancel
}

func readEvents(t *testing.T, sc *bufio.Scanner, ch chan<- sseEvent) {
	var name string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			var m map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m); err != nil {
				t.Errorf("bad data line %q: %v", line, err)
			}
			ch <- sseEvent{name, m}
		}
	}
	close(ch)
}

func next(t *testing.T, ch <-chan sseEvent, want string, within time.Duration) sseEvent {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("stream closed while waiting for %q", want)
			}
			if ev.name == want {
				return ev
			}
		case <-deadline:
			t.Fatalf("no %q event within %s", want, within)
		}
	}
}

func TestEventsStream(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CWM_HOME", root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	tok := strings.Repeat("a", 64)
	_, base, _ := startLive(t, Options{
		Version:    status.Version{Version: "test"},
		Layout:     storage.NewLayoutAt(root),
		EventsPoll: 40 * time.Millisecond,
		Heartbeat:  60 * time.Millisecond,
	}, tok)

	// Unauthenticated: plain JSON 401, not a stream.
	resp, err := http.Get(base + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /api/events = %d", resp.StatusCode)
	}

	req, _ := http.NewRequest("GET", base+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers = %v", resp.Header)
	}
	ch := make(chan sseEvent, 64)
	go readEvents(t, bufio.NewScanner(resp.Body), ch)

	hello := next(t, ch, "hello", 2*time.Second)
	snap, _ := hello.data["snapshot"].(map[string]any)
	if snap == nil || snap["initialized"] != false || snap["projects"] != float64(0) {
		t.Fatalf("hello = %v", hello.data)
	}
	if ag, _ := hello.data["agent"].(map[string]any); ag == nil || ag["agent"] != true {
		t.Errorf("hello lacks agent info: %v", hello.data)
	}
	next(t, ch, "heartbeat", time.Second) // immediate
	next(t, ch, "heartbeat", time.Second) // periodic

	// A new session file → a state event naming projects + sessions.
	writeFile(t, filepath.Join(root, "workspace", "projects", "gamma", "22222222-bbbb.jsonl"), "{}\n")
	st := next(t, ch, "state", 3*time.Second)
	changed, _ := st.data["changed"].([]any)
	var names []string
	for _, c := range changed {
		names = append(names, c.(string))
	}
	if got := strings.Join(names, ","); !strings.Contains(got, "projects") || !strings.Contains(got, "sessions") {
		t.Errorf("state changed = %s (%v)", got, st.data)
	}
}

func TestEventsEndOnTokenRotation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CWM_HOME", root)
	t.Setenv("PATH", t.TempDir())
	tok := strings.Repeat("a", 64)
	srv, base, _ := startLive(t, Options{Layout: storage.NewLayoutAt(root), EventsPoll: 30 * time.Millisecond, Heartbeat: time.Hour}, tok)
	req, _ := http.NewRequest("GET", base+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	ch := make(chan sseEvent, 64)
	go readEvents(t, bufio.NewScanner(resp.Body), ch)
	next(t, ch, "hello", 2*time.Second)
	if err := srv.SetToken(strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	next(t, ch, "unauthorized", 2*time.Second)
	select {
	case _, ok := <-ch:
		if ok {
			// drain until close
			for range ch {
			}
		}
	case <-time.After(2 * time.Second):
		t.Error("stream not closed after rotation")
	}
}

func TestHeadlessGuard(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if headless("linux", env(nil)) == nil {
		t.Error("linux without DISPLAY/WAYLAND_DISPLAY must not run xdg-open")
	}
	for _, c := range []struct {
		goos string
		vars map[string]string
	}{
		{"linux", map[string]string{"DISPLAY": ":0"}},
		{"linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}},
		{"darwin", nil},
		{"windows", nil},
	} {
		if err := headless(c.goos, env(c.vars)); err != nil {
			t.Errorf("%s %v: %v", c.goos, c.vars, err)
		}
	}
}
