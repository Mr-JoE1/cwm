package claudebin

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// goodClaude is what a real Claude Code prints for --version; the fake
// probe reads it from the file (a POSIX script would print it).
const goodClaude = "#!/bin/sh\necho '2.1.319 (Claude Code)'\n"

// winWorld is a Windows profile inside the temp root: USERPROFILE,
// APPDATA and LOCALAPPDATA all point into it and PATH has no claude.
func winWorld(t *testing.T) (*world, string) {
	t.Helper()
	w := newWorld(t)
	prof := filepath.Join(w.root, "Users", "u")
	w.vars["USERPROFILE"] = prof
	w.vars["APPDATA"] = filepath.Join(prof, "AppData", "Roaming")
	w.vars["LOCALAPPDATA"] = filepath.Join(prof, "AppData", "Local")
	w.vars["PATH"] = filepath.Join(w.root, "Windows", "System32")
	return w, "Users/u/"
}

func versionText(v string) string { return "#!/bin/sh\necho '" + v + " (Claude Code)'\n" }

func TestBundledDesktopWindowsHighestVersion(t *testing.T) {
	w, p := winWorld(t)
	// Lexically 2.1.99 > 2.1.319 > 2.1.300; numerically 319 wins.
	for _, v := range []string{"2.1.300", "2.1.99", "2.1.319"} {
		w.fileWith(t, p+"AppData/Roaming/Claude/claude-code/"+v+"/claude.exe", false, versionText(v))
	}
	res, err := Resolve(w.env("windows"), "")
	if err != nil || res.Source != SourceDesktop || res.Version != "2.1.319" {
		t.Fatalf("got %+v, %v; want 2.1.319 from desktop-app", res, err)
	}
	if !strings.Contains(res.Path, filepath.Join("2.1.319", "claude.exe")) || res.Bundle != "the Claude desktop app" {
		t.Errorf("path/bundle = %s / %q", res.Path, res.Bundle)
	}
	// Survey probes at most bundlePerFamily of the family.
	res, _ = Survey(w.env("windows"), "")
	n := 0
	for _, c := range res.Candidates {
		if c.Source == SourceDesktop {
			n++
		}
	}
	if n != bundlePerFamily {
		t.Errorf("probed %d desktop copies, want %d: %+v", n, bundlePerFamily, res.Candidates)
	}
}

func TestBundledDesktopMSIXVirtualisedAndSquirrel(t *testing.T) {
	w, p := winWorld(t)
	w.fileWith(t, p+"AppData/Local/Packages/Claude_abc123/LocalCache/Roaming/Claude/claude-code/2.1.250/claude.exe", false, versionText("2.1.250"))
	res, err := Resolve(w.env("windows"), "")
	if err != nil || res.Source != SourceDesktop || res.Version != "2.1.250" {
		t.Fatalf("MSIX LocalCache: got %+v, %v", res, err)
	}

	w2, p2 := winWorld(t)
	// app-<ver>\claude.exe is the desktop app itself: never probed.
	w2.fileWith(t, p2+"AppData/Local/AnthropicClaude/app-1.30.0/claude.exe", false, versionText("9.9.9"))
	w2.fileWith(t, p2+"AppData/Local/AnthropicClaude/app-1.4.0/resources/claude-code/claude.exe", false, versionText("2.1.100"))
	w2.fileWith(t, p2+"AppData/Local/AnthropicClaude/app-1.30.0/resources/claude-code/claude.exe", false, versionText("2.1.200"))
	res, err = Survey(w2.env("windows"), "")
	if err != nil || res.Source != SourceDesktop || res.Version != "2.1.200" {
		t.Fatalf("Squirrel: got %+v, %v; want app-1.30.0's 2.1.200", res, err)
	}
	for _, c := range res.Candidates {
		if filepath.Base(filepath.Dir(c.Path)) == "app-1.30.0" {
			t.Errorf("probed the desktop app's own exe: %s", c.Path)
		}
	}
}

