// Package claudebin finds the claude executable for every cwm command that
// runs it (account add, init --account, open/sessions, doctor, the GUI).
//
// v0.6.0 only looked on PATH, which fails in the most common Windows case:
// the Claude Code installer puts claude.exe in %USERPROFILE%\.local\bin and
// edits the user Path, but every process started before that edit (the
// shell the user is in, the cwm agent and the terminals it opens) still has
// the old PATH. So the lookup order is (D49):
//
//  1. $CWM_CLAUDE (must exist — an explicit setting pointing nowhere is an
//     error, not something to paper over);
//  2. config.toml `claude_path` (skipped when stale);
//  3. exec.LookPath("claude");
//  4. well-known install locations per OS, then `npm prefix -g` (3 s cap).
//
// $CWM_CLAUDE_NO_SEARCH=1 turns step 4 off (tests; users who want PATH only).
package claudebin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Env vars read by the resolver.
const (
	EnvClaude   = "CWM_CLAUDE"
	EnvNoSearch = "CWM_CLAUDE_NO_SEARCH"
)

// Source says which step of the lookup order produced a Result.
type Source string

const (
	SourceEnv    Source = "env"    // $CWM_CLAUDE
	SourceConfig Source = "config" // config.toml claude_path
	SourcePath   Source = "path"   // PATH lookup
	SourceKnown  Source = "known"  // well-known install location / npm prefix
)

// Result is a resolved claude executable.
type Result struct {
	Path   string `json:"path"`
	Source Source `json:"source"`
	// OnPath: a bare `claude` in a fresh shell reaches this binary's dir.
	// False means cwm works but the user's own shells and IDEs will not.
	OnPath bool `json:"onPath"`
}

// Dir is the folder holding the executable (what goes on PATH).
func (r Result) Dir() string { return filepath.Dir(r.Path) }

// NotFoundError lists every place the resolver looked.
type NotFoundError struct {
	GOOS     string
	Searched []string
}

// ErrNotFound is matched by errors.Is for any NotFoundError.
var ErrNotFound = errors.New("claude not found")

func (e *NotFoundError) Error() string {
	where := "on PATH"
	if len(e.Searched) > 0 {
		where = "on PATH or in " + strings.Join(e.Searched, ", ")
	}
	return fmt.Sprintf("claude (Claude Code) not found %s — install it with: %s  (or point cwm at it: set %s or claude_path in config.toml)",
		where, InstallCommand(e.GOOS), EnvClaude)
}

func (e *NotFoundError) Is(target error) bool { return target == ErrNotFound }

// Env is the resolver's view of the machine; tests fake every field.
type Env struct {
	GOOS     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Stat     func(string) (fs.FileInfo, error)
	Glob     func(string) ([]string, error)
	// NpmPrefix returns `npm prefix -g` ("" when npm is missing or slow).
	NpmPrefix func() string
	// Home is the user home dir ("" = unknown).
	Home string
}

// SystemEnv is the real machine.
func SystemEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{
		GOOS:      runtime.GOOS,
		Getenv:    os.Getenv,
		LookPath:  exec.LookPath,
		Stat:      os.Stat,
		Glob:      filepath.Glob,
		NpmPrefix: npmPrefix,
		Home:      home,
	}
}

// npmTimeout bounds the `npm prefix -g` probe: npm is a node script and a
// cold start can be slow, but a missing claude must not hang a command.
const npmTimeout = 3 * time.Second

func npmPrefix() string {
	npm, err := exec.LookPath("npm")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), npmTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, npm, "prefix", "-g")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Resolve runs the lookup order. configured is config.toml's claude_path
// ("" = unset).
func Resolve(env Env, configured string) (Result, error) {
	if p := strings.TrimSpace(env.Getenv(EnvClaude)); p != "" {
		p = expandHome(env, p)
		if !env.isExecutable(p) {
			return Result{}, fmt.Errorf("claudebin: %s=%s is not an executable file", EnvClaude, p)
		}
		return Result{Path: p, Source: SourceEnv, OnPath: env.dirOnPath(filepath.Dir(p))}, nil
	}
	if p := strings.TrimSpace(configured); p != "" {
		p = expandHome(env, p)
		if env.isExecutable(p) {
			return Result{Path: p, Source: SourceConfig, OnPath: env.dirOnPath(filepath.Dir(p))}, nil
		}
		// Stale (claude moved or was uninstalled): fall through.
	}
	if p, err := env.LookPath("claude"); err == nil {
		if abs, aerr := filepath.Abs(p); aerr == nil {
			p = abs
		}
		return Result{Path: p, Source: SourcePath, OnPath: true}, nil
	}
	if env.Getenv(EnvNoSearch) == "1" {
		return Result{}, &NotFoundError{GOOS: env.GOOS}
	}
	cands := Candidates(env)
	for _, p := range cands {
		if env.isExecutable(p) {
			return Result{Path: p, Source: SourceKnown, OnPath: env.dirOnPath(filepath.Dir(p))}, nil
		}
	}
	if prefix := env.npmPrefix(); prefix != "" {
		for _, p := range npmCandidates(env.GOOS, prefix) {
			cands = append(cands, p)
			if env.isExecutable(p) {
				return Result{Path: p, Source: SourceKnown, OnPath: env.dirOnPath(filepath.Dir(p))}, nil
			}
		}
	}
	return Result{}, &NotFoundError{GOOS: env.GOOS, Searched: cands}
}

