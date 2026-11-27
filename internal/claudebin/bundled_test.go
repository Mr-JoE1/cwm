package claudebin

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
	if err != nil || res.Source != SourceStore || res.Version != "2.1.250" {
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
	// D103: both verify — the newer Claude Code wins...
	w.fileWith(t, p+"AppData/Roaming/Claude/claude-code/2.1.300/claude.exe", false, versionText("2.1.300"))
	if res, err := Resolve(env, ""); err != nil || res.Path != ext || !strings.Contains(res.Why, "the newest (2.1.400)") {
		t.Fatalf("newer extension over older desktop: %+v, %v", res, err)
	}
	// ...and on a tie the desktop app's copy.
	desk := w.fileWith(t, p+"AppData/Roaming/Claude/claude-code/2.1.400/claude.exe", false, versionText("2.1.400"))
	if res, err := Resolve(env, ""); err != nil || res.Path != desk || !strings.Contains(res.Why, "on a tie the desktop app's copy is preferred") {
		t.Fatalf("desktop on a tie: %+v, %v", res, err)
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
	// macOS: the desktop app's claude-code/<ver>/claude.app/Contents/MacOS/claude (D103).
	mac := w.fileWith(t, "home/Library/Application Support/Claude/claude-code/2.1.319/claude.app/Contents/MacOS/claude", true, goodClaude)
	w.fileWith(t, "home/Library/Application Support/Claude/claude-code/2.1.300/claude.app/Contents/MacOS/claude", true, versionText("2.1.300"))
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

// D103: the layout inside the desktop app's <ver> folder.
func TestBundledDesktopLayouts(t *testing.T) {
	for _, rel := range []string{"2.1.328/claude.exe", "2.1.328/bin/claude.exe", "2.1.328/x64/bin/claude.exe"} {
		w, p := winWorld(t)
		want := w.fileWith(t, p+"AppData/Roaming/Claude/claude-code/"+rel, false, versionText("2.1.328"))
		res, err := Survey(w.env("windows"), "")
		if err != nil || res.Path != want || res.Source != SourceDesktop {
			t.Fatalf("%s: got %+v, %v", rel, res, err)
		}
		if len(res.Notes) != 0 {
			t.Errorf("%s: notes %q", rel, res.Notes)
		}
	}
	// Three levels down is out of bounds (the lookup stays three globs).
	w, p := winWorld(t)
	w.fileWith(t, p+"AppData/Roaming/Claude/claude-code/2.1.328/a/b/c/claude.exe", false, versionText("2.1.328"))
	if _, err := Resolve(w.env("windows"), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("too deep: %v", err)
	}
}

// The owner's machine B: the version folder exists, no claude.exe in it.
func TestBundledDesktopEmptyDirNote(t *testing.T) {
	w, p := winWorld(t)
	dir := filepath.Join(w.root, p, "AppData/Roaming/Claude/claude-code/2.1.328")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ext := w.fileWith(t, p+".vscode/extensions/anthropic.claude-code-2.1.320-win32-x64/resources/native-binary/claude.exe", false, versionText("2.1.320"))
	res, err := Survey(w.env("windows"), "")
	if err != nil || res.Path != ext {
		t.Fatalf("got %+v, %v", res, err)
	}
	want := "desktop app dir found (" + filepath.Join("~", "AppData", "Roaming", "Claude", "claude-code", "2.1.328") + ") but no claude.exe inside; it is empty"
	if len(res.Notes) != 1 || res.Notes[0] != want {
		t.Fatalf("notes = %q, want %q", res.Notes, want)
	}
	// With entries: up to five names, dirs marked.
	for _, n := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt", "f.txt"} {
		w.fileWith(t, p+"AppData/Roaming/Claude/claude-code/2.1.328/"+n, false, "x")
	}
	w.fileWith(t, p+"AppData/Roaming/Claude/claude-code/2.1.328/.verified", false, "")
	w.fileWith(t, p+"AppData/Roaming/Claude/claude-code/2.1.328/sub/readme", false, "")
	// The not-found error carries the note too.
	if err := os.Remove(ext); err != nil {
		t.Fatal(err)
	}
	_, err = Survey(w.env("windows"), "")
	var nf *NotFoundError
	if !errors.As(err, &nf) || len(nf.Notes) != 1 || !strings.Contains(nf.Notes[0], "entries: .verified, a.txt, b.txt, c.txt, d.txt (+3 more)") {
		t.Fatalf("not-found notes = %+v (%v)", nf, err)
	}
}

// Survey probes the newest copy of every family before any second copy,
// and always lists the newest desktop copy, however many are found first.
func TestBundledEveryFamilyProbed(t *testing.T) {
	w, p := winWorld(t)
	for _, v := range []string{"2.1.310", "2.1.311", "2.1.312"} {
		w.fileWith(t, p+".vscode/extensions/anthropic.claude-code-"+v+"-win32-x64/resources/native-binary/claude.exe", false, versionText(v))
		w.fileWith(t, p+".cursor/extensions/anthropic.claude-code-"+v+"-win32-x64/resources/native-binary/claude.exe", false, versionText(v))
	}
	desk := w.fileWith(t, p+"AppData/Roaming/Claude/claude-code/2.1.300/claude.exe", false, versionText("2.1.300"))
	// A PATH winner and four standalone copies fill SurveyLimit first.
	onPath := w.fileWith(t, "bin/claude.exe", false, goodClaude)
	w.vars["PATH"] = filepath.Dir(onPath)
	for _, rel := range []string{".local/bin/claude.exe", "AppData/Local/Programs/claude/claude.exe", "AppData/Local/Microsoft/WinGet/Links/claude.exe", "scoop/shims/claude.exe"} {
		w.fileWith(t, p+rel, false, goodClaude)
	}
	res, err := Survey(w.env("windows"), "")
	if err != nil || res.Path != onPath {
		t.Fatalf("got %+v, %v", res, err)
	}
	var fams []string
	for _, c := range res.Candidates {
		if c.Source.Bundled() {
			fams = append(fams, string(c.Source)+" "+filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(c.Path)))))
		}
		if c.Path == desk {
			fams = append(fams, "desktop newest")
		}
	}
	got := strings.Join(fams, ",")
	if !strings.Contains(got, "desktop newest") || !strings.Contains(got, "anthropic.claude-code-2.1.312-win32-x64") {
		t.Fatalf("bundled probed = %s", got)
	}
}

