package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/desktopapp"
	"github.com/Mr-JoE1/cwm/internal/sessions"
)

const desktopUUID = "11111111-aaaa-4bbb-8ccc-00000000000a"

// desktopHome is a temp HOME with a cwm config naming account work by its
// login uuid, and a Claude app profile (the Linux location) signed in to
// it (where this OS keeps it), with one Code-tab entry from another account to share.
func desktopHome(t *testing.T) (profile string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	l, err := claudepaths.NewLayout()
	if err != nil {
		t.Fatal(err)
	}
	mk := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk(l.ConfigPath(), "mode = \"capture\"\ndefault_account = \"work\"\n[[account]]\nname = \"work\"\ndir = \"~/.cwm/accounts/work\"\n[account.login]\naccount_uuid = \""+desktopUUID+"\"\n")
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", filepath.Join(home, "Roaming"))
		t.Setenv("LOCALAPPDATA", filepath.Join(home, "Local"))
		profile = filepath.Join(home, "Roaming", "Claude")
	case "darwin":
		profile = filepath.Join(home, "Library", "Application Support", "Claude")
	default:
		profile = filepath.Join(home, ".config", "Claude")
	}
	mk(filepath.Join(profile, "config.json"), `{"oauth:tokenCacheV2":"SECRETBLOB","lastKnownAccountUuid":"`+desktopUUID+`"}`)
	mk(filepath.Join(profile, "claude-code-sessions", desktopUUID, "orgW", "local_w.json"), `{"cliSessionId":"w"}`)
	mk(filepath.Join(profile, "claude-code-sessions", "22222222-0000-0000-0000-000000000000", "orgD", "local_d.json"), `{"cliSessionId":"d"}`)
	return profile
}

func TestDesktopStatusCmd(t *testing.T) {
	desktopHome(t)
	got := runCLI(t, "desktop", "status")
	if got.code != ExitOK || !strings.Contains(got.out, "desktop app: signed in as account work · Code-tab entries: ") ||
		!strings.Contains(got.out, "work 1") || !strings.Contains(got.out, "another account 1") || !strings.Contains(got.out, "sharing: on") {
		t.Fatalf("status = %d %q %q", got.code, got.out, got.err)
	}
	js := runCLI(t, "desktop", "status", "--json")
	for _, bad := range []string{desktopUUID, "SECRET", "2222"} {
		if strings.Contains(got.out+js.out, bad) {
			t.Fatalf("status shows %q: %s %s", bad, got.out, js.out)
		}
	}
	if !strings.Contains(js.out, `"state":"account"`) {
		t.Fatalf("json = %s", js.out)
	}
}

func TestDesktopSyncSessionsCmd(t *testing.T) {
	profile := desktopHome(t)
	dst := filepath.Join(profile, "claude-code-sessions", desktopUUID, "orgW", "local_d.json")
	dry := runCLI(t, "desktop", "sync-sessions", "--dry-run")
	if dry.code != ExitOK || !strings.Contains(dry.out, "would add 1 Code-tab entry for work") {
		t.Fatalf("dry = %d %q %q", dry.code, dry.out, dry.err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("dry run wrote")
	}
	got := runCLI(t, "desktop", "sync-sessions")
	if got.code != ExitOK || !strings.Contains(got.out, "added 1 Code-tab entry for work") {
		t.Fatalf("sync = %d %q %q", got.code, got.out, got.err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatal(err)
	}
	if again := runCLI(t, "desktop", "sync-sessions"); !strings.Contains(again.out, "already listed") {
		t.Fatalf("again = %q", again.out)
	}
	if off := runCLI(t, "config", "set", "features.desktop_session_share", "false"); off.code != ExitOK {
		t.Fatalf("config set = %q", off.err)
	}
	if got := runCLI(t, "desktop", "sync-sessions"); got.code == ExitOK || !strings.Contains(got.err, "sharing is off") {
		t.Fatalf("off = %d %q", got.code, got.err)
	}
}

// TestAgentDesktopShare (D124): the agent shares at start (also with the
// app running, saying it lists them after a restart), then only every
// desktopShareEvery and only while the app is not running.
func TestAgentDesktopShare(t *testing.T) {
	profile := desktopHome(t)
	old := desktopStatus
	t.Cleanup(func() { desktopStatus = old })
	running := true
	desktopStatus = func(cfg *config.Config) desktopapp.Status {
		st := old(cfg)
		st.Running = running
		return st
	}
	list := filepath.Join(profile, "claude-code-sessions", desktopUUID, "orgW")
	var w strings.Builder
	var d desktopSharer
	t0 := time.Now()
	d.tick(&w, "agent", t0)
	if !strings.Contains(w.String(), "agent: desktop app: added 1 Code-tab entry for work — the Claude app is running and lists them after it restarts") {
		t.Fatalf("start = %q", w.String())
	}
	other := filepath.Join(profile, "claude-code-sessions", "22222222-0000-0000-0000-000000000000", "orgD", "local_e.json")
	if err := os.WriteFile(other, []byte(`{"cliSessionId":"e"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	w.Reset()
	d.tick(&w, "agent", t0.Add(time.Minute))         // too soon
	d.tick(&w, "agent", t0.Add(desktopShareEvery+1)) // app running: skipped
	if _, err := os.Stat(filepath.Join(list, "local_e.json")); err == nil || w.Len() != 0 {
		t.Fatalf("shared too soon or while running: %q", w.String())
	}
	running = false
	d.tick(&w, "agent", t0.Add(2*desktopShareEvery+2))
	if !strings.Contains(w.String(), "added 1 Code-tab entry for work\n") {
		t.Fatalf("periodic = %q", w.String())
	}
}

// TestDesktopPrep (D123): before a hand-off the session's own Code-tab
// entry is shared into the app's account (only that one), and a session
// that ran under another account gets a one-line note.
func TestDesktopPrep(t *testing.T) {
	profile := desktopHome(t)
	other := filepath.Join(profile, "claude-code-sessions", "22222222-0000-0000-0000-000000000000", "orgD")
	if err := os.WriteFile(filepath.Join(other, "local_e.json"), []byte(`{"cliSessionId":"e"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := filepath.Join(t.TempDir(), "d.jsonl")
	if err := os.WriteFile(tr, []byte(`{"type":"session-owner","accountUuid":"22222222-0000-0000-0000-000000000000"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, _ := claudepaths.NewLayout()
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	e := &Env{Stdout: &out, Stderr: &errb}
	e.desktopPrep(cfg, sessions.Session{ID: "d", Path: tr})
	list := filepath.Join(profile, "claude-code-sessions", desktopUUID, "orgW")
	if _, err := os.Stat(filepath.Join(list, "local_d.json")); err != nil {
		t.Fatalf("session entry not shared: %v (%q)", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(list, "local_e.json")); err == nil {
		t.Fatal("shared another session's entry")
	}
	if !strings.Contains(out.String(), "added 1 Code-tab entry for work") ||
		!strings.Contains(errb.String(), "note: the Claude app is signed in as work, but this session ran as another account") {
		t.Fatalf("out=%q err=%q", out.String(), errb.String())
	}
	// Same account: no note.
	if err := os.WriteFile(tr, []byte(`{"type":"session-owner","accountUuid":"`+desktopUUID+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	e.desktopPrep(cfg, sessions.Session{ID: "d", Path: tr})
	if errb.Len() != 0 {
		t.Fatalf("note for the same account: %q", errb.String())
	}
}
