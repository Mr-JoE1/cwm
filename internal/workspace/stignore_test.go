package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

func TestStIgnoreAllowList(t *testing.T) {
	cfg := config.Default()
	got := StIgnoreFor(cfg)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if lines[len(lines)-1] != "*" {
		t.Fatalf("last line %q, want *", lines[len(lines)-1])
	}
	for _, want := range []string{"!/projects", "!/repos.tsv", "!/history.jsonl", "!/CLAUDE.md", "!/skills", "!/agents", "!/commands", "!/rules", "!/output-styles", "!/scheduled-tasks", "!/plugins", "!/settings.json", "!/mcp.json"} {
		if !strings.Contains(got, "\n"+want+"\n") {
			t.Errorf("missing include %s", want)
		}
	}
	// Never-synced lines come before any include (first match wins).
	firstInclude := strings.Index(got, "\n!/")
	for _, never := range []string{"/.credentials.json", "/.claude.json", "(?d)/plugins/cache", "(?d)*.lock"} {
		at := strings.Index(got, "\n"+never+"\n")
		if at < 0 || at > firstInclude {
			t.Errorf("%s missing or after the includes", never)
		}
	}
	for _, local := range []string{"todos", "debug", "statsig", "file-history", "sessions"} {
		if strings.Contains(got, "!/"+local+"\n") {
			t.Errorf("%s is included", local)
		}
	}
	// A switch turned off only drops its include.
	if err := cfg.Set("sync.settings", "off"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("sync.sessions", "off"); err != nil {
		t.Fatal(err)
	}
	off := StIgnoreFor(cfg)
	for _, gone := range []string{"!/settings.json", "!/projects", "!/repos.tsv"} {
		if strings.Contains(off, gone+"\n") {
			t.Errorf("%s still included with its switch off", gone)
		}
	}
	if !strings.Contains(off, "!/history.jsonl\n") {
		t.Error("history dropped with an unrelated switch")
	}
}

func TestWriteStIgnoreIdempotent(t *testing.T) {
	l := storage.NewLayoutAt(t.TempDir())
	l.SetWorkspaceDir(filepath.Join(t.TempDir(), "root"))
	cfg := config.Default()
	if wrote, err := WriteStIgnore(l, cfg); err != nil || !wrote {
		t.Fatalf("first write: %v %v", wrote, err)
	}
	if !StIgnoreCurrent(l, cfg) {
		t.Error("not current after write")
	}
	if wrote, _ := WriteStIgnore(l, cfg); wrote {
		t.Error("rewrote an unchanged .stignore")
	}
	_ = cfg.Set("sync.plugins", "off")
	if StIgnoreCurrent(l, cfg) {
		t.Error("current after a switch changed")
	}
}
