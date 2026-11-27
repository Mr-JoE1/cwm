// Package claudeopen decides which Claude opens a session (GitHub issue
// #3, D113): the `launch.claude` setting and the per-click override of the
// dashboard's Resume and Start buttons.
//
//   - auto: a terminal running the claude the resolver picks (doctor's
//     choice: the newest verified copy, standalone or bundled), by
//     absolute path, so no `claude` on PATH is needed;
//   - cli: the same, but only a standalone install;
//   - vscode: the session's folder in VS Code (or the VS Code-family
//     editor that holds the Claude Code extension), then the extension's
//     URI handler `<scheme>://anthropic.claude-code/open?session=<id>`
//     resumes it in a Claude Code tab (extension ≥ 2.1.72);
//   - desktop: the Claude desktop app. `claude --desktop --resume <id>`
//     hands a session over (Claude Code ≥ 2.1.285, macOS and x64 Windows);
//     elsewhere `claude://code/new?folder=<dir>` only opens the app in the
//     folder, and the option says so.
//
// Everything here builds exec argument vectors; nothing is ever joined
// into a shell or cmd.exe line, and every value that reaches a URI is
// escaped as a URI component.
package claudeopen

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/config"
)

// The launch.claude values.
const (
	Auto    = config.LaunchAuto
	CLI     = config.LaunchCLI
	VSCode  = config.LaunchVSCode
	Desktop = config.LaunchDesktop
)

// Options are the launch.claude values in display order.
var Options = config.LaunchChoices

// Valid reports whether s is one of Options.
func Valid(s string) bool { return slices.Contains(Options, s) }

// Effective is the option a launch uses: the per-click override, else the
// setting, else auto.
func Effective(override, setting string) string {
	if Valid(override) {
		return override
	}
	if Valid(setting) {
		return setting
	}
	return Auto
}

// Choice is one option as the dashboard and `cwm sessions resume` see it.
type Choice struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Help      string `json:"help"`
	Available bool   `json:"available"`
	// Reason says why an option cannot work on this machine.
	Reason string `json:"reason,omitempty"`
	// Resume: the option reopens the session itself, not only its folder.
	Resume bool `json:"resume"`
	// Note qualifies an available option ("it can't resume this session
	// directly").
	Note string `json:"note,omitempty"`
	// Editor names the editor the vscode option opens ("Cursor").
	Editor string `json:"editor,omitempty"`
}

// Env is the machine as detection sees it; tests fake every field.
type Env struct {
	GOOS, GOARCH string
	Getenv       func(string) string
	Glob         func(string) ([]string, error)
	Stat         func(string) (fs.FileInfo, error)
	LookPath     func(string) (string, error)
	Home         string
	// Claude is the resolver's answer (doctor's choice).
	Claude func() (claudebin.Result, error)
	// Capture: sessions live in ~/.claude, where the editor extension and
	// the desktop app read them (capture mode).
	Capture bool
}

// SystemEnv is the real machine.
func SystemEnv(claude func() (claudebin.Result, error), capture bool) Env {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		if p := os.Getenv("USERPROFILE"); p != "" {
			home = p
		}
	}
	return Env{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Getenv: os.Getenv, Glob: filepath.Glob, Stat: os.Stat,
		LookPath: exec.LookPath, Home: home, Claude: claude, Capture: capture}
}

// Reasons and notes, shown next to an option (tests pin them).
const (
	ReasonNoClaude     = "Claude Code not found on this machine"
	ReasonOnlyBundled  = "no standalone Claude Code CLI found (only copies bundled with an app)"
	ReasonNoStandalone = "no standalone Claude Code CLI found"
	ReasonNoExtension  = "the Claude Code extension is not installed (VS Code, Cursor, Windsurf, VSCodium)"
	ReasonIsolated     = "needs capture mode: the editor reads sessions from ~/.claude"
	ReasonHeadless     = "no graphical session"
	ReasonNoDesktop    = "the Claude desktop app is not installed"
	NoteDesktopFolder  = "opens the Claude app in the session's folder (it can't resume this session directly)"
)

// DesktopMinVersion is the first Claude Code with `--desktop` (handing a
// CLI session to the desktop app).
const DesktopMinVersion = "2.1.285"

