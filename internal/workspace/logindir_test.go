package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// loginSandbox: capture mode with a default "main" and a parallel "work".
func loginSandbox(t *testing.T) (home string, l *storage.Layout, cfg *config.Config) {
	t.Helper()
	home = t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		t.Setenv(k, home)
	}
	l = storage.NewLayoutAt(filepath.Join(home, ".cwm"))
	cfg = config.Default()
	for _, n := range []string{"main", "work"} {
		if err := cfg.AddAccount(n, filepath.Join(l.AccountsDir(), n)); err != nil {
			t.Fatal(err)
		}
	}
	return home, l, cfg
}

func TestLoginDirLinksEverySharedItem(t *testing.T) {
	home, l, cfg := loginSandbox(t)
	root := filepath.Join(home, ".claude")
	put := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(root, "projects", "p", "s.jsonl"), "{}\n")
	put(filepath.Join(root, "history.jsonl"), "a\n")
	dir := filepath.Join(l.AccountsDir(), "work")
	put(filepath.Join(dir, ".credentials.json"), `{"fake":"work"}`)

	rec := &recordingLinker{inner: fakeLinker{}}
	res, err := EnsureLoginDirWith(l, cfg, "work", rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range SharedItems() {
		dst := filepath.Join(dir, it.Path)
		got, err := os.Readlink(dst)
		if err != nil || got != filepath.Join(root, it.Path) {
			t.Errorf("%s -> %q (%v)", it.Path, got, err)
		}
	}
	for _, own := range []string{"mcp.json", "repos.tsv"} {
		if _, err := os.Lstat(filepath.Join(dir, own)); err == nil {
			t.Errorf("%s linked into the login-only dir", own)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".credentials.json")); string(b) != `{"fake":"work"}` {
		t.Errorf("own credentials touched: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "settings.json")); string(b) != "{}\n" {
		t.Errorf("missing settings.json target = %q, want {}", b)
	}
	// A session written through the link lands in the shared root.
	put(filepath.Join(dir, "projects", "p", "new.jsonl"), "{}\n")
	if _, err := os.Stat(filepath.Join(root, "projects", "p", "new.jsonl")); err != nil {
		t.Errorf("session did not land in the root: %v", err)
	}
	if len(res.Relinked) != len(SharedItems()) {
		t.Errorf("relinked %v", res.Relinked)
	}
	// Idempotent: nothing to do the second time.
	rec.ops = nil
	res2, err := EnsureLoginDirWith(l, cfg, "work", rec)
	if err != nil || len(res2.Relinked) != 0 || len(rec.ops) != 0 {
		t.Errorf("second run: %+v ops %v err %v", res2, rec.ops, err)
	}
	if _, err := EnsureLoginDirWith(l, cfg, "ghost", rec); err == nil {
		t.Error("unknown account accepted")
	}
}

// A hardlink broken by an atomic rename (or a real file claude wrote
// before the link existed) is merged back and re-linked.
func TestLoginDirMergesDivergedFiles(t *testing.T) {
	home, l, cfg := loginSandbox(t)
	root := filepath.Join(home, ".claude")
	dir := filepath.Join(l.AccountsDir(), "work")
	for p, body := range map[string]string{
		filepath.Join(root, "history.jsonl"):           "{\"t\":1}\n{\"t\":2}\n",
		filepath.Join(dir, "history.jsonl"):            "{\"t\":2}\n{\"t\":3}\n",
		filepath.Join(dir, "projects", "q", "x.jsonl"): "{}\n",
		filepath.Join(root, "CLAUDE.md"):               "old\n",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := EnsureLoginDirWith(l, cfg, "work", fakeLinker{})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "history.jsonl")); string(b) != "{\"t\":1}\n{\"t\":2}\n{\"t\":3}\n" {
		t.Errorf("history merge = %q", b)
	}
	if _, err := os.Stat(filepath.Join(root, "projects", "q", "x.jsonl")); err != nil {
		t.Errorf("session from the real dir not merged: %v", err)
	}
	if res.Backup == "" || !strings.Contains(strings.Join(res.Merged, ","), "history.jsonl") {
		t.Errorf("result %+v", res)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Lstat(filepath.Join(dir, "history.jsonl")); fi.Mode()&os.ModeSymlink == 0 {
			t.Error("history.jsonl not re-linked")
		}
	}
	// An intact hardlink is left alone.
	h := filepath.Join(dir, "CLAUDE.md")
	_ = os.Remove(h)
	if err := os.Link(filepath.Join(root, "CLAUDE.md"), h); err != nil {
		t.Skip("no hardlinks here")
	}
	res, err = EnsureLoginDirWith(l, cfg, "work", fakeLinker{})
	if err != nil || len(res.Relinked) != 0 {
		t.Errorf("intact hardlink relinked: %+v %v", res, err)
	}
}

func TestMergeLines(t *testing.T) {
	d := t.TempDir()
	a, b := filepath.Join(d, "a"), filepath.Join(d, "b")
	_ = os.WriteFile(a, []byte("1\n2\n"), 0o644)
	_ = os.WriteFile(b, []byte("2\n3\n\n1\n4"), 0o644)
	if err := MergeLines(a, b); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(a); string(got) != "1\n2\n3\n4\n" {
		t.Errorf("merged = %q", got)
	}
}
