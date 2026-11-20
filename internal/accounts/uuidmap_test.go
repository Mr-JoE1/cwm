package accounts

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/config"
)

// Claude Code itself writes camelCase oauthAccount keys.
func TestReadIdentityCamelCase(t *testing.T) {
	path := writeFixture(t,
		`{"accountUuid":"uuid-c","emailAddress":"cc@example.com","organizationUuid":"org-1","organizationName":"Acme Inc","displayName":"Dev"}`)
	id, err := ReadIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if id.Email != "cc@example.com" || id.AccountUUID != "uuid-c" || id.OrganizationName != "Acme Inc" {
		t.Errorf("identity = %+v", id)
	}
}

// snake_case (what cwm read first) wins when a file has both spellings.
func TestReadIdentityBothSpellings(t *testing.T) {
	path := writeFixture(t,
		`{"email":"snake@example.com","emailAddress":"camel@example.com","accountUuid":"uuid-c","organization_name":"snake-org","organizationName":"camel-org"}`)
	id, err := ReadIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if id.Email != "snake@example.com" || id.AccountUUID != "uuid-c" || id.OrganizationName != "snake-org" {
		t.Errorf("identity = %+v", id)
	}
}

func TestUUIDMap(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	cfg := config.Default()
	dir := func(name, claudeJSON string) string {
		d := filepath.Join(root, name)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if claudeJSON != "" {
			if err := os.WriteFile(filepath.Join(d, ".claude.json"), []byte(claudeJSON), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	add := func(name, d string, login *config.Login) {
		if err := cfg.AddAccount(name, d); err != nil {
			t.Fatal(err)
		}
		if login != nil {
			if err := cfg.SetAccountLogin(name, *login); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Recorded uuid wins over the file.
	add("main", dir("main", `{"oauthAccount":{"accountUuid":"file-main"}}`), &config.Login{Email: "m@x", AccountUUID: "cfg-main"})
	// Nothing recorded: read from .claude.json (camelCase).
	add("work", dir("work", `{"oauthAccount":{"accountUuid":"file-work","emailAddress":"w@x"}}`), &config.Login{Email: "w@x"})
	// No login block at all: file fallback (snake_case).
	add("old", dir("old", `{"oauthAccount":{"account_uuid":"file-old"}}`), nil)
	// Skipped: broken JSON, missing file, missing dir, no uuid, duplicate.
	add("broken", dir("broken", `{"oauthAccount":`), nil)
	add("nofile", dir("nofile", ""), nil)
	add("nodir", filepath.Join(root, "does-not-exist"), nil)
	add("nouuid", dir("nouuid", `{"oauthAccount":{"emailAddress":"n@x"}}`), nil)
	add("dup", dir("dup", `{"oauthAccount":{"accountUuid":"file-work"}}`), nil)
	add("notobj", dir("notobj", `[1,2]`), nil)

	got := UUIDMap(cfg)
	want := map[string]string{"cfg-main": "main", "file-work": "work", "file-old": "old"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UUIDMap = %v, want %v", got, want)
	}
	if m := UUIDMap(nil); m == nil || len(m) != 0 {
		t.Errorf("UUIDMap(nil) = %v", m)
	}
	if m := UUIDMap(config.Default()); len(m) != 0 {
		t.Errorf("UUIDMap(empty) = %v", m)
	}
}