// Detect lists every option with whether it can work here.
func Detect(env Env) []Choice {
	var res claudebin.Result
	var cerr error = claudebin.ErrNotFound
	if env.Claude != nil {
		res, cerr = env.Claude()
	}
	gui := env.GOOS == "windows" || env.GOOS == "darwin" || env.Getenv("DISPLAY") != "" || env.Getenv("WAYLAND_DISPLAY") != ""
	out := []Choice{
		{ID: Auto, Label: "Auto", Resume: true,
			Help: "A terminal running the newest Claude Code on this machine (standalone or bundled with an app), by full path, the one `cwm doctor` shows. No claude on PATH needed."},
		{ID: CLI, Label: "Claude Code CLI", Resume: true,
			Help: "A terminal running a standalone Claude Code install only, never a copy bundled with an app."},
		{ID: VSCode, Label: "VS Code", Resume: true,
			Help: "Opens the session's folder in VS Code (or the editor with the Claude Code extension) and resumes it in a Claude Code tab."},
		{ID: Desktop, Label: "Claude desktop app",
			Help: "Hands the session to the Claude desktop app (`claude --desktop --resume`)."},
	}
	set := func(i int, ok bool, reason string) {
		out[i].Available = ok
		if !ok {
			out[i].Reason = reason
		}
	}
	set(0, cerr == nil, ReasonNoClaude)
	switch {
	case cerr != nil:
		set(1, false, ReasonNoStandalone)
	case res.Source.Bundled():
		set(1, false, ReasonOnlyBundled)
	default:
		set(1, true, "")
	}
	ed, edOK := FindEditor(env)
	switch {
	case !edOK:
		set(2, false, ReasonNoExtension)
	case !env.Capture:
		set(2, false, ReasonIsolated)
	case !gui:
		set(2, false, ReasonHeadless)
	default:
		set(2, true, "")
		if ed.Name != "VS Code" {
			out[2].Label = ed.Name
		}
		out[2].Editor = ed.Name
	}
	switch {
	case !DesktopInstalled(env):
		set(3, false, ReasonNoDesktop)
	case !gui:
		set(3, false, ReasonHeadless)
	default:
		set(3, true, "")
		out[3].Resume = cerr == nil && DesktopCanResume(env.GOOS, env.GOARCH, res.Version)
		if !out[3].Resume {
			out[3].Note = NoteDesktopFolder
		}
	}
	return out
}

// Pick returns the choice for id from list.
func Pick(list []Choice, id string) (Choice, bool) {
	for _, c := range list {
		if c.ID == id {
			return c, true
		}
	}
	return Choice{}, false
}

// Unavailable is the error for an option that cannot work here; cli adds
// the install command.
func Unavailable(c Choice, goos string) error {
	msg := fmt.Sprintf("cannot open it with %s: %s", c.Label, c.Reason)
	if c.ID == CLI || c.ID == Auto {
		msg += " — install it with: " + claudebin.InstallCommand(goos)
	}
	return errors.New(msg)
}

// Editor is a VS Code-family editor that holds the Claude Code extension.
type Editor struct {
	Name string
	// CLI is the editor's command-line launcher ("" = not found; the
	// folder then opens through the editor's own URI scheme).
	CLI    string
	Scheme string
}

// editors are the VS Code family: the extensions dir under the home
// folder, the CLI name, the URI scheme and well-known CLI locations.
var editors = []struct {
	dir, name, cli, scheme string
	win, mac               []string
}{
	{".vscode", "VS Code", "code", "vscode",
		[]string{`LOCALAPPDATA\Programs\Microsoft VS Code\bin\code.cmd`, `ProgramFiles\Microsoft VS Code\bin\code.cmd`},
		[]string{"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"}},
	{".vscode-insiders", "VS Code Insiders", "code-insiders", "vscode-insiders",
		[]string{`LOCALAPPDATA\Programs\Microsoft VS Code Insiders\bin\code-insiders.cmd`},
		[]string{"/Applications/Visual Studio Code - Insiders.app/Contents/Resources/app/bin/code"}},
	{".cursor", "Cursor", "cursor", "cursor",
		[]string{`LOCALAPPDATA\Programs\cursor\resources\app\bin\cursor.cmd`},
		[]string{"/Applications/Cursor.app/Contents/Resources/app/bin/cursor"}},
	{".windsurf", "Windsurf", "windsurf", "windsurf",
		[]string{`LOCALAPPDATA\Programs\Windsurf\bin\windsurf.cmd`},
		[]string{"/Applications/Windsurf.app/Contents/Resources/app/bin/windsurf"}},
	{".vscode-oss", "VSCodium", "codium", "vscodium",
		[]string{`LOCALAPPDATA\Programs\VSCodium\bin\codium.cmd`},
		[]string{"/Applications/VSCodium.app/Contents/Resources/app/bin/codium"}},
}

// FindEditor returns the first editor (VS Code first) whose extensions
// folder holds the Claude Code extension.
func FindEditor(env Env) (Editor, bool) {
	if env.Home == "" || env.Glob == nil {
		return Editor{}, false
	}
	for _, e := range editors {
		m, err := env.Glob(filepath.Join(env.Home, e.dir, "extensions", "anthropic.claude-code-*"))
		if err != nil || len(m) == 0 {
			continue
		}
		return Editor{Name: e.name, CLI: editorCLI(env, e.cli, e.win, e.mac), Scheme: e.scheme}, true
	}
	return Editor{}, false
}

