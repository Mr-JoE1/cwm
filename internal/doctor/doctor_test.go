package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/syncthing"
	"github.com/Mr-JoE1/cwm/internal/testsafe"
	"github.com/Mr-JoE1/cwm/internal/workspace"
)

// TestMain keeps the claude resolver (D49) off this machine's real install
// folders; tests that exercise the search clear the variable themselves.
func TestMain(m *testing.M) {
	// No test ever queries the real registry, launchd or Keychain.
	regQueryUserEnv = func(string) (string, bool) { return "", false }
	launchctlGetenv = func(string) (string, bool) { return "", false }
	vaultHas = func(_ accounts.CredStore, p claudepaths.Paths) bool { return fileExists(p.Credentials) }
	os.Exit(testsafe.Main(m))
}

// stubBin creates a bin dir with the named executable stubs (sh scripts) and
// prepends it to PATH. The ssh stub appends its argv to marker so tests can
// prove whether doctor ever invoked it (default mode must not, D9).
// noClaude gives the test an empty PATH: doctor runs `claude --version`
// on whatever PATH finds, and a test must never reach a real claude.
func noClaude(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	for _, k := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		t.Setenv(k, t.TempDir())
	}
}

func stubBin(t *testing.T, files map[string]string) string {
	t.Helper()
	bin := t.TempDir()
	for name, body := range files {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", name, err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

func sshStub(t *testing.T, output string) (binDir, marker string) {
	t.Helper()
	marker = filepath.Join(t.TempDir(), "ssh-marker")
	body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nprintf '%%s\\n' %q\nexit 1\n", marker, output)
	binDir = stubBin(t, map[string]string{"ssh": body})
	return binDir, marker
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// healthyWorkspace builds a fully green $CWM_HOME and returns its root.
func healthyWorkspace(t *testing.T) string {
	t.Helper()
	root := isolatedRoot(t)
	ws := filepath.Join(root, "workspace")
	// The v0.7-shaped fixture is an isolated-mode workspace (D76).
	writeFile(t, filepath.Join(root, "config.toml"), "mode = \"isolated\"\n")

	// An executable stub standing in for the cwm binary named in settings.
	cwmStub := filepath.Join(t.TempDir(), "cwm-stub")
	if err := os.WriteFile(cwmStub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write cwm stub: %v", err)
	}
	settings := fmt.Sprintf(`{
  "hooks": {
    "SessionStart": [ { "hooks": [ { "type": "command", "command": "%s hook session-start" } ] } ],
    "SessionEnd":   [ { "hooks": [ { "type": "command", "command": "%s hook session-end" } ] } ]
  }
}`, cwmStub, cwmStub)
	writeFile(t, filepath.Join(ws, "settings.json"), settings)
	writeFile(t, filepath.Join(ws, ".stignore"), workspace.StIgnoreContent())
	writeFile(t, filepath.Join(ws, "repos.tsv"), "alpha\t/home/x/repos/alpha\n")
	writeFile(t, filepath.Join(ws, "hooks", "session-start.sh"), "#!/bin/sh\nexit 0\n")
	writeFile(t, filepath.Join(ws, "projects", "alpha", "20261102T000000-fake.jsonl"), "{}\n")
	writeFile(t, filepath.Join(root, "logs", "wip.log"),
		"line one\nline two\nline three\nline four\n")

	// Shell profile persistence + claude on PATH (via stub).
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".bashrc"), "export CLAUDE_CONFIG_DIR=\"$HOME/.cwm/workspace\"\n")
	t.Setenv("HOME", home)
	stubBin(t, map[string]string{"claude": "#!/bin/sh\necho '2.1.234 (Claude Code)'\n"})

	t.Setenv("CWM_HOME", root) // keep every cwm path inside the sandbox
	return root
}

func TestDoctorHealthyWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	root := healthyWorkspace(t)
	_, marker := sshStub(t, "Hi fake! You've successfully authenticated, but GitHub does not provide shell access.")

	var out strings.Builder
	if !Run(&out, root, false) {
		t.Fatalf("Run = false, want true (no FAIL)\n%s", out.String())
	}
	got := out.String()
	for _, want := range []string{
		"ok:   workspace dir exists",
		"ok:   settings.json wires hooks",
		"ok:   wired hook scripts exist and are executable",
		"ok:   hook scripts present in",
		"ok:   .stignore matches the [sync] switches",
		"ok:   claude 2.1.234 on PATH",
		"ok:   claude binary: ",
		"ok:   isolated mode: cwm-launched sessions read",
		"ok:   repos.tsv readable, 1 repo(s) recorded",
		"ok:   no unpinned (dash-prefixed) session folders",
		"ok:   WIP log tail (last 3 lines of logs/wip.log):",
		"      line two",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "FAIL:") {
		t.Errorf("healthy workspace must have no FAIL lines:\n%s", got)
	}
	if !regexp.MustCompile(`(?m)^doctor: 14 ok, 0 warn, 0 fail, 1 skipped$`).MatchString(got) {
		t.Errorf("summary line wrong (14 ok + syncthing skipped, no syncthing configured in the sandbox):\n%s", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("default mode must never invoke ssh, but the ssh stub was called")
	}
}

func TestDoctorMissingWorkspaceFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	noClaude(t)
	root := isolatedRoot(t)
	t.Setenv("CWM_HOME", root) // keep every cwm path inside the sandbox
	_, marker := sshStub(t, "irrelevant")
	var out strings.Builder
	if Run(&out, root, false) {
		t.Fatalf("Run = true, want false (workspace missing)\n%s", out.String())
	}
	got := out.String()
	if !strings.Contains(got, "FAIL: workspace dir missing") {
		t.Errorf("output missing workspace FAIL:\n%s", got)
	}
	if !regexp.MustCompile(`(?m)^doctor: \d+ ok, \d+ warn, [1-9]\d* fail(, \d+ skipped)?$`).MatchString(got) {
		t.Errorf("summary must report at least one fail:\n%s", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("default mode must never invoke ssh")
	}
}

func TestDoctorSettingsVariants(t *testing.T) {
	noClaude(t)
	root := isolatedRoot(t)
	t.Setenv("CWM_HOME", root)
	ws := filepath.Join(root, "workspace", "settings.json")

	// No settings.json: fine for the Go kit (D58), no wired-hook sub-checks.
	var out strings.Builder
	Run(&out, root, false)
	if !strings.Contains(out.String(), "ok:   no workspace settings.json yet") || strings.Contains(out.String(), "hooks-executable") {
		t.Errorf("missing settings must be ok:\n%s", out.String())
	}

	// Present but lacking hook keys.
	writeFile(t, ws, `{"env": {}}`)
	out.Reset()
	Run(&out, root, false)
	if !strings.Contains(out.String(), `warn: settings.json lacks "hooks"/"SessionStart" keys`) {
		t.Errorf("keyless settings should warn:\n%s", out.String())
	}

	// Wired hook binary missing on disk: FAIL.
	writeFile(t, ws, `{"hooks":{"SessionStart":[{"hooks":[{"command":"/nonexistent/cwm hook session-start"}]}]}}`)
	out.Reset()
	if Run(&out, root, false) {
		t.Error("wired-but-missing hook must FAIL (Run = true)")
	}
	if !strings.Contains(out.String(), "FAIL: hook wired but missing: /nonexistent/cwm") {
		t.Errorf("output missing hook-missing FAIL:\n%s", out.String())
	}
}

func TestDoctorWiredHookNotExecutable(t *testing.T) {
	noClaude(t)
	root := isolatedRoot(t)
	t.Setenv("CWM_HOME", root)
	ws := filepath.Join(root, "workspace")
	bin := filepath.Join(t.TempDir(), "cwm-notexec")
	writeFile(t, bin, "not executable\n") // 0644: no exec bit
	writeFile(t, filepath.Join(ws, "settings.json"),
		fmt.Sprintf(`{"hooks":{"SessionStart":[{"hooks":[{"command":"%s hook session-start"}]}]}}`, bin))

	var out strings.Builder
	Run(&out, root, false)
	if !strings.Contains(out.String(), "warn: hook not executable: "+bin) {
		t.Errorf("output missing not-executable warn:\n%s", out.String())
	}
}

// TestHookNotExecutableByOS: Windows never has exec bits, so a wired
// cwm.exe must pass on the extension alone and chmod is never the advice
// there; on Unix the +x bits still decide.
func TestHookNotExecutableByOS(t *testing.T) {
	t.Setenv("PATHEXT", "")
	dir := t.TempDir()
	exe := filepath.Join(dir, "cwm.exe")
	txt := filepath.Join(dir, "cwm.txt")
	plain := filepath.Join(dir, "cwm")
	for _, p := range []string{exe, txt, plain} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil { // no mode bits
			t.Fatal(err)
		}
	}
	if msg := hookNotExecutable("windows", exe); msg != "" {
		t.Errorf("windows .exe without mode bits flagged: %q", msg)
	}
	msg := hookNotExecutable("windows", txt)
	if !strings.Contains(msg, "hook not executable: "+txt) || !strings.Contains(msg, "PATHEXT") || strings.Contains(msg, "chmod") {
		t.Errorf("windows .txt: %q, want a PATHEXT warning without chmod", msg)
	}
	t.Setenv("PATHEXT", ".COM;.EXE;.TXT")
	if msg := hookNotExecutable("windows", txt); msg != "" {
		t.Errorf("windows .txt with .TXT in PATHEXT flagged: %q", msg)
	}
	if msg := hookNotExecutable("linux", plain); msg != "hook not executable: "+plain+" (chmod +x)" {
		t.Errorf("linux file without +x: %q, want the chmod advice", msg)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(plain, 0o755); err != nil {
			t.Fatal(err)
		}
		if msg := hookNotExecutable("linux", plain); msg != "" {
			t.Errorf("linux +x file flagged: %q", msg)
		}
	}
	if msg := hookNotExecutable("windows", dir); msg == "" {
		t.Error("a directory must never count as an executable hook")
	}
}

