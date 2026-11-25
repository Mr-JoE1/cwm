package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// stIgnoreHeader marks the file as generated, so nobody edits it by hand.
const stIgnoreHeader = "// managed by cwm: regenerated from [sync] in config.toml (cwm config set sync.<item> on|off); edits are lost\n"

// stIgnoreAlways are never synced, whatever the switches say, and come
// before the includes because Syncthing uses the first matching line.
// Everything else that is not included falls to the final "*".
var stIgnoreAlways = []string{
	"(?d).DS_Store",
	"(?d)Thumbs.db",
	"(?d)*.lock",
	"/.credentials.json",
	"/.claude.json",
	"(?d)/plugins/cache",
	"(?d)/plugins/**/cache",
}

// StIgnoreFor renders the allow-list .stignore of the workspace root
// (D81): the never-synced lines, one `!/<item>` per enabled allow-list
// item in spec order, and a final `*`, so anything a future Claude Code
// adds stays local. A switch turned off only drops its include line;
// nothing is ever deleted.
func StIgnoreFor(cfg *config.Config) string {
	var b strings.Builder
	b.WriteString(stIgnoreHeader)
	for _, line := range stIgnoreAlways {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, it := range config.SyncItems {
		if cfg.SyncEnabled(it.Key) {
			fmt.Fprintf(&b, "!/%s\n", it.Path)
		}
	}
	b.WriteString("*\n")
	return b.String()
}

// StIgnoreContent is StIgnoreFor the default switches.
func StIgnoreContent() string { return StIgnoreFor(config.Default()) }

// StIgnorePath is the .stignore of the workspace root.
func StIgnorePath(l *storage.Layout) string { return filepath.Join(l.WorkspaceDir(), ".stignore") }

// WriteStIgnore regenerates the root's .stignore when it differs; it
// reports whether it wrote.
func WriteStIgnore(l *storage.Layout, cfg *config.Config) (bool, error) {
	want := StIgnoreFor(cfg)
	p := StIgnorePath(l)
	if got, err := os.ReadFile(p); err == nil && string(got) == want {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false, fmt.Errorf("workspace: create %s: %w", filepath.Dir(p), err)
	}
	if err := storage.WriteFileAtomic(p, []byte(want), 0o644); err != nil {
		return false, fmt.Errorf("workspace: write %s: %w", p, err)
	}
	return true, nil
}

// StIgnoreCurrent reports whether the root's .stignore matches the
// switches (the doctor check).
func StIgnoreCurrent(l *storage.Layout, cfg *config.Config) bool {
	got, err := os.ReadFile(StIgnorePath(l))
	return err == nil && string(got) == StIgnoreFor(cfg)
}