// The Microsoft Store build (D112), as seen on a Windows 11 machine:
// Claude-Data\claude_<hash>\claude-code\<ver>\<buildid>\claude.exe.
const storeRel = "AppData/Local/Claude-Data/claude_pzs8sxrjxfjjc/claude-code/"

func TestBundledStoreLayout(t *testing.T) {
	w, p := winWorld(t)
	want := w.fileWith(t, p+storeRel+"2.1.330/9424831ad51c/claude.exe", false, versionText("2.1.330"))
	w.fileWith(t, p+storeRel+"2.1.300/0a1b2c3d4e5f/claude.exe", false, versionText("2.1.300"))
	res, err := Resolve(w.env("windows"), "")
	if err != nil || res.Path != want || res.Source != SourceStore || res.OnPath {
		t.Fatalf("got %+v, %v; want %s from %s", res, err, want, SourceStore)
	}
	if res.Bundle != "the Claude desktop app (Microsoft Store)" || !res.Source.Bundled() {
		t.Errorf("bundle = %q", res.Bundle)
	}
	if l := SourceStore.Label(); l != "bundled with the Claude desktop app (Microsoft Store)" {
		t.Errorf("label = %q", l)
	}
	// <ver>\claude.exe directly is a Store layout too.
	w2, p2 := winWorld(t)
	flat := w2.fileWith(t, p2+storeRel+"2.1.331/claude.exe", false, versionText("2.1.331"))
	if res, err := Resolve(w2.env("windows"), ""); err != nil || res.Path != flat {
		t.Fatalf("flat: got %+v, %v", res, err)
	}
}

