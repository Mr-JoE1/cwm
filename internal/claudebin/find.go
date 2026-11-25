package claudebin

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// Find resolves claude for a cwm command: config.toml's claude_path feeds
// the lookup, and an off-PATH standalone hit (SourceStandalone) is written back to
// claude_path so later commands — and the agent, whose PATH is frozen at
// login — skip the search. The write is best-effort and only happens when
// config.toml already exists: creating it here would make an
// uninitialised home look initialised to the installer. A saved
// claude_path that fails verification is removed (D54). A copy bundled
// with the desktop app or the editor extension is never pinned: its
// folder carries a version that changes on every app update (D98).
func Find(layout *storage.Layout) (Result, error) {
	return find(layout, SystemEnv(), true, false)
}

// Lookup is Find without pinning a new path, for read-only callers (the
// dashboard's state poll). It still drops a claude_path that failed
// verification: keeping a known-wrong pin helps nobody.
func Lookup(layout *storage.Layout) (Result, error) {
	return find(layout, SystemEnv(), false, false)
}

// LookupSurvey is Lookup in Survey mode, for reports (doctor,
// /api/claude, the debug bundle).
func LookupSurvey(layout *storage.Layout) (Result, error) {
	return find(layout, SystemEnv(), false, true)
}

func find(layout *storage.Layout, env Env, persist, survey bool) (Result, error) {
	var cfg *config.Config
	configured := ""
	if layout != nil {
		if c, err := config.Load(layout.ConfigPath()); err == nil {
			cfg, configured = c, c.ClaudePath
		}
	}
	res, err := resolve(env, configured, survey)
	cleared := res.Cleared
	var nf *NotFoundError
	if errors.As(err, &nf) {
		cleared = nf.Cleared
	}
	dirty := false
	if cleared != nil && cfg != nil && cfg.ClaudePath == configured {
		cfg.ClaudePath = ""
		dirty = true
		writeClearedNote(layout, *cleared)
	}
	if err == nil && persist && res.Source == SourceStandalone && cfg != nil && cfg.ClaudePath != res.Path {
		cfg.ClaudePath = res.Path
		dirty = true
	}
	if dirty {
		if _, serr := os.Stat(layout.ConfigPath()); serr == nil {
			_ = config.Save(layout.ConfigPath(), cfg)
		}
	}
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// clearedNoteName is the one-line record of the last claude_path cwm
// dropped (under state/), so doctor and the debug bundle can say what
// happened to a pin the user may have set by hand.
const clearedNoteName = "claude-cleared.txt"

func writeClearedNote(layout *storage.Layout, c Cleared) {
	if layout == nil {
		return
	}
	dir := layout.StateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	line := fmt.Sprintf("%s cleared claude_path %s: %s\n", c.Time.UTC().Format(time.RFC3339), c.Path, oneLine(c.Reason))
	_ = storage.WriteFileAtomic(filepath.Join(dir, clearedNoteName), []byte(line), 0o600)
}

// ClearedNote is the last "cleared claude_path" line ("" = none).
func ClearedNote(layout *storage.Layout) string {
	if layout == nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(layout.StateDir(), clearedNoteName))
	if err != nil {
		return ""
	}
	return oneLine(string(data))
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return s
}

// Remember stores res.Path as claude_path in an in-memory config the
// caller is about to save anyway (account add), so the persisted value is
// not clobbered by that later Save. A pin find() just dropped is dropped
// here too, for the same reason.
func Remember(cfg *config.Config, res Result) {
	if cfg == nil {
		return
	}
	if res.Cleared != nil && cfg.ClaudePath == res.Cleared.Path {
		cfg.ClaudePath = ""
	}
	if res.Source == SourceStandalone {
		cfg.ClaudePath = res.Path
	}
}

// Hint writes the "found off PATH" note: which binary cwm uses and the
// exact command that puts its folder on PATH. Nothing when it is on PATH.
func Hint(w io.Writer, goos string, res Result) {
	if res.OnPath || w == nil {
		return
	}
	if res.Source.Bundled() {
		fmt.Fprintf(w, "note: using the claude bundled with %s at %s (cwm looks it up afresh each time: it changes with app updates)\n", orApp(res.Bundle), res.Path)
		fmt.Fprintf(w, "      for a claude that does not change with the app, install the standalone CLI:\n      %s\n", InstallCommand(goos))
		return
	}
	fmt.Fprintf(w, "note: using claude at %s (its folder is not on PATH; cwm remembers it as claude_path in config.toml)\n", res.Path)
	fmt.Fprintf(w, "      to run claude yourself in new terminals, add the folder to PATH:\n      %s\n", PathHint(goos, res.Dir()))
}

func orApp(name string) string {
	if name == "" {
		return "an app"
	}
	return name
}