// TestDoctorWindowsHookExeIsOK: the whole report, as on a Windows box
// with cwm.exe wired, has no hook-wired warning.
func TestDoctorWindowsHookExeIsOK(t *testing.T) {
	noClaude(t)
	t.Setenv("PATHEXT", "")
	old := hostOS
	t.Cleanup(func() { hostOS = old })
	hostOS = "windows"
	root := isolatedRoot(t)
	t.Setenv("CWM_HOME", root)
	ws := filepath.Join(root, "workspace")
	bin := filepath.Join(t.TempDir(), "cwm.exe")
	writeFile(t, bin, "MZ\n") // 0644: no exec bit, as Go reports every Windows file
	cmd, _ := json.Marshal(bin + " hook session-start")
	writeFile(t, filepath.Join(ws, "settings.json"),
		fmt.Sprintf(`{"hooks":{"SessionStart":[{"hooks":[{"command":%s}]}]}}`, cmd))
	for _, c := range Checks(root, false) {
		if c.Name == "hook-wired" {
			t.Errorf("windows cwm.exe hook flagged: %+v", c)
		}
		if strings.Contains(c.Detail, "chmod") {
			t.Errorf("chmod advice on windows: %+v", c)
		}
	}
}

func TestDoctorLegacySessionFolders(t *testing.T) {
	noClaude(t)
	root := isolatedRoot(t)
	t.Setenv("CWM_HOME", root)
	writeFile(t, filepath.Join(root, "workspace", "projects", "-Users-x-legacy", "s.jsonl"), "{}\n")

	var out strings.Builder
	Run(&out, root, false)
	got := out.String()
	// The legacy folders are a warning; the only FAIL is the missing claude (D75).
	if n := strings.Count(got, "FAIL: "); n != 1 || !strings.Contains(got, "FAIL: claude not found") || !strings.Contains(got, "warn: unpinned session folders") {
		t.Fatalf("legacy folders must warn, not fail:\n%s", got)
	}
	if !strings.Contains(got, "-Users-x-legacy") || !strings.Contains(got, "plain claude") {
		t.Errorf("output must name the legacy folder and its cause:\n%s", got)
	}
}

func TestDoctorOldClaudeVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	root := healthyWorkspace(t)
	stubBin(t, map[string]string{"claude": "#!/bin/sh\necho '2.1.100 (Claude Code)'\n"})

	var out strings.Builder
	if !Run(&out, root, false) {
		t.Fatalf("old version is a warn, not a fail:\n%s", out.String())
	}
	got := out.String()
	if !strings.Contains(got, "warn: claude 2.1.100") || !strings.Contains(got, "2.1.234") {
		t.Errorf("output must warn about 2.1.100 vs 2.1.234:\n%s", got)
	}
}

func TestDoctorClaudeNotOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	root := healthyWorkspace(t)
	// Replace PATH with a stub dir that has no claude at all.
	bin := t.TempDir()
	t.Setenv("PATH", bin)

	var out strings.Builder
	if Run(&out, root, false) {
		t.Errorf("no verified claude at all is a FAIL (D75), so Run must report unhealthy:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "FAIL: claude not found. The Claude desktop app and the VS Code extension do not put `claude` on PATH. cwm looked on PATH") {
		t.Errorf("output missing claude-not-found FAIL:\n%s", out.String())
	}
}

// D49: claude installed in ~/.local/bin while that dir is not on PATH (the
// Windows tester's case) is found, reported ok, plus a PATH hint warning.
func TestDoctorClaudeOffPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	root := healthyWorkspace(t)
	t.Setenv("CWM_CLAUDE_NO_SEARCH", "")
	home := os.Getenv("HOME")
	writeFile(t, filepath.Join(home, ".local", "bin", "claude"), "#!/bin/sh\necho '2.1.300 (Claude Code)'\n")
	if err := os.Chmod(filepath.Join(home, ".local", "bin", "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	var out strings.Builder
	Run(&out, root, false)
	got := out.String()
	want := "ok:   claude 2.1.300 at " + filepath.Join(home, ".local", "bin", "claude")
	if !strings.Contains(got, want) {
		t.Errorf("output missing %q:\n%s", want, got)
	}
	hint := `echo 'export PATH="` + filepath.Join(home, ".local", "bin") + `:$PATH"' >> ~/`
	bin := "warn: claude binary: " + filepath.Join(home, ".local", "bin", "claude") + " (source: standalone install, found in a well-known install folder, verified: 2.1.300 (Claude Code)) — its folder is not on PATH"
	if !strings.Contains(got, bin) || !strings.Contains(got, hint) {
		t.Errorf("output missing the PATH hint %q:\n%s", hint, got)
	}
}

// D98: only the copy bundled with the VS Code extension is installed:
// found, a WARN (never a FAIL) with the standalone install command, no
// PATH advice (the folder carries the app version), nothing pinned.
func TestDoctorClaudeBundledOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	root := healthyWorkspace(t)
	t.Setenv("CWM_CLAUDE_NO_SEARCH", "")
	home := os.Getenv("HOME")
	exe := filepath.Join(home, ".vscode", "extensions", "anthropic.claude-code-2.1.319-linux-x64", "resources", "native-binary", "claude")
	writeFile(t, exe, "#!/bin/sh\necho '2.1.319 (Claude Code)'\n")
	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	var out strings.Builder
	if !Run(&out, root, false) {
		t.Errorf("a bundled-only claude must not FAIL:\n%s", out.String())
	}
	got := out.String()
	for _, want := range []string{
		"ok:   claude 2.1.319 found — bundled with the VS Code extension, ~/.vscode/extensions/anthropic.claude-code-2.1.319-linux-x64/resources/native-binary/claude",
		"ok:   no standalone claude CLI — using the copy bundled with the VS Code extension (2.1.319). Works; bundled copies update with their app. For a claude command in terminals: curl -fsSL https://claude.ai/install.sh | bash",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "export PATH=") || strings.Contains(got, "claude-binary") {
		t.Errorf("no PATH advice for a bundled copy:\n%s", got)
	}
}