func TestBundledExtensionWindows(t *testing.T) {
	w, p := winWorld(t)
	old := w.fileWith(t, p+".vscode/extensions/anthropic.claude-code-2.1.112-win32-x64/resources/native-binary/claude.exe", false, versionText("2.1.112"))
	cur := w.fileWith(t, p+".cursor/extensions/anthropic.claude-code-2.1.319-win32-x64/resources/native-binary/claude.exe", false, versionText("2.1.319"))
	res, err := Resolve(w.env("windows"), "")
	if err != nil || res.Path != cur || res.Source != SourceExtension || res.OnPath {
		t.Fatalf("got %+v, %v; want %s (highest across editors)", res, err, cur)
	}
	if res.Bundle != "the VS Code extension in Cursor" {
		t.Errorf("bundle = %q", res.Bundle)
	}
	if BundleName(SourceExtension, old) != "the VS Code extension" {
		t.Errorf("VS Code bundle name = %q", BundleName(SourceExtension, old))
	}
}

func TestBundledPrecedence(t *testing.T) {
	w, p := winWorld(t)
	ext := w.fileWith(t, p+".vscode/extensions/anthropic.claude-code-2.1.400-win32-x64/resources/native-binary/claude.exe", false, versionText("2.1.400"))
	env := w.env("windows")
	if res, err := Resolve(env, ""); err != nil || res.Path != ext {
		t.Fatalf("extension alone: %+v, %v", res, err)
	}
	// The desktop app beats the extension, even with an older version.
	desk := w.fileWith(t, p+"AppData/Roaming/Claude/claude-code/2.1.300/claude.exe", false, versionText("2.1.300"))
	if res, err := Resolve(env, ""); err != nil || res.Path != desk {
		t.Fatalf("desktop over extension: %+v, %v", res, err)
	}
	// A standalone install beats both...
	std := w.fileWith(t, p+".local/bin/claude.exe", false, goodClaude)
	if res, err := Resolve(env, ""); err != nil || res.Path != std || res.Source != SourceStandalone {
		t.Fatalf("standalone over bundled: %+v, %v", res, err)
	}
	// ...PATH beats the standalone folder...
	onPath := w.fileWith(t, "bin/claude.exe", false, goodClaude)
	w.vars["PATH"] = filepath.Dir(onPath)
	if res, err := Resolve(env, ""); err != nil || res.Path != onPath || res.Source != SourcePath {
		t.Fatalf("PATH over standalone: %+v, %v", res, err)
	}
	// ...and an explicit setting beats everything.
	pinned := w.fileWith(t, "pinned/claude.exe", false, goodClaude)
	if res, err := Resolve(env, pinned); err != nil || res.Path != pinned || res.Source != SourceConfig {
		t.Fatalf("config over PATH: %+v, %v", res, err)
	}
	w.vars[EnvClaude] = ext
	if res, err := Resolve(env, pinned); err != nil || res.Path != ext || res.Source != SourceEnv {
		t.Fatalf("env over config: %+v, %v", res, err)
	}
	// $CWM_CLAUDE_NO_SEARCH turns the bundled lookup off too.
	delete(w.vars, EnvClaude)
	w.vars["PATH"] = filepath.Join(w.root, "empty")
	w.vars[EnvNoSearch] = "1"
	if _, err := Resolve(env, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no-search: err = %v", err)
	}
}

