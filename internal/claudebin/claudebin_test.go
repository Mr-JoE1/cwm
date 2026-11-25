package claudebin

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// world is a fake machine rooted in a temp dir: real files (so Stat and
// Glob are the real ones), fake env vars, a PATH lookup that only sees the
// dirs listed in PATH and an injectable npm prefix.
type world struct {
	root string
	vars map[string]string
	npm  string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	return &world{root: t.TempDir(), vars: map[string]string{}}
}

func (w *world) env(goos string) Env {
	return Env{
		GOOS:      goos,
		Getenv:    func(k string) string { return w.vars[k] },
		Stat:      os.Stat,
		Glob:      filepath.Glob,
		NpmPrefix: func() string { return w.npm },
		Probe:     fakeProbe,
		Home:      filepath.Join(w.root, "home"),
	}
}

// fakeProbe verifies a file by reading it instead of running it, so the
// GOOS=windows cases work on every host: a file whose text carries a
// Claude Code version line verifies, anything else does not.
func fakeProbe(p string) ProbeResult {
	data, err := os.ReadFile(p)
	if err != nil {
		return ProbeResult{Reason: "could not run: " + err.Error()}
	}
	if v, line, ok := ParseVersionOutput(string(data)); ok {
		return ProbeResult{Verified: true, Version: v, Line: line, Output: string(data)}
	}
	return ProbeResult{Output: string(data), Reason: "no Claude Code version line in its --version output"}
}

// file creates an executable (mode 0755) or plain (0644) file under root
// that prints a good Claude Code version line.
func (w *world) file(t *testing.T, rel string, exec bool) string {
	t.Helper()
	return w.fileWith(t, rel, exec, "#!/bin/sh\necho '2.1.400 (Claude Code)'\n")
}

// fileWith creates a file under root with the given content.
func (w *world) fileWith(t *testing.T, rel string, exec bool, content string) string {
	t.Helper()
	p := filepath.Join(w.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o644)
	if exec {
		mode = 0o755
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// posixOnly skips tests that need POSIX exec bits on the host filesystem
// (the Windows rules are covered by the GOOS=windows cases, which run on
// every host).
func posixOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs POSIX exec bits (see docs/PLAN.md D22)")
	}
}

func TestResolveOrder(t *testing.T) {
	posixOnly(t)
	w := newWorld(t)
	onPath := w.file(t, "pathdir/claude", true)
	known := w.file(t, "home/.local/bin/claude", true)
	pinned := w.file(t, "pinned/claude", true)
	envBin := w.file(t, "envbin/claude", true)
	w.vars["PATH"] = filepath.Dir(onPath)
	env := w.env("linux")

	// PATH beats the well-known folders.
	res, err := Resolve(env, "")
	if err != nil || res.Path != onPath || res.Source != SourcePath || !res.OnPath {
		t.Fatalf("PATH: got %+v, %v; want %s from PATH", res, err, onPath)
	}
	// config claude_path beats PATH.
	res, err = Resolve(env, pinned)
	if err != nil || res.Path != pinned || res.Source != SourceConfig || res.OnPath {
		t.Fatalf("config: got %+v, %v; want %s from config", res, err, pinned)
	}
	// A missing claude_path falls through to PATH, nothing to clear.
	res, err = Resolve(env, filepath.Join(w.root, "gone", "claude"))
	if err != nil || res.Source != SourcePath || res.Cleared != nil {
		t.Fatalf("stale config: got %+v, %v; want PATH fallback", res, err)
	}
	// An existing claude_path that is not Claude Code falls through too,
	// and is reported for clearing.
	wrong := w.fileWith(t, "wrong/claude", true, "#!/bin/sh\necho 1.0.0\n")
	res, err = Resolve(env, wrong)
	if err != nil || res.Source != SourcePath || res.Cleared == nil || res.Cleared.Path != wrong {
		t.Fatalf("unverified config: got %+v, %v; want PATH fallback + Cleared", res, err)
	}
	// $CWM_CLAUDE beats everything.
	w.vars[EnvClaude] = envBin
	res, err = Resolve(env, pinned)
	if err != nil || res.Path != envBin || res.Source != SourceEnv {
		t.Fatalf("env: got %+v, %v; want %s from env", res, err, envBin)
	}
	// ...and pointing it nowhere is an error, not a silent fallback.
	w.vars[EnvClaude] = filepath.Join(w.root, "nope")
	if _, err := Resolve(env, pinned); err == nil || !strings.Contains(err.Error(), EnvClaude) {
		t.Fatalf("bad env: err = %v, want a %s error", err, EnvClaude)
	}
	delete(w.vars, EnvClaude)
	// Nothing on PATH: the native-installer folder is found, off PATH.
	w.vars["PATH"] = filepath.Join(w.root, "empty")
	res, err = Resolve(env, "")
	if err != nil || res.Path != known || res.Source != SourceStandalone || res.OnPath {
		t.Fatalf("known: got %+v, %v; want %s (known, off PATH)", res, err, known)
	}
	// A PATH entry with a trailing slash still counts as on PATH.
	w.vars["PATH"] = filepath.Dir(known) + "/"
	if !env.dirOnPath(filepath.Dir(known)) {
		t.Error("dir on PATH with a trailing slash must count as on PATH")
	}
}

