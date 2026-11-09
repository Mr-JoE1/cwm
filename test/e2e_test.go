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
	"time"

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

// --- v0.4-2: cwm sessions + cwm wip dirty ---

// seedSessionsWorkspace builds projects/alpha (2 sessions + index with a
// third, sidechain entry whose file exists) and projects/beta (1 raw
// session, no index). Mtimes are pinned so the listing order is
// deterministic: alpha2 newest, beta middle, alpha1 oldest.
func seedSessionsWorkspace(t *testing.T, e *env) (alpha1, alpha2, side, beta1 string) {
	t.Helper()
	alpha := filepath.Join(e.workspace(), "projects", "alpha")
	beta := filepath.Join(e.workspace(), "projects", "beta")
	for _, d := range []string{alpha, beta} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alpha1 = "11111111-aaaa-4bbb-8ccc-000000000001"
	alpha2 = "22222222-aaaa-4bbb-8ccc-000000000002"
	side = "33333333-aaaa-4bbb-8ccc-000000000003"
	beta1 = "44444444-aaaa-4bbb-8ccc-000000000004"
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(alpha, alpha1+".jsonl"), "{\"type\":\"user\",\"message\":{\"content\":\"raw alpha one\"}}\n")
	write(filepath.Join(alpha, alpha2+".jsonl"), "{\"type\":\"user\",\"message\":{\"content\":\"raw alpha two\"}}\n")
	write(filepath.Join(alpha, side+".jsonl"), "{\"type\":\"user\",\"message\":{\"content\":\"sidechain body\"}}\n")
	write(filepath.Join(beta, beta1+".jsonl"),
		"{\"type\":\"assistant\",\"message\":{\"content\":\"skip me\"}}\n"+
			"{\"type\":\"user\",\"message\":{\"content\":\"beta deploy checklist\"}}\n")
	index := fmt.Sprintf(`{"entries":[
  {"sessionId":%q, "firstPrompt":"fix the login flow", "summary":"login fix", "messageCount":12, "gitBranch":"main"},
  {"sessionId":%q, "firstPrompt":"refactor the parser core", "summary":"", "messageCount":4, "gitBranch":"feature/x"},
  {"sessionId":%q, "firstPrompt":"sidechain must stay hidden", "summary":"", "messageCount":1, "gitBranch":"main", "isSidechain":true}
]}`, alpha1, alpha2, side)
	write(filepath.Join(alpha, "sessions-index.json"), index)
	touch := func(path string, age time.Duration) {
		t.Helper()
		when := time.Now().Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	touch(filepath.Join(alpha, alpha2+".jsonl"), time.Hour)
	touch(filepath.Join(beta, beta1+".jsonl"), 6*time.Hour)
	touch(filepath.Join(alpha, alpha1+".jsonl"), 30*24*time.Hour)
	return alpha1, alpha2, side, beta1
}

func TestSessionsListsAcrossProjects(t *testing.T) {
	e := newEnv(t)
	alpha1, alpha2, side, beta1 := seedSessionsWorkspace(t, e)
	res := e.run(e.home, "sessions")
	if res.code != 0 {
		t.Fatalf("cwm sessions: exit %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.errs)
	}
	// 3 rows: 2 alpha + 1 beta; the sidechain stays hidden.
	for _, id := range []string{alpha1, alpha2, beta1} {
		if !strings.Contains(res.stdout, id[:8]) {
			t.Errorf("listing missing session %s:\n%s", id[:8], res.stdout)
		}
	}
	if strings.Contains(res.stdout, side[:8]) || strings.Contains(res.stdout, "sidechain") {
		t.Errorf("sidechain session leaked into listing:\n%s", res.stdout)
	}
	if got := strings.Count(res.stdout, "alpha"); got != 2 {
		t.Errorf("alpha row count = %d, want 2:\n%s", got, res.stdout)
	}
	// index-enriched fields render (branch + first prompt from the index)
	for _, want := range []string{"fix the login flow", "refactor the parser core", "feature/x", "beta deploy checklist", "1h ago", "6h ago"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("listing missing %q:\n%s", want, res.stdout)
		}
	}
	// newest first
	p2, pb, p1 := strings.Index(res.stdout, alpha2[:8]), strings.Index(res.stdout, beta1[:8]), strings.Index(res.stdout, alpha1[:8])
	if !(p2 < pb && pb < p1) {
		t.Errorf("rows not newest-first (alpha2=%d beta=%d alpha1=%d):\n%s", p2, pb, p1, res.stdout)
	}
}

