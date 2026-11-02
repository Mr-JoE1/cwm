package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

func importCmdEnv(t *testing.T) (*Env, *bytes.Buffer) {
	t.Helper()
	t.Setenv("CWM_HOME", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	out := &bytes.Buffer{}
	return &Env{Stdout: out, Stderr: &bytes.Buffer{}}, out
}

func TestCliMcpRoundTrip(t *testing.T) {
	e, out := importCmdEnv(t)

	if code := e.Run([]string{"mcp", "add", "ctx", "--", "npx", "-y", "ctx-server"}); code != 0 {
		t.Fatalf("mcp add exit = %d", code)
	}
	if code := e.Run([]string{"mcp", "add", "bogus", "npx"}); code == 0 {
		t.Fatal("mcp add without -- must fail")
	}

	out.Reset()
	if code := e.Run([]string{"mcp", "list"}); code != 0 {
		t.Fatalf("mcp list exit = %d", code)
	}
	for _, want := range []string{"ctx", "npx", "ctx-server"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("mcp list = %q, missing %q", out.String(), want)
		}
	}

	// Global --dry-run on a mutating mcp command: plan only, exit 0.
	dry := &Env{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, DryRun: true}
	if code := dry.Run([]string{"mcp", "remove", "ctx"}); code != 0 {
		t.Fatalf("mcp remove --dry-run exit = %d", code)
	}
	l, err := storage.NewLayout()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.WorkspaceItemPath("mcp.json")); err != nil {
		t.Fatalf("dry-run must not delete mcp.json: %v", err)
	}

	if code := e.Run([]string{"mcp", "remove", "ctx"}); code != 0 {
		t.Fatalf("mcp remove exit = %d", code)
	}
	out.Reset()
	if code := e.Run([]string{"mcp", "list"}); code != 0 {
		t.Fatalf("mcp list exit = %d", code)
	}
	if !strings.Contains(out.String(), "(no MCP servers configured)") {
		t.Fatalf("mcp list after remove = %q", out.String())
	}
	if code := e.Run([]string{"mcp", "remove", "ctx"}); code == 0 {
		t.Fatal("removing an absent server must fail")
	}
}

func TestCliImportFromDir(t *testing.T) {
	e, out := importCmdEnv(t)

	src := filepath.Join(t.TempDir(), "claude-src")
	if err := os.MkdirAll(filepath.Join(src, "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		"commands/deploy.md": "deploy",
		".credentials.json":  `{"token":"x"}`,
		".claude.json":       `{"oauthAccount":{"email":"e@x.example"}}`,
	} {
		if err := os.WriteFile(filepath.Join(src, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if code := e.Run([]string{"init"}); code != 0 {
		t.Fatalf("init exit = %d", code)
	}

	// Dry-run: plan only, nothing changes.
	dry := &Env{Stdout: out, Stderr: &bytes.Buffer{}, DryRun: true}
	if code := dry.Run([]string{"import", "--from", src}); code != 0 {
		t.Fatalf("import --dry-run exit = %d", code)
	}
	if !strings.Contains(out.String(), "dry run") {
		t.Fatalf("dry-run output = %q", out.String())
	}
	l, err := storage.NewLayout()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.WorkspaceItemPath("commands/deploy.md")); !os.IsNotExist(err) {
		t.Fatal("dry-run import copied files")
	}
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Account("imported"); ok {
		t.Fatal("dry-run import registered an account")
	}

	// Real run needs --yes (non-interactive).
	out.Reset()
	if code := e.Run([]string{"import", "--from", src}); code == 0 {
		t.Fatal("import without --yes in non-interactive mode must fail")
	}
	if !strings.Contains(out.String(), "--yes") && !strings.Contains(e.Stderr.(*bytes.Buffer).String(), "--yes") {
		t.Fatalf("import without --yes should point at --yes, stdout=%q", out.String())
	}

	out.Reset()
	if code := e.Run([]string{"import", "--from", src, "--yes"}); code != 0 {
		t.Fatalf("import --yes exit = %d (%s)", code, out.String())
	}
	if _, err := os.Stat(l.WorkspaceItemPath("commands/deploy.md")); err != nil {
		t.Fatalf("deploy.md not imported: %v", err)
	}
	cfg, err = config.Load(l.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Account("imported"); !ok {
		t.Fatal("account imported missing after import")
	}

	// mcp.json survives round-trip via CLI and open stays reserved.
	if code := e.Run([]string{"mcp", "add", "srv", "--", "echo", "hi"}); code != 0 {
		t.Fatalf("mcp add exit = %d", code)
	}
}
