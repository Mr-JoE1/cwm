package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
)

// TestSessionEndReconcilesLogin (D126): SessionEnd puts the newest copy of
// the default account's login in both places, also with handoff_push off,
// and says so in logs/vault.log.
func TestSessionEndReconcilesLogin(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(k, home)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	l, err := claudepaths.NewLayout()
	if err != nil {
		t.Fatal(err)
	}
	mk := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	vaultDir := filepath.Join(l.AccountsDir(), "main")
	mk(l.ConfigPath(), "mode = \"capture\"\ndefault_account = \"main\"\n[features]\nhandoff_push = false\n[[account]]\nname = \"main\"\ndir = \""+filepath.ToSlash(vaultDir)+"\"\n")
	live := filepath.Join(home, ".claude", ".credentials.json")
	mk(live, `{"claudeAiOauth":{"accessToken":"fake-old","expiresAt":100}}`)
	mk(filepath.Join(vaultDir, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"fake-renewed","expiresAt":200}}`)
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if got := runCLI(t, "hook", "session-end"); got.code != ExitOK {
		t.Fatalf("hook = %d %q", got.code, got.err)
	}
	if b, _ := os.ReadFile(live); !strings.Contains(string(b), "fake-renewed") {
		t.Fatalf("live = %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(l.LogsDir(), "vault.log")); !strings.Contains(string(b), "the vault held a newer login for main") {
		t.Fatalf("vault.log = %q", b)
	}
}
