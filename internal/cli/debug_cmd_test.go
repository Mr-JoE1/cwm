package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/gui/term"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
)

type clipCall struct {
	name  string
	args  []string
	stdin []byte
}

// stubDebug isolates every machine-facing seam of `cwm debug`: the
// service runner, syncthing, the agent fetch and the clipboard tools.
func stubDebug(t *testing.T, goos string, installed map[string]bool, fail map[string]bool) *[]clipCall {
	t.Helper()
	isolateHome(t)
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // doctor runs `claude --version`: never a real one
	stubService(t, "linux")
	calls := &[]clipCall{}
	og, ol, or, oe, os_, oa := clipGOOS, clipLookPath, clipRun, clipGetenv, debugSync, debugAgentBundle
	clipGOOS = goos
	clipLookPath = func(name string) (string, error) {
		if installed[name] {
			return "/fake/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	clipRun = func(name string, args []string, stdin []byte) error {
		*calls = append(*calls, clipCall{name, args, stdin})
		if fail[filepath.Base(name)] {
			return errors.New("exit status 1")
		}
		return nil
	}
	clipGetenv = func(k string) string { return map[string]string{"WAYLAND_DISPLAY": "wayland-0"}[k] }
	debugSync = func() (syncengine.Client, error) { return nil, errors.New("syncthing not configured (test)") }
	debugAgentBundle = func(agent.Config) (string, error) { return "", errors.New("no agent in tests") }
	t.Cleanup(func() {
		clipGOOS, clipLookPath, clipRun, clipGetenv, debugSync, debugAgentBundle = og, ol, or, oe, os_, oa
	})
	return calls
}

func TestDebugPrintsRedactedBundle(t *testing.T) {
	calls := stubDebug(t, "linux", nil, nil)
	home := os.Getenv("HOME")
	runCLI(t, "init")
	got := runCLI(t, "debug")
	if got.code != ExitOK {
		t.Fatalf("code %d: %s", got.code, got.err)
	}
	for _, w := range []string{"cwm debug bundle", "built by cwm debug", "== version ==", "== agent ==\nnot running",
		"== service ==", "== claude ==", "== config.toml ==", "== workspace ==", "initialized: true", "== syncthing ==",
		"== doctor ==", "== logs ==", "== recent API errors ==\nonly the running agent keeps API errors"} {
		if !strings.Contains(got.out, w) {
			t.Errorf("bundle lacks %q:\n%s", w, got.out)
		}
	}
	if strings.Contains(got.out, home) {
		t.Errorf("bundle leaks the home dir %s", home)
	}
	if len(*calls) != 0 {
		t.Errorf("no clipboard without --copy: %+v", *calls)
	}
	if r := runCLI(t, "debug", "--bogus"); r.code != ExitError || !strings.Contains(r.err, "usage: cwm debug") {
		t.Errorf("bad flag: %+v", r)
	}
}

// D99: project folders are numbered by default; --show-projects prints
// them; --help documents both.
func TestDebugShowProjects(t *testing.T) {
	stubDebug(t, "linux", nil, nil)
	runCLI(t, "init")
	r := runCLI(t, "debug", "--help")
	if r.code != ExitOK || !strings.Contains(r.out, "--show-projects") || !strings.Contains(r.out, "report a bug") {
		t.Fatalf("help: %+v", r)
	}
	home, err := os.UserHomeDir() // USERPROFILE on Windows
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(home, ".claude") // capture mode: the workspace root
	if err := os.MkdirAll(filepath.Join(ws, "projects", "-srv-code-topsecretrepo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "projects", "-srv-code-topsecretrepo", "a.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := runCLI(t, "debug")
	if strings.Contains(got.out, "topsecretrepo") || !strings.Contains(got.out, "<project-1> (1)") {
		t.Errorf("default bundle must alias project folders:\n%s", got.out)
	}
	got = runCLI(t, "debug", "--show-projects")
	if !strings.Contains(got.out, "-srv-code-topsecretrepo (1)") {
		t.Errorf("--show-projects must print the folder name:\n%s", got.out)
	}
}

func TestDebugCopyLinuxOrder(t *testing.T) {
	// wl-copy (Wayland) fails, xclip is missing, xsel takes it.
	calls := stubDebug(t, "linux", map[string]bool{"wl-copy": true, "xsel": true}, map[string]bool{"wl-copy": true})
	got := runCLI(t, "debug", "--copy")
	if got.code != ExitOK {
		t.Fatalf("code %d: %s", got.code, got.err)
	}
	if len(*calls) != 2 || filepath.Base((*calls)[0].name) != "wl-copy" || filepath.Base((*calls)[1].name) != "xsel" ||
		strings.Join((*calls)[1].args, " ") != "--clipboard --input" {
		t.Fatalf("clipboard calls = %+v", *calls)
	}
	if !bytes.HasPrefix((*calls)[1].stdin, []byte("cwm debug bundle")) {
		t.Errorf("xsel got %q", (*calls)[1].stdin[:min(40, len((*calls)[1].stdin))])
	}
	if !regexp.MustCompile(`copied \d+ lines to the clipboard \(via xsel\)`).MatchString(got.out) || strings.Contains(got.out, "== version ==") {
		t.Errorf("stdout = %q", got.out)
	}
}

func TestDebugCopyXclipArgs(t *testing.T) {
	calls := stubDebug(t, "linux", map[string]bool{"xclip": true}, nil)
	runCLI(t, "debug", "--copy")
	if len(*calls) != 1 || strings.Join((*calls)[0].args, " ") != "-selection clipboard" {
		t.Fatalf("calls = %+v", *calls)
	}
}

func TestDebugCopyWindowsUTF16(t *testing.T) {
	calls := stubDebug(t, "windows", map[string]bool{"clip.exe": true}, nil)
	if got := runCLI(t, "debug", "--copy"); got.code != ExitOK {
		t.Fatalf("code %d: %s", got.code, got.err)
	}
	if len(*calls) != 1 || filepath.Base((*calls)[0].name) != "clip.exe" {
		t.Fatalf("calls = %+v", *calls)
	}
	in := (*calls)[0].stdin
	if !bytes.HasPrefix(in, []byte{0xFF, 0xFE, 'c', 0, 'w', 0, 'm', 0}) {
		t.Errorf("clip.exe input is not UTF-16LE with BOM: % x", in[:min(8, len(in))])
	}
	if !bytes.Contains(in, []byte{'\r', 0, '\n', 0}) {
		t.Error("clip.exe input should use CRLF")
	}
}

func TestDebugCopyMacOS(t *testing.T) {
	calls := stubDebug(t, "darwin", map[string]bool{"pbcopy": true}, nil)
	runCLI(t, "debug", "--copy")
	if len(*calls) != 1 || filepath.Base((*calls)[0].name) != "pbcopy" {
		t.Fatalf("calls = %+v", *calls)
	}
}

func TestDebugCopyFallsBackToStdout(t *testing.T) {
	stubDebug(t, "linux", nil, nil)
	got := runCLI(t, "debug", "--copy")
	if got.code != ExitOK {
		t.Fatalf("copy failure is best-effort, code = %d", got.code)
	}
	if !strings.Contains(got.out, "== version ==") || !strings.Contains(got.err, "could not copy to the clipboard") ||
		!strings.Contains(got.err, "xclip: not installed") {
		t.Errorf("out=%q err=%q", got.out[:min(80, len(got.out))], got.err)
	}
}

// With an agent running, `cwm debug` adds the agent's API errors and its
// own claude lookup (its PATH can differ from the shell's).
func TestDebugBorrowsAgentSections(t *testing.T) {
	stubDebug(t, "linux", nil, nil)
	root := os.Getenv("CWM_HOME")
	runCLI(t, "init")
	startAgent(t, root, "--port", strconv.Itoa(freePort(t)), "--no-open")
	got := runCLI(t, "debug")
	for _, w := range []string{"== agent ==\npid:", "== recent API errors ==\nthe agent did not hand over its errors: no agent in tests"} {
		if !strings.Contains(got.out, w) {
			t.Errorf("bundle lacks %q:\n%s", w, got.out)
		}
	}
	debugAgentBundle = func(agent.Config) (string, error) {
		return "x\n\n== claude ==\nfound:   no\n\n== recent API errors ==\n2026-11-19T10:00:00Z GET /api/nope 404 no such endpoint\n", nil
	}
	got = runCLI(t, "debug")
	for _, w := range []string{"== recent API errors (from the agent) ==\n2026-11-19T10:00:00Z GET /api/nope 404", "== claude (as the agent sees it) ==\nfound:   no"} {
		if !strings.Contains(got.out, w) {
			t.Errorf("bundle lacks %q:\n%s", w, got.out)
		}
	}
}

// The dashboard's launches carry the hold flag in argv (D49): it must
// parse as a global flag and make HoldOnError wait without the env var.
func TestHoldFlag(t *testing.T) {
	t.Cleanup(func() { holdRequested.Store(false) })
	if got := runCLI(t, term.HoldFlag, "version"); got.code != ExitOK {
		t.Fatalf("hold flag + version: %+v", got)
	}
	var out bytes.Buffer
	HoldOnError(ExitError, func(string) string { return "" }, func() bool { return true }, strings.NewReader("\n"), &out)
	if !strings.Contains(out.String(), "press Enter") {
		t.Errorf("flag did not hold: %q", out.String())
	}
}
