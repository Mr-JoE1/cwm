package accounts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture writes a .claude.json with a large filler section so the
// parser is exercised against multi-megabyte documents.
func writeFixture(t *testing.T, oauth string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"firstRun":true,"projects":{`)
	for i := 0; i < 400; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"/repo/`)
		b.WriteString(strings.Repeat("p", 20))
		b.WriteString(strings.Repeat("0123456789", 60)) // ~600B filler per entry
		b.WriteString(`": {"history":[1,2,3],"counters":{"a":1,"b":[4,5,6]}}`)
	}
	b.WriteString(`}`)
	if oauth != "" {
		b.WriteString(`,"oauthAccount":` + oauth)
	}
	b.WriteString(`,"numStartups":42}`)
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadIdentity(t *testing.T) {
	path := writeFixture(t,
		`{"email":"dev@example.com","account_uuid":"uuid-1","organization_name":"acme","plan":"claude_max"}`)
	id, err := ReadIdentity(path)
	if err != nil {
		t.Fatalf("ReadIdentity: %v", err)
	}
	if id.Email != "dev@example.com" || id.AccountUUID != "uuid-1" ||
		id.OrganizationName != "acme" || id.Plan != "claude_max" {
		t.Errorf("identity = %+v, want all fields extracted", id)
	}
}

func TestReadIdentityToleratesMissingKeys(t *testing.T) {
	path := writeFixture(t, `{"email":"dev@example.com"}`)
	id, err := ReadIdentity(path)
	if err != nil {
		t.Fatalf("ReadIdentity: %v", err)
	}
	if id.Email != "dev@example.com" || id.AccountUUID != "" || id.OrganizationName != "" {
		t.Errorf("identity = %+v, want email only", id)
	}
}

func TestReadIdentityNoOAuthBlock(t *testing.T) {
	path := writeFixture(t, "")
	id, err := ReadIdentity(path)
	if err != nil {
		t.Fatalf("missing oauthAccount must not be an error: %v", err)
	}
	if id != (Identity{}) {
		t.Errorf("identity = %+v, want zero value", id)
	}
}

func TestReadIdentityTopLevelPlanFallback(t *testing.T) {
	path := writeFixture(t, `{"email":"dev@example.com"}`)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"numStartups":42`, `"numStartups":42,"plan":"pro"`, 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := ReadIdentity(path)
	if err != nil {
		t.Fatalf("ReadIdentity: %v", err)
	}
	if id.Plan != "pro" {
		t.Errorf("plan = %q, want top-level fallback picked up", id.Plan)
	}
}

func TestReadIdentityMissingFile(t *testing.T) {
	if _, err := ReadIdentity(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("missing file must be an error")
	}
}

func TestReadIdentityMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(`{"oauthAccount": "not-an-object"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadIdentity(path); err == nil {
		t.Fatal("malformed oauthAccount must be an error")
	}
}
