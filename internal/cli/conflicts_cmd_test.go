package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConflictsListAndResolve(t *testing.T) {
	home := syncWorkspaceHome(t)
	ws := filepath.Join(home, "workspace")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte("a\n"), 0o644))
	copyName := "CLAUDE.sync-conflict-20261124-101010-ABCDEF2.md"
	must(os.WriteFile(filepath.Join(ws, copyName), []byte("b\n"), 0o644))

	got := runCLI(t, "conflicts")
	if got.code != ExitOK || !strings.Contains(got.out, "2 versions of CLAUDE.md") {
		t.Fatalf("conflicts: %d %q %s", got.code, got.out, got.err)
	}
	got = runCLI(t, "conflicts", "--json")
	if got.code != ExitOK || !strings.Contains(got.out, `"path": "CLAUDE.md"`) {
		t.Fatalf("conflicts --json: %d %q", got.code, got.out)
	}
	got = runCLI(t, "conflicts", "resolve", "CLAUDE.md", copyName)
	if got.code != ExitOK {
		t.Fatalf("resolve: %d %s", got.code, got.err)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "CLAUDE.md")); string(b) != "b\n" {
		t.Fatalf("CLAUDE.md = %q", b)
	}
	got = runCLI(t, "conflicts")
	if !strings.Contains(got.out, "no conflict copies") {
		t.Fatalf("after resolve: %q", got.out)
	}
}

func TestMaintainOnceMergesHistory(t *testing.T) {
	home := syncWorkspaceHome(t)
	ws := filepath.Join(home, "workspace")
	if err := os.WriteFile(filepath.Join(ws, "history.jsonl"), []byte(`{"timestamp":2}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "history.sync-conflict-20261124-101010-ABCDEF2.jsonl"), []byte(`{"timestamp":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := maintainOnce(time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skipped != "" || rep.HistoryMerged != 1 {
		t.Fatalf("report %+v", rep)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "history.jsonl")); string(b) != `{"timestamp":1}`+"\n"+`{"timestamp":2}`+"\n" {
		t.Fatalf("history = %q", b)
	}
	if _, err := os.Stat(filepath.Join(ws, ".stignore")); err != nil {
		t.Fatalf(".stignore not written: %v", err)
	}
}

// `cwm doctor --fix` runs one maintenance pass and saves the live login
// into an empty vault entry; a pre-v0.8 config is never touched.
func TestDoctorFix(t *testing.T) {
	root := isolateHome(t)
	home, _ := os.UserHomeDir()
	ws := filepath.Join(home, ".claude")
	for p, body := range map[string]string{
		filepath.Join(ws, ".stignore"):         "stale\n",
		filepath.Join(ws, ".credentials.json"): `{"fake":"FAKE-LIVE"}`,
		filepath.Join(root, "config.toml"):     "",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := runCLI(t, "doctor", "--fix")
	if data, _ := os.ReadFile(filepath.Join(ws, ".stignore")); string(data) != "stale\n" || !strings.Contains(got.out, "cwm migrate --dry-run") {
		t.Fatalf("pre-v0.8 config was touched: %q / %s", got.out, data)
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("mode = \"capture\"\ndefault_account = \"main\"\n[[account]]\nname = \"main\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got = runCLI(t, "doctor", "--fix")
	if !strings.Contains(got.out, "fix: done") || !strings.Contains(got.out, "saved the live login into main's vault entry") {
		t.Fatalf("fix output: %q %q", got.out, got.err)
	}
	if strings.Contains(got.out, "FAKE-LIVE") {
		t.Error("a credential value was printed")
	}
	if !strings.Contains(got.out, "ok:   .stignore matches the [sync] switches") {
		t.Errorf("the report after --fix does not show the regenerated .stignore:\n%s", got.out)
	}
	if data, err := os.ReadFile(filepath.Join(root, "accounts", "main", ".credentials.json")); err != nil || string(data) != `{"fake":"FAKE-LIVE"}` {
		t.Errorf("vault entry: %v %s", err, data)
	}
}

// D102: before the migration, `cwm doctor --fix` still saves the live
// login into the default account's empty vault entry (the doctor's own
// suggestion), and touches nothing else.
func TestDoctorFixPreV08SavesVault(t *testing.T) {
	root := isolateHome(t)
	home, _ := os.UserHomeDir()
	ws := filepath.Join(home, ".claude")
	for p, body := range map[string]string{
		filepath.Join(ws, ".stignore"):                "stale\n",
		filepath.Join(ws, ".credentials.json"):        `{"fake":"FAKE-LIVE"}`,
		filepath.Join(root, "workspace", "CLAUDE.md"): "# v07\n",
		filepath.Join(root, "config.toml"):            "version = 1\ndefault_account = \"tat\"\n\n[[account]]\nname = \"tat\"\ndir = \"x\"\n",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := runCLI(t, "doctor", "--fix")
	if !strings.Contains(got.out, "saved the live login into tat's vault entry") || !strings.Contains(got.out, "cwm migrate --dry-run") {
		t.Fatalf("fix output: %q %q", got.out, got.err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "accounts", "tat", ".credentials.json")); string(data) != `{"fake":"FAKE-LIVE"}` {
		t.Fatalf("vault = %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(ws, ".stignore")); string(data) != "stale\n" {
		t.Error("the maintenance pass ran on a pre-v0.8 config")
	}
	if strings.Contains(got.out, "FAKE-LIVE") {
		t.Error("a credential value was printed")
	}
	// The migration then finds tat's login in the vault: no "default".
	got = runCLI(t, "migrate", "--dry-run")
	if strings.Contains(got.out, "account default") || !strings.Contains(got.out, "default account: tat") {
		t.Fatalf("plan: %s %s", got.out, got.err)
	}
}