// TestBundledNote: the owner's Windows case (D104) — two verified bundled
// copies, the newest used, the other listed; one copy names only itself.
// The note is an ok, optional check: nothing to fix.
func TestBundledNote(t *testing.T) {
	ext := `C:\Users\u\.vscode\extensions\anthropic.claude-code-2.1.332-win32-x64\resources\native-binary\claude.exe`
	desk := `C:\Users\u\AppData\Roaming\Claude\claude-code\2.1.330\claude.exe`
	res := claudebin.Result{Path: ext, Source: claudebin.SourceExtension, Version: "2.1.332", Bundle: "the VS Code extension",
		Candidates: []claudebin.Candidate{
			{Path: desk, Source: claudebin.SourceDesktop, Verified: true, Result: "verified: 2.1.330 (Claude Code)"},
			{Path: ext, Source: claudebin.SourceExtension, Verified: true, Result: "verified: 2.1.332 (Claude Code)"},
			{Path: `C:\x\claude.exe`, Source: claudebin.SourceDesktop, Verified: false, Result: "not Claude Code"},
		}}
	want := "no standalone claude CLI — using the copy bundled with the VS Code extension (2.1.332, newest); also found: Claude desktop app 2.1.330. Works; bundled copies update with their app. For a claude command in terminals: irm https://claude.ai/install.ps1 | iex"
	if got := bundledNote(res, "windows"); got != want {
		t.Errorf("several copies:\n got %q\nwant %q", got, want)
	}
	res.Candidates = res.Candidates[1:2]
	want = "no standalone claude CLI — using the copy bundled with the VS Code extension (2.1.332). Works; bundled copies update with their app. For a claude command in terminals: irm https://claude.ai/install.ps1 | iex"
	if got := bundledNote(res, "windows"); got != want {
		t.Errorf("one copy:\n got %q\nwant %q", got, want)
	}

	var r checkList
	r.ok("claude-bundled", "%s", bundledNote(res, "windows"))
	r.optional()
	if c := r[0]; c.Skipped() || textPrefix(c) != "ok:   " {
		t.Errorf("claude-bundled must print as ok, got %q skipped=%v", textPrefix(c), c.Skipped())
	}
	if _, nWarn, nFail := Counts(r); nWarn != 0 || nFail != 0 {
		t.Errorf("claude-bundled counted as a problem: warn=%d fail=%d", nWarn, nFail)
	}
}

func TestDoctorMalformedReposTSV(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	root := healthyWorkspace(t)
	writeFile(t, filepath.Join(root, "workspace", "repos.tsv"), "alpha\thttps://x/alpha\nbadline-no-tab\n")

	var out strings.Builder
	Run(&out, root, false)
	if !strings.Contains(out.String(), "warn: repos.tsv has 1 malformed line(s) (expected: name<TAB>url)") {
		t.Errorf("output missing malformed repos.tsv warn:\n%s", out.String())
	}
}

func TestDoctorNetProbeSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	root := healthyWorkspace(t)
	_, marker := sshStub(t, "Hi fake! You've successfully authenticated, but GitHub does not provide shell access.")

	var out strings.Builder
	if !Run(&out, root, true) {
		t.Fatalf("Run --net = false, want true:\n%s", out.String())
	}
	got := out.String()
	if !strings.Contains(got, "ok:   ssh git@github.com: successfully authenticated") {
		t.Errorf("output missing ssh ok line:\n%s", got)
	}
	if data, err := os.ReadFile(marker); err != nil {
		t.Errorf("ssh stub was not invoked with --net: %v", err)
	} else if !strings.Contains(string(data), "-T git@github.com") {
		t.Errorf("ssh stub args = %q, want BatchMode/ConnectTimeout/-T git@github.com", string(data))
	}
	if !regexp.MustCompile(`(?m)^doctor: 15 ok, 0 warn, 0 fail, 1 skipped$`).MatchString(got) {
		t.Errorf("summary must count 15 oks plus the skipped syncthing check with --net:\n%s", got)
	}
}

func TestDoctorNetProbeFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	root := healthyWorkspace(t)
	sshStub(t, "fake@example.com: Permission denied (publickey).")

	var out strings.Builder
	if !Run(&out, root, true) {
		t.Fatalf("ssh failure is a warn, not a fail:\n%s", out.String())
	}
	got := out.String()
	if !strings.Contains(got, "warn: ssh git@github.com failed — check your SSH key (fake@example.com: Permission denied (publickey).)") {
		t.Errorf("output missing ssh warn line:\n%s", got)
	}
}

func TestDoctorEmptyRootResolvesEnvHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	noClaude(t)
	root := isolatedRoot(t)
	t.Setenv("CWM_HOME", root)
	sshStub(t, "unreachable")

	var out strings.Builder
	Run(&out, "", false) // root "" must resolve $CWM_HOME, not crash
	if !strings.Contains(out.String(), filepath.Join(root, "workspace")) {
		t.Errorf("output should reference %s:\n%s", filepath.Join(root, "workspace"), out.String())
	}
}

func TestDoctorSyncthingUnreachableWarns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	root := healthyWorkspace(t)
	// Both env overrides set: discovery is deterministic, the port refuses
	// connections immediately — no real daemon, no timeouts.
	t.Setenv(syncthing.EnvURL, "http://127.0.0.1:1")
	t.Setenv(syncthing.EnvAPIKey, "test")

	var out strings.Builder
	if !Run(&out, root, false) {
		t.Fatalf("unreachable syncthing is optional, never a FAIL (exit stays 0):\n%s", out.String())
	}
	got := out.String()
	if !strings.Contains(got, "skip: syncthing not reachable (optional per machine — cwm pair sets it up)") {
		t.Errorf("output missing syncthing skip:\n%s", got)
	}
	if !strings.Contains(got, ", 1 skipped\n") {
		t.Errorf("summary must count the skipped optional check:\n%s", got)
	}
	if strings.Contains(got, "FAIL:") {
		t.Errorf("check 12 must never FAIL:\n%s", got)
	}
}

