package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// removeFixture builds an isolated cwm home with accounts main (default)
// and work; work's dir is workDir ("" = accounts/work inside the home).
func removeFixture(t *testing.T, workDir string) (*storage.Layout, string) {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		t.Setenv(k, home)
	}
	t.Setenv("CWM_HOME", root)
	l := storage.NewLayoutAt(root)
	if workDir == "" {
		workDir = filepath.Join(l.AccountsDir(), "work")
	}
	cfg := config.Default()
	for name, dir := range map[string]string{"main": filepath.Join(l.AccountsDir(), "main"), "work": workDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := cfg.AddAccount(name, dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := cfg.SetDefaultAccount("main"); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(l.ConfigPath(), cfg); err != nil {
		t.Fatal(err)
	}
	return l, workDir
}

func runRemove(t *testing.T, tty bool, stdin string, args ...string) string {
	t.Helper()
	old := accountStdinIsTTY
	accountStdinIsTTY = func() bool { return tty }
	t.Cleanup(func() { accountStdinIsTTY = old })
	var out, errOut bytes.Buffer
	e := &Env{Stdout: &out, Stderr: &errOut, Stdin: strings.NewReader(stdin)}
	if code := e.Run(append([]string{"account", "remove", "work"}, args...)); code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errOut.String())
	}
	cfg, err := config.Load(storage.NewLayoutAt(os.Getenv("CWM_HOME")).ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Account("work"); ok {
		t.Error("account work still configured")
	}
	return out.String()
}

func exists(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Stat(p)
	return err == nil
}

func TestAccountRemoveDeleteConfigFlag(t *testing.T) {
	for _, flag := range []string{"--delete-config", "--delete-dir"} {
		t.Run(flag, func(t *testing.T) {
			l, dir := removeFixture(t, "")
			out := runRemove(t, false, "", flag, "--yes")
			if exists(t, dir) {
				t.Errorf("%s still there", dir)
			}
			if !strings.Contains(out, "deleted its account dir") {
				t.Errorf("output = %q", out)
			}
			// The backup was taken before the delete.
			entries, _ := os.ReadDir(l.BackupsDir())
			if len(entries) != 1 || !exists(t, filepath.Join(l.BackupsDir(), entries[0].Name(), ".claude.json")) {
				t.Errorf("backup missing: %v", entries)
			}
		})
	}
}

func TestAccountRemoveDeleteConfigOutsideHomeKept(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "imported")
	_, dir := removeFixture(t, outside)
	out := runRemove(t, false, "", "--delete-config", "--yes")
	if !exists(t, dir) {
		t.Errorf("imported dir %s was deleted", dir)
	}
	if !strings.Contains(out, "kept its config folder "+dir) {
		t.Errorf("output = %q, want a kept note", out)
	}
}

func TestAccountRemoveNonTTYWithoutFlagKeeps(t *testing.T) {
	_, dir := removeFixture(t, "")
	// stdin offers a "y" that must never be read as a delete answer.
	out := runRemove(t, false, "y\ny\n", "--yes")
	if !exists(t, dir) {
		t.Errorf("%s deleted without --delete-config", dir)
	}
	if strings.Contains(out, "[y/N]") {
		t.Errorf("non-interactive run prompted: %q", out)
	}
}

func TestAccountRemoveTTYPromptDeletes(t *testing.T) {
	_, dir := removeFixture(t, "")
	out := runRemove(t, true, "y\ny\n")
	if exists(t, dir) {
		t.Errorf("%s still there after answering y", dir)
	}
	if !strings.Contains(out, "also delete its config folder "+dir+"? [y/N]") {
		t.Errorf("output = %q, want the second prompt", out)
	}
}

func TestAccountRemoveTTYPromptDefaultKeeps(t *testing.T) {
	_, dir := removeFixture(t, "")
	out := runRemove(t, true, "y\n\n")
	if !exists(t, dir) {
		t.Errorf("%s deleted after answering N", dir)
	}
	if !strings.Contains(out, "also delete its config folder") {
		t.Errorf("output = %q, want the second prompt", out)
	}
}

func TestAccountRemoveTTYOutsideHomeNoSecondPrompt(t *testing.T) {
	_, dir := removeFixture(t, filepath.Join(t.TempDir(), "imported"))
	out := runRemove(t, true, "y\ny\n")
	if !exists(t, dir) {
		t.Errorf("imported dir %s was deleted", dir)
	}
	if strings.Contains(out, "also delete") {
		t.Errorf("asked to delete an undeletable dir: %q", out)
	}
}