func TestSessionsProjectFilter(t *testing.T) {
	e := newEnv(t)
	alpha1, alpha2, side, beta1 := seedSessionsWorkspace(t, e)
	res := e.run(e.home, "sessions", "alpha")
	if res.code != 0 {
		t.Fatalf("cwm sessions alpha: exit %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.errs)
	}
	for _, id := range []string{alpha1, alpha2} {
		if !strings.Contains(res.stdout, id[:8]) {
			t.Errorf("alpha listing missing %s:\n%s", id[:8], res.stdout)
		}
	}
	if strings.Contains(res.stdout, beta1[:8]) || strings.Contains(res.stdout, side[:8]) {
		t.Errorf("alpha filter leaked other projects:\n%s", res.stdout)
	}
}

func TestSessionsJSON(t *testing.T) {
	e := newEnv(t)
	alpha1, alpha2, side, beta1 := seedSessionsWorkspace(t, e)
	res := e.run(e.home, "sessions", "--json")
	if res.code != 0 {
		t.Fatalf("cwm sessions --json: exit %d\nstderr: %s", res.code, res.errs)
	}
	lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("json lines = %d, want 3:\n%s", len(lines), res.stdout)
	}
	if strings.Contains(res.stdout, side) {
		t.Errorf("sidechain leaked into json:\n%s", res.stdout)
	}
	ids := map[string]bool{}
	for _, l := range lines {
		var obj struct {
			Project     string `json:"project"`
			ID          string `json:"id"`
			FirstPrompt string `json:"firstPrompt"`
			Summary     string `json:"summary"`
			Messages    int    `json:"messages"`
			Branch      string `json:"branch"`
			Modified    string `json:"modified"`
		}
		if err := json.Unmarshal([]byte(l), &obj); err != nil {
			t.Fatalf("bad json line %q: %v", l, err)
		}
		if _, err := time.Parse(time.RFC3339, obj.Modified); err != nil {
			t.Errorf("modified %q is not RFC3339: %v", obj.Modified, err)
		}
		ids[obj.ID] = true
		if obj.Project == "" || obj.FirstPrompt == "" {
			t.Errorf("thin object: %+v", obj)
		}
	}
	for _, id := range []string{alpha1, alpha2, beta1} {
		if !ids[id] {
			t.Errorf("json missing id %s (got %v)", id, ids)
		}
	}
}

func TestSessionsPickClonesOnDemand(t *testing.T) {
	e := newEnv(t)
	seed, _ := e.seedRepo("pickme", map[string]string{"README.md": "x\n"})
	if err := os.MkdirAll(filepath.Join(e.workspace(), "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if res := e.run(seed, "hook", "session-start"); res.code != 0 {
		t.Fatalf("hook session-start: %s", res.errs)
	}
	id := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	pickDir := filepath.Join(e.workspace(), "projects", "pickme")
	if err := os.MkdirAll(pickDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessFile := filepath.Join(pickDir, id+".jsonl")
	if err := os.WriteFile(sessFile, []byte("{\"type\":\"user\",\"message\":{\"content\":\"pick me\"}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Hour) // ensure row 1 regardless of hook side effects
	if err := os.Chtimes(sessFile, future, future); err != nil {
		t.Fatal(err)
	}

	// fresh-device story: the repo is not cloned yet
	if _, err := os.Stat(filepath.Join(e.repos, "pickme")); !os.IsNotExist(err) {
		t.Fatal("clone dir already exists")
	}
	res := e.run(e.home, "sessions", "--pick", "1")
	if res.code != 0 {
		t.Fatalf("cwm sessions --pick 1: exit %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.errs)
	}
	if !strings.Contains(res.stdout, "cloning") {
		t.Errorf("clone-on-demand did not fire:\n%s", res.stdout)
	}
	if _, err := os.Stat(filepath.Join(e.repos, "pickme", ".git")); err != nil {
		t.Errorf("clone missing: %v", err)
	}
	lines := e.captureLines()
	last := lines[len(lines)-1]
	if !contains(last.Args, "--resume") || !contains(last.Args, id) {
		t.Errorf("args %v missing --resume %s", last.Args, id)
	}
	if last.ProjectDirName != "pickme" {
		t.Errorf("project_dir_name = %q, want pickme", last.ProjectDirName)
	}
}

func TestWipDirty(t *testing.T) {
	e := newEnv(t)

	// empty repos root: nothing cloned → nothing dirty
	res := e.run(e.home, "wip", "dirty")
	if res.code != 0 || !strings.Contains(res.stdout, "(no dirty repos — everything committed)") {
		t.Fatalf("empty repos root: exit %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.errs)
	}

	_, originDirty := e.seedRepo("dirtyone", map[string]string{"README.md": "x\n"})
	_, originClean := e.seedRepo("cleanone", map[string]string{"README.md": "y\n"})
	if err := os.MkdirAll(e.repos, 0o755); err != nil {
		t.Fatal(err)
	}
	e.git(e.home, "clone", "-q", originDirty, filepath.Join(e.repos, "dirtyone"))
	e.git(e.home, "clone", "-q", originClean, filepath.Join(e.repos, "cleanone"))
	if err := os.WriteFile(filepath.Join(e.repos, "dirtyone", "wip.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// not a git repo: must be skipped, not listed
	if err := os.MkdirAll(filepath.Join(e.repos, "notgit"), 0o755); err != nil {
		t.Fatal(err)
	}

	res = e.run(e.home, "wip", "dirty")
	if res.code != 0 {
		t.Fatalf("cwm wip dirty: exit %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.errs)
	}
	for _, want := range []string{"dirtyone", "1 changed file(s)", "seed"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("dirty listing missing %q:\n%s", want, res.stdout)
		}
	}
	if strings.Contains(res.stdout, "cleanone") || strings.Contains(res.stdout, "notgit") {
		t.Errorf("clean/non-git repos leaked into dirty listing:\n%s", res.stdout)
	}
	if strings.Contains(res.stdout, "(no dirty repos") {
		t.Errorf("dirty repo reported as none:\n%s", res.stdout)
	}

	// clean-only world: every clone committed → the empty message
	e2 := newEnv(t)
	_, originOnly := e2.seedRepo("onlyclean", map[string]string{"README.md": "z\n"})
	if err := os.MkdirAll(e2.repos, 0o755); err != nil {
		t.Fatal(err)
	}
	e2.git(e2.home, "clone", "-q", originOnly, filepath.Join(e2.repos, "onlyclean"))
	res = e2.run(e2.home, "wip", "dirty")
	if res.code != 0 || !strings.Contains(res.stdout, "(no dirty repos — everything committed)") {
		t.Fatalf("clean world: exit %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.errs)
	}
}
