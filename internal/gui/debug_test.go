package gui

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/debugbundle"
	"github.com/Mr-JoE1/cwm/internal/service"
)

func authed(f *fixture) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(f.cookie()) }
}

func TestAPIClaudeOffPath(t *testing.T) {
	f, _ := actionFixture(t, okSync)
	bin := "/home/u/.local/bin/claude"
	f.srv.opts.FindClaude = func() (claudebin.Result, error) {
		return claudebin.Result{Path: bin, Source: claudebin.SourceDiscovered}, nil
	}
	var got claudeInfo
	getJSON(t, f, "/api/claude", 200, &got)
	if !got.Found || got.Path != bin || got.Source != "discovered" || got.OnPath ||
		got.PathCommand != claudebin.PathHint("linux", filepath.Dir(bin)) || !strings.Contains(got.Hint, "not on PATH") || got.OS != "linux" {
		t.Errorf("claude info = %+v", got)
	}
	if len(got.Install) < 2 || got.Install[0].Command != "curl -fsSL https://claude.ai/install.sh | bash" {
		t.Errorf("install options = %+v", got.Install)
	}
	// Raw shape: snake_case on_path is always present.
	rec := f.do(t, "GET", "/api/claude", authed(f))
	if !strings.Contains(rec.Body.String(), `"on_path":false`) || !strings.Contains(rec.Body.String(), `"path_command":`) {
		t.Errorf("raw body = %s", rec.Body.String())
	}
}

func TestAPIClaudeNotFoundWindows(t *testing.T) {
	f, _ := actionFixture(t, okSync)
	f.srv.opts.GOOS = "windows"
	f.srv.opts.FindClaude = func() (claudebin.Result, error) {
		return claudebin.Result{}, &claudebin.NotFoundError{GOOS: "windows"}
	}
	var got claudeInfo
	getJSON(t, f, "/api/claude", 200, &got)
	if got.Found || got.Error == "" || got.PathCommand != "" {
		t.Errorf("claude info = %+v", got)
	}
	cmds := []string{}
	for _, o := range got.Install {
		cmds = append(cmds, o.Command)
	}
	want := []string{
		"irm https://claude.ai/install.ps1 | iex",
		"curl -fsSL https://claude.ai/install.cmd -o install.cmd && install.cmd && del install.cmd",
		"winget install Anthropic.ClaudeCode",
		"npm install -g @anthropic-ai/claude-code",
	}
	if strings.Join(cmds, "\n") != strings.Join(want, "\n") {
		t.Errorf("install = %q", cmds)
	}
}

func TestAPIClaudeAndDebugGates(t *testing.T) {
	f, _ := actionFixture(t, okSync)
	for _, path := range []string{"/api/claude", "/api/debug"} {
		if rec := f.do(t, "GET", path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without auth = %d", path, rec.Code)
		}
		if rec := f.do(t, "GET", path, func(r *http.Request) {
			r.Host = "evil.com:47805"
			r.AddCookie(f.cookie())
		}); rec.Code != http.StatusForbidden {
			t.Errorf("%s with a rebinding Host = %d", path, rec.Code)
		}
		if rec := f.do(t, "GET", path, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.token) }); rec.Code != http.StatusOK {
			t.Errorf("%s with bearer = %d", path, rec.Code)
		}
		if rec := f.post(t, path, `{}`, nil); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405 (read-only)", path, rec.Code)
		}
	}
}

