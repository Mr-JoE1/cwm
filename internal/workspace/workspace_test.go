package workspace

import (
	"bytes"
	"encoding/json"
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

func TestAssemblyLinksEveryWorkspaceItem(t *testing.T) {
	l, cfg := seedWorkspace(t)
	rec := &recordingLinker{inner: OSLinker{}}

	if err := EnsureLaunchWithLinker(l, cfg, "main", rec); err != nil {
		t.Fatalf("EnsureLaunch: %v", err)
	}
	launch := filepath.Join(l.LaunchDir(), "main")
	for _, item := range storage.WorkspaceItems() {
		dst := filepath.Join(launch, item)
		if runtime.GOOS == "windows" {
			// Junctions defeat os.Readlink on windows runners (D22), so
			// assert presence + shape instead of the link target.
			dfi, err := os.Stat(dst)
			if err != nil {
				t.Errorf("%s: not present in launch dir: %v", item, err)
				continue
			}
			sfi, serr := os.Stat(l.WorkspaceItemPath(item))
			if serr != nil || dfi.IsDir() != sfi.IsDir() {
				t.Errorf("%s: isDir = %v, want %v", item, dfi.IsDir(), sfi.IsDir())
			}
			continue
		}
		got, err := os.Readlink(dst)
		if err != nil {
			t.Errorf("%s: not a symlink: %v", item, err)
			continue
		}
		if want := l.WorkspaceItemPath(item); got != want {
			t.Errorf("%s -> %q, want %q", item, got, want)
		}
	}
	if runtime.GOOS == "windows" {
		// The real linker junctions every dir and places each file via
		// symlink-or-copy (either is correct, D14); creds are always copies.
		if got := rec.count("junction"); got != len(storage.WorkspaceDirs) {
			t.Errorf("junction ops = %d, want %d (one per workspace dir): %v", got, len(storage.WorkspaceDirs), rec.ops)
		}
		for _, f := range storage.WorkspaceFiles {
			if n := rec.count("symlink "+f) + rec.count("copy "+f); n != 1 {
				t.Errorf("%s placed %d time(s), want exactly one symlink-or-copy: %v", f, n, rec.ops)
			}
		}
		for _, cred := range credFiles {
			if rec.count("copy "+cred) != 1 {
				t.Errorf("expected exactly one copy op for %s: %v", cred, rec.ops)
			}
		}
		return
	}
	if rec.count("symlink") != len(storage.WorkspaceItems()) {
		t.Errorf("symlink ops = %d, want %d (one per item): %v", rec.count("symlink"), len(storage.WorkspaceItems()), rec.ops)
	}
	if rec.count("junction") != 0 {
		t.Errorf("no junctions expected on unix: %v", rec.ops)
	}
}

func TestAssemblyCopiesCredentialsNotLinks(t *testing.T) {
	l, cfg := seedWorkspace(t)
	rec := &recordingLinker{inner: OSLinker{}}

	if err := EnsureLaunchWithLinker(l, cfg, "main", rec); err != nil {
		t.Fatalf("EnsureLaunch: %v", err)
	}
	launch := filepath.Join(l.LaunchDir(), "main")
	for _, cred := range credFiles {
		dst := filepath.Join(launch, cred)
		if fi, err := os.Lstat(dst); err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
			t.Errorf("%s must be a real file, got %+v (%v)", cred, fi, err)
		}
		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		src, err := os.ReadFile(filepath.Join(l.AccountsDir(), "main", cred))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != string(src) {
			t.Errorf("%s content = %q, want account copy %q", cred, data, src)
		}
		if rec.count("copy "+cred) != 1 {
			t.Errorf("expected exactly one copy op for %s: %v", cred, rec.ops)
		}
	}
}

