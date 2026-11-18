// Package service installs the per-user autostart for the cwm agent
// (v0.6, D34): a systemd --user unit plus an XDG autostart opener on
// Linux, a Task Scheduler logon task on Windows, LaunchAgents on macOS.
// Never system-wide, never admin/sudo.
//
// Everything is split into a pure planning step — Build* returns the files
// (rendered from text/templates) and the commands to run — and Apply, which
// writes/runs them through the injected Runner or only prints them (dry
// run). Tests assert plans and fake-runner calls; they never run real
// systemctl / schtasks / launchctl / reg.
package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Runner runs one external command and returns its combined output.
type Runner interface {
	Run(name string, args ...string) (string, error)
}

// ExecRunner is the real Runner.
type ExecRunner struct{}

// Run executes name with args (no shell) and returns combined output.
func (ExecRunner) Run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// Env is the platform view every plan is built from; tests fill each field.
type Env struct {
	GOOS    string
	Home    string // the user's home directory
	Root    string // the cwm home (server.toml, logs)
	CwmHome string // $CWM_HOME at install time ("" = default), baked into units
	Exe     string // absolute path of the cwm binary
	UID     int    // macOS launchctl gui/<uid> domain
	UserID  string // Windows DOMAIN\user for the logon trigger
	// XDGConfig / XDGRuntime are $XDG_CONFIG_HOME / $XDG_RUNTIME_DIR ("" =
	// defaults ~/.config and /run/user/<uid>).
	XDGConfig  string
	XDGRuntime string
	LookPath   func(string) (string, error)
	Stat       func(string) (os.FileInfo, error)
	Runner     Runner
	Out        io.Writer
	DryRun     bool
}

// Options select what Install sets up.
type Options struct {
	NoOpen   bool   // do not open the browser at login
	OpenOnly bool   // only the browser opener for URL (Docker host)
	URL      string // with OpenOnly
}

// File is one generated file.
type File struct {
	Path    string
	Content []byte // UTF-8 text
	Mode    os.FileMode
	UTF16   bool // written as UTF-16LE with BOM (Task Scheduler XML)
}

// Bytes is the on-disk form of the file.
func (f File) Bytes() []byte {
	if !f.UTF16 {
		return f.Content
	}
	u := utf16.Encode([]rune(string(f.Content)))
	b := make([]byte, 2, 2+2*len(u))
	b[0], b[1] = 0xFF, 0xFE
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

// Command is one external command. IgnoreError marks best-effort steps
// (e.g. removing something that may not exist); Fallback runs instead when
// the command fails.
type Command struct {
	Name        string
	Args        []string
	IgnoreError bool
	Fallback    []Command
}

func (c Command) String() string {
	parts := append([]string{c.Name}, c.Args...)
	for i, p := range parts {
		if p == "" || strings.ContainsAny(p, " \t\"'") {
			parts[i] = fmt.Sprintf("%q", p)
		}
	}
	return strings.Join(parts, " ")
}

// Plan is what an operation writes, runs and removes.
type Plan struct {
	Pre      []Command // run before anything is removed or written
	Files    []File
	Commands []Command
	Remove   []string // files to delete (missing = fine)
	Notes    []string
}

// Apply executes the plan: Pre commands, removals, file writes, then
// Commands, each in order.
// In dry-run mode it prints everything and changes nothing.
func (e Env) Apply(p Plan) error {
	out := e.Out
	if out == nil {
		out = io.Discard
	}
	if e.DryRun {
		for _, c := range p.Pre {
			fmt.Fprintf(out, "would run: %s\n", c)
		}
		for _, r := range p.Remove {
			fmt.Fprintf(out, "would remove %s\n", r)
		}
		for _, f := range p.Files {
			fmt.Fprintf(out, "would write %s (mode %04o):\n", f.Path, f.Mode.Perm())
			for _, line := range strings.Split(strings.TrimRight(string(f.Content), "\r\n"), "\n") {
				fmt.Fprintf(out, "    %s\n", strings.TrimRight(line, "\r"))
			}
		}
		for _, c := range p.Commands {
			fmt.Fprintf(out, "would run: %s\n", c)
			for _, fb := range c.Fallback {
				fmt.Fprintf(out, "  (if that fails) would run: %s\n", fb)
			}
		}
		for _, n := range p.Notes {
			fmt.Fprintf(out, "note: %s\n", n)
		}
		return nil
	}
	for _, c := range p.Pre {
		if err := e.run(c); err != nil {
			return err
		}
	}
	for _, r := range p.Remove {
		if !e.exists(r) {
			continue
		}
		if err := os.Remove(r); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("service: remove %s: %w", r, err)
		}
		fmt.Fprintf(out, "removed %s\n", r)
	}
	for _, f := range p.Files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			return fmt.Errorf("service: create %s: %w", filepath.Dir(f.Path), err)
		}
		if err := os.WriteFile(f.Path, f.Bytes(), f.Mode); err != nil {
			return fmt.Errorf("service: write %s: %w", f.Path, err)
		}
		fmt.Fprintf(out, "wrote %s\n", f.Path)
	}
	for _, c := range p.Commands {
		if err := e.run(c); err != nil {
			return err
		}
	}
	for _, n := range p.Notes {
		fmt.Fprintf(out, "note: %s\n", n)
	}
	return nil
}

