package status

import (
	"os"
	"testing"
)

// TestMain keeps the claude resolver (D49) from searching this machine's
// real install folders: a test must never reach a real claude.
func TestMain(m *testing.M) {
	os.Setenv("CWM_CLAUDE_NO_SEARCH", "1")
	os.Exit(m.Run())
}
