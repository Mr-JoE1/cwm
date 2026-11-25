package claudepaths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

func sandbox(t *testing.T) (home string, l *storage.Layout) {
	t.Helper()
	home = t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		t.Setenv(k, home)
	}
	root := filepath.Join(home, ".cwm")
	t.Setenv(storage.EnvHome, root)
	return home, storage.NewLayoutAt(root)
}

func TestLiveEveryModeAndAccount(t *testing.T) {
	home, l := sandbox(t)
	claude := filepath.Join(home, ".claude")
	ws := filepath.Join(home, ".cwm", "workspace")
	work := filepath.Join(home, ".cwm", "accounts", "work")
	cases := []struct {
		mode, account string
		want          Paths
	}{
		{config.ModeCapture, "", Paths{Mode: "capture", Account: "main", Default: true, Root: claude, ConfigDir: claude,
			ClaudeJSON: filepath.Join(home, ".claude.json"), Credentials: filepath.Join(claude, ".credentials.json")}},
		{config.ModeCapture, "main", Paths{Mode: "capture", Account: "main", Default: true, Root: claude, ConfigDir: claude,
			ClaudeJSON: filepath.Join(home, ".claude.json"), Credentials: filepath.Join(claude, ".credentials.json")}},
		{config.ModeCapture, "work", Paths{Mode: "capture", Account: "work", Root: claude, ConfigDir: work, Env: work,
			ClaudeJSON: filepath.Join(work, ".claude.json"), Credentials: filepath.Join(work, ".credentials.json")}},
		{config.ModeIsolated, "", Paths{Mode: "isolated", Account: "main", Default: true, Root: ws, ConfigDir: ws, Env: ws,
			ClaudeJSON: filepath.Join(ws, ".claude.json"), Credentials: filepath.Join(ws, ".credentials.json")}},
		{config.ModeIsolated, "work", Paths{Mode: "isolated", Account: "work", Root: ws, ConfigDir: work, Env: work,
			ClaudeJSON: filepath.Join(work, ".claude.json"), Credentials: filepath.Join(work, ".credentials.json")}},
		{"", "", Paths{Mode: "capture", Account: "main", Default: true, Root: claude, ConfigDir: claude,
			ClaudeJSON: filepath.Join(home, ".claude.json"), Credentials: filepath.Join(claude, ".credentials.json")}},
	}
	for _, c := range cases {
		got, err := Live(l, c.mode, c.account, "main")
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("Live(%q, %q):\n got %+v\nwant %+v", c.mode, c.account, got, c.want)
		}
	}
	// No accounts at all: the default is "" and still the default.
	got, err := Live(l, config.ModeCapture, "", "")
	if err != nil || !got.Default || got.Env != "" {
		t.Errorf("no accounts: %+v %v", got, err)
	}
}

func TestResolveFollowsConfig(t *testing.T) {
	home, l := sandbox(t)
	if _, err := Resolve(l); err != nil {
		t.Fatal(err)
	}
	if got := l.WorkspaceDir(); got != filepath.Join(home, ".claude") {
		t.Errorf("no config: root %s", got)
	}
	if err := os.MkdirAll(l.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.ConfigPath(), []byte("mode = \"isolated\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(l); err != nil {
		t.Fatal(err)
	}
	if got := l.WorkspaceDir(); got != l.IsolatedWorkspaceDir() {
		t.Errorf("isolated: root %s", got)
	}
	if got := l.ManifestPath(); got != filepath.Join(l.IsolatedWorkspaceDir(), "repos.tsv") {
		t.Errorf("manifest %s", got)
	}
	// A broken config: NewLayout fails, the lenient form stays on capture.
	if err := os.WriteFile(l.ConfigPath(), []byte("mode = \"cloud\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLayout(); err == nil {
		t.Error("NewLayout accepted a broken config")
	}
	ll, err := NewLayoutLenient()
	if err != nil || ll.WorkspaceDir() != filepath.Join(home, ".claude") {
		t.Errorf("lenient: %v %v", ll, err)
	}
}

func TestTilde(t *testing.T) {
	home, _ := sandbox(t)
	sep := string(filepath.Separator)
	for in, want := range map[string]string{
		home:                                   "~",
		filepath.Join(home, ".claude"):         "~" + sep + ".claude",
		filepath.Join(home, "..x", "y"):        "~" + sep + filepath.Join("..x", "y"),
		filepath.Join(filepath.Dir(home), "o"): filepath.Join(filepath.Dir(home), "o"),
	} {
		if got := Tilde(in); got != want {
			t.Errorf("Tilde(%s) = %s, want %s", in, got, want)
		}
	}
}
