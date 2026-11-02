//go:build e2e

// End-to-end tests for stage B2: the launcher (clone-on-demand, project
// pinning, exec) exercised through the real binary with fakeclaude
// standing in for claude. Local git remotes only — no network.
package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

var cwmBin string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "cwm-e2e-bin-")
	if err != nil {
		panic(err)
	}
	cwmBin = filepath.Join(tmp, "cwm")
	build := exec.Command("go", "build", "-o", cwmBin, "../cmd/cwm")
	if out, err := build.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// env is one isolated test world: its own HOME, CWM_HOME, repos root, PATH
// stubs and fakeclaude capture file.
type env struct {
	t       *testing.T
	home    string
	root    string
	repos   string
	binDir  string
	capture string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	e := &env{
		t:       t,
		home:    home,
		root:    filepath.Join(home, ".cwm"),
		repos:   filepath.Join(home, "repos"),
		binDir:  filepath.Join(home, "bin"),
		capture: filepath.Join(home, "capture.jsonl"),
	}
	if err := os.MkdirAll(e.binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// claude on PATH = fakeclaude stub
	src, err := os.ReadFile(filepath.Join("..", "testdata", "fakeclaude", "fakeclaude.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.binDir, "claude"), src, 0o755); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) stub(name, body string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.binDir, name), []byte(body), 0o755); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) childEnv(extra ...string) []string {
	out := append(os.Environ(),
		"HOME="+e.home,
		"CWM_HOME="+e.root,
		"CWM_REPOS_ROOT="+e.repos,
		"FAKECLAUDE_CAPTURE="+e.capture,
		"PATH="+e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	return append(out, extra...)
}

type runResult struct {
	code         int
	stdout, errs string
}

func (e *env) run(dir string, args ...string) runResult {
	return e.runWith(dir, nil, args...)
}

func (e *env) runWith(dir string, extraEnv []string, args ...string) runResult {
	e.t.Helper()
	cmd := exec.Command(cwmBin, args...)
	cmd.Dir = dir
	cmd.Env = e.childEnv(extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			e.t.Fatalf("run cwm %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return runResult{code: code, stdout: stdout.String(), errs: stderr.String()}
}

func (e *env) git(dir string, args ...string) string {
	e.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = e.childEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

type capLine struct {
	Args            []string `json:"args"`
	Cwd             string   `json:"cwd"`
	ClaudeConfigDir string   `json:"claude_config_dir"`
	ProjectDirName  string   `json:"project_dir_name"`
}

func (e *env) captureLines() []capLine {
	e.t.Helper()
	data, err := os.ReadFile(e.capture)
	if err != nil {
		e.t.Fatalf("read capture: %v", err)
	}
	var lines []capLine
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if l == "" {
			continue
		}
		var c capLine
		if err := json.Unmarshal([]byte(l), &c); err != nil {
			e.t.Fatalf("bad capture line %q: %v", l, err)
		}
		lines = append(lines, c)
	}
	return lines
}

// seedRepo creates a repo at <home>/seed/<name> pushed to a bare origin
// and returns (seedDir, originPath).
func (e *env) seedRepo(name string, files map[string]string) (string, string) {
	e.t.Helper()
	seed := filepath.Join(e.home, "seed", name)
	origin := filepath.Join(e.home, "origin", name+".git")
	for _, d := range []string{seed, origin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			e.t.Fatal(err)
		}
	}
	e.git(seed, "init", "-q")
	e.git(origin, "init", "-q", "--bare")
	for path, content := range files {
		full := filepath.Join(seed, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	e.git(seed, "add", "-A")
	e.git(seed, "-c", "user.name=e2e", "-c", "user.email=e2e@test", "commit", "-qm", "seed")
	e.git(seed, "remote", "add", "origin", origin)
	e.git(seed, "push", "-q", "origin", "HEAD")
	return seed, origin
}

// healthyWorkspace builds the fixture doctor reports green on.
func (e *env) healthyWorkspace() {
	e.t.Helper()
	ws := filepath.Join(e.root, "workspace")
	for _, d := range []string{
		filepath.Join(ws, "projects", "alpha"),
		filepath.Join(ws, "hooks"),
		filepath.Join(e.root, "logs"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			e.t.Fatal(err)
		}
	}
	write := func(path, content string) {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	write(filepath.Join(ws, "settings.json"), fmt.Sprintf(`{
  "hooks": {
    "SessionStart": [ { "hooks": [ { "type": "command", "command": "%s hook session-start" } ] } ],
    "SessionEnd":   [ { "hooks": [ { "type": "command", "command": "%s hook session-end" } ] } ]
  }
}`, cwmBin, cwmBin))
	write(filepath.Join(ws, ".stignore"), "settings.json\n")
	write(filepath.Join(ws, "repos.tsv"), "alpha\t/home/x/repos/alpha\n")
	write(filepath.Join(ws, "hooks", "session-start.sh"), "#!/bin/sh\nexit 0\n")
	write(filepath.Join(ws, "projects", "alpha", "s1.jsonl"), "{}\n")
	write(filepath.Join(e.root, "logs", "wip.log"), "2026-01-01T00:00:00Z line one\n")
	write(filepath.Join(e.home, ".bashrc"), "export CLAUDE_CONFIG_DIR=\"$HOME/.cwm/workspace\"\n")
}

func (e *env) workspace() string {
	return filepath.Join(e.root, "workspace")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestOpenWithoutWorkspaceFails(t *testing.T) {
	e := newEnv(t)
	res := e.run(e.home, "open", "alpha")
	if res.code == 0 {
		t.Fatalf("expected failure, got exit 0 (stdout: %s)", res.stdout)
	}
	if !strings.Contains(res.errs, "cwm init") {
		t.Fatalf("stderr should mention cwm init: %s", res.errs)
	}
}

func TestOpenFlowCloneAndResume(t *testing.T) {
	e := newEnv(t)
	seed, origin := e.seedRepo("alpha", map[string]string{"README.md": "seed\n"})
	if err := os.MkdirAll(filepath.Join(e.workspace(), "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if res := e.run(seed, "hook", "session-start"); res.code != 0 {
		t.Fatalf("hook session-start: exit %d: %s", res.code, res.errs)
	}
	data, err := os.ReadFile(filepath.Join(e.workspace(), "repos.tsv"))
	if err != nil || !strings.Contains(string(data), "alpha\t"+origin) {
		t.Fatalf("repos.tsv = %q err=%v (want alpha tab %s)", data, err, origin)
	}

	// clone-on-demand: the target must not exist yet
	if _, err := os.Stat(filepath.Join(e.repos, "alpha")); !os.IsNotExist(err) {
		t.Fatal("clone dir already exists")
	}
	res := e.run(e.home, "open", "alpha")
	if res.code != 0 {
		t.Fatalf("cwm open alpha: exit %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.errs)
	}
	if !strings.Contains(res.stdout, "cloning") {
		t.Errorf("expected cloning message, stdout: %s", res.stdout)
	}
	if _, err := os.Stat(filepath.Join(e.repos, "alpha", ".git")); err != nil {
		t.Errorf("clone missing: %v", err)
	}
	lines := e.captureLines()
	if len(lines) != 1 {
		t.Fatalf("capture lines = %d, want 1", len(lines))
	}
	got := lines[0]
	if !contains(got.Args, "--resume") {
		t.Errorf("args missing --resume: %v", got.Args)
	}
	if want := e.workspace(); got.ClaudeConfigDir != want {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want %q", got.ClaudeConfigDir, want)
	}
	if got.ProjectDirName != "alpha" {
		t.Errorf("pin = %q, want alpha", got.ProjectDirName)
	}
	if want := filepath.Join(e.repos, "alpha"); got.Cwd != want {
		t.Errorf("cwd = %q, want %q", got.Cwd, want)
	}
}

func TestOpenModes(t *testing.T) {
	e := newEnv(t)
	seed, _ := e.seedRepo("beta", map[string]string{"README.md": "x\n"})
	if err := os.MkdirAll(filepath.Join(e.workspace(), "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if res := e.run(seed, "hook", "session-start"); res.code != 0 {
		t.Fatalf("hook: %s", res.errs)
	}
	cases := []struct {
		mode    string
		want    []string
		notWant []string
	}{
		{"new", nil, []string{"--resume", "--continue"}},
		{"last", []string{"--continue"}, []string{"--resume"}},
	}
	for _, c := range cases {
		args := []string{"open", "beta"}
		if c.mode != "" {
			args = append(args, c.mode)
		}
		if res := e.run(e.home, args...); res.code != 0 {
			t.Fatalf("open beta %s: exit %d: %s", c.mode, res.code, res.errs)
		}
		lines := e.captureLines()
		last := lines[len(lines)-1]
		for _, w := range c.want {
			if !contains(last.Args, w) {
				t.Errorf("mode %s: args %v missing %s", c.mode, last.Args, w)
			}
		}
		for _, nw := range c.notWant {
			if contains(last.Args, nw) {
				t.Errorf("mode %s: args %v must not contain %s", c.mode, last.Args, nw)
			}
		}
	}
}

func TestDirectForm(t *testing.T) {
	e := newEnv(t)
	seed, _ := e.seedRepo("gamma", map[string]string{"README.md": "x\n"})
	if err := os.MkdirAll(filepath.Join(e.workspace(), "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if res := e.run(seed, "hook", "session-start"); res.code != 0 {
		t.Fatalf("hook: %s", res.errs)
	}
	res := e.run(e.home, "gamma")
	if res.code != 0 {
		t.Fatalf("cwm gamma: exit %d: %s", res.code, res.errs)
	}
	lines := e.captureLines()
	if len(lines) != 1 || !contains(lines[0].Args, "--resume") || lines[0].ProjectDirName != "gamma" {
		t.Fatalf("direct form capture = %+v", lines)
	}
}

func TestOpenMCPConfig(t *testing.T) {
	e := newEnv(t)
	seed, _ := e.seedRepo("delta", map[string]string{"README.md": "x\n"})
	if err := os.MkdirAll(filepath.Join(e.workspace(), "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if res := e.run(seed, "hook", "session-start"); res.code != 0 {
		t.Fatalf("hook: %s", res.errs)
	}
	if err := os.WriteFile(filepath.Join(e.workspace(), "mcp.json"),
		[]byte(`{"mcpServers":{"echo":{"command":"echo"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := e.run(e.home, "open", "delta", "new"); res.code != 0 {
		t.Fatalf("open delta new: exit %d: %s", res.code, res.errs)
	}
	lines := e.captureLines()
	last := lines[len(lines)-1]
	if len(last.Args) < 2 || last.Args[0] != "--mcp-config" || !strings.HasSuffix(last.Args[1], "mcp.json") {
		t.Errorf("mcp args: %v", last.Args)
	}
}

func TestSessionEndPushesSyncBranch(t *testing.T) {
	e := newEnv(t)
	seed, origin := e.seedRepo("wiprepo", map[string]string{"README.md": "x\n"})
	if err := os.MkdirAll(filepath.Join(e.workspace(), "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if res := e.run(seed, "hook", "session-start"); res.code != 0 {
		t.Fatalf("hook: %s", res.errs)
	}
	if res := e.run(e.home, "open", "wiprepo", "new"); res.code != 0 {
		t.Fatalf("open: %s", res.errs)
	}
	clone := filepath.Join(e.repos, "wiprepo")
	if err := os.WriteFile(filepath.Join(clone, "dirty.txt"), []byte("wip content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := e.run(clone, "hook", "session-end"); res.code != 0 {
		t.Fatalf("hook session-end: exit %d: %s", res.code, res.errs)
	}
	host := storage.HostName()
	refs := e.git(origin, "for-each-ref", "--format=%(refname:short)", "refs/heads/sync/")
	if !strings.Contains(refs, "sync/"+host) {
		t.Fatalf("no sync branch for host %q: %q", host, refs)
	}
	content := e.git(origin, "cat-file", "-p", "sync/"+host+":dirty.txt")
	if !strings.Contains(content, "wip content") {
		t.Errorf("dirty file not on sync branch: %q", content)
	}
}

func TestDoctorHealthyNoNetwork(t *testing.T) {
	e := newEnv(t)
	e.healthyWorkspace()
	marker := filepath.Join(e.home, "ssh-marker")
	e.stub("ssh", "#!/bin/sh\necho ssh-invoked >> "+marker+"\n")
	res := e.run(e.home, "doctor")
	if res.code != 0 {
		t.Fatalf("doctor exit %d\n%s", res.code, res.stdout)
	}
	if !strings.Contains(res.stdout, "0 fail") {
		t.Errorf("summary should report 0 fail:\n%s", res.stdout)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("doctor invoked ssh in default mode")
	}
}

func TestDoctorLegacySlugWarn(t *testing.T) {
	e := newEnv(t)
	e.healthyWorkspace()
	legacy := filepath.Join(e.workspace(), "projects", "-Users-x-legacy")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "s.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := e.run(e.home, "doctor")
	if !strings.Contains(res.stdout, "-Users-x-legacy") {
		t.Errorf("doctor should warn about legacy slug:\n%s", res.stdout)
	}
}

func TestDoctorOldVersionWarn(t *testing.T) {
	e := newEnv(t)
	e.healthyWorkspace()
	res := e.runWith(e.home, []string{"FAKECLAUDE_VERSION=2.1.100"}, "doctor")
	if !strings.Contains(res.stdout, "2.1.100") || !strings.Contains(res.stdout, "2.1.234") {
		t.Errorf("doctor should warn about old version:\n%s", res.stdout)
	}
}

func TestOpenReservedName(t *testing.T) {
	e := newEnv(t)
	res := e.run(e.home, "open", "list")
	if res.code == 0 {
		t.Fatal("expected non-zero exit for reserved name")
	}
	if !strings.Contains(res.errs, "reserved") {
		t.Errorf("stderr should mention reserved: %s", res.errs)
	}
}
