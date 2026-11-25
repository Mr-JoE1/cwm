package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func configSandbox(t *testing.T) (home, root string) {
	t.Helper()
	home = t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		t.Setenv(k, home)
	}
	root = filepath.Join(home, ".cwm")
	t.Setenv("CWM_HOME", root)
	return home, root
}

func TestConfigListGetSet(t *testing.T) {
	home, root := configSandbox(t)
	got := runCLI(t, "config", "list")
	if got.code != ExitOK || !strings.Contains(got.out, "sync.sessions") || !strings.Contains(got.out, "features.update_check") {
		t.Fatalf("list: %d %q %q", got.code, got.out, got.err)
	}
	if got := runCLI(t, "config", "set", "sync.settings", "off"); got.code != ExitOK || !strings.Contains(got.out, "sync.settings = false") {
		t.Fatalf("set: %d %q %q", got.code, got.out, got.err)
	}
	if got := runCLI(t, "config", "get", "sync.settings"); strings.TrimSpace(got.out) != "false" {
		t.Errorf("get = %q", got.out)
	}
	data, err := os.ReadFile(filepath.Join(root, "config.toml"))
	if err != nil || !strings.Contains(string(data), "settings = false") {
		t.Errorf("config.toml = %q %v", data, err)
	}
	// capture mode: the root is ~/.claude and its allow-list follows.
	ig, err := os.ReadFile(filepath.Join(home, ".claude", ".stignore"))
	if err != nil || strings.Contains(string(ig), "!/settings.json") || !strings.Contains(string(ig), "!/projects") {
		t.Errorf(".stignore after sync.settings off = %q %v", ig, err)
	}
	if got := runCLI(t, "--dry-run", "config", "set", "features.stats", "off"); got.code != ExitOK || !strings.Contains(got.out, "would set") {
		t.Errorf("dry-run: %q", got.out)
	}
	if got := runCLI(t, "config", "get", "features.stats"); strings.TrimSpace(got.out) != "true" {
		t.Errorf("dry-run changed the value: %q", got.out)
	}
	for _, bad := range [][]string{{"config", "get", "nope"}, {"config", "set", "sync.sessions", "maybe"}, {"config", "frob"}, {"config"}} {
		if got := runCLI(t, bad...); got.code == ExitOK {
			t.Errorf("%v accepted", bad)
		}
	}
	got = runCLI(t, "config", "list", "--json")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(got.out), &rows); err != nil || len(rows) < 20 {
		t.Fatalf("list --json: %v %d", err, len(rows))
	}
	if rows[0]["key"] != "mode" || rows[0]["value"] != "capture" {
		t.Errorf("first row = %v", rows[0])
	}
}

func TestModeShowAndSet(t *testing.T) {
	home, root := configSandbox(t)
	got := runCLI(t, "mode", "show")
	if got.code != ExitOK || !strings.Contains(got.out, "mode: capture") || !strings.Contains(got.out, "not set") {
		t.Fatalf("show: %q %q", got.out, got.err)
	}
	if strings.Contains(got.out, home) {
		t.Errorf("show prints the home dir instead of ~: %q", got.out)
	}
	if got := runCLI(t, "config", "set", "mode", "isolated"); got.code == ExitOK || !strings.Contains(got.err, "--yes") {
		t.Fatalf("non-interactive set without --yes: %q %q", got.out, got.err)
	}
	if got := runCLI(t, "mode", "set", "isolated", "--yes"); got.code != ExitOK || !strings.Contains(got.out, "migrated to isolated mode") {
		t.Fatalf("set: %q %q", got.out, got.err)
	}
	got = runCLI(t, "mode", "show", "--json")
	var rep map[string]any
	if err := json.Unmarshal([]byte(got.out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep["mode"] != "isolated" || !strings.HasSuffix(rep["root"].(string), filepath.Join(".cwm", "workspace")) {
		t.Errorf("show --json = %v", rep)
	}
	if got := runCLI(t, "mode", "set", "cloud"); got.code == ExitOK {
		t.Error("accepted mode cloud")
	}
	// A v0.7 config (no mode) is flagged for migration.
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := runCLI(t, "mode", "show"); !strings.Contains(got.out, "cwm migrate --dry-run") {
		t.Errorf("legacy show = %q", got.out)
	}
}