func TestDoctorSyncthingReachableOK(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	root := healthyWorkspace(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ping":"pong"}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv(syncthing.EnvURL, srv.URL)
	t.Setenv(syncthing.EnvAPIKey, "test")

	var out strings.Builder
	if !Run(&out, root, false) {
		t.Fatalf("reachable syncthing must not fail doctor:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "ok:   syncthing API reachable at "+srv.URL) {
		t.Errorf("output missing syncthing ok line:\n%s", out.String())
	}
}

// D58: a fresh Go-kit workspace (what `cwm init` creates) has no
// settings.json and no hooks dir; neither may warn. Workspaces that wire
// bash-kit hook scripts keep the warning.
func TestDoctorFreshGoKitWorkspaceIsQuiet(t *testing.T) {
	noClaude(t)
	root := isolatedRoot(t)
	t.Setenv("CWM_HOME", root)
	writeFile(t, filepath.Join(root, "workspace", ".stignore"), "(?d)settings.json\n")
	var out strings.Builder
	Run(&out, root, false)
	got := out.String()
	for _, want := range []string{
		"ok:   no workspace settings.json yet (cwm adds its hooks to it on the next agent tick; now: cwm doctor --fix)",
		"ok:   no bash-kit hook scripts (not needed: the Go kit runs hooks as `cwm hook …`)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "warn: settings") || strings.Contains(got, "warn: no hook scripts") {
		t.Errorf("fresh workspace must not warn about settings/hooks:\n%s", got)
	}

	// A bash-kit settings.json wiring hooks/*.sh with an empty hooks dir
	// still warns.
	script := filepath.Join(root, "workspace", "hooks", "session-start.sh")
	// Bash-kit wiring names the script with forward slashes on every OS
	// (and a JSON string cannot hold raw Windows backslashes anyway).
	writeFile(t, filepath.Join(root, "workspace", "settings.json"),
		fmt.Sprintf(`{"hooks":{"SessionStart":[{"hooks":[{"command":"%s"}]}]}}`, filepath.ToSlash(script)))
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	Run(&out, root, false)
	if !strings.Contains(out.String(), "warn: no hook scripts in ") || !strings.Contains(out.String(), "wires bash-kit hooks") {
		t.Errorf("bash-kit wiring without scripts must warn:\n%s", out.String())
	}
}

// captureRoot is a v0.8 capture-mode cwm home whose workspace is
// $HOME/.claude (HOME is a temp dir, see noClaude).
func captureRoot(t *testing.T) (root, ws string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("CWM_HOME", root)
	writeFile(t, filepath.Join(root, "config.toml"), "mode = \"capture\"\n")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	ws = filepath.Join(home, ".claude")
	writeFile(t, filepath.Join(ws, ".stignore"), workspace.StIgnoreContent())
	return root, ws
}

func checkNamed(t *testing.T, list []Check, name string) Check {
	t.Helper()
	for _, c := range list {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q check in %+v", name, list)
	return Check{}
}

func hasCheck(list []Check, name string) bool {
	for _, c := range list {
		if c.Name == name {
			return true
		}
	}
	return false
}

// The v0.7 "CLAUDE_CONFIG_DIR not exported in ~/.bashrc" check is gone:
// capture mode FAILs on a persistent CLAUDE_CONFIG_DIR instead.
func TestDoctorClaudeReadsFrom(t *testing.T) {
	noClaude(t)
	unreachableSyncthing(t)
	root, _ := captureRoot(t)
	home, _ := os.UserHomeDir()

	c := checkNamed(t, Checks(root, false), "claude-reads-from")
	if c.Status != StatusOK || !strings.Contains(c.Detail, "capture mode") {
		t.Fatalf("clean capture machine = %+v, want ok", c)
	}
	want := []string{"workspace root: ~" + string(filepath.Separator) + ".claude", ".claude.json:   ~" + string(filepath.Separator) + ".claude.json"}
	for i, w := range want {
		if len(c.Lines) <= i || c.Lines[i] != w {
			t.Errorf("Lines = %q, want %q first", c.Lines, want)
		}
	}
	if hasCheck(Checks(root, false), "config-dir") {
		t.Error("the bashrc config-dir check must be gone")
	}

	writeFile(t, filepath.Join(home, ".bashrc"), "# export CLAUDE_CONFIG_DIR=/old\nalias x=y\nexport CLAUDE_CONFIG_DIR=\""+home+"/.cwm/workspace\"\n")
	writeFile(t, filepath.Join(home, ".zprofile"), "CLAUDE_CONFIG_DIR=/x; export CLAUDE_CONFIG_DIR\n")
	c = checkNamed(t, Checks(root, false), "claude-reads-from")
	if c.Status != StatusFail || !strings.Contains(c.Detail, "2 place(s)") {
		t.Fatalf("persistent export in capture mode = %+v, want FAIL with 2 places", c)
	}
	joined := strings.Join(c.Lines, "\n")
	for _, w := range []string{`~/.bashrc:3: export CLAUDE_CONFIG_DIR="~/.cwm/workspace"`, "sed -i.cwm-bak '3d' ~/.bashrc", "~/.zprofile:1:"} {
		if !strings.Contains(joined, w) {
			t.Errorf("lines missing %q:\n%s", w, joined)
		}
	}
	if strings.Contains(joined, home) {
		t.Errorf("lines must show the home dir as ~:\n%s", joined)
	}
	var out strings.Builder
	if Run(&out, root, false) {
		t.Error("a FAIL must make Run report unhealthy (cwm doctor exits 1)")
	}

	// Isolated mode does not care.
	writeFile(t, filepath.Join(root, "config.toml"), "mode = \"isolated\"\n")
	if c := checkNamed(t, Checks(root, false), "claude-reads-from"); c.Status != StatusOK || len(c.Lines) != 3 {
		t.Errorf("isolated = %+v, want ok with only the 3 path lines", c)
	}
	// A pre-v0.8 config only warns (migrate first).
	writeFile(t, filepath.Join(root, "config.toml"), "")
	list := Checks(root, false)
	if c := checkNamed(t, list, "claude-reads-from"); c.Status != StatusWarn {
		t.Errorf("pre-v0.8 = %+v, want warn", c)
	}
	if c := checkNamed(t, list, "migration"); c.Status != StatusWarn || !strings.Contains(c.Detail, "cwm migrate --dry-run") {
		t.Errorf("migration = %+v", c)
	}
}

func TestDoctorClaudeReadsFromWindowsAndLaunchd(t *testing.T) {
	noClaude(t)
	unreachableSyncthing(t)
	root, _ := captureRoot(t)
	oldOS, oldReg, oldLd := hostOS, regQueryUserEnv, launchctlGetenv
	t.Cleanup(func() { hostOS, regQueryUserEnv, launchctlGetenv = oldOS, oldReg, oldLd })
	regQueryUserEnv = func(string) (string, bool) { return `C:\x\ws`, true }
	launchctlGetenv = func(string) (string, bool) { return "/x/ws", true }

	hostOS = "windows"
	c := checkNamed(t, Checks(root, false), "claude-reads-from")
	if c.Status != StatusFail || !strings.Contains(strings.Join(c.Lines, "\n"), "SetEnvironmentVariable('CLAUDE_CONFIG_DIR', $null, 'User')") {
		t.Errorf("windows HKCU = %+v", c)
	}
	hostOS = "darwin"
	c = checkNamed(t, Checks(root, false), "claude-reads-from")
	if c.Status != StatusFail || !strings.Contains(strings.Join(c.Lines, "\n"), "launchctl unsetenv CLAUDE_CONFIG_DIR") {
		t.Errorf("launchd = %+v", c)
	}
	hostOS = "linux" // the registry and launchd are not consulted
	if c := checkNamed(t, Checks(root, false), "claude-reads-from"); c.Status != StatusOK {
		t.Errorf("linux = %+v, want ok", c)
	}
}

func TestDoctorV08Checks(t *testing.T) {
	noClaude(t)
	unreachableSyncthing(t)
	root, ws := captureRoot(t)

	list := Checks(root, false)
	if c := checkNamed(t, list, "stignore"); c.Status != StatusOK {
		t.Errorf("generated .stignore = %+v, want ok", c)
	}
	if c := checkNamed(t, list, "conflicts"); c.Status != StatusOK {
		t.Errorf("conflicts = %+v", c)
	}
	if c := checkNamed(t, list, "vault"); c.Status != StatusOK {
		t.Errorf("no accounts = %+v", c)
	}
	if hasCheck(list, "unknown-items") || hasCheck(list, "mcp-bridge") {
		t.Errorf("unknown-items / mcp-bridge must be absent on a plain root: %+v", list)
	}

	// Switch off an item: the old .stignore no longer matches.
	writeFile(t, filepath.Join(root, "config.toml"), "mode = \"capture\"\n[sync]\nskills = false\n")
	if c := checkNamed(t, Checks(root, false), "stignore"); c.Status != StatusWarn || !strings.Contains(c.Detail, FixCommand) {
		t.Errorf("stale .stignore = %+v", c)
	}
	writeFile(t, filepath.Join(root, "config.toml"), "mode = \"capture\"\n")

	// A conflict copy of CLAUDE.md.
	writeFile(t, filepath.Join(ws, "CLAUDE.md"), "a\n")
	writeFile(t, filepath.Join(ws, "CLAUDE.sync-conflict-20261124-101010-ABCDEFG.md"), "b\n")
	c := checkNamed(t, Checks(root, false), "conflicts")
	if c.Status != StatusWarn || !strings.Contains(c.Detail, "2 versions of CLAUDE.md — pick one") ||
		len(c.Lines) != 1 || !strings.Contains(c.Lines[0], "cwm conflicts resolve CLAUDE.md current|merge|<copy>") {
		t.Errorf("conflict = %+v", c)
	}

	// Items Claude Code added that cwm does not know stay local (INFO).
	writeFile(t, filepath.Join(ws, "brand-new-thing", "x"), "x")
	writeFile(t, filepath.Join(ws, "todos", "x"), "x")
	writeFile(t, filepath.Join(ws, "history.jsonl"), "{}\n")
	c = checkNamed(t, Checks(root, false), "unknown-items")
	if c.Status != StatusOK || c.Detail != "Claude added brand-new-thing; it stays local (not on the sync allow-list)" {
		t.Errorf("unknown items = %+v", c)
	}

	// The default account: live login only, then saved in the vault.
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(root, "config.toml"), "mode = \"capture\"\ndefault_account = \"main\"\n[[account]]\nname = \"main\"\n")
	if c := checkNamed(t, Checks(root, false), "vault"); c.Status != StatusWarn || !strings.Contains(c.Detail, "has no login") {
		t.Errorf("no login anywhere = %+v", c)
	}
	writeFile(t, filepath.Join(ws, ".credentials.json"), `{"fake":"FAKE-TOKEN-NOT-REAL"}`)
	c = checkNamed(t, Checks(root, false), "vault")
	if c.Status != StatusWarn || !strings.Contains(c.Detail, "only live") {
		t.Errorf("live only = %+v", c)
	}
	writeFile(t, filepath.Join(root, "accounts", "main", ".credentials.json"), `{"fake":"FAKE-TOKEN-NOT-REAL"}`)
	if c := checkNamed(t, Checks(root, false), "vault"); c.Status != StatusOK {
		t.Errorf("vault entry = %+v", c)
	}

	// MCP bridge: a workspace server missing from ~/.claude.json.
	writeFile(t, filepath.Join(ws, "mcp.json"), `{"mcpServers":{"fs":{"command":"fake-mcp"}}}`)
	writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{}}`)
	if c := checkNamed(t, Checks(root, false), "mcp-bridge"); c.Status != StatusWarn || !strings.Contains(c.Detail, "default") {
		t.Errorf("bridge out of sync = %+v", c)
	}
	writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"fs":{"command":"fake-mcp"}}}`)
	if c := checkNamed(t, Checks(root, false), "mcp-bridge"); c.Status != StatusOK {
		t.Errorf("bridge in sync = %+v", c)
	}
	for _, c := range Checks(root, false) {
		if strings.Contains(c.Detail+strings.Join(c.Lines, ""), "FAKE-TOKEN") {
			t.Fatalf("a credential value leaked into %+v", c)
		}
	}
}

