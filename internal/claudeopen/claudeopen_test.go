package claudeopen

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/claudebin"
)

func TestValidAndEffective(t *testing.T) {
	for _, v := range []string{"auto", "cli", "vscode", "desktop"} {
		if !Valid(v) {
			t.Errorf("%s invalid", v)
		}
	}
	for _, v := range []string{"", "Auto", "code", "cli;rm", "desktop "} {
		if Valid(v) {
			t.Errorf("%q valid", v)
		}
	}
	cases := [][3]string{{"", "", "auto"}, {"", "vscode", "vscode"}, {"cli", "vscode", "cli"}, {"bogus", "desktop", "desktop"}, {"", "bogus", "auto"}}
	for _, c := range cases {
		if got := Effective(c[0], c[1]); got != c[2] {
			t.Errorf("Effective(%q,%q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

// fakeMachine is a home folder in a temp dir with the given files.
type fakeMachine struct {
	root string
	vars map[string]string
	path map[string]string // LookPath answers
}

func newMachine(t *testing.T, files ...string) *fakeMachine {
	t.Helper()
	m := &fakeMachine{root: t.TempDir(), vars: map[string]string{}, path: map[string]string{}}
	for _, f := range files {
		p := filepath.Join(m.root, f)
		if strings.HasSuffix(f, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func (m *fakeMachine) env(goos string, res claudebin.Result, err error, capture bool) Env {
	return Env{GOOS: goos, GOARCH: "amd64", Getenv: func(k string) string { return m.vars[k] },
		Glob: filepath.Glob, Stat: os.Stat, Home: filepath.Join(m.root, "home"),
		LookPath: func(n string) (string, error) {
			if p, ok := m.path[n]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Claude:  func() (claudebin.Result, error) { return res, err },
		Capture: capture}
}

func byID(list []Choice) map[string]Choice {
	out := map[string]Choice{}
	for _, c := range list {
		out[c.ID] = c
	}
	return out
}

func TestDetectNothingInstalled(t *testing.T) {
	m := newMachine(t, "home/")
	got := byID(Detect(m.env("linux", claudebin.Result{}, &claudebin.NotFoundError{}, true)))
	want := map[string]string{Auto: ReasonNoClaude, CLI: ReasonNoStandalone, VSCode: ReasonNoExtension, Desktop: ReasonNoDesktop}
	for id, reason := range want {
		if c := got[id]; c.Available || c.Reason != reason {
			t.Errorf("%s = %+v, want unavailable: %q", id, c, reason)
		}
	}
	if len(got) != 4 {
		t.Errorf("got %d options", len(got))
	}
}

func TestDetectOnlyBundledWindows(t *testing.T) {
	// Both owner machines: no standalone CLI, a desktop-app copy, the
	// extension in VS Code, the Store app.
	m := newMachine(t, "home/.vscode/extensions/anthropic.claude-code-2.1.332-win32-x64/package.json",
		"local/Packages/Claude_pzs8sxrjxfjjc/")
	m.vars["LOCALAPPDATA"] = filepath.Join(m.root, "local")
	m.path["code"] = `C:\VS\bin\code.cmd`
	res := claudebin.Result{Path: `C:\x\claude.exe`, Source: claudebin.SourceStore, Version: "2.1.330", Bundle: "the Claude desktop app (Microsoft Store)"}
	got := byID(Detect(m.env("windows", res, nil, true)))
	if c := got[Auto]; !c.Available || !c.Resume {
		t.Errorf("auto = %+v", c)
	}
	if c := got[CLI]; c.Available || c.Reason != ReasonOnlyBundled {
		t.Errorf("cli = %+v", c)
	}
	if c := got[VSCode]; !c.Available || c.Editor != "VS Code" || c.Label != "VS Code" || !c.Resume {
		t.Errorf("vscode = %+v", c)
	}
	if c := got[Desktop]; !c.Available || !c.Resume || c.Note != "" {
		t.Errorf("desktop = %+v (2.1.330 on windows/amd64 hands sessions over)", c)
	}
}

func TestDetectDesktopFolderOnlyAndEditorFamily(t *testing.T) {
	m := newMachine(t, "home/.cursor/extensions/anthropic.claude-code-2.1.300/package.json", "home/.config/Claude/claude-code/")
	m.vars["DISPLAY"] = ":0"
	res := claudebin.Result{Path: "/usr/local/bin/claude", Source: claudebin.SourcePath, Version: "2.1.400"}
	got := byID(Detect(m.env("linux", res, nil, true)))
	if c := got[CLI]; !c.Available {
		t.Errorf("cli = %+v", c)
	}
	if c := got[VSCode]; !c.Available || c.Label != "Cursor" || c.Editor != "Cursor" {
		t.Errorf("vscode = %+v, want Cursor", c)
	}
	// Linux: no `claude --desktop`, so the app only opens the folder.
	if c := got[Desktop]; !c.Available || c.Resume || c.Note != NoteDesktopFolder {
		t.Errorf("desktop = %+v", c)
	}
	// Isolated mode: the editor cannot see cwm's workspace.
	if c := byID(Detect(m.env("linux", res, nil, false)))[VSCode]; c.Available || c.Reason != ReasonIsolated {
		t.Errorf("isolated vscode = %+v", c)
	}
	// No graphical session.
	delete(m.vars, "DISPLAY")
	got = byID(Detect(m.env("linux", res, nil, true)))
	if c := got[VSCode]; c.Available || c.Reason != ReasonHeadless {
		t.Errorf("headless vscode = %+v", c)
	}
	if c := got[Desktop]; c.Available || c.Reason != ReasonHeadless {
		t.Errorf("headless desktop = %+v", c)
	}
}

func TestDesktopCanResume(t *testing.T) {
	cases := []struct {
		goos, arch, ver string
		want            bool
	}{
		{"darwin", "arm64", "2.1.285", true},
		{"darwin", "arm64", "2.1.284", false},
		{"windows", "amd64", "2.2.0", true},
		{"windows", "arm64", "2.2.0", false},
		{"linux", "amd64", "9.0.0", false},
		{"darwin", "arm64", "", false},
	}
	for _, c := range cases {
		if got := DesktopCanResume(c.goos, c.arch, c.ver); got != c.want {
			t.Errorf("%+v = %v", c, got)
		}
	}
}

func TestEditorCLIKnownLocations(t *testing.T) {
	m := newMachine(t, "home/.vscode/extensions/anthropic.claude-code-2.1.332/package.json",
		"local/Programs/Microsoft VS Code/bin/code.cmd")
	m.vars["LOCALAPPDATA"] = filepath.Join(m.root, "local")
	env := m.env("windows", claudebin.Result{}, nil, true)
	ed, ok := FindEditor(env)
	// The Windows-style suffix is joined with a backslash; on a POSIX test
	// host that is one odd file name, so only check the lookup ran.
	if !ok || ed.Scheme != "vscode" || ed.Name != "VS Code" {
		t.Fatalf("editor = %+v, %v", ed, ok)
	}
	m.path["code"] = "/usr/bin/code"
	if ed, _ := FindEditor(m.env("linux", claudebin.Result{}, nil, true)); ed.CLI != "/usr/bin/code" {
		t.Errorf("PATH code = %q", ed.CLI)
	}
}

func TestURIs(t *testing.T) {
	if got := SessionURI("vscode", "1f0e-ab"); got != "vscode://anthropic.claude-code/open?session=1f0e-ab" {
		t.Errorf("session uri = %s", got)
	}
	if got := SessionURI("cursor", ""); got != "cursor://anthropic.claude-code/open" {
		t.Errorf("new tab uri = %s", got)
	}
	if got := SessionURI("vscode", "a&b=c"); got != "vscode://anthropic.claude-code/open?session=a%26b%3Dc" {
		t.Errorf("escaped = %s", got)
	}
	if got := FolderURI("windows", "vscode", `C:\Users\u\repos\my app`); got != "vscode://file/C:/Users/u/repos/my%20app/" {
		t.Errorf("windows folder uri = %s", got)
	}
	if got := FolderURI("linux", "vscode", "/home/u/repos/a#b"); got != "vscode://file/home/u/repos/a%23b/" {
		t.Errorf("linux folder uri = %s", got)
	}
	if got := DesktopURI(`C:\r\x&y`); got != "claude://code/new?folder=C%3A%5Cr%5Cx%26y" {
		t.Errorf("desktop uri = %s", got)
	}
	if got := DesktopURI(""); got != "claude://code/new" {
		t.Errorf("desktop uri = %s", got)
	}
}

// TestStepsGolden pins the exact argv vectors per OS (no shell anywhere).
func TestStepsGolden(t *testing.T) {
	id := "0b9c2f4e-1111-2222-3333-444455556666"
	cases := []struct {
		name, goos string
		ed         Editor
		folder     string
		want       [][]string
	}{
		{"linux code", "linux", Editor{Name: "VS Code", CLI: "/usr/bin/code", Scheme: "vscode"}, "/home/u/repos/api",
			[][]string{{"/usr/bin/code", "/home/u/repos/api"}, {"xdg-open", "vscode://anthropic.claude-code/open?session=" + id}}},
		{"mac cursor", "darwin", Editor{Name: "Cursor", CLI: "/Applications/Cursor.app/Contents/Resources/app/bin/cursor", Scheme: "cursor"}, "/Users/u/api",
			[][]string{{"/Applications/Cursor.app/Contents/Resources/app/bin/cursor", "/Users/u/api"}, {"open", "cursor://anthropic.claude-code/open?session=" + id}}},
		{"windows code.cmd", "windows", Editor{Name: "VS Code", CLI: `C:\VS\bin\code.cmd`, Scheme: "vscode"}, `C:\r\api`,
			[][]string{{`C:\VS\bin\code.cmd`, `C:\r\api`}, {"rundll32.exe", "url.dll,FileProtocolHandler", "vscode://anthropic.claude-code/open?session=" + id}}},
		// A folder cmd.exe would mangle never reaches code.cmd: the folder
		// opens by URI instead.
		{"windows unsafe folder", "windows", Editor{Name: "VS Code", CLI: `C:\VS\bin\code.cmd`, Scheme: "vscode"}, `C:\r\a&b%x`,
			[][]string{{"rundll32.exe", "url.dll,FileProtocolHandler", "vscode://file/C:/r/a&b%25x/"}, {"rundll32.exe", "url.dll,FileProtocolHandler", "vscode://anthropic.claude-code/open?session=" + id}}},
		{"no cli", "linux", Editor{Name: "VSCodium", Scheme: "vscodium"}, "/srv/x",
			[][]string{{"xdg-open", "vscodium://file/srv/x/"}, {"xdg-open", "vscodium://anthropic.claude-code/open?session=" + id}}},
		{"no folder", "linux", Editor{Name: "VS Code", CLI: "/usr/bin/code", Scheme: "vscode"}, "",
			[][]string{{"xdg-open", "vscode://anthropic.claude-code/open?session=" + id}}},
	}
	for _, c := range cases {
		steps := EditorSteps(c.goos, c.ed, c.folder, id)
		var got [][]string
		for _, s := range steps {
			got = append(got, s.Argv)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
		if len(steps) > 1 && !steps[0].Wait || steps[len(steps)-1].Wait {
			t.Errorf("%s: the folder step waits, the URI does not: %+v", c.name, steps)
		}
	}
	for goos, want := range map[string][]string{
		"windows": {"rundll32.exe", "url.dll,FileProtocolHandler", "claude://code/new?folder=%2Fx"},
		"darwin":  {"open", "claude://code/new?folder=%2Fx"},
		"linux":   {"xdg-open", "claude://code/new?folder=%2Fx"},
	} {
		if got := OpenURL(goos, DesktopURI("/x")); !reflect.DeepEqual(got, want) {
			t.Errorf("%s desktop = %q", goos, got)
		}
	}
}

func TestRunStopsAtFirstError(t *testing.T) {
	var ran [][]string
	boom := errors.New("boom")
	err := Run([]Step{{Argv: []string{"a"}}, {Argv: []string{"b"}}, {Argv: []string{"c"}}}, func(s Step) error {
		ran = append(ran, s.Argv)
		if s.Argv[0] == "b" {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) || len(ran) != 2 {
		t.Errorf("err %v, ran %v", err, ran)
	}
}

func TestUnavailableMessages(t *testing.T) {
	c := Choice{ID: CLI, Label: "Claude Code CLI", Reason: ReasonOnlyBundled}
	if got := Unavailable(c, "windows").Error(); !strings.Contains(got, ReasonOnlyBundled) || !strings.Contains(got, "irm https://claude.ai/install.ps1 | iex") {
		t.Errorf("cli = %s", got)
	}
	c = Choice{ID: VSCode, Label: "VS Code", Reason: ReasonNoExtension}
	if got := Unavailable(c, "linux").Error(); got != "cannot open it with VS Code: "+ReasonNoExtension {
		t.Errorf("vscode = %s", got)
	}
}
