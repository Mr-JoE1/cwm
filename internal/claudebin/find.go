package claudebin

import (
	"fmt"
	"io"
	"os"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// Find resolves claude for a cwm command: config.toml's claude_path feeds
// the lookup, and an off-PATH hit (SourceDiscovered) is written back to
// claude_path so later commands — and the agent, whose PATH is frozen at
// login — skip the search. The write is best-effort and only happens when
// config.toml already exists: creating it here would make an
// uninitialised home look initialised to the installer.
func Find(layout *storage.Layout) (Result, error) {
	return find(layout, SystemEnv(), true)
}

// Lookup is Find without the write-back, for read-only callers (doctor,
// the dashboard's state poll).
func Lookup(layout *storage.Layout) (Result, error) {
	return find(layout, SystemEnv(), false)
}

func find(layout *storage.Layout, env Env, persist bool) (Result, error) {
	var cfg *config.Config
	configured := ""
	if layout != nil {
		if c, err := config.Load(layout.ConfigPath()); err == nil {
			cfg, configured = c, c.ClaudePath
		}
	}
	res, err := Resolve(env, configured)
	if err != nil {
		return Result{}, err
	}
	if persist && res.Source == SourceDiscovered && cfg != nil && cfg.ClaudePath != res.Path {
		if _, serr := os.Stat(layout.ConfigPath()); serr == nil {
			cfg.ClaudePath = res.Path
			_ = config.Save(layout.ConfigPath(), cfg)
		}
	}
	return res, nil
}

// Remember stores res.Path as claude_path in an in-memory config the
// caller is about to save anyway (account add), so the persisted value is
// not clobbered by that later Save.
func Remember(cfg *config.Config, res Result) {
	if cfg != nil && res.Source == SourceDiscovered {
		cfg.ClaudePath = res.Path
	}
}

// Hint writes the "found off PATH" note: which binary cwm uses and the
// exact command that puts its folder on PATH. Nothing when it is on PATH.
func Hint(w io.Writer, goos string, res Result) {
	if res.OnPath || w == nil {
		return
	}
	fmt.Fprintf(w, "note: using claude at %s (its folder is not on PATH; cwm remembers it as claude_path in config.toml)\n", res.Path)
	fmt.Fprintf(w, "      to run claude yourself in new terminals, add the folder to PATH:\n      %s\n", PathHint(goos, res.Dir()))
}