func TestDoctorPathDuplicatesNote(t *testing.T) {
	noClaude(t)
	root := isolatedRoot(t)
	t.Setenv("CWM_HOME", root)
	a, b := t.TempDir(), t.TempDir()
	t.Setenv("PATH", strings.Join([]string{a, b, a, b + string(filepath.Separator), a}, string(os.PathListSeparator)))
	list := Checks(root, false)
	var note *Check
	for i := range list {
		if list[i].Name == "path-duplicates" {
			note = &list[i]
		}
	}
	if note == nil || note.Status != StatusOK || note.Detail != "PATH has 3 duplicate entries (harmless; cwm does not change PATH)" {
		t.Fatalf("path-duplicates = %+v", note)
	}
	if strings.Contains(note.Detail, a) {
		t.Error("the note lists the count, not the entries")
	}
}

// D54: a claude on PATH that is not Claude Code is reported, not used.
func TestDoctorUnverifiedClaude(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	noClaude(t)
	root := isolatedRoot(t)
	t.Setenv("CWM_HOME", root)
	bin := stubBin(t, map[string]string{"claude": "#!/bin/sh\necho 'Claude Desktop'\n"})
	t.Setenv("PATH", bin)
	var out strings.Builder
	Run(&out, root, false)
	want := "FAIL: claude not found. The Claude desktop app and the VS Code extension do not put `claude` on PATH. cwm looked on PATH. 1 candidate(s) found but not Claude Code: " + filepath.Join(bin, "claude") + ": no Claude Code version line in its --version output — install Claude Code: "
	if !strings.Contains(out.String(), want) {
		t.Errorf("output missing %q:\n%s", want, out.String())
	}
}

