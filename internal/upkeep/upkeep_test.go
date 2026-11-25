package upkeep

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/mcp"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/workspace"
)

// captureWorld is a temp HOME with ~/.claude, ~/.claude.json and a cwm
// home holding a default and a parallel account.
func captureWorld(t *testing.T) (*storage.Layout, *config.Config, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"userID":"fake-0000","mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	l := storage.NewLayoutAt(filepath.Join(home, ".cwm"))
	cfg := config.Default()
	cfg.Mode = config.ModeCapture
	if err := cfg.AddAccount("main", "~/.cwm/accounts/main"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddAccount("work", "~/.cwm/accounts/work"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetDefaultAccount("main"); err != nil {
		t.Fatal(err)
	}
	if err := claudepaths.Apply(l, cfg); err != nil {
		t.Fatal(err)
	}
	return l, cfg, home
}

func TestTickBridgesHooksAndLinks(t *testing.T) {
	l, cfg, home := captureWorld(t)
	if _, err := workspace.EnsureLoginDir(l, cfg, "work"); err != nil {
		t.Fatal(err)
	}
	workJSON := filepath.Join(claudepaths.LoginDir(l, "work"), ".claude.json")
	if err := os.WriteFile(workJSON, []byte(`{"oauthAccount":{"emailAddress":"w@example.invalid"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mcp.Add(l, "shared", "npx", []string{"-y", "x"}); err != nil {
		t.Fatal(err)
	}
	rep := Tick(l, cfg, time.Now())
	if rep.Skipped != "" || len(rep.Errors) != 0 {
		t.Fatalf("tick %+v", rep)
	}
	if !rep.StIgnore || !rep.Hooks || len(rep.Bridge.Updated) != 2 {
		t.Fatalf("tick %+v", rep)
	}
	for _, p := range []string{filepath.Join(home, ".claude.json"), workJSON} {
		b, _ := os.ReadFile(p)
		if !strings.Contains(string(b), `"shared"`) {
			t.Errorf("%s lacks the workspace server: %s", p, b)
		}
	}
	if !workspace.HooksWired(l) {
		t.Error("hooks not in ~/.claude/settings.json")
	}
	if ok, _, err := mcp.InSync(l, mustTargets(t, l, cfg)); err != nil || !ok {
		t.Errorf("not in sync: %v", err)
	}
	// Idempotent.
	rep = Tick(l, cfg, time.Now())
	if rep.StIgnore || rep.Hooks || len(rep.Bridge.Updated) != 0 || rep.Bridge.WorkspaceChanged {
		t.Fatalf("second tick changed things: %+v", rep)
	}
	// BeforeLaunch: bridged, so no --mcp-config fallback.
	if res := BeforeLaunch(l, cfg, "work"); res.MCPFallback || len(res.Errors) != 0 {
		t.Fatalf("BeforeLaunch %+v", res)
	}
	// Hooks off: the tick leaves them, ApplyHooks removes them.
	cfg.Features.Hooks = false
	if rep := Tick(l, cfg, time.Now()); rep.Hooks {
		t.Fatal("tick touched hooks while features.hooks is off")
	}
	if wrote, err := ApplyHooks(l, cfg); err != nil || !wrote || workspace.HooksWired(l) {
		t.Fatalf("ApplyHooks(off) = %v %v", wrote, err)
	}
}

func TestTickNeverRunsBeforeMigration(t *testing.T) {
	l, cfg, home := captureWorld(t)
	cfg.Mode = "" // a pre-v0.8 config
	if err := mcp.Add(l, "shared", "npx", nil); err != nil {
		t.Fatal(err)
	}
	rep := Tick(l, cfg, time.Now())
	if rep.Skipped == "" {
		t.Fatal("tick ran on an un-migrated config")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", ".stignore")); !os.IsNotExist(err) {
		t.Fatal(".stignore written before migration")
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".claude.json")); strings.Contains(string(b), "shared") {
		t.Fatal(".claude.json touched before migration")
	}
	if res := BeforeLaunch(l, cfg, ""); !res.MCPFallback {
		t.Fatal("an un-migrated launch must fall back to --mcp-config")
	}
}

func mustTargets(t *testing.T, l *storage.Layout, cfg *config.Config) []mcp.BridgeTarget {
	t.Helper()
	ts, err := Targets(l, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}