func TestBundledRejectsNonClaude(t *testing.T) {
	w, p := winWorld(t)
	bad := w.fileWith(t, p+".vscode/extensions/anthropic.claude-code-2.1.319-win32-x64/resources/native-binary/claude.exe", false, "#!/bin/sh\necho 'hello 1.2.3'\n")
	_, err := Resolve(w.env("windows"), "")
	var nf *NotFoundError
	if !errors.As(err, &nf) || len(nf.Rejected) != 1 || nf.Rejected[0].Path != bad {
		t.Fatalf("err = %v; want not found with %s rejected", err, bad)
	}
	msg := err.Error()
	for _, want := range []string{
		"do not put `claude` on PATH",
		`~` + string(filepath.Separator) + filepath.Join(".local", "bin"),
		"in the desktop app (",
		filepath.Join("Roaming", "Claude", "claude-code"),
		"in editor extensions (~" + string(filepath.Separator) + ".vscode, ",
		"1 candidate(s) found but not Claude Code",
		"irm https://claude.ai/install.ps1 | iex",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
	// A broken newest copy falls back to the next version.
	good := w.fileWith(t, p+".vscode/extensions/anthropic.claude-code-2.1.300-win32-x64/resources/native-binary/claude.exe", false, versionText("2.1.300"))
	if res, err := Resolve(w.env("windows"), ""); err != nil || res.Path != good {
		t.Fatalf("fallback to older copy: %+v, %v", res, err)
	}
}

func TestBundledPosixFamilies(t *testing.T) {
	posixOnly(t)
	w := newWorld(t)
	w.vars["PATH"] = filepath.Join(w.root, "empty")
	// macOS: the desktop app's claude-code/<ver>/claude.
	mac := w.fileWith(t, "home/Library/Application Support/Claude/claude-code/2.1.319/claude", true, goodClaude)
	w.fileWith(t, "home/Library/Application Support/Claude/claude-code/2.1.300/claude", true, versionText("2.1.300"))
	if res, err := Resolve(w.env("darwin"), ""); err != nil || res.Path != mac || res.Source != SourceDesktop {
		t.Fatalf("darwin desktop: %+v, %v", res, err)
	}
	// Linux has no desktop family: the macOS tree is ignored there.
	if _, err := Resolve(w.env("linux"), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("linux must not look in Library/Application Support: %v", err)
	}
	// Remote VS Code server on Linux.
	srv := w.fileWith(t, "home/.vscode-server/extensions/anthropic.claude-code-2.1.319-linux-x64/resources/native-binary/claude", true, goodClaude)
	if res, err := Resolve(w.env("linux"), ""); err != nil || res.Path != srv || res.Source != SourceExtension {
		t.Fatalf("linux extension: %+v, %v", res, err)
	}
	// A non-executable copy is not a candidate.
	if err := os.Chmod(srv, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(w.env("linux"), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-executable extension copy taken: %v", err)
	}
}

func TestParseBundleVersionOrder(t *testing.T) {
	order := []string{"2.10.0", "2.2.0", "2.1.319-linux-x64", "2.1.319-beta.1", "2.1.300", "2.1.99", "2.1", "junk"}
	for i := range order {
		for j := range order {
			a, b := parseBundleVersion(order[i]), parseBundleVersion(order[j])
			if got, want := a.newer(b), i < j; got != want {
				t.Errorf("newer(%s, %s) = %t, want %t", order[i], order[j], got, want)
			}
		}
	}
}

// Find never pins a bundled copy (its folder changes on every update),
// and the stale-pin auto-clear still works next to it.
func TestFindNeverPinsBundled(t *testing.T) {
	w, p := winWorld(t)
	w.fileWith(t, p+".vscode/extensions/anthropic.claude-code-2.1.319-win32-x64/resources/native-binary/claude.exe", false, goodClaude)
	layout := storage.NewLayoutAt(filepath.Join(w.root, "cwmhome"))
	if err := os.MkdirAll(filepath.Dir(layout.ConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.ClaudePath = w.fileWith(t, "stale/claude.exe", false, "not claude\n")
	if err := config.Save(layout.ConfigPath(), cfg); err != nil {
		t.Fatal(err)
	}
	res, err := find(layout, w.env("windows"), true, false)
	if err != nil || res.Source != SourceExtension || res.Cleared == nil {
		t.Fatalf("find = %+v, %v", res, err)
	}
	got, err := config.Load(layout.ConfigPath())
	if err != nil || got.ClaudePath != "" {
		t.Fatalf("claude_path = %q (%v); want cleared and nothing pinned", got.ClaudePath, err)
	}
	c := config.Default()
	Remember(c, res)
	if c.ClaudePath != "" {
		t.Errorf("Remember pinned a bundled copy: %q", c.ClaudePath)
	}
	var b strings.Builder
	Hint(&b, "windows", res)
	if !strings.Contains(b.String(), "bundled with the VS Code extension") || strings.Contains(b.String(), "SetEnvironmentVariable") {
		t.Errorf("bundled hint = %q", b.String())
	}
}

func TestShortPath(t *testing.T) {
	sep := string(filepath.Separator)
	home := filepath.Join(string(filepath.Separator)+"h", "u")
	for in, want := range map[string]string{
		home:                         "~",
		filepath.Join(home, "x"):     "~" + sep + "x",
		home + "ser" + sep + "x":     home + "ser" + sep + "x",
		filepath.Join("/other", "x"): filepath.Join("/other", "x"),
	} {
		if got := ShortPath(home, in); got != want {
			t.Errorf("ShortPath(%q) = %q, want %q", in, got, want)
		}
	}
	if runtime.GOOS != "windows" {
		if got := ShortPath(`C:\Users\U`, `c:\users\u\.vscode`); got != `~\.vscode` {
			t.Errorf("windows-style ShortPath = %q", got)
		}
	}
}
