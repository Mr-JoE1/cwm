package accounts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// stubClaude puts a fake `claude` on PATH that mimics fakeclaude.sh's
// `auth login` handling: it writes credentials into $CLAUDE_CONFIG_DIR.
func stubClaude(t *testing.T, email string) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "login" ]; then
  d=${CLAUDE_CONFIG_DIR:?CLAUDE_CONFIG_DIR not set}
  echo '{"token":"fake"}' > "$d/.credentials.json"
  echo '{"oauthAccount":{"email":"` + email + `","account_uuid":"uuid-1"},"plan":"claude_max"}' > "$d/.claude.json"
  echo done
  exit 0
fi
echo 2.1.400
`
	p := filepath.Join(bin, "claude")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func newTestEnv(t *testing.T) (*storage.Layout, *config.Config) {
	t.Helper()
	l := storage.NewLayoutAt(t.TempDir())
	return l, config.Default()
}

func TestAddRegistersAccountWithIdentity(t *testing.T) {
	stubClaude(t, "dev@example.com")
	l, cfg := newTestEnv(t)
	var out bytes.Buffer

	if err := Add(l, cfg, "main", &out); err != nil {
		t.Fatalf("Add: %v", err)
	}
	acct, ok := cfg.Account("main")
	if !ok {
		t.Fatal("account main not registered")
	}
	if want := filepath.Join(l.AccountsDir(), "main"); acct.Dir != want {
		t.Errorf("dir = %q, want %q", acct.Dir, want)
	}
	if acct.Login == nil || acct.Login.Email != "dev@example.com" ||
		acct.Login.Plan != "claude_max" || acct.Login.AccountUUID != "uuid-1" {
		t.Errorf("login = %+v, want identity stored in config", acct.Login)
	}
	if cfg.DefaultAccount != "main" {
		t.Errorf("first account must become default, got %q", cfg.DefaultAccount)
	}
	if got, want := out.String(), "✓ main (dev@example.com) added\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	info, err := os.Stat(filepath.Join(l.AccountsDir(), "main"))
	if err != nil || !info.IsDir() {
		t.Fatalf("account dir not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("account dir perm = %o, want 700", perm)
	}
}

func TestAddInvalidName(t *testing.T) {
	l, cfg := newTestEnv(t)
	for _, bad := range []string{"Main", "-x", "1", "", strings.Repeat("a", 33)} {
		if err := Add(l, cfg, bad, &bytes.Buffer{}); err == nil {
			t.Errorf("Add(%q) must fail", bad)
		}
	}
}

func TestAddDuplicate(t *testing.T) {
	stubClaude(t, "dev@example.com")
	l, cfg := newTestEnv(t)
	if err := Add(l, cfg, "main", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := Add(l, cfg, "main", &bytes.Buffer{}); err == nil {
		t.Fatal("duplicate add must fail")
	}
}

func TestAddWithoutIdentityWarnsAndKeepsAccount(t *testing.T) {
	// Stub that succeeds at login but writes no credentials.
	bin := t.TempDir()
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	l, cfg := newTestEnv(t)
	var out bytes.Buffer
	if err := Add(l, cfg, "main", &out); err != nil {
		t.Fatalf("Add: %v (account must be kept)", err)
	}
	if _, ok := cfg.Account("main"); !ok {
		t.Fatal("account must stay registered without identity")
	}
	if !strings.Contains(out.String(), "⚠ main added") {
		t.Errorf("output = %q, want a warning", out.String())
	}
}

func TestAddLoginFails(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\necho boom >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	l, cfg := newTestEnv(t)
	err := Add(l, cfg, "main", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "auth login failed") {
		t.Fatalf("err = %v, want login failure", err)
	}
	if _, ok := cfg.Account("main"); ok {
		t.Error("failed login must not register the account")
	}
}

func seededConfig(t *testing.T) (*storage.Layout, *config.Config) {
	t.Helper()
	l := storage.NewLayoutAt(t.TempDir())
	cfg := config.Default()
	for _, name := range []string{"main", "second"} {
		dir := filepath.Join(l.AccountsDir(), name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := cfg.AddAccount(name, dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := cfg.SetAccountLogin("main", config.Login{Email: "dev@example.com", Plan: "claude_max"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetDefaultAccount("main"); err != nil {
		t.Fatal(err)
	}
	return l, cfg
}

func TestListTable(t *testing.T) {
	l, cfg := seededConfig(t)
	cred := filepath.Join(l.AccountsDir(), "main", ".credentials.json")
	if err := os.WriteFile(cred, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 11, 3, 10, 30, 0, 0, time.Local)
	if err := os.Chtimes(cred, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := List(cfg, l, false, &out); err != nil {
		t.Fatalf("List: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"#", "name", "email", "plan", "default*", "last-login",
		"main", "dev@example.com", "claude_max", "*",
		"second", "-", "2026-11-03 10:30",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q:\n%s", want, got)
		}
	}
}

func TestListJSON(t *testing.T) {
	l, cfg := seededConfig(t)
	if err := os.WriteFile(filepath.Join(l.AccountsDir(), "second", ".credentials.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := List(cfg, l, true, &out); err != nil {
		t.Fatalf("List --json: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0]["name"] != "main" || rows[0]["default"] != true ||
		rows[0]["email"] != "dev@example.com" || rows[0]["plan"] != "claude_max" {
		t.Errorf("row 0 = %v", rows[0])
	}
	if _, has := rows[0]["last_login"]; has {
		t.Errorf("row 0 last_login = %v, want absent (no creds file)", rows[0]["last_login"])
	}
	if rows[1]["default"] != false {
		t.Errorf("row 1 default = %v, want false", rows[1]["default"])
	}
	if _, has := rows[1]["last_login"]; !has {
		t.Errorf("row 1 last_login missing")
	}
}

func TestUse(t *testing.T) {
	_, cfg := newTestEnv(t)
	if err := cfg.AddAccount("main", "/x/main"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddAccount("second", "/x/second"); err != nil {
		t.Fatal(err)
	}
	if err := Use(cfg, "second"); err != nil {
		t.Fatalf("Use: %v", err)
	}
	if cfg.DefaultAccount != "second" {
		t.Errorf("default = %q, want second", cfg.DefaultAccount)
	}
	if err := Use(cfg, "nope"); err == nil {
		t.Error("Use of unknown account must fail")
	}
}

func TestRemoveBacksUpAndDrops(t *testing.T) {
	l, cfg := newTestEnv(t)
	dir := filepath.Join(l.AccountsDir(), "main")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"token":"t"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddAccount("main", dir); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetDefaultAccount("main"); err != nil {
		t.Fatal(err)
	}

	if err := Remove(l, cfg, "main"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := cfg.Account("main"); ok {
		t.Error("account not dropped from config")
	}
	if cfg.DefaultAccount != "" {
		t.Errorf("default = %q, want cleared", cfg.DefaultAccount)
	}
	// Backup must hold a copy of the credentials; the account dir itself
	// stays on disk (Remove only deregisters + backs up).
	backups, err := os.ReadDir(l.BackupsDir())
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, err = %v, want exactly one entry", backups, err)
	}
	if !strings.Contains(backups[0].Name(), "account-main") {
		t.Errorf("backup name = %q, want *-account-main", backups[0].Name())
	}
	data, err := os.ReadFile(filepath.Join(l.BackupsDir(), backups[0].Name(), ".credentials.json"))
	if err != nil || string(data) != `{"token":"t"}` {
		t.Errorf("backup credentials = %q, err = %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".credentials.json")); err != nil {
		t.Errorf("source account dir must survive remove: %v", err)
	}
}

func TestRemoveUnknown(t *testing.T) {
	l, cfg := newTestEnv(t)
	if err := Remove(l, cfg, "ghost"); err == nil {
		t.Fatal("removing an unknown account must fail")
	}
}
