package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/accounts"
)

// useSandbox: capture mode, accounts main (default, live login) and
// work (vault login), obviously fake tokens, no /proc scan.
func useSandbox(t *testing.T) (home, root string) {
	t.Helper()
	home, root = configSandbox(t)
	old := accountSwitcher
	accountSwitcher = accounts.Switcher{Store: accounts.FileStore{}, Running: func() []int { return []int{7} }}
	t.Cleanup(func() { accountSwitcher = old })
	cfg := "version = 1\nmode = \"capture\"\ndefault_account = \"main\"\n\n[[account]]\nname = \"main\"\ndir = " +
		`"` + filepath.ToSlash(filepath.Join(root, "accounts", "main")) + `"` + "\n\n[[account]]\nname = \"work\"\ndir = " +
		`"` + filepath.ToSlash(filepath.Join(root, "accounts", "work")) + `"` + "\n"
	for p, body := range map[string]string{
		filepath.Join(root, "config.toml"):                           cfg,
		filepath.Join(home, ".claude", ".credentials.json"):          `{"fake":"main-live"}`,
		filepath.Join(home, ".claude.json"):                          `{"userID":"fake-u","oauthAccount":{"emailAddress":"main@example.invalid"}}`,
		filepath.Join(root, "accounts", "work", ".credentials.json"): `{"fake":"work-vault"}`,
		filepath.Join(root, "accounts", "work", ".claude.json"):      `{"oauthAccount":{"emailAddress":"work@example.invalid"}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, root
}

func TestAccountUseSwapsLiveLogin(t *testing.T) {
	home, root := useSandbox(t)
	if got := runCLI(t, "--dry-run", "account", "use", "work"); got.code != ExitOK || !strings.Contains(got.out, "would save") {
		t.Fatalf("dry-run: %q %q", got.out, got.err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json")); string(b) != `{"fake":"main-live"}` {
		t.Fatalf("dry-run touched the live login: %s", b)
	}
	got := runCLI(t, "account", "use", "work")
	if got.code != ExitOK || !strings.Contains(got.out, "default account: work") || !strings.Contains(got.out, "keep using main until restarted") {
		t.Fatalf("use: %q %q", got.out, got.err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json")); string(b) != `{"fake":"work-vault"}` {
		t.Errorf("live = %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "accounts", "main", ".credentials.json")); string(b) != `{"fake":"main-live"}` {
		t.Errorf("main not saved back: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".claude.json")); !bytes.Contains(b, []byte(`"userID": "fake-u"`)) || !bytes.Contains(b, []byte("work@example.invalid")) {
		t.Errorf("live .claude.json = %s", b)
	}
	if strings.Contains(got.out+got.err, "fake") {
		t.Error("a credential value reached the output")
	}
}

// Removing the default hands the live login to the next account first.
func TestAccountRemoveDefaultSwitchesFirst(t *testing.T) {
	home, root := useSandbox(t)
	got := runCLI(t, "account", "remove", "main", "--yes")
	if got.code != ExitOK {
		t.Fatalf("remove: %q %q", got.out, got.err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json")); string(b) != `{"fake":"work-vault"}` {
		t.Errorf("live after removing the default = %s", b)
	}
	if got := runCLI(t, "config", "list"); got.code != ExitOK {
		t.Fatal(got.err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "config.toml"))
	if !strings.Contains(string(data), `default_account = "work"`) || strings.Contains(string(data), `name = "main"`) {
		t.Errorf("config = %s", data)
	}
}