func TestResolveSkipsNonExecutableAndDirs(t *testing.T) {
	posixOnly(t)
	w := newWorld(t)
	w.file(t, "home/.local/bin/claude", false) // not executable
	if err := os.MkdirAll(filepath.Join(w.root, "home", ".claude", "local", "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := w.file(t, "home/.bun/bin/claude", true)
	res, err := Resolve(w.env("linux"), "")
	if err != nil || res.Path != want {
		t.Fatalf("got %+v, %v; want %s", res, err, want)
	}
}

func TestResolveNoSearch(t *testing.T) {
	w := newWorld(t)
	w.file(t, "home/.local/bin/claude", true)
	w.vars[EnvNoSearch] = "1"
	_, err := Resolve(w.env("linux"), "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound with the search disabled", err)
	}
}

func TestResolveNotFoundListsSearchAndInstall(t *testing.T) {
	w := newWorld(t)
	_, err := Resolve(w.env("linux"), "")
	var nf *NotFoundError
	if !errors.As(err, &nf) || !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want *NotFoundError", err)
	}
	msg := err.Error()
	for _, want := range []string{
		filepath.FromSlash("~/.local/bin"), filepath.FromSlash("/usr/local/bin"), filepath.FromSlash("~/.vscode, ~/.vscode-insiders, ~/.cursor"),
		"do not put `claude` on PATH",
		"curl -fsSL https://claude.ai/install.sh | bash",
		EnvClaude, "claude_path",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
	w2 := newWorld(t)
	_, err = Resolve(w2.env("windows"), "")
	if err == nil || !strings.Contains(err.Error(), "irm https://claude.ai/install.ps1 | iex") {
		t.Errorf("windows not-found must carry the PowerShell install command: %v", err)
	}
}

func TestCandidatesWindows(t *testing.T) {
	w := newWorld(t)
	prof := filepath.Join(w.root, "Users", "u")
	local := filepath.Join(prof, "AppData", "Local")
	roaming := filepath.Join(prof, "AppData", "Roaming")
	w.vars["USERPROFILE"], w.vars["LOCALAPPDATA"], w.vars["APPDATA"] = prof, local, roaming
	got := Candidates(w.env("windows"))
	want := []string{
		filepath.Join(prof, ".local", "bin", "claude.exe"),
		filepath.Join(local, "Programs", "claude", "claude.exe"),
		filepath.Join(local, "AnthropicClaude", "claude.exe"),
		filepath.Join(roaming, "npm", "claude.cmd"),
		filepath.Join(local, "Microsoft", "WinGet", "Links", "claude.exe"),
		filepath.Join(prof, "scoop", "shims", "claude.exe"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("windows candidates:\n got %q\nwant %q", got, want)
	}

	// The tester's machine: native installer, folder not on the agent's PATH.
	exe := w.file(t, "Users/u/.local/bin/claude.exe", false) // no exec bit on Windows
	w.vars["PATH"] = filepath.Join(w.root, "Windows", "System32")
	res, err := Resolve(w.env("windows"), "")
	if err != nil || res.Path != exe || res.OnPath {
		t.Fatalf("got %+v, %v; want %s off PATH", res, err, exe)
	}
	// Windows PATH entries match case-insensitively.
	w.vars["PATH"] = strings.ToUpper(filepath.Dir(exe)) + `\;` + filepath.Join(w.root, "x")
	if !w.env("windows").dirOnPath(filepath.Dir(exe)) {
		t.Error("windows dirOnPath must ignore case and a trailing backslash")
	}
}

func TestResolveNpmPrefix(t *testing.T) {
	w := newWorld(t)
	w.npm = filepath.Join(w.root, "npm-prefix")
	cmd := w.file(t, "npm-prefix/claude.cmd", false)
	res, err := Resolve(w.env("windows"), "")
	if err != nil || res.Path != cmd || res.Source != SourceStandalone {
		t.Fatalf("windows npm prefix: got %+v, %v; want %s", res, err, cmd)
	}
	if runtime.GOOS == "windows" {
		return // the POSIX half needs exec bits
	}
	w2 := newWorld(t)
	w2.npm = filepath.Join(w2.root, "npm-prefix")
	sh := w2.file(t, "npm-prefix/bin/claude", true)
	res, err = Resolve(w2.env("linux"), "")
	if err != nil || res.Path != sh {
		t.Fatalf("posix npm prefix: got %+v, %v; want %s", res, err, sh)
	}
}

func TestCandidatesNvmNewestFirst(t *testing.T) {
	w := newWorld(t)
	for _, v := range []string{"v18.20.1", "v22.3.0", "v20.11.0", "v22.12.0"} {
		w.file(t, "home/.nvm/versions/node/"+v+"/bin/claude", true)
	}
	got := Candidates(w.env("linux"))
	var nvm []string
	for _, p := range got {
		if strings.Contains(p, ".nvm") {
			nvm = append(nvm, filepath.Base(filepath.Dir(filepath.Dir(p))))
		}
	}
	want := []string{"v22.12.0", "v22.3.0", "v20.11.0", "v18.20.1"}
	if !reflect.DeepEqual(nvm, want) {
		t.Errorf("nvm order = %v, want %v", nvm, want)
	}
	w.vars["NVM_DIR"] = filepath.Join(w.root, "elsewhere")
	w.file(t, "elsewhere/versions/node/v24.0.0/bin/claude", true)
	if got := Candidates(w.env("linux")); !strings.Contains(strings.Join(got, "\n"), filepath.Join("elsewhere", "versions", "node", "v24.0.0")) {
		t.Errorf("$NVM_DIR must be searched: %q", got)
	}
}

func TestPathHint(t *testing.T) {
	if got, want := PathHint("windows", `C:\Users\u\.local\bin`),
		`[Environment]::SetEnvironmentVariable("Path", [Environment]::GetEnvironmentVariable("Path","User") + ";C:\Users\u\.local\bin", "User")`; got != want {
		t.Errorf("windows hint:\n got %s\nwant %s", got, want)
	}
	if got, want := PathHint("linux", "/home/u/.local/bin"),
		`echo 'export PATH="/home/u/.local/bin:$PATH"' >> ~/.profile`; got != want {
		t.Errorf("linux hint:\n got %s\nwant %s", got, want)
	}
	if got := PathHint("darwin", "/Users/u/.local/bin"); !strings.HasSuffix(got, ">> ~/.zprofile") {
		t.Errorf("macOS hint must target ~/.zprofile: %s", got)
	}
	if got, want := PathHint("linux", "/home/o'neil/bin"),
		`echo 'export PATH="/home/o'\''neil/bin:$PATH"' >> ~/.profile`; got != want {
		t.Errorf("quote escaping:\n got %s\nwant %s", got, want)
	}
}

func TestFindPersistsOffPathHit(t *testing.T) {
	posixOnly(t)
	w := newWorld(t)
	known := w.file(t, "home/.local/bin/claude", true)
	layout := storage.NewLayoutAt(filepath.Join(w.root, "cwmhome"))
	env := w.env("linux")

	// No config.toml yet: found, but nothing is created (the installer
	// reads config.toml's presence as "initialised").
	if _, err := find(layout, env, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(layout.ConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("config.toml must not be created by the resolver (stat err = %v)", err)
	}

	if err := os.MkdirAll(filepath.Dir(layout.ConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(layout.ConfigPath(), config.Default()); err != nil {
		t.Fatal(err)
	}
	// Lookup (read-only) leaves it alone...
	if _, err := find(layout, env, false, false); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := config.Load(layout.ConfigPath()); cfg.ClaudePath != "" {
		t.Fatalf("Lookup persisted claude_path = %q", cfg.ClaudePath)
	}
	// ...Find writes it back.
	res, err := find(layout, env, true, false)
	if err != nil || res.Path != known {
		t.Fatalf("find = %+v, %v", res, err)
	}
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil || cfg.ClaudePath != known {
		t.Fatalf("claude_path = %q (%v), want %q", cfg.ClaudePath, err, known)
	}
	// Next time it comes from config.
	res, err = find(layout, env, true, false)
	if err != nil || res.Source != SourceConfig {
		t.Fatalf("second find = %+v, %v; want SourceConfig", res, err)
	}

	// Remember only pins off-PATH hits.
	c := config.Default()
	Remember(c, Result{Path: "/x/claude", Source: SourcePath, OnPath: true})
	if c.ClaudePath != "" {
		t.Error("Remember must not pin a PATH hit")
	}
	Remember(c, Result{Path: "/x/claude", Source: SourceStandalone})
	if c.ClaudePath != "/x/claude" {
		t.Error("Remember must pin an off-PATH hit")
	}
}

func TestHint(t *testing.T) {
	var b strings.Builder
	Hint(&b, "linux", Result{Path: "/p/claude", OnPath: true})
	if b.Len() != 0 {
		t.Errorf("no hint when on PATH, got %q", b.String())
	}
	Hint(&b, "windows", Result{Path: filepath.Join("C:", "u", "claude.exe")})
	if !strings.Contains(b.String(), "SetEnvironmentVariable") || !strings.Contains(b.String(), "claude_path") {
		t.Errorf("hint = %q", b.String())
	}
}

func TestVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell stub")
	}
	w := newWorld(t)
	p := w.file(t, "bin/claude", true)
	if got := Version(p); got != "2.1.400" {
		t.Errorf("Version = %q, want 2.1.400", got)
	}
	if got := Version(filepath.Join(w.root, "missing")); got != "" {
		t.Errorf("Version(missing) = %q, want empty", got)
	}
	for out, want := range map[string]string{"2.1.234 (Claude Code)\n": "2.1.234", "x\n": "", "": ""} {
		if got := versionRe.FindString(out); got != want {
			t.Errorf("parse(%q) = %q, want %q", out, got, want)
		}
	}
}