func editorCLI(env Env, name string, win, mac []string) string {
	if env.LookPath != nil {
		if p, err := env.LookPath(name); err == nil {
			return p
		}
	}
	var known []string
	switch env.GOOS {
	case "windows":
		for _, w := range win {
			v, rest, _ := strings.Cut(w, `\`)
			if base := env.Getenv(v); base != "" {
				known = append(known, base+`\`+rest)
			}
		}
	case "darwin":
		known = mac
	}
	for _, p := range known {
		if fi, err := env.stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

func (env Env) stat(p string) (fs.FileInfo, error) {
	if env.Stat == nil {
		return nil, fs.ErrNotExist
	}
	return env.Stat(p)
}

// DesktopInstalled looks for the Claude desktop app without running
// anything (Get-AppxPackage takes seconds).
func DesktopInstalled(env Env) bool {
	var dirs, globs []string
	switch env.GOOS {
	case "windows":
		if l := env.Getenv("LOCALAPPDATA"); l != "" {
			dirs = append(dirs, filepath.Join(l, "AnthropicClaude"))
			globs = append(globs, filepath.Join(l, "Packages", "Claude_*"), filepath.Join(l, "Claude-Data", "claude_*"))
		}
	case "darwin":
		dirs = append(dirs, "/Applications/Claude.app")
		if env.Home != "" {
			dirs = append(dirs, filepath.Join(env.Home, "Applications", "Claude.app"))
		}
	default:
		if env.Home != "" {
			dirs = append(dirs, filepath.Join(env.Home, ".config", "Claude", "claude-code"))
		}
	}
	for _, d := range dirs {
		if fi, err := env.stat(d); err == nil && fi.IsDir() {
			return true
		}
	}
	for _, g := range globs {
		if env.Glob == nil {
			break
		}
		if m, err := env.Glob(g); err == nil && len(m) > 0 {
			return true
		}
	}
	return false
}

var verRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

// DesktopCanResume: `claude --desktop --resume` exists in this Claude Code
// on this platform (macOS and x64 Windows only).
func DesktopCanResume(goos, goarch, version string) bool {
	if !(goos == "darwin" || goos == "windows" && goarch == "amd64") {
		return false
	}
	return versionAtLeast(version, DesktopMinVersion)
}

func versionAtLeast(v, min string) bool {
	a, b := verRe.FindStringSubmatch(v), verRe.FindStringSubmatch(min)
	if a == nil || b == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		var x, y int
		fmt.Sscan(a[i], &x)
		fmt.Sscan(b[i], &y)
		if x != y {
			return x > y
		}
	}
	return true
}

// SessionURI is the extension's handler URI: resume id, or open a new
// Claude Code tab when id is "".
func SessionURI(scheme, id string) string {
	u := scheme + "://anthropic.claude-code/open"
	if id != "" {
		u += "?session=" + url.QueryEscape(id)
	}
	return u
}

// FolderURI opens folder through the editor's own scheme
// (<scheme>://file/<path>/), the fallback when its CLI is not found.
func FolderURI(goos, scheme, folder string) string {
	p := folder
	if goos == "windows" {
		p = strings.ReplaceAll(p, `\`, "/")
	}
	p = "/" + strings.Trim(p, "/") + "/"
	return (&url.URL{Scheme: scheme, Host: "file", Path: p}).String()
}

// DesktopURI opens the desktop app on a new Code session, in folder when
// it is set (the app asks to confirm a folder from a link).
func DesktopURI(folder string) string {
	if folder == "" {
		return "claude://code/new"
	}
	return "claude://code/new?folder=" + url.QueryEscape(folder)
}

// OpenURL is the OS URL opener as an argv: rundll32's FileProtocolHandler
// on Windows (no cmd.exe, so nothing in the URI is a metacharacter),
// open on macOS, xdg-open elsewhere.
func OpenURL(goos, uri string) []string {
	switch goos {
	case "windows":
		return []string{"rundll32.exe", "url.dll,FileProtocolHandler", uri}
	case "darwin":
		return []string{"open", uri}
	}
	return []string{"xdg-open", uri}
}

// cmdUnsafe are the characters cmd.exe expands or splits on: a .cmd
// launcher (code.cmd) runs through cmd.exe, so a folder holding one goes
// through the URI route instead.
const cmdUnsafe = "\"%!^&|<>()"

// Step is one program to start; Wait: let it finish (bounded) before the
// next step, so the URI reaches a window that is already open.
type Step struct {
	Argv []string
	Wait bool
}

// EditorSteps opens folder in the editor ("" = no folder), then resumes
// session id in its Claude Code tab ("" = a new tab).
func EditorSteps(goos string, ed Editor, folder, id string) []Step {
	var steps []Step
	if folder != "" {
		cli := ed.CLI
		ext := strings.ToLower(filepath.Ext(cli))
		if cli != "" && goos == "windows" && (ext == ".cmd" || ext == ".bat") && strings.ContainsAny(folder, cmdUnsafe) {
			cli = ""
		}
		if cli != "" {
			steps = append(steps, Step{Argv: []string{cli, folder}, Wait: true})
		} else {
			steps = append(steps, Step{Argv: OpenURL(goos, FolderURI(goos, ed.Scheme, folder)), Wait: true})
		}
	}
	return append(steps, Step{Argv: OpenURL(goos, SessionURI(ed.Scheme, id))})
}
