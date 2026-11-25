package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/testsafe"
)

// TestMain keeps the claude resolver (D49) from searching this machine's
// real install folders and strips real claude dirs from PATH: a test must
// never reach a real claude.
func TestMain(m *testing.M) {
	os.Exit(testsafe.Main(m))
}

// isolatedCWMHome sets CWM_HOME to root and selects isolated mode there,
// so root/workspace is the workspace root of these v0.7-shaped fixtures
// (D76: capture mode resolves ~/.claude instead).
func isolatedCWMHome(t *testing.T, root string) {
	t.Helper()
	t.Setenv("CWM_HOME", root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("mode = \"isolated\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
