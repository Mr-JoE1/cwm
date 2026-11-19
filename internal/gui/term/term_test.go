package term

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnv resolves only the binaries in onPath (name → path).
func fakeEnv(goos string, onPath map[string]string, vars map[string]string) Env {
	return Env{
		GOOS: goos,
		LookPath: func(file string) (string, error) {
			if p, ok := onPath[file]; ok {
				return p, nil
			}
			return "", exec.ErrNotFound
		},
		Getenv: func(k string) string { return vars[k] },
	}
}

var display = map[string]string{"DISPLAY": ":0"}

func argvOf(s Spec) string {
	return strings.Join(append([]string{s.Path}, s.Args...), "|")
}

func TestResolveLinuxCandidateOrder(t *testing.T) {
	argv := []string{"/opt/cwm/cwm", "account", "add", "work"}
	tail := "|/opt/cwm/cwm|account|add|work"
	all := map[string]string{
		"x-terminal-emulator": "/usr/bin/x-terminal-emulator",
		"gnome-terminal":      "/usr/bin/gnome-terminal",
		"konsole":             "/usr/bin/konsole",
		"xfce4-terminal":      "/usr/bin/xfce4-terminal",
		"alacritty":           "/usr/bin/alacritty",
		"kitty":               "/usr/bin/kitty",
		"xterm":               "/usr/bin/xterm",
	}
	// Remove candidates one by one from the front: each next one wins.
	want := []string{
		"/usr/bin/x-terminal-emulator|-e" + tail,
		"/usr/bin/gnome-terminal|--" + tail,
		"/usr/bin/konsole|-e" + tail,
		"/usr/bin/xfce4-terminal|-x" + tail,
		"/usr/bin/alacritty|-e" + tail,
		"/usr/bin/kitty" + tail,
		"/usr/bin/xterm|-e" + tail,
	}
	order := []string{"x-terminal-emulator", "gnome-terminal", "konsole", "xfce4-terminal", "alacritty", "kitty", "xterm"}
	for i, w := range want {
		path := map[string]string{}
		for _, name := range order[i:] {
			path[name] = all[name]
		}
		got, err := Resolve(fakeEnv("linux", path, display), argv)
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if argvOf(got) != w {
			t.Errorf("step %d: got %s, want %s", i, argvOf(got), w)
		}
		if got.CmdLine != "" {
			t.Errorf("linux spec must not carry a raw command line: %q", got.CmdLine)
		}
	}
}

func TestResolveLinuxTerminalEnv(t *testing.T) {
	argv := []string{"cwm", "open", "p"}
	path := map[string]string{"foot": "/usr/bin/foot", "xterm": "/usr/bin/xterm", "gnome-terminal": "/usr/bin/gnome-terminal"}
	vars := map[string]string{"WAYLAND_DISPLAY": "wayland-0", "TERMINAL": "foot"}
	got, err := Resolve(fakeEnv("linux", path, vars), argv)
	if err != nil || argvOf(got) != "/usr/bin/foot|-e|cwm|open|p" {
		t.Fatalf("$TERMINAL=foot: %s, %v", argvOf(got), err)
	}
	// A known emulator in $TERMINAL keeps its own flag.
	vars["TERMINAL"] = "gnome-terminal"
	got, _ = Resolve(fakeEnv("linux", path, vars), argv)
	if argvOf(got) != "/usr/bin/gnome-terminal|--|cwm|open|p" {
		t.Errorf("$TERMINAL=gnome-terminal: %s", argvOf(got))
	}
	// $TERMINAL is one program, never split into shell words; a missing
	// one falls through to the candidate list.
	vars["TERMINAL"] = "foot; rm -rf ~"
	got, _ = Resolve(fakeEnv("linux", path, vars), argv)
	if argvOf(got) != "/usr/bin/gnome-terminal|--|cwm|open|p" {
		t.Errorf("bogus $TERMINAL: %s", argvOf(got))
	}
}

func TestResolveNoTerminal(t *testing.T) {
	argv := []string{"cwm", "account", "add", "x"}
	cases := map[string]Env{
		"linux nothing on PATH":    fakeEnv("linux", nil, display),
		"linux headless":           fakeEnv("linux", map[string]string{"xterm": "/usr/bin/xterm"}, nil),
		"darwin without osascript": fakeEnv("darwin", map[string]string{"open": "/usr/bin/open"}, display),
		"windows no cmd":           fakeEnv("windows", nil, nil),
	}
	for name, env := range cases {
		if _, err := Resolve(env, argv); !errors.Is(err, ErrNoTerminal) {
			t.Errorf("%s: err = %v, want ErrNoTerminal", name, err)
		}
	}
}