// TestAssemblyIdempotent uses the fake Linker (D22): placement logic is
// asserted cross-platform with plain symlinks, because real junctions
// cannot be re-read via os.Readlink on windows runners. The real-linker
// junction path is exercised by TestRealLinkerIdempotencyWindows below.
func TestAssemblyIdempotent(t *testing.T) {
	l, cfg := seedWorkspace(t)
	first := &recordingLinker{inner: fakeLinker{}}
	if err := EnsureLaunchWithLinker(l, cfg, "main", first); err != nil {
		t.Fatalf("first EnsureLaunch: %v", err)
	}

	second := &recordingLinker{inner: fakeLinker{}}
	if err := EnsureLaunchWithLinker(l, cfg, "main", second); err != nil {
		t.Fatalf("second EnsureLaunch: %v", err)
	}
	if n := second.count("symlink") + second.count("junction"); n != 0 {
		t.Errorf("second run re-linked %d items (want 0): %v", n, second.ops)
	}
	// Credentials are re-copied every assembly (always fresh), and nothing
	// else changes; in particular no backup is created.
	if second.count("copy .credentials.json") != 1 || second.count("copy .claude.json") != 1 {
		t.Errorf("second run ops = %v, want fresh copies of both credential files only", second.ops)
	}
	entries, err := os.ReadDir(l.BackupsDir())
	if err != nil {
		if !os.IsNotExist(err) {
			t.Errorf("backups dir: %v", err)
		}
	} else if len(entries) != 0 {
		t.Errorf("idempotent run must not create backups, got %v", entries)
	}
}

// TestRealLinkerIdempotencyWindows verifies the REAL OSLinker junction path
// end to end. Opt-in only (CWM_WIN_INTEGRATION=1 on a real windows box):
// windows-latest CI cannot settle it — os.Readlink on a `mklink /J`
// junction fails there ("Incorrect function"), which is exactly the
// real-Windows question this test exists to answer (D22).
func TestRealLinkerIdempotencyWindows(t *testing.T) {
	if runtime.GOOS != "windows" || os.Getenv("CWM_WIN_INTEGRATION") != "1" {
		t.Skip("opt-in integration test: set CWM_WIN_INTEGRATION=1 on a real windows box (D22)")
	}
	l, cfg := seedWorkspace(t)
	first := &recordingLinker{inner: OSLinker{}}
	if err := EnsureLaunchWithLinker(l, cfg, "main", first); err != nil {
		t.Fatalf("first EnsureLaunch: %v", err)
	}

	second := &recordingLinker{inner: OSLinker{}}
	if err := EnsureLaunchWithLinker(l, cfg, "main", second); err != nil {
		t.Fatalf("second EnsureLaunch: %v", err)
	}
	if n := second.count("symlink") + second.count("junction"); n != 0 {
		t.Errorf("second run re-linked %d items (want 0): %v", n, second.ops)
	}
	if second.count("copy .credentials.json") != 1 || second.count("copy .claude.json") != 1 {
		t.Errorf("second run ops = %v, want fresh copies of both credential files only", second.ops)
	}
	entries, err := os.ReadDir(l.BackupsDir())
	if err != nil {
		if !os.IsNotExist(err) {
			t.Errorf("backups dir: %v", err)
		}
	} else if len(entries) != 0 {
		t.Errorf("idempotent run must not create backups, got %v", entries)
	}
}

