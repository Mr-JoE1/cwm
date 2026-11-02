package workspace

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

func migrateTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func migrateWriteTree(t *testing.T, base string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func migrateSnapTree(t *testing.T, base string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		rel, relErr := filepath.Rel(base, p)
		if relErr != nil {
			return nil
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func migrateInitLayout(t *testing.T) (*storage.Layout, *config.Config) {
	t.Helper()
	l := storage.NewLayoutAt(t.TempDir())
	if err := Init(l, "", false, io.Discard); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return l, cfg
}

func migrateReadFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func TestMigrateFromBashKit(t *testing.T) {
	home := migrateTestHome(t)
	l, cfg := migrateInitLayout(t)

	src := filepath.Join(home, ".claude-me")
	migrateWriteTree(t, src, map[string]string{
		"projects/alpha/notes.md": "alpha notes",
		"skills/team/SKILL.md":    "source skill",
		"CLAUDE.md":               "migrated playbook",
		".credentials.json":       `{"token":"tk"}`,
		".claude.json":            `{"oauthAccount":{"email":"dev@example.com","account_uuid":"u1"},"plan":"claude_max"}`,
		"junk.txt":                "not a workspace item",
	})
	// Pre-existing workspace content: one conflict, one untouched file.
	migrateWriteTree(t, l.WorkspaceDir(), map[string]string{
		"skills/team/SKILL.md":  "existing skill",
		"projects/beta/keep.md": "beta stays",
	})
	before := migrateSnapTree(t, src)

	var out bytes.Buffer
	if err := MigrateFromBashKit(l, cfg, MigrateOptions{Stdout: &out, Yes: true}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Workspace populated, conflicts kept, junk ignored.
	if got := migrateReadFile(t, l.WorkspaceItemPath("CLAUDE.md")); got != "migrated playbook" {
		t.Fatalf("CLAUDE.md = %q", got)
	}
	if got := migrateReadFile(t, l.WorkspaceItemPath("projects/alpha/notes.md")); got != "alpha notes" {
		t.Fatalf("notes.md = %q", got)
	}
	if got := migrateReadFile(t, l.WorkspaceItemPath("skills/team/SKILL.md")); got != "existing skill" {
		t.Fatalf("conflicting SKILL.md = %q, want existing kept", got)
	}
	if got := migrateReadFile(t, l.WorkspaceItemPath("projects/beta/keep.md")); got != "beta stays" {
		t.Fatalf("pre-existing keep.md = %q", got)
	}
	if _, err := os.Stat(l.WorkspaceItemPath("junk.txt")); !os.IsNotExist(err) {
		t.Fatal("junk.txt must not be imported")
	}

	// Account main: creds copied, registered as default, identity recorded.
	acctDir := filepath.Join(l.AccountsDir(), "main")
	if got := migrateReadFile(t, filepath.Join(acctDir, ".credentials.json")); got != `{"token":"tk"}` {
		t.Fatalf("credentials = %q", got)
	}
	reload, err := config.Load(l.ConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	acct, ok := reload.Account("main")
	if !ok {
		t.Fatal("account main not registered")
	}
	if reload.DefaultAccount != "main" {
		t.Fatalf("default account = %q, want main", reload.DefaultAccount)
	}
	if acct.Login == nil || acct.Login.Email != "dev@example.com" || acct.Login.Plan != "claude_max" {
		t.Fatalf("account login = %+v", acct.Login)
	}

	// Launch dir assembled and readable through the links.
	if got := migrateReadFile(t, filepath.Join(l.LaunchDir(), "main", "CLAUDE.md")); got != "migrated playbook" {
		t.Fatalf("launch CLAUDE.md = %q", got)
	}
	if got := migrateReadFile(t, filepath.Join(l.LaunchDir(), "main", ".credentials.json")); got != `{"token":"tk"}` {
		t.Fatalf("launch credentials = %q", got)
	}

	// Receipt written listing the copies.
	receipts, err := filepath.Glob(filepath.Join(l.BackupsDir(), "*-migrate", "RECEIPT.md"))
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipt glob = %v, %v", receipts, err)
	}
	if got := migrateReadFile(t, receipts[0]); !strings.Contains(got, "CLAUDE.md") || !strings.Contains(got, "copied") {
		t.Fatalf("receipt = %q", got)
	}

	// Plan output mentions the conflict; source stays untouched.
	if !strings.Contains(out.String(), "existing content kept") {
		t.Fatalf("plan output missing conflict line: %q", out.String())
	}
	if after := migrateSnapTree(t, src); !reflect.DeepEqual(before, after) {
		t.Fatal("source ~/.claude-me was modified")
	}
}

func TestMigrateFromBashKitDryRunTouchesNothing(t *testing.T) {
	home := migrateTestHome(t)
	l, cfg := migrateInitLayout(t)

	src := filepath.Join(home, ".claude-me")
	migrateWriteTree(t, src, map[string]string{
		"CLAUDE.md":         "migrated playbook",
		".credentials.json": `{"token":"tk"}`,
		".claude.json":      `{"oauthAccount":{"email":"dev@example.com"}}`,
	})
	before := migrateSnapTree(t, src)

	var out bytes.Buffer
	if err := MigrateFromBashKit(l, cfg, MigrateOptions{Stdout: &out, DryRun: true}); err != nil {
		t.Fatalf("dry-run migrate: %v", err)
	}
	if !strings.Contains(out.String(), "dry run") {
		t.Fatalf("dry-run output = %q", out.String())
	}

	if fi, err := os.Stat(l.WorkspaceItemPath("CLAUDE.md")); err != nil || fi.Size() != 0 {
		t.Fatalf("workspace CLAUDE.md changed on dry-run (fi=%v err=%v)", fi, err)
	}
	if _, err := os.Stat(filepath.Join(l.AccountsDir(), "main")); !os.IsNotExist(err) {
		t.Fatal("accounts/main created on dry-run")
	}
	receipts, _ := filepath.Glob(filepath.Join(l.BackupsDir(), "*-*"))
	if len(receipts) != 0 {
		t.Fatalf("dry-run created backup dirs: %v", receipts)
	}
	reload, err := config.Load(l.ConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if len(reload.Accounts) != 0 || reload.DefaultAccount != "" {
		t.Fatalf("config mutated on dry-run: %+v", reload)
	}
	if after := migrateSnapTree(t, src); !reflect.DeepEqual(before, after) {
		t.Fatal("source modified on dry-run")
	}
}

func TestMigrateFromBashKitMissingSource(t *testing.T) {
	migrateTestHome(t)
	l, cfg := migrateInitLayout(t)
	err := MigrateFromBashKit(l, cfg, MigrateOptions{Yes: true, Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing source err = %v", err)
	}
}

func TestImportDir(t *testing.T) {
	l, cfg := migrateInitLayout(t)
	src := filepath.Join(t.TempDir(), "claude-src")
	migrateWriteTree(t, src, map[string]string{
		"commands/deploy.md": "deploy notes",
		"history.jsonl":      "line1\n",
		".credentials.json":  `{"token":"imp"}`,
		".claude.json":       `{"oauthAccount":{"email":"imported@example.com"}}`,
	})

	var out bytes.Buffer
	if err := ImportDir(l, cfg, src, "", MigrateOptions{Stdout: &out, Yes: true}); err != nil {
		t.Fatalf("import: %v", err)
	}

	if got := migrateReadFile(t, l.WorkspaceItemPath("commands/deploy.md")); got != "deploy notes" {
		t.Fatalf("deploy.md = %q", got)
	}
	if got := migrateReadFile(t, l.WorkspaceItemPath("history.jsonl")); got != "line1\n" {
		t.Fatalf("history.jsonl = %q", got)
	}
	reload, err := config.Load(l.ConfigPath())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	acct, ok := reload.Account("imported")
	if !ok {
		t.Fatal("account imported missing")
	}
	if acct.Login == nil || acct.Login.Email != "imported@example.com" {
		t.Fatalf("imported login = %+v", acct.Login)
	}
	if _, err := os.Stat(filepath.Join(l.LaunchDir(), "imported", ".credentials.json")); err != nil {
		t.Fatalf("launch dir not assembled: %v", err)
	}
	if receipts, _ := filepath.Glob(filepath.Join(l.BackupsDir(), "*-import", "RECEIPT.md")); len(receipts) != 1 {
		t.Fatalf("import receipts = %v", receipts)
	}

	// Second import with an explicit account name: registered, default untouched.
	if err := ImportDir(l, cfg, src, "work", MigrateOptions{Stdout: &out, Yes: true}); err != nil {
		t.Fatalf("import work: %v", err)
	}
	reload, err = config.Load(l.ConfigPath())
	if err != nil {
		t.Fatalf("reload 2: %v", err)
	}
	if _, ok := reload.Account("work"); !ok {
		t.Fatal("account work missing")
	}
	if reload.DefaultAccount != "imported" {
		t.Fatalf("default changed by second import: %q", reload.DefaultAccount)
	}

	// Errors: bad account name, missing source.
	if err := ImportDir(l, cfg, src, "1", MigrateOptions{Yes: true, Stdout: io.Discard}); err == nil ||
		!strings.Contains(err.Error(), "invalid account name") {
		t.Fatalf("invalid name err = %v", err)
	}
	if err := ImportDir(l, cfg, filepath.Join(t.TempDir(), "nope"), "", MigrateOptions{Yes: true, Stdout: io.Discard}); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing source err = %v", err)
	}
}

func TestImportCcam(t *testing.T) {
	home := migrateTestHome(t)
	l, cfg := migrateInitLayout(t)

	ccam := filepath.Join(home, ".local", "share", "ccam")
	migrateWriteTree(t, ccam, map[string]string{
		"profiles/dev/.credentials.json":  `{"token":"d"}`,
		"profiles/dev/.claude.json":       `{"oauthAccount":{"email":"dev@x.example"}}`,
		"profiles/work/.credentials.json": `{"token":"w"}`,
		"profiles/work/.claude.json":      `{"oauthAccount":{"email":"work@x.example"}}`,
		"profiles/empty/README.md":        "no creds here",
		"groups/default/CLAUDE.md":        "default group clap",
		"groups/default/settings.json":    "{}",
		"groups/team/CLAUDE.md":           "team group clap",
		"groups/team/rules/pair.md":       "pairing rule",
	})
	before := migrateSnapTree(t, ccam)

	var out bytes.Buffer
	if err := ImportCcam(l, cfg, MigrateOptions{Stdout: &out, Yes: true}); err != nil {
		t.Fatalf("import ccam: %v", err)
	}

	reload, err := config.Load(l.ConfigPath())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for name, email := range map[string]string{"dev": "dev@x.example", "work": "work@x.example"} {
		acct, ok := reload.Account(name)
		if !ok {
			t.Fatalf("account %s missing", name)
		}
		if acct.Login == nil || acct.Login.Email != email {
			t.Fatalf("%s login = %+v", name, acct.Login)
		}
		if got := migrateReadFile(t, filepath.Join(l.AccountsDir(), name, ".credentials.json")); got == "" {
			t.Fatalf("%s credentials missing", name)
		}
	}

	// Group items: first-content-wins across groups, non-items ignored.
	if got := migrateReadFile(t, l.WorkspaceItemPath("CLAUDE.md")); got != "default group clap" {
		t.Fatalf("CLAUDE.md = %q, want default group's content", got)
	}
	if got := migrateReadFile(t, l.WorkspaceItemPath("rules/pair.md")); got != "pairing rule" {
		t.Fatalf("rules/pair.md = %q", got)
	}
	if _, err := os.Stat(l.WorkspaceItemPath("settings.json")); !os.IsNotExist(err) {
		t.Fatal("settings.json is not a workspace item and must be ignored")
	}
	if !strings.Contains(out.String(), "existing content kept") {
		t.Fatalf("conflict not logged: %q", out.String())
	}

	// Launch dirs assembled with fresh credentials.
	if got := migrateReadFile(t, filepath.Join(l.LaunchDir(), "dev", ".credentials.json")); got != `{"token":"d"}` {
		t.Fatalf("launch dev creds = %q", got)
	}
	if receipts, _ := filepath.Glob(filepath.Join(l.BackupsDir(), "*-import-ccam", "RECEIPT.md")); len(receipts) != 1 {
		t.Fatalf("ccam receipts = %v", receipts)
	}

	// ccam dirs never modified.
	if after := migrateSnapTree(t, ccam); !reflect.DeepEqual(before, after) {
		t.Fatal("ccam dirs were modified")
	}
}

func TestImportCcamNoneFound(t *testing.T) {
	migrateTestHome(t)
	l, cfg := migrateInitLayout(t)
	err := ImportCcam(l, cfg, MigrateOptions{Yes: true, Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "no ccam profiles found at ~/.local/share/ccam") {
		t.Fatalf("missing ccam err = %v", err)
	}
}