func TestResolveWindows(t *testing.T) {
	argv := []string{`C:\Program Files\cwm\cwm.exe`, "open", "proj", "new", "--resume", "abc-123"}
	wt := map[string]string{"wt.exe": `C:\WindowsApps\wt.exe`, "cmd.exe": `C:\Windows\System32\cmd.exe`}
	got, err := Resolve(fakeEnv("windows", wt, nil), argv)
	if err != nil {
		t.Fatal(err)
	}
	if argvOf(got) != `C:\WindowsApps\wt.exe|--|C:\Program Files\cwm\cwm.exe|open|proj|new|--resume|abc-123` || got.CmdLine != "" {
		t.Errorf("wt route: %s (cmdline %q)", argvOf(got), got.CmdLine)
	}

	cmdOnly := map[string]string{"cmd.exe": `C:\Windows\System32\cmd.exe`}
	got, err = Resolve(fakeEnv("windows", cmdOnly, nil), argv)
	if err != nil {
		t.Fatal(err)
	}
	wantLine := `cmd.exe /c start "cwm" "C:\Program Files\cwm\cwm.exe" open proj new --resume abc-123`
	if got.CmdLine != wantLine {
		t.Errorf("cmd route line:\n got %s\nwant %s", got.CmdLine, wantLine)
	}
	if got.Path != `C:\Windows\System32\cmd.exe` {
		t.Errorf("cmd route path = %s", got.Path)
	}

	// ';' would split a wt command line: such argv takes the cmd route.
	got, err = Resolve(fakeEnv("windows", wt, nil), []string{`C:\a;b\cwm.exe`, "x"})
	if err != nil || got.CmdLine == "" {
		t.Errorf("';' argv should use cmd.exe: %+v %v", got, err)
	}

	// cmd metacharacters are forced into quotes; % ! " are refused.
	got, _ = Resolve(fakeEnv("windows", cmdOnly, nil), []string{`C:\R&D\cwm.exe`, "x"})
	if !strings.Contains(got.CmdLine, `"C:\R&D\cwm.exe" x`) {
		t.Errorf("metachar arg not quoted: %s", got.CmdLine)
	}
	for _, bad := range []string{"%PATH%", "a!b", `a"b`} {
		if _, err := Resolve(fakeEnv("windows", cmdOnly, nil), []string{"cwm", bad}); err == nil || errors.Is(err, ErrNoTerminal) {
			t.Errorf("cmd route accepted %q (err %v)", bad, err)
		}
	}
}

func TestResolveRejectsBadArgv(t *testing.T) {
	env := fakeEnv("linux", map[string]string{"xterm": "/usr/bin/xterm"}, display)
	for _, argv := range [][]string{nil, {""}, {"cwm", "a\nb"}, {"cwm", "a\x00"}} {
		if _, err := Resolve(env, argv); err == nil {
			t.Errorf("Resolve accepted %q", argv)
		}
	}
}

func TestEscapeArgAndDisplay(t *testing.T) {
	cases := map[string]string{
		"":                   `""`,
		"plain":              "plain",
		"two words":          `"two words"`,
		`C:\dir\`:            `C:\dir\`,
		`C:\my dir\`:         `"C:\my dir\\"`,
		`say "hi"`:           `"say \"hi\""`,
		`back\"slash`:        `back\\\"slash`,
		"tab\there":          "\"tab\there\"",
		`C:\Program Files\x`: `"C:\Program Files\x"`,
	}
	for in, want := range cases {
		if got := portableEscapeArg(in); got != want {
			t.Errorf("portableEscapeArg(%q) = %s, want %s", in, got, want)
		}
	}
	if got := Display("linux", []string{"/home/u/bin/cwm", "open", "it's", "a b", ""}); got != `/home/u/bin/cwm open 'it'\''s' 'a b' ''` {
		t.Errorf("posix display = %s", got)
	}
	if got := Display("windows", []string{`C:\Program Files\cwm.exe`, "account", "add", "work"}); got != `"C:\Program Files\cwm.exe" account add work` {
		t.Errorf("windows display = %s", got)
	}
}

func TestSystemOpenUsesResolvedSpec(t *testing.T) {
	var started []Spec
	s := &System{
		env:   fakeEnv("linux", map[string]string{"xterm": "/usr/bin/xterm"}, display),
		start: func(sp Spec) error { started = append(started, sp); return nil },
	}
	if err := s.Open([]string{"cwm", "account", "add", "w"}); err != nil {
		t.Fatal(err)
	}
	if len(started) != 1 || argvOf(started[0]) != "/usr/bin/xterm|-e|cwm|account|add|w" {
		t.Errorf("started = %+v", started)
	}
	s.env = fakeEnv("linux", nil, display)
	if err := s.Open([]string{"cwm"}); !errors.Is(err, ErrNoTerminal) || len(started) != 1 {
		t.Errorf("no terminal: err = %v, started = %d", err, len(started))
	}
	s.env = fakeEnv("linux", map[string]string{"xterm": "/usr/bin/xterm"}, display)
	s.start = func(Spec) error { return errors.New("exec format error") }
	if err := s.Open([]string{"cwm"}); err == nil || errors.Is(err, ErrNoTerminal) || !strings.Contains(err.Error(), "xterm") {
		t.Errorf("start failure: %v", err)
	}
}

// D52: the terminal emulator's own output goes to the launch log (header
// per launch, start errors too); without logs/ nothing is created.
func TestSystemOpenLogsTerminalOutput(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "logs", "terminal.log")
	s := &System{
		env: fakeEnv("linux", map[string]string{"xterm": "/usr/bin/xterm"}, display),
		start: func(sp Spec) error {
			if sp.log == nil {
				return nil // no logs/ dir yet
			}
			_, err := sp.log.WriteString("xterm: cannot open display\n")
			return err
		},
		LogPath: func() string { return logPath },
	}
	if err := s.Open([]string{"cwm", "account", "add", "w"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(logPath)); !os.IsNotExist(err) {
		t.Fatalf("logs/ must not be created by a launch: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Open([]string{"cwm", "account", "add", "w"}); err != nil {
		t.Fatal(err)
	}
	s.start = func(Spec) error { return errors.New("exec format error") }
	_ = s.Open([]string{"cwm", "open", "x"})
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, w := range []string{"open xterm for: cwm account add w\n", "xterm: cannot open display\n", "open xterm for: cwm open x\n  start failed: exec format error\n"} {
		if !strings.Contains(got, w) {
			t.Errorf("log lacks %q:\n%s", w, got)
		}
	}
}