func TestAssemblyReplacesWrongLinkAndRealFiles(t *testing.T) {
	l, cfg := seedWorkspace(t)
	launch := filepath.Join(l.LaunchDir(), "main")
	if err := os.MkdirAll(launch, 0o700); err != nil {
		t.Fatal(err)
	}

	// Wrong-target link.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(launch, "skills")); err != nil {
		t.Fatal(err)
	}
	// Unexpected real file where a link belongs.
	if err := os.WriteFile(filepath.Join(launch, "CLAUDE.md"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureLaunch(l, cfg, "main"); err != nil {
		t.Fatalf("EnsureLaunch: %v", err)
	}
	// launchItemPointsAt asserts dst resolves to the workspace item. On
	// windows os.Readlink cannot cross a junction ("Incorrect function",
	// D22), so accept either a matching link target or the same underlying
	// file (junctions and symlinks resolve identically through os.Stat).
	launchItemPointsAt := func(item string) {
		dst := filepath.Join(launch, item)
		if got, err := os.Readlink(dst); err == nil && sameLinkTarget(l.WorkspaceItemPath(item), got) {
			return
		}
		dfi, derr := os.Stat(dst)
		sfi, serr := os.Stat(l.WorkspaceItemPath(item))
		if derr != nil || serr != nil || !os.SameFile(dfi, sfi) {
			t.Errorf("%s does not resolve to the workspace item", item)
		}
	}
	launchItemPointsAt("skills")
	launchItemPointsAt("CLAUDE.md")

	// Both replaced entries were backed up first.
	entries, err := os.ReadDir(l.BackupsDir())
	if err != nil || len(entries) != 1 {
		t.Fatalf("backups = %v (%v), want one run dir", entries, err)
	}
	backup := filepath.Join(l.BackupsDir(), entries[0].Name())
	if !strings.Contains(entries[0].Name(), "launch-main") {
		t.Errorf("backup name = %q, want *-launch-main", entries[0].Name())
	}
	target, err := os.ReadFile(filepath.Join(backup, "skills.link-target"))
	if err != nil || strings.TrimSpace(string(target)) != elsewhere {
		t.Errorf("skills backup = %q (%v), want recorded old target", target, err)
	}
	data, err := os.ReadFile(filepath.Join(backup, "CLAUDE.md"))
	if err != nil || string(data) != "stale\n" {
		t.Errorf("CLAUDE.md backup = %q (%v), want old content", data, err)
	}
}

func TestAssemblyReplacesLinkedCredentialsWithCopies(t *testing.T) {
	l, cfg := seedWorkspace(t)
	launch := filepath.Join(l.LaunchDir(), "main")
	if err := os.MkdirAll(launch, 0o700); err != nil {
		t.Fatal(err)
	}
	// A rogue link where the credential copy must live.
	rogue := filepath.Join(t.TempDir(), "rogue-creds")
	if err := os.WriteFile(rogue, []byte(`{"token":"rogue"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rogue, filepath.Join(launch, ".credentials.json")); err != nil {
		t.Fatal(err)
	}

	if err := EnsureLaunch(l, cfg, "main"); err != nil {
		t.Fatalf("EnsureLaunch: %v", err)
	}
	fi, err := os.Lstat(filepath.Join(launch, ".credentials.json"))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("credentials must be a real file after assembly: %+v (%v)", fi, err)
	}
	data, _ := os.ReadFile(filepath.Join(launch, ".credentials.json"))
	if string(data) != `{"token":"tok-main"}` {
		t.Errorf("credentials = %q, want the account copy", data)
	}
	// The rogue link was backed up before replacement.
	entries, err := os.ReadDir(l.BackupsDir())
	if err != nil || len(entries) != 1 {
		t.Fatalf("backups = %v (%v), want one", entries, err)
	}
	if _, err := os.Stat(filepath.Join(l.BackupsDir(), entries[0].Name(), ".credentials.json.link-target")); err != nil {
		t.Errorf("rogue link target not recorded: %v", err)
	}
}

func TestSettingsGeneration(t *testing.T) {
	l, cfg := seedWorkspace(t)
	if err := EnsureLaunch(l, cfg, "main"); err != nil {
		t.Fatalf("EnsureLaunch: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(l.LaunchDir(), "main", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s settingsFile
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("settings.json invalid: %v\n%s", err, data)
	}
	for _, k := range []string{
		"DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING",
		"DISABLE_BUG_COMMAND", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
	} {
		if s.Env[k] != "1" {
			t.Errorf("env[%s] = %q, want 1", k, s.Env[k])
		}
	}
	if len(s.Env) != 4 {
		t.Errorf("env has %d keys, want exactly 4: %v", len(s.Env), s.Env)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for hook, sub := range map[string]string{"SessionStart": "session-start", "SessionEnd": "session-end"} {
		ms := s.Hooks[hook]
		if len(ms) != 1 || len(ms[0].Hooks) != 1 {
			t.Fatalf("%s hooks = %+v, want one command", hook, ms)
		}
		cmdStr := ms[0].Hooks[0].Command
		if !strings.HasSuffix(cmdStr, " hook "+sub) {
			t.Errorf("%s command = %q, want suffix %q", hook, cmdStr, " hook "+sub)
		}
		bin := strings.TrimSuffix(strings.TrimSuffix(cmdStr, " hook "+sub), `"`)
		bin = strings.Trim(bin, `"`)
		if !filepath.IsAbs(bin) {
			t.Errorf("hook binary %q must be absolute", bin)
		}
		if want, _ := filepath.Abs(exe); bin != want {
			t.Errorf("hook binary = %q, want the current binary %q", bin, want)
		}
		if ms[0].Hooks[0].Type != "command" {
			t.Errorf("hook type = %q, want command", ms[0].Hooks[0].Type)
		}
	}
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
		{"windows", "auto", false, "symlink-or-copy"},
		{"windows", "symlink", false, "symlink-or-copy"},
		{"windows", "junction", false, "symlink-or-copy"},
		{"windows", "copy", false, "copy"},
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

func TestEnsureLaunchUnknownAccount(t *testing.T) {
	l, cfg := seedWorkspace(t)
	if err := EnsureLaunch(l, cfg, "ghost"); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("err = %v, want unknown account error", err)
	}
}

func TestEnsureLaunchMissingAccountDir(t *testing.T) {
	l, cfg := seedWorkspace(t)
	other := filepath.Join(l.AccountsDir(), "other")
	if err := cfg.AddAccount("other", other); err != nil {
		t.Fatal(err)
	}
	err := EnsureLaunch(l, cfg, "other")
	if err == nil || !strings.Contains(err.Error(), "cwm account add") {
		t.Errorf("err = %v, want missing account-dir hint", err)
	}
}

// --- Init ---

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
	for _, d := range []string{l.AccountsDir(), l.LaunchDir(), l.BackupsDir(), l.StateDir(), l.LogsDir()} {
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
	wantLines := []string{
		"(?d)settings.json", "(?d)settings.local.json", "(?d)cache", "(?d)debug",
		"(?d)telemetry", "(?d)statsig", "(?d)shell-snapshots", "(?d)session-env",
		"(?d)paste-cache", "(?d)file-history", "(?d)backups", "(?d)downloads",
		"(?d)ide", "(?d)chrome", "(?d)todos", "(?d)tasks", "(?d)sessions",
		"(?d)worktrees", "(?d)logs", "(?d).DS_Store", "(?d)Thumbs.db",
	}
	gotLines := strings.Split(strings.TrimSpace(string(stignore)), "\n")
	if len(gotLines) != len(wantLines) {
		t.Fatalf(".stignore has %d lines, want %d", len(gotLines), len(wantLines))
	}
	for i := range wantLines {
		if gotLines[i] != wantLines[i] {
			t.Errorf(".stignore line %d = %q, want %q", i, gotLines[i], wantLines[i])
		}
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
echo 2.1.400
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestInitWithAccountAssemblesLaunch(t *testing.T) {
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
	launch := filepath.Join(l.LaunchDir(), "main")
	if _, err := os.Stat(filepath.Join(launch, "settings.json")); err != nil {
		t.Errorf("launch settings.json missing: %v", err)
	}
	if got, err := os.Readlink(filepath.Join(launch, "projects")); err != nil || got != l.WorkspaceItemPath("projects") {
		t.Errorf("launch/projects -> %q (%v), want workspace link", got, err)
	}
	creds, err := os.ReadFile(filepath.Join(launch, ".claude.json"))
	if err != nil || !strings.Contains(string(creds), "fake@example.com") {
		t.Errorf("launch .claude.json = %q (%v), want copied account file", creds, err)
	}
	if !strings.Contains(out.String(), "✓ main (fake@example.com) added") {
		t.Errorf("output = %q, want the add summary", out.String())
	}
	if !strings.Contains(out.String(), "launch dir assembled") {
		t.Errorf("output = %q, want assembly confirmation", out.String())
	}
}
