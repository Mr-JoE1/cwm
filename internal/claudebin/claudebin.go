// Package claudebin finds the claude executable for every cwm command that
// runs it (account add, init --account, open/sessions, doctor, the GUI).
//
// v0.6.0 only looked on PATH, which fails in the most common Windows case:
// the Claude Code installer puts claude.exe in %USERPROFILE%\.local\bin and
// edits the user Path, but every process started before that edit (the
// shell the user is in, the cwm agent and the terminals it opens) still has
// the old PATH. So the lookup order is (D49):
//
//  1. $CWM_CLAUDE (must exist and verify — an explicit setting pointing at
//     the wrong thing is an error, not something to paper over);
//  2. config.toml `claude_path` (skipped when missing, dropped when it no
//     longer verifies);
//  3. every `claude` on PATH, in PATH order;
//  4. well-known install locations per OS, then `npm prefix -g` (3 s cap).
//
// A candidate counts only when it verifies by behaviour: `<file>
// --version` exits 0 and prints a Claude Code version line (D54). Where a
// file lives is never a reason to accept or to reject it.
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
	SourceEnv        Source = "env"        // $CWM_CLAUDE
	SourceConfig     Source = "config"     // config.toml claude_path
	SourcePath       Source = "path"       // PATH lookup
	SourceDiscovered Source = "discovered" // well-known install location / npm prefix
)

// Result is a resolved, verified claude executable.
type Result struct {
	Path   string `json:"path"`
	Source Source `json:"source"`
	// OnPath: a bare `claude` in a fresh shell reaches this binary's dir.
	// False means cwm works but the user's own shells and IDEs will not.
	OnPath bool `json:"onPath"`
	// Version and ProbeOutput come from the verifying `--version` probe
	// (D54); ProbeOutput is cut to ProbeOutputShown characters.
	Version     string `json:"version,omitempty"`
	ProbeOutput string `json:"probeOutput,omitempty"`
	// Candidates are the existing files the resolver probed, in order
	// (Survey keeps probing past the winner, up to SurveyLimit).
	Candidates []Candidate `json:"candidates,omitempty"`
	// Cleared is set when config.toml's claude_path pointed at a file
	// that failed verification; find() removes it from the config.
	Cleared *Cleared `json:"cleared,omitempty"`
}

// Candidate is one probed file: what the resolver tried and why it did
// or did not take it.
type Candidate struct {
	Path     string `json:"path"`
	Source   Source `json:"source"`
	OnPath   bool   `json:"on_path"`
	Verified bool   `json:"verified"`
	// Result is a short human text: the version line, or the reason.
	Result string `json:"result"`
}

// Cleared records a saved claude_path that stopped being Claude Code.
type Cleared struct {
	Path   string    `json:"path"`
	Reason string    `json:"reason"`
	Time   time.Time `json:"time"`
}

// Dir is the folder holding the executable (what goes on PATH).
func (r Result) Dir() string { return filepath.Dir(r.Path) }

// NotFoundError lists every place the resolver looked and every file it
// found there that was not Claude Code.
type NotFoundError struct {
	GOOS     string
	Searched []string
	// Rejected are existing candidates that failed verification.
	Rejected []Candidate
	// Cleared: as Result.Cleared (a failed claude_path was dropped).
	Cleared *Cleared
}

// ErrNotFound is matched by errors.Is for any NotFoundError.
var ErrNotFound = errors.New("claude not found")

func (e *NotFoundError) Error() string {
	where := "on PATH"
	if len(e.Searched) > 0 {
		where = "on PATH or in " + strings.Join(e.Searched, ", ")
	}
	rejected := ""
	if n := len(e.Rejected); n > 0 {
		parts := make([]string, 0, n)
		for _, c := range e.Rejected {
			parts = append(parts, c.Path+": "+c.Result)
		}
		rejected = fmt.Sprintf(" (%d candidate(s) found but not Claude Code: %s)", n, strings.Join(parts, "; "))
	}
	return fmt.Sprintf("claude (Claude Code) not found %s%s — install it with: %s  (or point cwm at it: set %s or claude_path in config.toml)",
		where, rejected, InstallCommand(e.GOOS), EnvClaude)
}

func (e *NotFoundError) Is(target error) bool { return target == ErrNotFound }

// Env is the resolver's view of the machine; tests fake every field.
type Env struct {
	GOOS   string
	Getenv func(string) string
	Stat   func(string) (fs.FileInfo, error)
	Glob   func(string) ([]string, error)
	// NpmPrefix returns `npm prefix -g` ("" when npm is missing or slow).
	NpmPrefix func() string
	// Probe verifies one file (nil = the real `--version` probe).
	Probe func(path string) ProbeResult
	// Home is the user home dir ("" = unknown).
	Home string
}

