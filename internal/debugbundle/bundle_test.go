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
	"github.com/Mr-JoE1/cwm/internal/desktopapp"
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
			return claudebin.Result{Path: filepath.Join(home, ".local", "bin", "claude"), Source: claudebin.SourceStandalone}, nil
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
		"== claude ==", "path:    " + tilde(".local/bin/claude"), "source:  standalone", "on PATH: false", "fix:     ",
		"PATH of this process:", "  " + tilde("bin"),
		"== config.toml ==", `default_account = work`, "link_mode = auto", "api_token = <set, not shown>",
		"account[0].login.email = <set, not shown>", "account[0].login.plan = max", "account[1].name = home",
		"== workspace ==", "projects:    2, sessions: 3", "<project-1> (2), <project-2> (1)", "accounts:    2 (1 with a recorded login)", "work (default, login: done), home (login: never)",
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

// v0.8: fake tokens planted in every vault and live credential location
// (vault entry, login-only .claude.json, live ~/.claude, ~/.claude.json,
// a switch backup) never reach the bundle; the resolved paths do, as ~.
func TestBundleNeverReadsVaultOrLiveCredentials(t *testing.T) {
	src, home := fixtureSources(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := src.Layout.Root
	planted := map[string]string{
		filepath.Join(root, "accounts", "work", ".credentials.json"):                                `{"claudeAiOauth":{"accessToken":"fake-VAULT-TOKEN-1"}}`,
		filepath.Join(root, "accounts", "work", ".claude.json"):                                     `{"oauthAccount":{"emailAddress":"vault@example.invalid"},"primaryApiKey":"fake-VAULT-APIKEY-2"}`,
		filepath.Join(home, ".claude", ".credentials.json"):                                         `{"claudeAiOauth":{"refreshToken":"fake-LIVE-TOKEN-3"}}`,
		filepath.Join(home, ".claude.json"):                                                         `{"oauthAccount":{"emailAddress":"live@example.invalid"},"primaryApiKey":"fake-LIVE-APIKEY-4"}`,
		filepath.Join(root, "backups", "20261124T000000Z-switch-work-to-home", ".credentials.json"): `{"claudeAiOauth":{"accessToken":"fake-BACKUP-TOKEN-5"}}`,
	}
	for p, body := range planted {
		write(t, p, body)
	}
	got := Build(src)
	for _, leak := range []string{"fake-VAULT-TOKEN-1", "fake-VAULT-APIKEY-2", "fake-LIVE-TOKEN-3", "fake-LIVE-APIKEY-4", "fake-BACKUP-TOKEN-5", "vault@example.invalid", "live@example.invalid"} {
		if strings.Contains(got, leak) {
			t.Errorf("bundle leaks %q", leak)
		}
	}
	for _, want := range []string{"mode:        capture (config predates v0.8: needs migration)", "login file:  " + tilde(".claude/.credentials.json"), "claude.json: " + tilde(".claude.json")} {
		if !strings.Contains(got, want) {
			t.Errorf("bundle lacks %q:\n%s", want, Extract(got, "workspace"))
		}
	}
}

// D98: the claude section names the bundle and gives the standalone
// install command instead of a PATH line.
func TestClaudeSectionBundled(t *testing.T) {
	p := filepath.Join("h", "AppData", "Roaming", "Claude", "claude-code", "2.1.319", "claude.exe")
	got := ClaudeSection(func() (claudebin.Result, error) {
		return claudebin.Result{Path: p, Source: claudebin.SourceDesktop, Bundle: "the Claude desktop app", Version: "2.1.319"}, nil
	}, "windows", nil, "")
	for _, want := range []string{
		"source:  desktop-app (bundled with the Claude desktop app)",
		"bundle:  the Claude desktop app (looked up afresh each time, never pinned)",
		"fix:     only a bundled copy; standalone CLI: irm https://claude.ai/install.ps1 | iex",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("section missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "SetEnvironmentVariable") {
		t.Errorf("no PATH advice for a bundled copy:\n%s", got)
	}
}

// D103: which bundled copy won and why, and a desktop-app folder that
// holds no executable, both reach the bundle.
func TestClaudeSectionBundleChoiceAndNotes(t *testing.T) {
	note := `desktop app dir found (~\AppData\Roaming\Claude\claude-code\2.1.328) but no claude.exe inside; it is empty`
	got := ClaudeSection(func() (claudebin.Result, error) {
		return claudebin.Result{Path: "x", Source: claudebin.SourceExtension, Bundle: "the VS Code extension", Version: "2.1.330",
			Why: "chosen: the VS Code extension's copy, the newest (2.1.330); the Claude desktop app has 2.1.328", Notes: []string{note}}, nil
	}, "windows", nil, "")
	for _, want := range []string{"choice:  chosen: the VS Code extension's copy, the newest (2.1.330)", "bundled: " + note} {
		if !strings.Contains(got, want) {
			t.Errorf("section missing %q:\n%s", want, got)
		}
	}
	got = ClaudeSection(func() (claudebin.Result, error) {
		return claudebin.Result{}, &claudebin.NotFoundError{GOOS: "windows", Notes: []string{note}}
	}, "windows", nil, "")
	if !strings.Contains(got, "bundled: "+note) {
		t.Errorf("not-found section without the note:\n%s", got)
	}
}

// D99: session folder names are Claude Code's dash-encoded paths, so
// they carry the user name and repo names. By default every one is a
// numbered alias wherever it appears (workspace section, doctor, logs,
// API errors); --show-projects prints them, with the user still
// redacted inside.
func TestBuildProjectsAreAliased(t *testing.T) {
	home := isolate(t)
	root := filepath.Join(home, ".cwm")
	t.Setenv("CWM_HOME", root)
	write(t, filepath.Join(root, "config.toml"), "version = 1\n")
	secret := "F--Home-Jdoe-Projects-secretrepo"
	other := "-home-jdoe-work-acme-billing"
	write(t, filepath.Join(root, "workspace", "projects", secret, "a.jsonl"), "{}\n")
	write(t, filepath.Join(root, "workspace", "projects", secret, "b.jsonl"), "{}\n")
	write(t, filepath.Join(root, "workspace", "projects", other, "c.jsonl"), "{}\n")
	write(t, filepath.Join(root, "logs", "agent.log"), "mirrored "+secret+"/a.jsonl into "+other+"\nopened F:\\Home\\Jdoe\\x and "+`F:\Home\Jdoe\x`+"\n")
	src := Sources{
		From:   "test",
		Layout: storage.NewLayoutAt(root),
		Doctor: func() []doctor.Check {
			return []doctor.Check{{Name: "session-folders", Status: doctor.StatusWarn, Detail: "unpinned session folders: " + other + " " + secret}}
		},
		APIErrors: []APIError{{Method: "GET", Endpoint: "/api/projects/" + secret, Code: 404, Message: "no " + secret}},
		Redact:    Redactor{Home: home, User: "Jdoe"},
	}
	got := Build(src)
	for _, leak := range []string{"Jdoe", "jdoe", "secretrepo", "acme", "billing"} {
		if strings.Contains(strings.ToLower(got), strings.ToLower(leak)) {
			t.Errorf("bundle leaks %q:\n%s", leak, got)
		}
	}
	for _, want := range []string{"<project-1> (1), <project-2> (2)", "Project folders are numbered", "mirrored <project-2>/a.jsonl into <project-1>",
		"unpinned session folders: <project-1> <project-2>", "/api/projects/<project-2> 404 no <project-2>", `F:\Home\<user>\x`} {
		if !strings.Contains(got, want) {
			t.Errorf("bundle lacks %q:\n%s", want, got)
		}
	}
	src.ShowProjects = true
	src.Redact.Projects = nil
	got = Build(src)
	for _, want := range []string{"F--Home-<user>-Projects-secretrepo (2)", "-home-<user>-work-acme-billing (1)", "--show-projects"} {
		if !strings.Contains(got, want) {
			t.Errorf("--show-projects bundle lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(strings.ToLower(got), "jdoe") {
		t.Errorf("--show-projects still must not show the user name:\n%s", got)
	}
}

// TestDesktopSection (D125): state, account by name, entry counts, the
// handoff.log tail; no uuid survives.
func TestDesktopSection(t *testing.T) {
	root := t.TempDir()
	l := storage.NewLayoutAt(root)
	if err := os.MkdirAll(l.LogsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.LogsDir(), "handoff.log"), []byte("2026-11-27T10:00:00Z claude --desktop --resume 44444444-dddd-4444-dddd-444444444444 (in /x) exit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := Sources{Layout: l, Desktop: func() desktopapp.Status {
		return desktopapp.Status{State: desktopapp.StateAccount, Account: "work", Root: "/u/AppData/Roaming/Claude",
			Lists: []desktopapp.Count{{Name: "work", Entries: 2, Signed: true}, {Entries: 1}}}
	}}
	got := Extract(Build(src), "desktop app")
	for _, want := range []string{"signed in as account work", "account:  work", "list:     work: 2 entries (signed in)", "list:     <uuid>: 1 entries",
		"--- handoff.log (last 1 lines)", "--resume <uuid> (in /x) exit 0"} {
		if !strings.Contains(got, want) {
			t.Errorf("desktop section lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "44444444") {
		t.Errorf("uuid in desktop section:\n%s", got)
	}
	if got := Extract(Build(Sources{}), "desktop app"); !strings.Contains(got, "not checked") {
		t.Errorf("nil Desktop: %q", got)
	}
}
