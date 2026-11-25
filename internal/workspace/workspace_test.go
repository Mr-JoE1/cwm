package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// recordingLinker wraps a real linker and records every call, so assembly
// tests can assert both filesystem effects and which primitives ran.
type recordingLinker struct {
	inner Linker
	ops   []string
}

func (r *recordingLinker) Symlink(oldname, newname string) error {
	r.ops = append(r.ops, "symlink "+filepath.Base(newname))
	return r.inner.Symlink(oldname, newname)
}

func (r *recordingLinker) Junction(target, link string) error {
	r.ops = append(r.ops, "junction "+filepath.Base(link))
	return r.inner.Junction(target, link)
}

func (r *recordingLinker) Copy(src, dst string) error {
	r.ops = append(r.ops, "copy "+filepath.Base(dst))
	return r.inner.Copy(src, dst)
}

func (r *recordingLinker) Hardlink(oldname, newname string) error {
	r.ops = append(r.ops, "hardlink "+filepath.Base(newname))
	return r.inner.Hardlink(oldname, newname)
}

func (r *recordingLinker) count(prefix string) int {
	n := 0
	for _, op := range r.ops {
		if strings.HasPrefix(op, prefix) {
			n++
		}
	}
	return n
}

// fakeLinker is the cross-platform test double (D22): it models EVERY link
// op — junctions included — as a real symlink. os.Readlink cannot read a
// `mklink /J` junction on windows runners ("Incorrect function"), but it
// reads symlinks everywhere, so idempotency of the placement logic can be
// asserted on CI without real junctions. Real-junction behavior is the
// opt-in integration test below.
type fakeLinker struct{}

func (fakeLinker) Symlink(oldname, newname string) error {
	return os.Symlink(oldname, newname)
}

func (fakeLinker) Junction(target, link string) error {
	return os.Symlink(target, link)
}

func (fakeLinker) Copy(src, dst string) error {
	return storage.CopyFile(src, dst)
}

func (fakeLinker) Hardlink(oldname, newname string) error {
	return os.Link(oldname, newname)
}

