//go:build e2e

package test

import (
	"os"
	"path/filepath"
	"testing"
)

// newCaptureEnv is a v0.8 capture-mode world: ~/.claude is the workspace,
// "main" is the default account and "work" a second, vaulted login.
func newCaptureEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	cfg := "version = 1\nmode = \"capture\"\ndefault_account = \"main\"\n\n[[account]]\nname = \"main\"\ndir = \"" +
		filepath.ToSlash(filepath.Join(e.root, "accounts", "main")) + "\"\n\n[[account]]\nname = \"work\"\ndir = \"" +
		filepath.ToSlash(filepath.Join(e.root, "accounts", "work")) + "\"\n"
	for p, body := range map[string]string{
		filepath.Join(e.root, "config.toml"):                           cfg,
		filepath.Join(e.home, ".claude", ".credentials.json"):          `{"fake":"main-live"}`,
		filepath.Join(e.home, ".claude", "projects", ".keep"):          "",
		filepath.Join(e.root, "accounts", "work", ".credentials.json"): `{"fake":"work-vault"}`,
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func TestRunCaptureDefaultSetsNoConfigDir(t *testing.T) {
	e := newCaptureEnv(t)
	// Even an inherited CLAUDE_CONFIG_DIR is dropped: the capture default
	// must see what plain `claude` sees.
	r := e.runWith(e.home, []string{"CLAUDE_CONFIG_DIR=" + filepath.Join(e.home, "elsewhere")}, "run", "--", "-p", "hi")
	if r.code != 0 {
		t.Fatalf("cwm run: %d %s", r.code, r.errs)
	}
	lines := e.captureLines()
	if len(lines) != 1 || lines[0].ClaudeConfigDir != "" {
		t.Fatalf("capture default launch = %+v, want no CLAUDE_CONFIG_DIR", lines)
	}
	if got := lines[0].Args; len(got) < 2 || got[len(got)-2] != "-p" || got[len(got)-1] != "hi" {
		t.Errorf("args = %v", got)
	}
}

func TestRunParallelAccountUsesLoginOnlyDir(t *testing.T) {
	e := newCaptureEnv(t)
	r := e.run(e.home, "run", "--account", "work", "--", "-p", "hi")
	if r.code != 0 {
		t.Fatalf("cwm run --account work: %d %s", r.code, r.errs)
	}
	work := filepath.Join(e.root, "accounts", "work")
	lines := e.captureLines()
	if len(lines) != 1 || lines[0].ClaudeConfigDir != work {
		t.Fatalf("parallel launch = %+v, want CLAUDE_CONFIG_DIR=%s", lines, work)
	}
	got, err := os.Readlink(filepath.Join(work, "projects"))
	if err != nil || got != filepath.Join(e.home, ".claude", "projects") {
		t.Errorf("login-only projects -> %q (%v)", got, err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.home, ".claude", ".credentials.json")); string(b) != `{"fake":"main-live"}` {
		t.Errorf("a parallel run touched the default's live login: %s", b)
	}
	if r := e.run(e.home, "run", "--account", "ghost"); r.code == 0 {
		t.Error("unknown account accepted")
	}
}
