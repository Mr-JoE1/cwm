package cli

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/claudeopen"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/launcher"
)

func TestWithFlag(t *testing.T) {
	cases := []struct {
		args    []string
		val     string
		next    int
		ok, bad bool
	}{
		{[]string{"--with", "vscode"}, "vscode", 1, true, false},
		{[]string{"--with=desktop"}, "desktop", 0, true, false},
		{[]string{"--with"}, "", 0, true, true},
		{[]string{"--with", "code"}, "", 1, true, true},
		{[]string{"--with=cli;x"}, "", 0, true, true},
		{[]string{"--dry-run"}, "", 0, false, false},
	}
	for _, c := range cases {
		val, next, ok, err := withFlag(c.args, 0)
		if val != c.val || next != c.next || ok != c.ok || (err != nil) != c.bad {
			t.Errorf("%v = %q %d %v %v", c.args, val, next, ok, err)
		}
		if err != nil && !strings.Contains(err.Error(), "auto, cli, vscode, desktop") {
			t.Errorf("%v error lacks the choices: %v", c.args, err)
		}
	}
}

// TestDesktopHandOff (D117): claude --desktop runs only with a terminal on
// stdin and stdout; otherwise, or when it fails, the app opens in the
// folder and the copyable resume command is printed.
func TestDesktopHandOff(t *testing.T) {
	oldTTY, oldRun := handOffTTY, runSteps
	t.Cleanup(func() { handOffTTY, runSteps = oldTTY, oldRun })
	var ran [][]string
	runSteps = func(steps []claudeopen.Step) error {
		for _, s := range steps {
			ran = append(ran, s.Argv)
		}
		return nil
	}
	c := claudeopen.Choice{ID: claudeopen.Desktop, Label: "Claude desktop app", Available: true, Resume: true}
	const id = "44444444-dddd-4444-dddd-444444444444"
	for _, tc := range []struct {
		name     string
		tty      bool
		resume   error
		calls    int
		wantErr  bool
		wantOut  string
		wantStep bool
	}{
		{"no terminal", false, nil, 0, false, "no terminal to hand the session over from", true},
		{"hand-off ok", true, nil, 1, false, "handed session 44444444 to the Claude desktop app", false},
		{"hand-off fails", true, errors.New("claude --desktop: exit status 1"), 1, true, "", true},
	} {
		ran = nil
		handOffTTY = func() bool { return tc.tty }
		calls := 0
		var out, errb strings.Builder
		e := &Env{Stdout: &out, Stderr: &errb}
		err := e.openApp(c, "/home/a/api", id, func() error { calls++; return tc.resume })
		if calls != tc.calls || (err != nil) != tc.wantErr || !strings.Contains(out.String(), tc.wantOut) {
			t.Errorf("%s: calls %d err %v out %q", tc.name, calls, err, out.String())
		}
		if tc.wantStep != (len(ran) == 1) || (tc.wantStep && !strings.Contains(strings.Join(ran[0], " "), "claude://code/new?folder=%2Fhome%2Fa%2Fapi")) {
			t.Errorf("%s: steps %q", tc.name, ran)
		}
		if tc.wantErr && !strings.Contains(err.Error(), "claude --resume "+id) {
			t.Errorf("%s: error lacks the resume command: %v", tc.name, err)
		}
	}
}

// TestHandOffCmdHasNoPipes: the hand-off child gets this process's own
// standard handles (a console/terminal), never pipes, and no window flags.
func TestHandOffCmdHasNoPipes(t *testing.T) {
	cmd := launcher.HandOffCmd("/opt/claude", []string{"--desktop", "--resume", "x"})
	if cmd.Stdin != os.Stdin || cmd.Stdout != os.Stdout || cmd.Stderr != os.Stderr || cmd.SysProcAttr != nil {
		t.Fatalf("hand-off cmd = in %v out %v err %v attr %+v", cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.SysProcAttr)
	}
	if strings.Join(cmd.Args, " ") != "/opt/claude --desktop --resume x" {
		t.Errorf("argv = %q", cmd.Args)
	}
	if !launcher.IsHandOff([]string{"--mcp-config", "m.json", "--desktop", "--resume", "x"}) || launcher.IsHandOff([]string{"--resume", "x"}) {
		t.Error("IsHandOff")
	}
}

func TestIsConsoleRefusesPipesAndNull(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if isConsole(r) || isConsole(w) || isConsole(null) {
		t.Error("a pipe or the null device counted as a terminal")
	}
}

// TestRunDesktopRoute (D122): `cwm run --account X --with desktop` opens
// the app only when it is signed in to X; every other state falls back
// to a terminal with the note.
func TestRunDesktopRoute(t *testing.T) {
	oldAcct, oldRun := desktopAccount, runSteps
	t.Cleanup(func() { desktopAccount, runSteps = oldAcct, oldRun })
	c := claudeopen.Choice{ID: claudeopen.Desktop, Label: "Claude desktop app", Available: true, Resume: true}
	for _, tc := range []struct {
		name   string
		app    claudeopen.AppAccount
		opened bool
		note   string
	}{
		{"same", claudeopen.AppAccount{State: claudeopen.AppAccountNamed, Name: "work"}, true, "opened the Claude app (signed in as work)"},
		{"another cwm account", claudeopen.AppAccount{State: claudeopen.AppAccountNamed, Name: "default"}, false, "note: the Claude app is signed in as default — opened work in a terminal instead"},
		{"other", claudeopen.AppAccount{State: claudeopen.AppOther}, false, "signed in as another account"},
		{"signed out", claudeopen.AppAccount{State: claudeopen.AppSignedOut}, false, "signed in as nobody"},
		{"unknown", claudeopen.AppAccount{}, false, "signed in as another account"},
	} {
		var ran [][]string
		runSteps = func(steps []claudeopen.Step) error {
			for _, s := range steps {
				ran = append(ran, s.Argv)
			}
			return nil
		}
		desktopAccount = func(*config.Config) claudeopen.AppAccount { return tc.app }
		var out, errb strings.Builder
		e := &Env{Stdout: &out, Stderr: &errb}
		opened, err := e.runDesktop(&config.Config{}, "work", c)
		if err != nil || opened != tc.opened || !strings.Contains(out.String()+errb.String(), tc.note) {
			t.Errorf("%s: opened=%v err=%v out=%q err=%q", tc.name, opened, err, out.String(), errb.String())
		}
		if tc.opened != (len(ran) == 1 && strings.Contains(strings.Join(ran[0], " "), "claude://code/new")) {
			t.Errorf("%s: steps %q", tc.name, ran)
		}
		if strings.Contains(errb.String(), "signs in on its own") {
			t.Errorf("%s: stale D118 note: %q", tc.name, errb.String())
		}
	}
}
