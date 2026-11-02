package launcher

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

func TestClaudeArgsModes(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	layout := storage.NewLayoutAt(root)
	cases := []struct {
		mode Mode
		want []string
	}{
		{ModeNew, []string{}},
		{ModeResume, []string{"--resume"}},
		{ModeLast, []string{"--continue"}},
	}
	for _, c := range cases {
		got, err := claudeArgs(layout, c.mode, nil)
		if err != nil {
			t.Fatalf("mode %s: %v", c.mode, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("mode %s: got %v want %v", c.mode, got, c.want)
		}
	}
	if _, err := claudeArgs(layout, Mode("bogus"), nil); err == nil {
		t.Error("bogus mode: want error")
	}
}

func TestClaudeArgsMCPAndExtra(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	layout := storage.NewLayoutAt(root)
	mcp := filepath.Join(ws, "mcp.json")

	if err := os.WriteFile(mcp, []byte(`{"mcpServers":{"x":{"command":"echo"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := claudeArgs(layout, ModeResume, []string{"--verbose"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--mcp-config", mcp, "--resume", "--verbose"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mcp: got %v want %v", got, want)
	}

	if err := os.WriteFile(mcp, []byte("   \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = claudeArgs(layout, ModeResume, nil)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"--resume"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("blank mcp: got %v want %v", got, want)
	}
}

func TestReposRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(ReposRootEnv, "")
	got, err := ReposRoot()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "repos"); got != want {
		t.Errorf("default: got %q want %q", got, want)
	}
	t.Setenv(ReposRootEnv, "~/xyz")
	got, err = ReposRoot()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "xyz"); got != want {
		t.Errorf("override: got %q want %q", got, want)
	}
}