// D75: an optional check that is not set up keeps its JSON status for
// compatibility, but counts as neither a warning nor a pass.
func TestOptionalCheckCounts(t *testing.T) {
	list := []Check{
		{Name: "workspace", Status: StatusOK},
		{Name: "config-dir", Status: StatusWarn},
		{Name: "syncthing", Status: StatusWarn, Optional: true},
		{Name: "claude", Status: StatusFail},
	}
	nOK, nWarn, nFail := Counts(list)
	if nOK != 1 || nWarn != 1 || nFail != 1 || Skipped(list) != 1 {
		t.Fatalf("counts = %d ok, %d warn, %d fail, %d skipped", nOK, nWarn, nFail, Skipped(list))
	}
	if (Check{Status: StatusOK, Optional: true}).Skipped() {
		t.Error("a passing optional check is not skipped")
	}
	var out strings.Builder
	Render(&out, list[:3])
	if got := out.String(); !strings.Contains(got, "skip: \n") || !strings.HasSuffix(got, "doctor: 1 ok, 1 warn, 0 fail, 1 skipped\n") {
		t.Errorf("render:\n%s", got)
	}
	out.Reset()
	Render(&out, list[:2])
	if !strings.HasSuffix(out.String(), "doctor: 1 ok, 1 warn, 0 fail\n") {
		t.Errorf("no skipped checks: the summary keeps the bash-kit format:\n%s", out.String())
	}
}

// isolatedRoot is a $CWM_HOME whose config.toml selects isolated mode, so
// root/workspace is the workspace root these v0.7-shaped fixtures build
// (D76: capture mode would resolve ~/.claude instead).
func isolatedRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "config.toml"), "mode = \"isolated\"\n")
	return root
}

// A service-started agent may not have cwm on its PATH. The bare
// `cwm hook …` entries then give ONE warning, not a failure per hook.
func TestDoctorBareCwmHookNotOnPath(t *testing.T) {
	noClaude(t)
	root := isolatedRoot(t)
	t.Setenv("CWM_HOME", root)
	t.Setenv("PATH", t.TempDir())
	old := selfExe
	selfExe = func() string { return filepath.Join("/opt/x", "cwm") }
	t.Cleanup(func() { selfExe = old })
	writeFile(t, filepath.Join(root, "workspace", "settings.json"),
		`{"hooks":{"SessionStart":[{"hooks":[{"command":"cwm hook session-start"}]}],"SessionEnd":[{"hooks":[{"command":"cwm hook session-end"}]}]}}`)
	var n, fails int
	for _, c := range Checks(root, false) {
		if c.Name == "hook-wired" {
			fails++
		}
		if c.Name == "cwm-path" {
			n++
			if c.Status != "warn" || !strings.Contains(c.Detail, "fix: `"+cwmPathFix(hostOS, selfExe())+"`") {
				fails++
			}
		}
	}
	if n != 1 || fails != 0 {
		t.Errorf("cwm-path checks = %d (bad or hook-wired %d), want exactly one warning with the fix", n, fails)
	}
}

func TestCwmPathFix(t *testing.T) {
	for _, c := range [][3]string{
		{"linux", "/home/jane/.local/bin/cwm", "cwm service install && systemctl --user restart cwm-agent"},
		{"darwin", "/Users/jane/my bin/cwm", "cwm service install"},
		{"linux", "", "cwm service install && systemctl --user restart cwm-agent"},
		{"windows", `C:\cwm\cwm.exe`, claudebinPathHint},
	} {
		if got := cwmPathFix(c[0], c[1]); c[2] != claudebinPathHint && got != c[2] {
			t.Errorf("cwmPathFix(%s, %q) = %q, want %q", c[0], c[1], got, c[2])
		} else if c[2] == claudebinPathHint && !strings.Contains(got, `SetEnvironmentVariable("Path"`) {
			t.Errorf("windows fix = %q", got)
		}
	}
}

const claudebinPathHint = "<PathHint>"