func (e Env) run(c Command) error {
	if e.Runner == nil {
		return errors.New("service: no command runner")
	}
	outText, err := e.Runner.Run(c.Name, c.Args...)
	if err == nil {
		return nil
	}
	if len(c.Fallback) > 0 {
		if e.Out != nil {
			fmt.Fprintf(e.Out, "%s failed (%s) — using the fallback\n", c.Name, strings.TrimSpace(outText))
		}
		for _, fb := range c.Fallback {
			if ferr := e.run(fb); ferr != nil {
				return fmt.Errorf("service: %w (fallback after: %v)", ferr, err)
			}
		}
		return nil
	}
	if c.IgnoreError {
		return nil
	}
	if t := strings.TrimSpace(outText); t != "" {
		return fmt.Errorf("service: %w: %s", err, t)
	}
	return fmt.Errorf("service: %w", err)
}

// BuildInstall plans `cwm service install` for e.GOOS.
func BuildInstall(e Env, o Options) (Plan, error) {
	if !absFor(e.GOOS, e.Exe) {
		return Plan{}, fmt.Errorf("service: need the absolute path of the cwm binary, got %q", e.Exe)
	}
	if o.OpenOnly {
		if err := ValidateURL(o.URL); err != nil {
			return Plan{}, err
		}
	} else if o.URL != "" {
		return Plan{}, errors.New("--url only makes sense with --open-only")
	}
	switch e.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		return linuxInstall(e, o)
	case "windows":
		return windowsInstall(e, o)
	case "darwin":
		return darwinInstall(e, o)
	}
	return Plan{}, fmt.Errorf("service: autostart is not supported on %s", e.GOOS)
}

// BuildUninstall plans `cwm service uninstall` (removes every variant,
// including --open-only, whatever was installed).
func BuildUninstall(e Env) (Plan, error) {
	switch e.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		return linuxUninstall(e), nil
	case "windows":
		return windowsUninstall(e), nil
	case "darwin":
		return darwinUninstall(e), nil
	}
	return Plan{}, fmt.Errorf("service: autostart is not supported on %s", e.GOOS)
}

// Action is a start/stop request.
type Action string

// Start / Stop actions for BuildControl.
const (
	Start Action = "start"
	Stop  Action = "stop"
)

// BuildControl plans start/stop through the service manager. ok=false
// means no managed agent is installed (the caller falls back to spawning /
// signalling the agent directly).
func BuildControl(e Env, a Action) (Plan, bool) {
	switch e.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		return linuxControl(e, a)
	case "windows":
		return windowsControl(e, a)
	case "darwin":
		return darwinControl(e, a)
	}
	return Plan{}, false
}

// Item is one line of `cwm service status`.
type Item struct {
	Name   string
	State  string // "installed", "missing", or the manager's own word
	Detail string
}

// Status reports what is installed and what the service manager says.
// Query failures are reported as state, never returned as errors.
func Status(e Env) []Item {
	switch e.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		return linuxStatus(e)
	case "windows":
		return windowsStatus(e)
	case "darwin":
		return darwinStatus(e)
	}
	return []Item{{Name: "autostart", State: "unsupported", Detail: e.GOOS}}
}

func (e Env) exists(p string) bool {
	stat := e.Stat
	if stat == nil {
		stat = os.Stat
	}
	_, err := stat(p)
	return err == nil
}

func fileItem(e Env, name, path string) Item {
	if e.exists(path) {
		return Item{Name: name, State: "installed", Detail: path}
	}
	return Item{Name: name, State: "missing", Detail: path}
}

// ValidateURL accepts the --open-only URL: http(s), a host, and none of the
// characters that would need quoting in a desktop Exec line, a Windows
// command line or a shell (whitespace, quotes, %, \, backticks, $).
func ValidateURL(raw string) error {
	if raw == "" {
		return errors.New("--open-only needs --url (e.g. --url http://127.0.0.1:47800)")
	}
	if strings.ContainsAny(raw, " \t\r\n\"'%\\`$<>^|;&") {
		return fmt.Errorf("--url %q contains characters that are not allowed (whitespace, quotes, %%, \\, `, $, <, >, ^, |, ;, &)", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("--url %q: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return fmt.Errorf("--url must be an http(s) URL with a host (no credentials), got %q", raw)
	}
	return nil
}

// absFor reports whether p is absolute on the target OS (plans for one OS
// are unit-tested on every OS, so the host's filepath rules do not apply).
func absFor(goos, p string) bool {
	if goos == "windows" {
		return len(p) > 2 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') || strings.HasPrefix(p, `\\`)
	}
	return strings.HasPrefix(p, "/")
}

// checkPath refuses paths with control characters: no file format here can
// carry them safely.
func checkPath(p string) error {
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("service: path %q contains a control character", p)
		}
	}
	return nil
}

// render executes a template into bytes.
func render(t interface {
	Execute(io.Writer, any) error
}, data any) ([]byte, error) {
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return nil, fmt.Errorf("service: render: %w", err)
	}
	return b.Bytes(), nil
}