// seedWorkspace creates the full workspace skeleton plus an account with
// credentials, mirroring a completed `cwm init --account main`.
func seedWorkspace(t *testing.T) (*storage.Layout, *config.Config) {
	t.Helper()
	l := storage.NewLayoutAt(t.TempDir())
	cfg := config.Default()
	for _, d := range storage.WorkspaceDirs {
		if err := os.MkdirAll(l.WorkspaceItemPath(d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range storage.WorkspaceFiles {
		if err := os.WriteFile(l.WorkspaceItemPath(f), []byte("content of "+f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	acct := filepath.Join(l.AccountsDir(), "main")
	if err := os.MkdirAll(acct, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acct, ".credentials.json"), []byte(`{"token":"tok-main"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acct, ".claude.json"), []byte(`{"oauthAccount":{"email":"dev@example.com"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddAccount("main", acct); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetDefaultAccount("main"); err != nil {
		t.Fatal(err)
	}
	return l, cfg
}

func TestLinkKind(t *testing.T) {
	tests := []struct {
		goos, mode string
		isDir      bool
		want       string
	}{
		{"linux", "auto", true, "symlink"},
		{"linux", "auto", false, "symlink"},
		{"darwin", "copy", false, "symlink"},
		{"windows", "auto", true, "junction"},
		{"windows", "junction", true, "junction"},
		{"windows", "copy", true, "junction"},
		{"windows", "auto", false, "symlink-or-hardlink"},
		{"windows", "symlink", false, "symlink"},
		{"windows", "junction", false, "symlink-or-hardlink"},
		{"windows", "copy", false, "hardlink"},
	}
	for _, tt := range tests {
		if got := linkKind(tt.goos, tt.isDir, tt.mode); got != tt.want {
			t.Errorf("linkKind(%q, dir=%v, %q) = %q, want %q", tt.goos, tt.isDir, tt.mode, got, tt.want)
		}
	}
}

func TestSameLinkTarget(t *testing.T) {
	if !sameLinkTarget("/a/b", "/a/b/") {
		t.Error("cleaned paths must match")
	}
	if sameLinkTarget("/a/b", "/a/c") {
		t.Error("different targets must not match")
	}
}

func TestInitSkeleton(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no ~/.claude-me hint
	l := storage.NewLayoutAt(t.TempDir())
	var out bytes.Buffer

	if err := Init(l, "", false, &out); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, d := range storage.WorkspaceDirs {
		if fi, err := os.Stat(l.WorkspaceItemPath(d)); err != nil || !fi.IsDir() {
			t.Errorf("workspace dir %s missing (%v)", d, err)
		}
	}
	for _, f := range storage.WorkspaceFiles {
		if _, err := os.Stat(l.WorkspaceItemPath(f)); err != nil {
			t.Errorf("workspace file %s missing (%v)", f, err)
		}
	}
	for _, d := range []string{l.AccountsDir(), l.BackupsDir(), l.StateDir(), l.LogsDir()} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("skeleton dir %s missing (%v)", d, err)
		}
	}
	stignore, err := os.ReadFile(l.WorkspaceItemPath(".stignore"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(stignore), StIgnoreContent(); got != want {
		t.Errorf(".stignore = %q,\nwant %q", got, want)
	}
	if lines := strings.Split(strings.TrimSpace(string(stignore)), "\n"); lines[len(lines)-1] != "*" {
		t.Errorf(".stignore must end with *, got %q", lines[len(lines)-1])
	}

	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.Version != config.CurrentVersion || len(cfg.Accounts) != 0 {
		t.Errorf("config = %+v, want fresh v1 with no accounts", cfg)
	}
	if !strings.Contains(out.String(), "cwm home initialized") {
		t.Errorf("output = %q, want init confirmation", out.String())
	}
	if strings.Contains(out.String(), ".claude-me") {
		t.Errorf("no migrate hint without ~/.claude-me: %q", out.String())
	}
}

func TestInitMigrateHint(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude-me"), 0o755); err != nil {
		t.Fatal(err)
	}
	// printMigrateHint reads os.UserHomeDir: HOME on unix, USERPROFILE on
	// windows — isolate both or the real runner home leaks in (D22).
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	l := storage.NewLayoutAt(t.TempDir())
	var out bytes.Buffer
	if err := Init(l, "", false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "cwm init --migrate") {
		t.Errorf("output = %q, want ~/.claude-me hint", out.String())
	}
}

func TestInitDryRunTouchesNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l := storage.NewLayoutAt(t.TempDir())
	var out bytes.Buffer
	if err := Init(l, "main", true, &out); err != nil {
		t.Fatalf("Init dry-run: %v", err)
	}
	// t.TempDir pre-creates the root itself; check no child was written.
	if _, err := os.Stat(l.WorkspaceDir()); err == nil {
		t.Errorf("dry-run created %s", l.WorkspaceDir())
	}
	if _, err := os.Stat(l.ConfigPath()); err == nil {
		t.Errorf("dry-run created %s", l.ConfigPath())
	}
	if !strings.Contains(out.String(), "would") {
		t.Errorf("output = %q, want a plan", out.String())
	}
}

func TestInitIdempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l := storage.NewLayoutAt(t.TempDir())
	if err := os.MkdirAll(l.WorkspaceDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing content must survive re-init.
	if err := os.WriteFile(l.WorkspaceItemPath("CLAUDE.md"), []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Init(l, "", false, &bytes.Buffer{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := Init(l, "", false, &bytes.Buffer{}); err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	data, err := os.ReadFile(l.WorkspaceItemPath("CLAUDE.md"))
	if err != nil || string(data) != "keep me\n" {
		t.Errorf("CLAUDE.md = %q (%v), want content preserved across re-init", data, err)
	}
}

// stubClaude puts a fake `claude` on PATH that handles `auth login` like
// testdata/fakeclaude.sh (credentials into $CLAUDE_CONFIG_DIR).
func stubClaude(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "login" ]; then
  d=${CLAUDE_CONFIG_DIR:?CLAUDE_CONFIG_DIR not set}
  echo '{"token":"fake"}' > "$d/.credentials.json"
  echo '{"oauthAccount":{"email":"fake@example.com","account_uuid":"uuid-1"},"plan":"claude_max"}' > "$d/.claude.json"
  echo done
  exit 0
fi
echo "2.1.400 (Claude Code)"
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestInitWithAccountRegistersDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	stubClaude(t)
	home := t.TempDir()
	// HOME/USERPROFILE both isolated: init's migrate hint probes
	// os.UserHomeDir, which is USERPROFILE on windows (D22).
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	l := storage.NewLayoutAt(t.TempDir())
	var out bytes.Buffer

	if err := Init(l, "main", false, &out); err != nil {
		t.Fatalf("Init --account main: %v", err)
	}
	if _, err := os.Stat(filepath.Join(l.AccountsDir(), "main", ".credentials.json")); err != nil {
		t.Errorf("account credentials missing: %v", err)
	}
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Accounts) != 1 || cfg.DefaultAccount != "main" {
		t.Fatalf("config accounts = %+v, default = %q", cfg.Accounts, cfg.DefaultAccount)
	}
	if cfg.Accounts[0].Login == nil || cfg.Accounts[0].Login.Email != "fake@example.com" {
		t.Errorf("login = %+v, want identity from the login step", cfg.Accounts[0].Login)
	}
	if _, err := os.Stat(l.LaunchDir()); !os.IsNotExist(err) {
		t.Errorf("v0.8 init must not create launch dirs (D76): %v", err)
	}
	if !strings.Contains(out.String(), "✓ main (fake@example.com) added") {
		t.Errorf("output = %q, want the add summary", out.String())
	}
	if !strings.Contains(out.String(), "✓ default account: main") {
		t.Errorf("output = %q, want the default account line", out.String())
	}
}
