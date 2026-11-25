package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/upkeep"
)

// agentMaintainEvery is how often the agent runs its maintenance tick
// (a seam for tests).
var agentMaintainEvery = time.Minute

// maintainOnce is one agent maintenance tick (upkeep.Tick) on the
// current config: the config is re-read every time, so a switch changed
// from the CLI or the dashboard takes effect on the next tick.
func maintainOnce(now time.Time) (upkeep.Report, error) {
	l, err := storage.NewLayout()
	if err != nil {
		return upkeep.Report{}, err
	}
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		return upkeep.Report{}, err
	}
	if err := claudepaths.Apply(l, cfg); err != nil {
		return upkeep.Report{}, err
	}
	return upkeep.Tick(l, cfg, now), nil
}

// logMaintain prints the lines worth a log entry (changes only, so an
// idle agent's log stays quiet).
func logMaintain(w io.Writer, name string, rep upkeep.Report, err error, lastConflicts *int) {
	if err != nil {
		fmt.Fprintf(w, "%s: maintenance: %v\n", name, err)
		return
	}
	for _, e := range rep.Errors {
		fmt.Fprintf(w, "%s: maintenance: %s\n", name, e)
	}
	if rep.StIgnore {
		fmt.Fprintf(w, "%s: regenerated the workspace .stignore from [sync]\n", name)
	}
	if rep.HistoryMerged > 0 {
		fmt.Fprintf(w, "%s: merged %d history.jsonl conflict cop(ies) into history.jsonl (originals in backups/conflicts)\n", name, rep.HistoryMerged)
	}
	if len(rep.Relinked) > 0 {
		fmt.Fprintf(w, "%s: repaired login-only dir links: %s\n", name, strings.Join(rep.Relinked, ", "))
	}
	if rep.Bridge.WorkspaceChanged {
		fmt.Fprintf(w, "%s: mcp bridge: picked up from .claude.json: %s\n", name, strings.Join(rep.Bridge.FromLive, ", "))
	}
	if len(rep.Bridge.Updated) > 0 {
		fmt.Fprintf(w, "%s: mcp bridge: wrote the workspace MCP servers into .claude.json of: %s\n", name, strings.Join(rep.Bridge.Updated, ", "))
	}
	for _, c := range rep.Bridge.Conflicts {
		fmt.Fprintf(w, "%s: mcp bridge: server %q changed in both mcp.json and %s's .claude.json — kept the workspace version\n", name, c.Server, c.Target)
	}
	if len(rep.Mirror.Mirrored) > 0 {
		fmt.Fprintf(w, "%s: path mirror: made %d session(s) from other devices resumable here\n", name, len(rep.Mirror.Mirrored))
	}
	if rep.Hooks {
		fmt.Fprintf(w, "%s: merged cwm's hooks into settings.json\n", name)
	}
	if rep.TextConflicts != *lastConflicts && rep.Skipped == "" {
		if rep.TextConflicts > 0 {
			fmt.Fprintf(w, "%s: %d file(s) have Syncthing conflict copies — pick a version: cwm conflicts\n", name, rep.TextConflicts)
		}
		*lastConflicts = rep.TextConflicts
	}
}

// doctorFix is `cwm doctor --fix`: one agent maintenance pass now (the
// generated .stignore, history merge, login-only dir links, MCP bridge,
// cwm's hooks, path mirroring) plus saving the live login into the
// default account's vault entry. On a pre-v0.8 config it only saves the
// live login (safe in either layout, D102): the rest waits for the
// migration the user reviews.
func doctorFix(w io.Writer, dry bool) error {
	l, err := storage.NewLayout()
	if err != nil {
		return err
	}
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		return err
	}
	if err := claudepaths.Apply(l, cfg); err != nil {
		return err
	}
	if why := upkeep.Ready(l, cfg); why != "" {
		if !cfg.NeedsMigration() || cfg.DefaultAccount == "" {
			fmt.Fprintf(w, "fix: nothing changed — %s\n", why)
			return nil
		}
		if dry {
			fmt.Fprintf(w, "fix (dry run): would save the live login into %s's vault entry if it has none; the rest waits for the migration (review: cwm migrate --dry-run)\n", cfg.DefaultAccount)
			return nil
		}
		if saved, err := accounts.SaveLive(l, cfg, nil); err != nil {
			fmt.Fprintf(w, "fix: vault: %v\n", err)
		} else if saved {
			fmt.Fprintf(w, "fix: saved the live login into %s's vault entry\n", cfg.DefaultAccount)
		} else {
			fmt.Fprintln(w, "fix: vault: nothing to save")
		}
		fmt.Fprintf(w, "fix: the rest waits for the migration — %s\n", why)
		return nil
	}
	if dry {
		fmt.Fprintln(w, "fix (dry run): would run one maintenance pass, save the live login into the vault, or put the vault login live when there is none")
		return nil
	}
	rep := upkeep.Tick(l, cfg, time.Now())
	var none int
	logMaintain(w, "fix", rep, nil, &none)
	if saved, err := accounts.SaveLive(l, cfg, nil); err != nil {
		fmt.Fprintf(w, "fix: vault: %v\n", err)
	} else if saved {
		fmt.Fprintf(w, "fix: saved the live login into %s's vault entry\n", cfg.DefaultAccount)
	}
	if put, err := accounts.InstallDefault(l, cfg, cfg.DefaultAccount, nil, false, time.Now()); err != nil {
		fmt.Fprintf(w, "fix: live login: %v\n", err)
	} else if put {
		fmt.Fprintf(w, "fix: made %s's saved login the live one (there was none)\n", cfg.DefaultAccount)
	}
	fmt.Fprintln(w, "fix: done")
	return nil
}