func TestBundledStoreNewestBuildByMtime(t *testing.T) {
	w, p := winWorld(t)
	// By name "fff…" sorts last and "000…" first; the newest file must win.
	older := w.fileWith(t, p+storeRel+"2.1.330/ffffffffffff/claude.exe", false, versionText("2.1.330"))
	newer := w.fileWith(t, p+storeRel+"2.1.330/000000000000/claude.exe", false, versionText("2.1.330"))
	mid := w.fileWith(t, p+storeRel+"2.1.330/777777777777/claude.exe", false, versionText("2.1.330"))
	now := time.Now()
	for f, age := range map[string]time.Duration{older: 72 * time.Hour, mid: 24 * time.Hour, newer: 0} {
		if err := os.Chtimes(f, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Survey(w.env("windows"), "")
	if err != nil || res.Path != newer {
		t.Fatalf("got %s, %v; want the newest build %s", res.Path, err, newer)
	}
	// The second probe of the family is the next newest, never the oldest.
	var store []string
	for _, c := range res.Candidates {
		if c.Source == SourceStore {
			store = append(store, c.Path)
		}
	}
	if len(store) != 2 || store[1] != mid {
		t.Errorf("store probes = %v, want [%s %s]", store, newer, mid)
	}
}

func TestBundledStoreAndClassicTogether(t *testing.T) {
	w, p := winWorld(t)
	classic := w.fileWith(t, p+"AppData/Roaming/Claude/claude-code/2.1.320/claude.exe", false, versionText("2.1.320"))
	store := w.fileWith(t, p+storeRel+"2.1.330/9424831ad51c/claude.exe", false, versionText("2.1.330"))
	res, err := Survey(w.env("windows"), "")
	if err != nil || res.Path != store || res.Source != SourceStore {
		t.Fatalf("got %+v, %v; want the newer Store copy", res, err)
	}
	if !strings.Contains(res.Why, "Microsoft Store") || !strings.Contains(res.Why, "2.1.320") {
		t.Errorf("why = %q", res.Why)
	}
	seen := false
	for _, c := range res.Candidates {
		seen = seen || (c.Path == classic && c.Source == SourceDesktop)
	}
	if !seen {
		t.Errorf("classic copy not probed: %+v", res.Candidates)
	}
	// Same version: the classic and the Store copies tie; either is the
	// desktop app, so the editor extension never wins the tie.
	w2, p2 := winWorld(t)
	w2.fileWith(t, p2+".vscode/extensions/anthropic.claude-code-2.1.330-win32-x64/resources/native-binary/claude.exe", false, versionText("2.1.330"))
	w2.fileWith(t, p2+storeRel+"2.1.330/9424831ad51c/claude.exe", false, versionText("2.1.330"))
	if res, err := Resolve(w2.env("windows"), ""); err != nil || res.Source != SourceStore {
		t.Fatalf("tie: got %+v, %v", res, err)
	}
}

func TestBundledStoreNeverProbesWindowsApps(t *testing.T) {
	w, p := winWorld(t)
	pf := filepath.Join(w.root, "Program Files")
	w.vars["ProgramFiles"] = pf
	w.vars["ProgramW6432"] = pf
	app := w.fileWith(t, "Program Files/WindowsApps/Claude_6.28899.6.0_x64__pzs8sxrjxfjjc/app/Claude.exe", false, versionText("9.9.9"))
	w.fileWith(t, "Program Files/WindowsApps/Claude_6.28899.6.0_x64__pzs8sxrjxfjjc/app/claude.exe", false, versionText("9.9.9"))
	w.fileWith(t, p+"AppData/Local/Packages/Claude_pzs8sxrjxfjjc/Settings/x", false, "")
	res, err := Survey(w.env("windows"), "")
	var nf *NotFoundError
	if err == nil || !errors.As(err, &nf) {
		t.Fatalf("want not found, got %+v, %v", res, err)
	}
	for _, c := range nf.Rejected {
		if strings.HasPrefix(c.Path, pf) {
			t.Errorf("probed the desktop app itself: %s", c.Path)
		}
	}
	for _, d := range append(nf.Desktop, nf.Searched...) {
		if strings.HasPrefix(d, pf) {
			t.Errorf("searched %s (looking for %s)", d, app)
		}
	}
	if !slicesContains(nf.Notes, StoreInstalledNote) {
		t.Errorf("notes = %q, want the Store-installed note", nf.Notes)
	}
}

func TestBundledStoreInstalledNote(t *testing.T) {
	w, p := winWorld(t)
	if n := BundleNotes(w.env("windows")); slicesContains(n, StoreInstalledNote) {
		t.Errorf("no Store package, yet note: %q", n)
	}
	w.fileWith(t, p+"AppData/Local/Packages/Claude_pzs8sxrjxfjjc/LocalCache/Roaming/Claude/config.json", false, "{}")
	if n := BundleNotes(w.env("windows")); !slicesContains(n, StoreInstalledNote) {
		t.Errorf("Store package without claude-code: notes = %q", n)
	}
	// Once Claude Code is downloaded the note goes away; a standalone
	// CLI elsewhere does not matter.
	w.fileWith(t, p+storeRel+"2.1.330/9424831ad51c/claude.exe", false, versionText("2.1.330"))
	if n := BundleNotes(w.env("windows")); slicesContains(n, StoreInstalledNote) {
		t.Errorf("Store copy present, yet note: %q", n)
	}
	if n := BundleNotes(w.env("linux")); slicesContains(n, StoreInstalledNote) {
		t.Errorf("linux: %q", n)
	}
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
