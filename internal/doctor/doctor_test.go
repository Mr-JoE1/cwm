package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stubBin creates a bin dir with the named executable stubs (sh scripts) and
// prepends it to PATH. The ssh stub appends its argv to marker so tests can
// prove whether doctor ever invoked it (default mode must not, D9).
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
	root := t.TempDir()
	ws := filepath.Join(root, "workspace")

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
	writeFile(t, filepath.Join(ws, ".stignore"), "settings.json\n")
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

	t.Setenv("CWM_HOME", root) // so storage.ClaudeBin caches under root/state
	return root
}

func TestDoctorHealthyWorkspace(t *testing.T) {
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
		"ok:   .stignore present",
		"ok:   claude 2.1.234 on PATH",
		"ok:   CLAUDE_CONFIG_DIR persisted",
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
	if !regexp.MustCompile(`(?m)^doctor: 10 ok, 0 warn, 0 fail$`).MatchString(got) {
		t.Errorf("summary line wrong:\n%s", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("default mode must never invoke ssh, but the ssh stub was called")
	}
}

func TestDoctorMissingWorkspaceFails(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CWM_HOME", root) // keep storage.ClaudeBin's cache inside the sandbox
	_, marker := sshStub(t, "irrelevant")
	var out strings.Builder
	if Run(&out, root, false) {
		t.Fatalf("Run = true, want false (workspace missing)\n%s", out.String())
	}
	got := out.String()
	if !strings.Contains(got, "FAIL: workspace dir missing") {
		t.Errorf("output missing workspace FAIL:\n%s", got)
	}
	if !regexp.MustCompile(`(?m)^doctor: \d+ ok, \d+ warn, [1-9]\d* fail$`).MatchString(got) {
		t.Errorf("summary must report at least one fail:\n%s", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("default mode must never invoke ssh")
	}
}

func TestDoctorSettingsVariants(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CWM_HOME", root)
	ws := filepath.Join(root, "workspace", "settings.json")

	// No settings.json: warn, no wired-hook sub-checks.
	var out strings.Builder
	Run(&out, root, false)
	if !strings.Contains(out.String(), "warn: settings.json missing — hooks not wired") {
		t.Errorf("missing settings should warn:\n%s", out.String())
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
	root := t.TempDir()
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

func TestDoctorLegacySessionFolders(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CWM_HOME", root)
	writeFile(t, filepath.Join(root, "workspace", "projects", "-Users-x-legacy", "s.jsonl"), "{}\n")

	var out strings.Builder
	if !Run(&out, root, false) {
		t.Fatalf("warn-only doctor must not fail:\n%s", out.String())
	}
	got := out.String()
	if !strings.Contains(got, "-Users-x-legacy") || !strings.Contains(got, "plain claude") {
		t.Errorf("output must name the legacy folder and its cause:\n%s", got)
	}
}

func TestDoctorOldClaudeVersion(t *testing.T) {
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
	root := healthyWorkspace(t)
	// Replace PATH with a stub dir that has no claude at all.
	bin := t.TempDir()
	t.Setenv("PATH", bin)

	var out strings.Builder
	Run(&out, root, false)
	if !strings.Contains(out.String(), "warn: claude not on PATH") {
		t.Errorf("output missing claude-not-on-PATH warn:\n%s", out.String())
	}
}

func TestDoctorMalformedReposTSV(t *testing.T) {
	root := healthyWorkspace(t)
	writeFile(t, filepath.Join(root, "workspace", "repos.tsv"), "alpha\thttps://x/alpha\nbadline-no-tab\n")

	var out strings.Builder
	Run(&out, root, false)
	if !strings.Contains(out.String(), "warn: repos.tsv has 1 malformed line(s) (expected: name<TAB>url)") {
		t.Errorf("output missing malformed repos.tsv warn:\n%s", out.String())
	}
}

func TestDoctorNetProbeSuccess(t *testing.T) {
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
	if !regexp.MustCompile(`(?m)^doctor: 11 ok, 0 warn, 0 fail$`).MatchString(got) {
		t.Errorf("summary must count 11 oks with --net:\n%s", got)
	}
}

func TestDoctorNetProbeFailure(t *testing.T) {
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
	root := t.TempDir()
	t.Setenv("CWM_HOME", root)
	sshStub(t, "unreachable")

	var out strings.Builder
	Run(&out, "", false) // root "" must resolve $CWM_HOME, not crash
	if !strings.Contains(out.String(), filepath.Join(root, "workspace")) {
		t.Errorf("output should reference %s:\n%s", filepath.Join(root, "workspace"), out.String())
	}
}