// Candidates are the well-known install locations for env.GOOS, most
// likely first (npm prefix excluded: it needs a subprocess).
func Candidates(env Env) []string {
	var out []string
	add := func(base string, parts ...string) {
		if base == "" {
			return
		}
		out = append(out, filepath.Join(append([]string{base}, parts...)...))
	}
	if env.GOOS == "windows" {
		profile := env.Getenv("USERPROFILE")
		if profile == "" {
			profile = env.Home
		}
		local := env.Getenv("LOCALAPPDATA")
		add(profile, ".local", "bin", "claude.exe") // native installer
		add(local, "Programs", "claude", "claude.exe")
		add(local, "AnthropicClaude", "claude.exe")
		add(env.Getenv("APPDATA"), "npm", "claude.cmd") // npm -g default prefix
		add(local, "Microsoft", "WinGet", "Links", "claude.exe")
		add(profile, "scoop", "shims", "claude.exe")
		return out
	}
	home := env.Home
	add(home, ".local", "bin", "claude") // native installer
	add(home, ".claude", "local", "claude")
	add(home, ".npm-global", "bin", "claude")
	out = append(out, "/usr/local/bin/claude", "/opt/homebrew/bin/claude")
	add(home, ".bun", "bin", "claude")
	add(home, ".volta", "bin", "claude")
	nvmDir := env.Getenv("NVM_DIR")
	if nvmDir == "" && home != "" {
		nvmDir = filepath.Join(home, ".nvm")
	}
	if nvmDir != "" {
		out = append(out, newestFirst(env, filepath.Join(nvmDir, "versions", "node", "*", "bin", "claude"))...)
	}
	return out
}

func npmCandidates(goos, prefix string) []string {
	if goos == "windows" {
		return []string{filepath.Join(prefix, "claude.cmd"), filepath.Join(prefix, "claude.exe")}
	}
	return []string{filepath.Join(prefix, "bin", "claude")}
}

var nodeVerRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// newestFirst globs pattern (…/versions/node/<ver>/bin/claude) and orders
// the matches by node version, newest first: nvm users mostly installed
// claude under the node they use today.
func newestFirst(env Env, pattern string) []string {
	if env.Glob == nil {
		return nil
	}
	matches, err := env.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil
	}
	ver := func(p string) [3]int {
		var v [3]int
		m := nodeVerRe.FindStringSubmatch(filepath.Base(filepath.Dir(filepath.Dir(p))))
		if m != nil {
			for i := range 3 {
				fmt.Sscan(m[i+1], &v[i])
			}
		}
		return v
	}
	sort.SliceStable(matches, func(i, j int) bool {
		a, b := ver(matches[i]), ver(matches[j])
		for k := range 3 {
			if a[k] != b[k] {
				return a[k] > b[k]
			}
		}
		return false
	})
	return matches
}

func (env Env) npmPrefix() string {
	if env.NpmPrefix == nil {
		return ""
	}
	return env.NpmPrefix()
}

func (env Env) isExecutable(p string) bool {
	if env.Stat == nil {
		return false
	}
	fi, err := env.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	if env.GOOS == "windows" {
		return true // no exec bit; the extension decides
	}
	return fi.Mode().Perm()&0o111 != 0
}

// dirOnPath reports whether dir is an entry of $PATH (case-insensitive and
// trailing-separator tolerant on Windows).
func (env Env) dirOnPath(dir string) bool {
	sep := string(os.PathListSeparator)
	if env.GOOS == "windows" {
		sep = ";"
	}
	norm := func(s string) string {
		s = strings.TrimRight(filepath.Clean(s), `/\`)
		if env.GOOS == "windows" {
			s = strings.ToLower(s)
		}
		return s
	}
	want := norm(dir)
	for _, e := range strings.Split(env.Getenv("PATH"), sep) {
		if e != "" && norm(e) == want {
			return true
		}
	}
	return false
}

func expandHome(env Env, p string) string {
	if env.Home == "" {
		return p
	}
	if p == "~" {
		return env.Home
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(env.Home, p[2:])
	}
	return p
}

// InstallCommand is the copy-paste Claude Code install command for goos.
func InstallCommand(goos string) string {
	if goos == "windows" {
		return "irm https://claude.ai/install.ps1 | iex"
	}
	return "curl -fsSL https://claude.ai/install.sh | bash"
}

// PathHint is the copy-paste command that puts dir on the user's PATH for
// new shells: PowerShell on Windows, a profile line elsewhere (~/.zprofile
// on macOS, whose default shell is zsh; ~/.profile on Linux).
func PathHint(goos, dir string) string {
	if goos == "windows" {
		return `[Environment]::SetEnvironmentVariable("Path", [Environment]::GetEnvironmentVariable("Path","User") + ";` + dir + `", "User")`
	}
	profile := "~/.profile"
	if goos == "darwin" {
		profile = "~/.zprofile"
	}
	// The line is single-quoted for echo; a ' in dir closes, escapes and
	// reopens the quote.
	line := `export PATH="` + strings.ReplaceAll(dir, `'`, `'\''`) + `:$PATH"`
	return "echo '" + line + "' >> " + profile
}

// Version runs `<path> --version` (bounded, hidden window on Windows) and
// returns the first dotted x.y.z it prints ("" when none).
func Version(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			out = append(out, ee.Stderr...)
		}
	}
	return versionRe.FindString(string(out))
}

var versionRe = regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)
