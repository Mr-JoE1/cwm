package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

func TestEnsureHooksIdempotentMerge(t *testing.T) {
	l := storage.NewLayoutAt(t.TempDir())
	ws := t.TempDir()
	l.SetWorkspaceDir(ws)
	p := filepath.Join(ws, "settings.json")
	orig := `{
  "model": "opus",
  "permissions": {"allow": ["Bash(ls:*)"]},
  "hooks": {
    "SessionStart": [
      {"matcher": "startup", "hooks": [
        {"type": "command", "command": "echo mine"},
        {"type": "command", "command": "\"/opt/old path/cwm\" hook session-start"}
      ]}
    ],
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "guard.sh"}]}]
  }
}`
	if err := os.WriteFile(p, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureHooks(l, true)
	if err != nil || !changed {
		t.Fatalf("EnsureHooks = %v, %v", changed, err)
	}
	if !HooksWired(l) {
		t.Fatal("hooks not wired")
	}
	data, _ := os.ReadFile(p)
	s := string(data)
	for _, want := range []string{`"model": "opus"`, `Bash(ls:*)`, `echo mine`, `guard.sh`, `"cwm hook session-start"`, `"cwm hook session-end"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in\n%s", want, s)
		}
	}
	if strings.Contains(s, "/opt/old path/cwm") {
		t.Error("old absolute-path cwm hook kept")
	}
	if strings.Count(s, "cwm hook session-start") != 1 {
		t.Error("duplicate session-start hook")
	}
	// Windows has no Unix permission bits (os.Stat reports 0666).
	if info, _ := os.Stat(p); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	// Second run: nothing to do.
	if changed, err := EnsureHooks(l, true); err != nil || changed {
		t.Fatalf("second EnsureHooks = %v, %v", changed, err)
	}
	// Off: only cwm's entries go.
	if changed, err := EnsureHooks(l, false); err != nil || !changed {
		t.Fatalf("EnsureHooks(off) = %v, %v", changed, err)
	}
	data, _ = os.ReadFile(p)
	var top map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "cwm hook") || !strings.Contains(string(data), "echo mine") || !strings.Contains(string(data), "guard.sh") {
		t.Fatalf("after off:\n%s", data)
	}
	if _, ok := top["hooks"].(map[string]any)["SessionEnd"]; ok {
		t.Error("empty SessionEnd left behind")
	}
}

func TestEnsureHooksMissingAndBrokenFile(t *testing.T) {
	l := storage.NewLayoutAt(t.TempDir())
	ws := t.TempDir()
	l.SetWorkspaceDir(ws)
	if changed, err := EnsureHooks(l, false); err != nil || changed {
		t.Fatalf("off on a missing file = %v, %v", changed, err)
	}
	if _, err := os.Stat(SettingsPath(l)); !os.IsNotExist(err) {
		t.Fatal("off created settings.json")
	}
	if changed, err := EnsureHooks(l, true); err != nil || !changed || !HooksWired(l) {
		t.Fatalf("on a missing file = %v, %v", changed, err)
	}
	if err := os.WriteFile(SettingsPath(l), []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureHooks(l, true); err == nil {
		t.Fatal("broken settings.json must not be overwritten")
	}
	if b, _ := os.ReadFile(SettingsPath(l)); string(b) != `{broken` {
		t.Fatal("broken settings.json rewritten")
	}
}

func TestIsCwmHook(t *testing.T) {
	for _, yes := range []string{"cwm hook session-start", `"C:\Program Files\cwm\cwm.exe" hook session-end`, "/home/u/bin/cwm hook session-end"} {
		if !IsCwmHook(yes) {
			t.Errorf("%q not recognised", yes)
		}
	}
	for _, no := range []string{"echo cwm hook session-start", "mycwm hook session-start", "cwm hook other", "cwm wip"} {
		if IsCwmHook(no) {
			t.Errorf("%q recognised", no)
		}
	}
}
