package debugbundle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/doctor"
	"github.com/Mr-JoE1/cwm/internal/service"
	"github.com/Mr-JoE1/cwm/internal/status"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		t.Setenv(k, home)
	}
	return home
}

// fixtureSources builds a cwm home full of things that must not leak.
func fixtureSources(t *testing.T) (Sources, string) {
	home := isolate(t)
	root := filepath.Join(home, ".cwm")
	t.Setenv("CWM_HOME", root)
	write(t, filepath.Join(root, "config.toml"), `version = 1
default_account = "work"
link_mode = "auto"
claude_path = "`+filepath.ToSlash(filepath.Join(home, ".local", "bin", "claude"))+`"
api_token = "`+hex64+`"

[[account]]
name = "work"
dir = "~/.cwm/accounts/work"
[account.login]
email = "someone@example.com"
account_uuid = "1234-5678"
plan = "max"

[[account]]
name = "home"
dir = "~/.cwm/accounts/home"
`)
	write(t, filepath.Join(root, "accounts", "work", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"CREDSECRET-XYZ"}}`)
	write(t, filepath.Join(root, "workspace", "projects", "alpha", "s1.jsonl"), "{}\n")
	write(t, filepath.Join(root, "workspace", "projects", "alpha", "s2.jsonl"), "{}\n")
	write(t, filepath.Join(root, "workspace", "projects", "beta", "s3.jsonl"), "{}\n")
	var agentLog strings.Builder
	for i := range 500 {
		fmt.Fprintf(&agentLog, "agent line %d\n", i)
	}
	agentLog.WriteString("open http://127.0.0.1:47800/?t=" + hex64 + "\n")
	write(t, filepath.Join(root, "logs", "agent.log"), agentLog.String())
	write(t, filepath.Join(root, "logs", "terminal.log"), "x-terminal-emulator: error: user someone@example.com in "+filepath.Join(home, "x")+"\n")
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(root, "logs", "terminal.log"), old, old)

	started := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	src := Sources{
		Version: status.Version{Version: "v0.6.1", Commit: "abc1234", OS: "linux", Arch: "amd64"},
		From:    "test",
		Layout:  storage.NewLayoutAt(root),
		Agent:   &agent.Info{Agent: true, PID: 4242, Port: 47800, Bind: "127.0.0.1", Started: started, Version: "v0.6.1"},
		Service: func() []service.Item {
			return []service.Item{{Name: "systemd unit", State: "installed", Detail: filepath.Join(home, ".config", "systemd", "user", "cwm-agent.service")}}
		},
		Claude: func() (claudebin.Result, error) {
			return claudebin.Result{Path: filepath.Join(home, ".local", "bin", "claude"), Source: claudebin.SourceDiscovered}, nil
		},
		Doctor: func() []doctor.Check {
			return []doctor.Check{{Name: "workspace", Status: doctor.StatusOK, Detail: "workspace dir exists (" + home + ")"}}
		},
		SyncClient: func() (syncengine.Client, error) {
			return nil, errors.New("connection refused")
		},
		APIErrors: []APIError{{Time: started, Method: "POST", Endpoint: "/api/accounts", Code: 424, Message: "claude not found in " + home}},
		GOOS:      runtime.GOOS,
		OSInfo:    func() string { return "Test OS 1.0" },
		Getenv: func(k string) string {
			return map[string]string{"PATH": filepath.FromSlash("/usr/bin") + string(os.PathListSeparator) + filepath.Join(home, "bin"), "SHELL": "/bin/bash", "CWM_TOKEN": hex64}[k]
		},
		Now:    func() time.Time { return started.Add(90 * time.Minute) },
		Redact: Redactor{Home: home, User: "tester"},
	}
	return src, home
}

// tilde is ~ joined with a slash path, in this OS's spelling.
func tilde(rel string) string { return "~" + string(filepath.Separator) + filepath.FromSlash(rel) }

func TestBuildSectionsAndRedaction(t *testing.T) {
	src, home := fixtureSources(t)
	got := Build(src)
	for _, w := range []string{
		"== version ==", "cwm:     v0.6.1 (commit abc1234)", "system:  Test OS 1.0", "go:      go",
		"== agent ==", "pid:     4242", "port:    47800", "bind:    127.0.0.1", "uptime:  1h30m0s",
		"== service ==", "systemd unit", tilde(".config/systemd/user/cwm-agent.service"),
		"== claude ==", "path:    " + tilde(".local/bin/claude"), "source:  discovered", "on PATH: false", "fix:     ",
		"PATH of this process:", "  " + tilde("bin"),
		"== config.toml ==", `default_account = work`, "link_mode = auto", "api_token = <set, not shown>",
		"account[0].login.email = <set, not shown>", "account[0].login.plan = max", "account[1].name = home",
		"== workspace ==", "projects:    2, sessions: 3", "alpha (2), beta (1)", "accounts:    2 (1 with a recorded login)", "work (default), home",
		"== syncthing ==", "not reachable: connection refused",
		"== doctor ==", "ok:   workspace dir exists (~)", "doctor: 1 ok, 0 warn, 0 fail",
		"== logs ==", "--- agent.log", "agent line 499", "--- terminal.log",
		"== recent API errors ==", "2026-11-19T10:00:00Z POST /api/accounts 424 claude not found in ~",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("bundle lacks %q", w)
		}
	}
	for _, leak := range []string{home, hex64, "someone@example.com", "CREDSECRET", "1234-5678", "agent line 0\n"} {
		if strings.Contains(got, leak) {
			t.Errorf("bundle leaks %q", leak)
		}
	}
	if anyHex64.MatchString(got) || anyEmail.MatchString(got) {
		t.Errorf("bundle has a hex64 or e-mail:\n%s", got)
	}
	// The log tail budget: ~200 lines over all files.
	logs := Extract(got, "logs")
	if n := Lines(logs); n > LogLines+10 {
		t.Errorf("log tail has %d lines, budget %d", n, LogLines)
	}
	// Newest file first.
	if strings.Index(logs, "--- agent.log") > strings.Index(logs, "--- terminal.log") {
		t.Errorf("newest log should come first:\n%s", logs)
	}
}

func TestBuildDegradesGracefully(t *testing.T) {
	isolate(t)
	got := Build(Sources{From: "test", AgentNote: "no answer", APIErrorsNote: "kept by the agent"})
	for _, w := range []string{"== agent ==\nnot running (no answer)", "== service ==\nnot available", "== claude ==\nresolver: not available",
		"== syncthing ==\nnot configured", "== recent API errors ==\nkept by the agent"} {
		if !strings.Contains(got, w) {
			t.Errorf("bundle lacks %q:\n%s", w, got)
		}
	}
}

func TestBuildNoAPIErrorsYet(t *testing.T) {
	isolate(t)
	got := Build(Sources{APIErrors: []APIError{}})
	if !strings.Contains(got, "none since the agent started") {
		t.Errorf("got:\n%s", got)
	}
}

func TestExtractAndExtra(t *testing.T) {
	isolate(t)
	got := Build(Sources{Extra: []Section{{Title: "claude (as the agent sees it)", Body: "found: yes"}}})
	if Extract(got, "claude (as the agent sees it)") != "found: yes" {
		t.Errorf("extract failed:\n%s", got)
	}
	if Extract(got, "nope") != "" {
		t.Error("extract of a missing section must be empty")
	}
}

func TestConfigMissingAndBroken(t *testing.T) {
	home := isolate(t)
	l := storage.NewLayoutAt(filepath.Join(home, ".cwm"))
	if got := Build(Sources{Layout: l}); !strings.Contains(got, "config.toml: missing") {
		t.Errorf("missing config not reported:\n%s", got)
	}
	write(t, l.ConfigPath(), "version = = 1\npassword = \"hunter2\"\n")
	got := Build(Sources{Layout: l})
	if !strings.Contains(got, "does not parse") || strings.Contains(got, "hunter2") {
		t.Errorf("broken config:\n%s", got)
	}
}