func TestAPIDebugBundle(t *testing.T) {
	f, _ := actionFixture(t, okSync)
	f.srv.opts.FindClaude = func() (claudebin.Result, error) {
		return claudebin.Result{Path: filepath.Join(f.root, "bin", "claude"), Source: claudebin.SourceDiscovered}, nil
	}
	f.srv.opts.DebugSources = func() debugbundle.Sources {
		return debugbundle.Sources{
			Service: func() []service.Item {
				return []service.Item{{Name: "systemd unit", State: "installed", Detail: "cwm-agent.service"}}
			},
			Redact: debugbundle.Redactor{Home: f.root, User: "tester"},
		}
	}
	writeFile(t, filepath.Join(f.root, "config.toml"), "version = 1\ndefault_account = \"work\"\n")
	writeFile(t, filepath.Join(f.root, "logs", "agent.log"), "cwm agent: serving on http://127.0.0.1:47805/?t="+f.token+"\n")
	// Two errors to record (a 401 is not recorded).
	f.do(t, "GET", "/api/nope", authed(f))
	f.post(t, "/api/accounts", `{"name":"BAD"}`, nil)
	f.do(t, "GET", "/api/version", nil)

	rec := f.do(t, "GET", "/api/debug", authed(f))
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("debug = %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("debug bundle must not be cached")
	}
	body := rec.Body.String()
	for _, w := range []string{"built by dashboard agent", "cwm:     v0.5.0-test (commit abc123)",
		"== agent ==\npid:", "port:    47805", "bind:    127.0.0.1",
		"== service ==\nsystemd unit", "source:  discovered", "== config.toml ==", "default_account = work",
		"== syncthing ==\nreachable: yes\ndevices:   1 paired, 1 connected", "== doctor ==", "--- agent.log",
		"GET /api/nope 404 no such endpoint /api/nope", "POST /api/accounts 400 invalid account name"} {
		if !strings.Contains(body, w) {
			t.Errorf("bundle lacks %q:\n%s", w, body)
		}
	}
	if strings.Contains(body, f.token) || strings.Contains(body, f.srv.csrf) || strings.Contains(body, f.root) ||
		regexp.MustCompile(`(?i)[0-9a-f]{64}`).MatchString(body) {
		t.Errorf("bundle leaks a token or the home:\n%s", body)
	}
	if strings.Contains(body, "GET /api/version 401") {
		t.Error("401s must not be recorded")
	}
}

func TestErrorRingKeepsLast100(t *testing.T) {
	var r errorRing
	if got := r.list(); got == nil || len(got) != 0 {
		t.Fatalf("empty ring = %#v", got)
	}
	for i := range 250 {
		r.add(debugbundle.APIError{Time: time.Unix(int64(i), 0), Message: fmt.Sprint(i)})
	}
	got := r.list()
	if len(got) != apiErrorKeep || got[0].Message != "150" || got[len(got)-1].Message != "249" {
		t.Errorf("ring = %d entries, first %s, last %s", len(got), got[0].Message, got[len(got)-1].Message)
	}
}

// D49: Resume prechecks claude like Add account, and an off-PATH claude
// comes back as a PATH hint next to the launch.
func TestResumePrecheckAndPathHint(t *testing.T) {
	f, l := actionFixture(t, okSync)
	body := `{"project":"alpha","id":"11111111-aaaa"}`
	f.srv.opts.FindClaude = func() (claudebin.Result, error) {
		return claudebin.Result{}, &claudebin.NotFoundError{GOOS: "linux"}
	}
	rec := f.post(t, "/api/sessions/resume", body, nil)
	var e apiError
	decode(t, rec, &e)
	if rec.Code != http.StatusFailedDependency || e.Command != "curl -fsSL https://claude.ai/install.sh | bash" || len(l.calls) != 0 {
		t.Fatalf("resume without claude = %d %+v, launches %q", rec.Code, e, l.calls)
	}
	f.srv.opts.FindClaude = func() (claudebin.Result, error) {
		return claudebin.Result{Path: "/home/u/.local/bin/claude", Source: claudebin.SourceDiscovered}, nil
	}
	for _, target := range []string{"/api/sessions/resume", "/api/accounts"} {
		b := body
		if target == "/api/accounts" {
			b = `{"name":"work"}`
		}
		rec = f.post(t, target, b, nil)
		var res launchResult
		decode(t, rec, &res)
		if rec.Code != 200 || res.ClaudePath != "/home/u/.local/bin/claude" ||
			res.PathHint != claudebin.PathHint("linux", filepath.Dir("/home/u/.local/bin/claude")) {
			t.Errorf("%s off PATH = %d %+v", target, rec.Code, res)
		}
	}
	// On PATH: no hint fields at all.
	f.srv.opts.FindClaude = func() (claudebin.Result, error) {
		return claudebin.Result{Path: "/usr/bin/claude", Source: claudebin.SourcePath, OnPath: true}, nil
	}
	rec = f.post(t, "/api/sessions/resume", body, nil)
	if strings.Contains(rec.Body.String(), "pathHint") {
		t.Errorf("on-PATH launch carries a hint: %s", rec.Body.String())
	}
	// A broken explicit setting is a 424 too, with the error text.
	f.srv.opts.FindClaude = func() (claudebin.Result, error) {
		return claudebin.Result{}, errors.New("CWM_CLAUDE=/x is not an executable file")
	}
	rec = f.post(t, "/api/sessions/resume", body, nil)
	if rec.Code != http.StatusFailedDependency || !strings.Contains(rec.Body.String(), "CWM_CLAUDE") {
		t.Errorf("bad setting = %d %s", rec.Code, rec.Body.String())
	}
}
