package sessions

import (
	"os"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/testsafe"
)

// TestMain keeps the claude resolver (D49) from searching this machine's
// real install folders and strips real claude dirs from PATH: a test must
// never reach a real claude.
func TestMain(m *testing.M) {
	testsafe.NoRealClaude()
	os.Exit(m.Run())
}
