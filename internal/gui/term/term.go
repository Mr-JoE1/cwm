// Package term opens a real terminal window running a cwm command (D27).
// Claude Code and `claude auth login` are interactive TUIs, so the GUI
// never hosts them: it hands an exec argument array to the platform's
// terminal emulator instead. The argv is never joined into a shell string —
// every element travels as its own exec argument (on Windows' cmd.exe path,
// as an individually quoted argument), and elements that could be
// reinterpreted by an intermediate parser are refused.
package term

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrNoTerminal means no usable terminal emulator exists (none on PATH, a
// headless session, or an unsupported OS). The GUI answers 501 with the
// copyable command so the user can run it themselves.
var ErrNoTerminal = errors.New("no terminal found")

// HoldEnv is set on the spawned command's environment; cwm reads it to keep
// the window open on failure so an error is never a flash-and-gone window.
const HoldEnv = "CWM_HOLD_ON_ERROR"

// Launcher opens a terminal window running argv (argv[0] = the program).
type Launcher interface {
	Open(argv []string) error
}

// Env is the platform view Resolve consults; tests inject every field.
type Env struct {
	GOOS     string
	LookPath func(file string) (string, error)
	Getenv   func(key string) string
}

// SystemEnv is the real platform view.
func SystemEnv() Env {
	return Env{GOOS: runtime.GOOS, LookPath: exec.LookPath, Getenv: os.Getenv}
}

// Spec is a resolved terminal invocation: exec Path with Args (Args
// excludes the program itself). CmdLine, when set (Windows cmd.exe path
// only), is the exact raw command line to hand to CreateProcess.
type Spec struct {
	Path    string
	Args    []string
	CmdLine string
}

// candidate is a terminal emulator and the flag(s) that precede the
// command it should run.
type candidate struct {
	bin    string
	prefix []string
}

// linuxCandidates is the D27 search order after $TERMINAL. The Debian
// x-terminal-emulator alternative is required to accept -e.
var linuxCandidates = []candidate{
	{"x-terminal-emulator", []string{"-e"}},
	{"gnome-terminal", []string{"--"}},
	{"konsole", []string{"-e"}},
	{"xfce4-terminal", []string{"-x"}},
	{"alacritty", []string{"-e"}},
	{"kitty", nil},
	{"xterm", []string{"-e"}},
}

// Resolve picks the terminal for env and builds the invocation for argv.
func Resolve(env Env, argv []string) (Spec, error) {
	if err := checkArgv(argv); err != nil {
		return Spec{}, err
	}
	switch env.GOOS {
	case "windows":
		return resolveWindows(env, argv)
	case "darwin":
		return resolveDarwin(env, argv)
	default:
		return resolveUnix(env, argv)
	}
}

// checkArgv refuses empty argv and control characters (newlines would
// split commands in several terminal/cmd parsers).
func checkArgv(argv []string) error {
	if len(argv) == 0 || argv[0] == "" {
		return errors.New("term: empty command")
	}
	for _, a := range argv {
		for _, r := range a {
			if r < 0x20 || r == 0x7f {
				return fmt.Errorf("term: command argument contains a control character")
			}
		}
	}
	return nil
}

func resolveUnix(env Env, argv []string) (Spec, error) {
	if env.Getenv("DISPLAY") == "" && env.Getenv("WAYLAND_DISPLAY") == "" {
		return Spec{}, fmt.Errorf("%w (headless session: neither DISPLAY nor WAYLAND_DISPLAY is set)", ErrNoTerminal)
	}
	if t := strings.TrimSpace(env.Getenv("TERMINAL")); t != "" {
		// $TERMINAL is one program name/path, never split into words. A
		// known emulator keeps its own flag; anything else gets the
		// conventional -e.
		if path, err := env.LookPath(t); err == nil {
			prefix := []string{"-e"}
			base := filepath.Base(t)
			for _, c := range linuxCandidates {
				if c.bin == base {
					prefix = c.prefix
				}
			}
			return Spec{Path: path, Args: concat(prefix, argv)}, nil
		}
	}
	for _, c := range linuxCandidates {
		if path, err := env.LookPath(c.bin); err == nil {
			return Spec{Path: path, Args: concat(c.prefix, argv)}, nil
		}
	}
	return Spec{}, fmt.Errorf("%w (none of $TERMINAL, x-terminal-emulator, gnome-terminal, konsole, xfce4-terminal, alacritty, kitty, xterm on PATH)", ErrNoTerminal)
}

// resolveDarwin drives Terminal.app through osascript (D36). Terminal only
// accepts a command string, so the string is built — never from raw input
// — by POSIX-single-quoting every argv element and then escaping the
// whole line as an AppleScript string literal. checkArgv already refused
// control characters, the only thing neither layer can carry.
func resolveDarwin(env Env, argv []string) (Spec, error) {
	path, err := env.LookPath("osascript")
	if err != nil {
		return Spec{}, fmt.Errorf("%w (osascript not on PATH)", ErrNoTerminal)
	}
	return Spec{Path: path, Args: []string{
		"-e", `tell application "Terminal" to do script ` + AppleScriptString(DarwinCommand(argv)),
		"-e", `tell application "Terminal" to activate`,
	}}, nil
}

// DarwinCommand is the shell line Terminal runs: HoldEnv set through
// env(1) (Terminal's new shell does not inherit our environment), then
// each argv element as one single-quoted shell word.
func DarwinCommand(argv []string) string {
	words := make([]string, 0, len(argv)+2)
	words = append(words, "env", HoldEnv+"=1")
	for _, a := range argv {
		words = append(words, shellQuote(a))
	}
	return strings.Join(words, " ")
}

// AppleScriptString renders s as an AppleScript string literal: only the
// backslash and the double quote need escaping inside "...".
func AppleScriptString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func resolveWindows(env Env, argv []string) (Spec, error) {
	// wt.exe treats ';' as its own command separator, so such argv goes
	// the cmd.exe route instead.
	if !anyContains(argv, ";") {
		if path, err := env.LookPath("wt.exe"); err == nil {
			return Spec{Path: path, Args: concat([]string{"--"}, argv)}, nil
		}
	}
	path, err := env.LookPath("cmd.exe")
	if err != nil {
		return Spec{}, fmt.Errorf("%w (neither wt.exe nor cmd.exe on PATH)", ErrNoTerminal)
	}
	var b strings.Builder
	// The literal "cwm" is start's window title: without a quoted title,
	// start would treat a quoted program path as the title.
	b.WriteString(`cmd.exe /c start "cwm"`)
	for _, a := range argv {
		q, err := cmdQuote(a)
		if err != nil {
			return Spec{}, err
		}
		b.WriteByte(' ')
		b.WriteString(q)
	}
	return Spec{Path: path, Args: concat([]string{"/c", "start", "cwm"}, argv), CmdLine: b.String()}, nil
}

// cmdQuote quotes one argument for a cmd.exe command line: EscapeArg
// quoting, forced quotes around cmd metacharacters, and outright refusal
// of characters cmd.exe expands even inside quotes.
func cmdQuote(a string) (string, error) {
	if strings.ContainsAny(a, "\"%!") {
		return "", fmt.Errorf("term: argument %q contains a character cmd.exe cannot pass safely", a)
	}
	q := escapeArg(a)
	if !strings.HasPrefix(q, `"`) && strings.ContainsAny(a, "^&|<>()") {
		// No '"' in a (refused above), so only trailing backslashes need
		// doubling to keep the closing quote literal.
		trail := len(a) - len(strings.TrimRight(a, `\`))
		q = `"` + a + strings.Repeat(`\`, trail) + `"`
	}
	return q, nil
}

// Display renders argv as one copyable command line for goos — for the
// user to read and paste, never executed by cwm.
func Display(goos string, argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if goos == "windows" {
			parts[i] = escapeArg(a)
		} else {
			parts[i] = shellQuote(a)
		}
	}
	return strings.Join(parts, " ")
}

// shellQuote single-quotes a POSIX shell word unless it is plainly safe.
func shellQuote(a string) string {
	if a == "" {
		return "''"
	}
	safe := true
	for _, r := range a {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r)) {
			safe = false
			break
		}
	}
	if safe {
		return a
	}
	return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
}

// EscapeWindowsArg quotes one argument by the Windows command-line rules
// (CommandLineToArgvW-compatible), on every OS — for generating command
// lines that run on Windows later (Task Scheduler, the Run key).
func EscapeWindowsArg(s string) string { return portableEscapeArg(s) }

// escapeArg is syscall.EscapeArg on Windows (wired in term_windows.go);
// elsewhere the portable copy below keeps Resolve testable on every OS.
var escapeArg = portableEscapeArg

// portableEscapeArg mirrors syscall.EscapeArg (Windows command-line
// quoting rules, CommandLineToArgvW-compatible).
func portableEscapeArg(s string) string {
	if s == "" {
		return `""`
	}
	needsBackslash, hasSpace := false, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\\':
			needsBackslash = true
		case ' ', '\t':
			hasSpace = true
		}
	}
	if !needsBackslash && !hasSpace {
		return s
	}
	if !needsBackslash {
		return `"` + s + `"`
	}
	var b []byte
	if hasSpace {
		b = append(b, '"')
	}
	slashes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		default:
			slashes = 0
		case '\\':
			slashes++
		case '"':
			for ; slashes > 0; slashes-- {
				b = append(b, '\\')
			}
			b = append(b, '\\')
		}
		b = append(b, c)
	}
	if hasSpace {
		for ; slashes > 0; slashes-- {
			b = append(b, '\\')
		}
		b = append(b, '"')
	}
	return string(b)
}

func concat(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	return append(append(out, a...), b...)
}

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// System is the real Launcher: Resolve against the live platform, then
// start the terminal detached (it outlives `cwm gui` and never receives
// the dashboard's Ctrl-C).
type System struct {
	env   Env
	start func(Spec) error
}

// NewSystem returns the platform Launcher.
func NewSystem() *System {
	return &System{env: SystemEnv(), start: startDetached}
}

// Open resolves and starts the terminal; ErrNoTerminal is wrapped through.
func (s *System) Open(argv []string) error {
	spec, err := Resolve(s.env, argv)
	if err != nil {
		return err
	}
	if err := s.start(spec); err != nil {
		return fmt.Errorf("term: start %s: %w", filepath.Base(spec.Path), err)
	}
	return nil
}

// command builds the exec.Cmd shared by both platform start files: no
// stdio (the terminal owns its own), HoldEnv set for the child cwm.
func command(spec Spec) *exec.Cmd {
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Env = append(os.Environ(), HoldEnv+"=1")
	return cmd
}