// SystemEnv is the real machine.
func SystemEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{
		GOOS:      runtime.GOOS,
		Getenv:    os.Getenv,
		Stat:      os.Stat,
		Glob:      filepath.Glob,
		NpmPrefix: npmPrefix,
		Probe:     Probe,
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

// SurveyLimit caps how many existing candidates Survey probes.
const SurveyLimit = 5

// Resolve runs the lookup order and returns the first candidate that
// verifies (D54). configured is config.toml's claude_path ("" = unset).
func Resolve(env Env, configured string) (Result, error) {
	return resolve(env, configured, false)
}

// Survey is Resolve for reports (/api/claude, doctor, the debug bundle):
// it keeps probing after the winner, up to SurveyLimit existing
// candidates, so shadowed or broken copies show up too. It never runs
// `npm prefix -g` once a winner is known.
func Survey(env Env, configured string) (Result, error) {
	return resolve(env, configured, true)
}

// resolver walks the candidates in lookup order.
type resolver struct {
	env    Env
	survey bool
	seen   map[string]bool
	probed []Candidate
	win    *Result
}

// try probes p when it is an executable file not seen before. It reports
// whether the walk should stop.
func (r *resolver) try(p string, src Source) bool {
	key := filepath.Clean(p)
	if r.env.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	if r.seen[key] || !r.env.isExecutable(p) {
		return false
	}
	r.seen[key] = true
	onPath := src == SourcePath || r.env.dirOnPath(filepath.Dir(p))
	pr := r.env.probe(p)
	r.probed = append(r.probed, Candidate{Path: p, Source: src, OnPath: onPath, Verified: pr.Verified, Result: pr.Short()})
	if pr.Verified && r.win == nil {
		r.win = &Result{Path: p, Source: src, OnPath: onPath, Version: pr.Version, ProbeOutput: shorten(pr.Output, ProbeOutputShown)}
	}
	if r.win == nil {
		return false
	}
	return !r.survey || len(r.probed) >= SurveyLimit
}

func resolve(env Env, configured string, survey bool) (Result, error) {
	if p := strings.TrimSpace(env.Getenv(EnvClaude)); p != "" {
		p = expandHome(env, p)
		if !env.isExecutable(p) {
			return Result{}, fmt.Errorf("claudebin: %s=%s is not an executable file", EnvClaude, p)
		}
		// An explicit setting is never papered over: a wrong binary is
		// an error, not a reason to search elsewhere.
		pr := env.probe(p)
		if !pr.Verified {
			return Result{}, fmt.Errorf("claudebin: %s=%s is not Claude Code (%s)", EnvClaude, p, pr.Short())
		}
		onPath := env.dirOnPath(filepath.Dir(p))
		return Result{Path: p, Source: SourceEnv, OnPath: onPath, Version: pr.Version, ProbeOutput: shorten(pr.Output, ProbeOutputShown),
			Candidates: []Candidate{{Path: p, Source: SourceEnv, OnPath: onPath, Verified: true, Result: pr.Short()}}}, nil
	}
	r := &resolver{env: env, survey: survey, seen: map[string]bool{}}
	var cleared *Cleared
	if p := strings.TrimSpace(configured); p != "" {
		p = expandHome(env, p)
		// A missing file just falls through (claude moved or was
		// uninstalled); an existing one that fails the probe is dropped
		// from config.toml by find().
		if env.isExecutable(p) {
			stop := r.try(p, SourceConfig)
			if last := r.probed[len(r.probed)-1]; !last.Verified {
				cleared = &Cleared{Path: p, Reason: last.Result, Time: time.Now().UTC()}
			}
			if stop {
				return r.result(cleared), nil
			}
		}
	}
	for _, p := range pathCandidates(env) {
		if r.try(p, SourcePath) {
			return r.result(cleared), nil
		}
	}
	if env.Getenv(EnvNoSearch) == "1" {
		if r.win != nil {
			return r.result(cleared), nil
		}
		return Result{}, &NotFoundError{GOOS: env.GOOS, Rejected: r.probed, Cleared: cleared}
	}
	cands := Candidates(env)
	for _, p := range cands {
		if r.try(p, SourceDiscovered) {
			return r.result(cleared), nil
		}
	}
	if r.win == nil {
		if prefix := env.npmPrefix(); prefix != "" {
			for _, p := range npmCandidates(env.GOOS, prefix) {
				cands = append(cands, p)
				if r.try(p, SourceDiscovered) {
					return r.result(cleared), nil
				}
			}
		}
	}
	if r.win != nil {
		return r.result(cleared), nil
	}
	return Result{}, &NotFoundError{GOOS: env.GOOS, Searched: cands, Rejected: r.probed, Cleared: cleared}
}

func (r *resolver) result(cleared *Cleared) Result {
	res := *r.win
	res.Candidates = r.probed
	res.Cleared = cleared
	return res
}

// pathCandidates lists every `claude` on PATH in PATH order, not only the
// first: a broken or foreign claude early on PATH must not hide a good
// one later (D54). On Windows the PATHEXT executable extensions apply.
func pathCandidates(env Env) []string {
	sep := string(os.PathListSeparator)
	exts := []string{""}
	if env.GOOS == "windows" {
		sep = ";"
		exts = windowsExts(env.Getenv("PATHEXT"))
	} else if sep == ";" {
		sep = ":" // a GOOS=linux test on a Windows host
	}
	var out []string
	for _, dir := range strings.Split(env.Getenv("PATH"), sep) {
		dir = strings.Trim(strings.TrimSpace(dir), `"`)
		if dir == "" {
			continue
		}
		for _, ext := range exts {
			out = append(out, filepath.Join(dir, "claude"+ext))
		}
	}
	return out
}

// windowsExts is PATHEXT reduced to the extensions CreateProcess can run
// directly, lower-cased, in PATHEXT order.
func windowsExts(pathext string) []string {
	allowed := map[string]bool{".com": true, ".exe": true, ".bat": true, ".cmd": true}
	var out []string
	for _, e := range strings.Split(strings.ToLower(pathext), ";") {
		e = strings.TrimSpace(e)
		if allowed[e] {
			out = append(out, e)
			delete(allowed, e)
		}
	}
	if len(out) == 0 {
		return []string{".com", ".exe", ".bat", ".cmd"}
	}
	return out
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

func (env Env) probe(p string) ProbeResult {
	if env.Probe == nil {
		return Probe(p)
	}
	return env.Probe(p)
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

// InstallOption is one official way to install Claude Code.
type InstallOption struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

// InstallOptions are the official install commands for goos, exactly as
// the Claude Code setup docs (code.claude.com/docs/en/setup) give them,
// the recommended native installer first.
func InstallOptions(goos string) []InstallOption {
	npm := InstallOption{Label: "npm (Node.js 22+)", Command: "npm install -g @anthropic-ai/claude-code"}
	switch goos {
	case "windows":
		return []InstallOption{
			{Label: "PowerShell", Command: InstallCommand(goos)},
			{Label: "CMD", Command: "curl -fsSL https://claude.ai/install.cmd -o install.cmd && install.cmd && del install.cmd"},
			{Label: "WinGet", Command: "winget install Anthropic.ClaudeCode"},
			npm,
		}
	case "darwin":
		return []InstallOption{
			{Label: "Terminal", Command: InstallCommand(goos)},
			{Label: "Homebrew", Command: "brew install --cask claude-code"},
			npm,
		}
	}
	return []InstallOption{{Label: "Terminal", Command: InstallCommand(goos)}, npm}
}

// Label is the human name of a lookup step, for doctor and the debug
// bundle.
func (s Source) Label() string {
	switch s {
	case SourceEnv:
		return "$" + EnvClaude
	case SourceConfig:
		return "config.toml claude_path"
	case SourcePath:
		return "PATH"
	case SourceDiscovered:
		return "found in a well-known install folder"
	}
	return string(s)
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

// Version runs `<path> --version` (through the cached Probe) and returns
// the first dotted x.y.z it prints ("" when none), verified or not.
func Version(path string) string {
	pr := Probe(path)
	if pr.Version != "" {
		return pr.Version
	}
	return versionRe.FindString(pr.Output)
}

var versionRe = regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)

// PathEntry is one PATH element; Dup marks a repeat of an earlier one.
type PathEntry struct {
	Dir string
	Dup bool
}

// PathEntries splits path for goos and marks repeated entries
// (case-insensitive and trailing-separator tolerant on Windows).
// Informational only: cwm never rewrites PATH.
func PathEntries(path, goos string) []PathEntry {
	sep := ":"
	if goos == "windows" {
		sep = ";"
	}
	norm := func(s string) string {
		s = strings.TrimRight(strings.Trim(strings.TrimSpace(s), `"`), `/\`)
		if goos == "windows" {
			s = strings.ToLower(strings.ReplaceAll(s, "/", `\`))
		}
		return s
	}
	seen := map[string]bool{}
	var out []PathEntry
	for _, e := range strings.Split(path, sep) {
		k := norm(e)
		if k == "" {
			continue
		}
		out = append(out, PathEntry{Dir: e, Dup: seen[k]})
		seen[k] = true
	}
	return out
}

// PathDuplicates counts the PATH entries that repeat an earlier one.
func PathDuplicates(path, goos string) int {
	n := 0
	for _, e := range PathEntries(path, goos) {
		if e.Dup {
			n++
		}
	}
	return n
}
