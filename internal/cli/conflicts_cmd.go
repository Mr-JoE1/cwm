package cli

import (
	"fmt"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/workspace"
)

const conflictsUsage = "usage: cwm conflicts [--json] | resolve <file> current|merge|<copy>"

// cmdConflicts lists the Syncthing conflict copies inside the synced
// items and settles one (spec "Conflicts"): keep the current file, keep
// both merged (text only), or take a copy. Losing versions go to
// ~/.cwm/backups/conflicts/; nothing is deleted.
func cmdConflicts(e *Env, args []string) error {
	l, err := claudepaths.NewLayout()
	if err != nil {
		return err
	}
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "resolve" {
		if len(args) != 3 {
			return fmt.Errorf("%s", conflictsUsage)
		}
		if e.DryRun {
			fmt.Fprintf(e.Stdout, "would resolve %s with %s\n", args[1], args[2])
			return nil
		}
		bdir, err := workspace.ResolveConflict(l, cfg, args[1], args[2], time.Now())
		if err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "✓ %s resolved (%s); other versions moved to %s\n", args[1], args[2], claudepaths.Tilde(bdir))
		return nil
	}
	asJSON, err := onlyJSONFlag(args, conflictsUsage)
	if err != nil {
		return err
	}
	cs, err := workspace.FindConflicts(l.WorkspaceDir(), cfg)
	if err != nil {
		return err
	}
	if asJSON {
		if cs == nil {
			cs = []workspace.Conflict{}
		}
		return writeJSON(e.Stdout, cs)
	}
	if len(cs) == 0 {
		fmt.Fprintln(e.Stdout, "no conflict copies in the workspace")
		return nil
	}
	for _, c := range cs {
		fmt.Fprintf(e.Stdout, "%d versions of %s — pick one:\n", len(c.Copies)+1, c.Path)
		if c.Exists {
			fmt.Fprintf(e.Stdout, "  current                 cwm conflicts resolve %s current\n", c.Path)
		}
		if c.Mergeable {
			fmt.Fprintf(e.Stdout, "  keep both merged        cwm conflicts resolve %s merge\n", c.Path)
		}
		for _, cc := range c.Copies {
			fmt.Fprintf(e.Stdout, "  from %s (%s)  cwm conflicts resolve %s %s\n", cc.Device, cc.When, c.Path, cc.Name)
		}
	}
	return nil
}
