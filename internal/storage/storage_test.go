package storage

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func setTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(EnvHome, "")
	return home
}

func TestWorkspaceItemsCanonicalList(t *testing.T) {
	got := WorkspaceItems()
	want := []string{
		"projects", "skills", "agents", "commands", "rules", "plugins",
		"output-styles", "scheduled-tasks", "CLAUDE.md", "history.jsonl", "repos.tsv",
	}
	if len(got) != len(want) {
		t.Fatalf("WorkspaceItems() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("WorkspaceItems()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestIsWorkspaceItem(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"dir", "projects", true},
		{"file", "repos.tsv", true},
		{"mcp excluded", "mcp.json", false},
		{"unknown", "nope", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsWorkspaceItem(tt.in); got != tt.want {
				t.Errorf("IsWorkspaceItem(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestHomeDirEnvOverride(t *testing.T) {
	setTestHome(t)
	home := t.TempDir()
	t.Setenv(EnvHome, home)
	got, err := HomeDir()
	if err != nil {
		t.Fatalf("HomeDir() error = %v", err)
	}
	if got != home {
		t.Fatalf("HomeDir() = %q, want %q", got, home)
	}
}

func TestHomeDirDefault(t *testing.T) {
	home := setTestHome(t)
	got, err := HomeDir()
	if err != nil {
		t.Fatalf("HomeDir() error = %v", err)
	}
	if want := filepath.Join(home, ".cwm"); got != want {
		t.Fatalf("HomeDir() = %q, want %q", got, want)
	}
}

func TestNewLayout(t *testing.T) {
	setTestHome(t)
	root := t.TempDir()
	t.Setenv(EnvHome, root)
	l, err := NewLayout()
	if err != nil {
		t.Fatalf("NewLayout() error = %v", err)
	}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"config", l.ConfigPath(), filepath.Join(root, "config.toml")},
		{"workspace", l.WorkspaceDir(), filepath.Join(root, "workspace")},
		{"accounts", l.AccountsDir(), filepath.Join(root, "accounts")},
		{"launch", l.LaunchDir(), filepath.Join(root, "launch")},
		{"backups", l.BackupsDir(), filepath.Join(root, "backups")},
		{"state", l.StateDir(), filepath.Join(root, "state")},
		{"logs", l.LogsDir(), filepath.Join(root, "logs")},
		{"manifest", l.ManifestPath(), filepath.Join(root, "workspace", "repos.tsv")},
		{"item", l.WorkspaceItemPath("projects"), filepath.Join(root, "workspace", "projects")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestExpandTilde(t *testing.T) {
	home := setTestHome(t)
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare tilde", "~", home},
		{"tilde slash", "~/some/dir", filepath.Join(home, "some", "dir")},
		{"no tilde", "/abs/path", "/abs/path"},
		{"inner tilde", "/tmp/~odd", "/tmp/~odd"},
		{"empty", "", ""},
		{"relative", "rel/path", "rel/path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpandTilde(tt.in); got != tt.want {
				t.Errorf("ExpandTilde(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"unix path", "/home/user/repos/foo", "-home-user-repos-foo"},
		{"windows path", `C:\Users\ed\repos\foo`, "C--Users-ed-repos-foo"},
		{"spaces", "my repo", "my-repo"},
		{"dots", "a.b.c", "a-b-c"},
		{"already slug", "Simple-Slug-123", "Simple-Slug-123"},
		{"empty", "", ""},
		{"unicode", "café", "caf-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Slugify(tt.in); got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeHost(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"fqdn", "lap-top.example.com", "lap-top"},
		{"short", "box01", "box01"},
		{"underscores", "my_host", "my-host"},
		{"leading dash", "-lead", "lead"},
		{"trailing dash", "trail-", "trail"},
		{"only symbols", "...", "unknown"},
		{"empty", "", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeHost(tt.in); got != tt.want {
				t.Errorf("SanitizeHost(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHostName(t *testing.T) {
	got := HostName()
	if got == "" {
		t.Fatal("HostName() = empty, want non-empty")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9-]+$`).MatchString(got) {
		t.Errorf("HostName() = %q, want [A-Za-z0-9-]+", got)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"2.1.234", "2.1.234", 0},
		{"2.1.235", "2.1.234", 1},
		{"2.1.233", "2.1.234", -1},
		{"2.2.0", "2.1.999", 1},
		{"3.0.0", "2.99.99", 1},
		{"2.1", "2.1.0", 0},
		{"2.1.234 (Claude Code)", "2.1.234", 0},
		{"", "2.1.234", -1},
		{"10.0.0", "9.9.9", 1},
	}
	for _, tt := range tests {
		t.Run(tt.a+" vs "+tt.b, func(t *testing.T) {
			if got := CompareVersions(tt.a, tt.b); got != tt.want {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestVersionAtLeast(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"2.1.234", true},
		{"2.1.400", true},
		{"2.1.233", false},
		{"3.0.0", true},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := VersionAtLeast(tt.version); got != tt.want {
				t.Errorf("VersionAtLeast(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := WriteFileAtomic(path, []byte("one"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic() error = %v", err)
	}
	if err := WriteFileAtomic(path, []byte("two"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic() overwrite error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "two" {
		t.Fatalf("content = %q, want %q", data, "two")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("dir has %d entries (%s), want 1 (no temp leftovers)", len(entries), strings.Join(names, ","))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Errorf("perm = %o, want 644", info.Mode().Perm())
	}
}

func TestWriteFileAtomicMissingDir(t *testing.T) {
	dir := t.TempDir()
	err := WriteFileAtomic(filepath.Join(dir, "missing", "f.txt"), []byte("x"), 0o644)
	if err == nil {
		t.Fatal("WriteFileAtomic() into missing dir: want error, got nil")
	}
}

func writeClaudeStub(t *testing.T, dir, version string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '%s (Claude Code)\\n' '" + version + "'; fi\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	return path
}

func claudeStatePath(t *testing.T, cwmHome string) string {
	t.Helper()
	return filepath.Join(cwmHome, "state", "claude-path")
}

func TestClaudeBinNotFound(t *testing.T) {
	home := setTestHome(t)
	cwmHome := filepath.Join(home, ".cwm")
	t.Setenv(EnvHome, cwmHome)
	t.Setenv("PATH", t.TempDir())
	_, _, err := ClaudeBin()
	if err == nil {
		t.Fatal("ClaudeBin() with no claude on PATH: want error, got nil")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("error = %v, want it to mention 'not found on PATH'", err)
	}
}

func TestClaudeBinResolveAndCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub does not exec on windows")
	}
	setTestHome(t)
	cwmHome := t.TempDir()
	t.Setenv(EnvHome, cwmHome)
	stubDir := t.TempDir()
	stub := writeClaudeStub(t, stubDir, "2.1.400")
	t.Setenv("PATH", stubDir)

	path, version, err := ClaudeBin()
	if err != nil {
		t.Fatalf("ClaudeBin() error = %v", err)
	}
	if path != stub {
		t.Errorf("path = %q, want %q", path, stub)
	}
	if version != "2.1.400" {
		t.Errorf("version = %q, want %q", version, "2.1.400")
	}

	cache := claudeStatePath(t, cwmHome)
	data, err := os.ReadFile(cache)
	if err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("cache = %q, want 2 lines", data)
	}
	if lines[0] != pathHash(stubDir) {
		t.Errorf("cache hash = %q, want hash of PATH", lines[0])
	}
	if lines[1] != stub {
		t.Errorf("cache path = %q, want %q", lines[1], stub)
	}

	offPath := t.TempDir()
	offStub := writeClaudeStub(t, offPath, "2.1.400")
	hash := pathHash(stubDir)
	if err := writeClaudeCacheRaw(cache, hash, offStub); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	path, _, err = ClaudeBin()
	if err != nil {
		t.Fatalf("ClaudeBin() cache-hit error = %v", err)
	}
	if path != offStub {
		t.Errorf("cache hit path = %q, want %q (cached path off PATH must still win)", path, offStub)
	}
}

func TestClaudeBinPathChangeInvalidatesCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub does not exec on windows")
	}
	setTestHome(t)
	cwmHome := t.TempDir()
	t.Setenv(EnvHome, cwmHome)
	dirA := t.TempDir()
	dirB := t.TempDir()
	writeClaudeStub(t, dirB, "2.1.400")

	t.Setenv("PATH", dirB)
	if _, _, err := ClaudeBin(); err != nil {
		t.Fatalf("ClaudeBin() error = %v", err)
	}

	stubA := writeClaudeStub(t, dirA, "2.1.100")
	t.Setenv("PATH", dirA+string(os.PathListSeparator)+dirB)
	path, version, err := ClaudeBin()
	if err != nil {
		t.Fatalf("ClaudeBin() after PATH change error = %v", err)
	}
	if path != stubA {
		t.Errorf("path = %q, want re-resolved %q", path, stubA)
	}
	if version != "2.1.100" {
		t.Errorf("version = %q, want %q", version, "2.1.100")
	}
}

func TestClaudeBinStaleCacheEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub does not exec on windows")
	}
	setTestHome(t)
	cwmHome := t.TempDir()
	t.Setenv(EnvHome, cwmHome)
	dirA := t.TempDir()
	dirB := t.TempDir()

	t.Setenv("PATH", dirA+string(os.PathListSeparator)+dirB)
	gone := writeClaudeStub(t, dirA, "2.1.400")
	stubB := writeClaudeStub(t, dirB, "2.1.400")
	if _, _, err := ClaudeBin(); err != nil {
		t.Fatalf("ClaudeBin() error = %v", err)
	}

	if err := os.Remove(gone); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	path, _, err := ClaudeBin()
	if err != nil {
		t.Fatalf("ClaudeBin() after cached path vanished error = %v", err)
	}
	if path != stubB {
		t.Errorf("path = %q, want fallback %q", path, stubB)
	}
}

func TestClaudeVersionParseTolerant(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{"normal", "2.1.234 (Claude Code)\n", "2.1.234"},
		{"bare", "2.1.234\n", "2.1.234"},
		{"no version", "some other output\n", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := versionRe.FindString(tt.out); got != tt.want {
				t.Errorf("parse(%q) = %q, want %q", tt.out, got, tt.want)
			}
		})
	}
}

func writeClaudeCacheRaw(path, hash, claude string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(hash+"\n"+claude+"\n"), 0o644)
}
