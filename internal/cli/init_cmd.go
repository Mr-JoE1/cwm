// Stage C1: `cwm init` — workspace skeleton + optional first account.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/migrate"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/workspace"
)

func cmdInit(e *Env, args []string) error {
	var account string
	migrate := false
	dryRun := e.DryRun
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--account":
			if i+1 >= len(args) {
				return fmt.Errorf("--account needs a value (try: --account <name>)")
			}
			i++
			account = args[i]
		case strings.HasPrefix(a, "--account="):
			account = strings.TrimPrefix(a, "--account=")
		case a == "--migrate":
			migrate = true
		case a == "--dry-run":
			dryRun = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown init flag %q", a)
		default:
			return fmt.Errorf("unexpected init argument %q (try: cwm init [--account <name>])", a)
		}
	}
	if migrate {
		return fmt.Errorf("--migrate is not implemented yet (planned for v0.2-C2); run `cwm init` for a fresh workspace")
	}
	if account != "" && !config.ValidAccountName(account) {
		return fmt.Errorf("invalid account name %q (want [a-z0-9][a-z0-9_-]{0,31}, not all digits)", account)
	}
	layout, err := claudepaths.NewLayout()
	if err != nil {
		return err
	}
	if err := firstRun(e, layout, dryRun); err != nil {
		return err
	}
	return workspace.Init(layout, account, dryRun, e.Stdout)
}

// firstRun is the installer's first-run path (SPEC-V0.8 Migration): with
// no config.toml yet, the migration engine adopts an existing ~/.claude in
// place (nothing moved, .stignore written, the live login imported as
// account "default") or prepares a fresh capture root, and records it,
// so `cwm migrate rollback` can undo even the first run. A machine that
// already has a config is never migrated silently.
func firstRun(e *Env, layout *storage.Layout, dryRun bool) error {
	if _, err := os.Stat(layout.ConfigPath()); err == nil {
		return nil
	}
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		return err
	}
	o := e.migrateEngineOpts("")
	plan, err := migrate.BuildPlan(layout, cfg, o)
	if err != nil {
		return err
	}
	if plan.Kind == migrate.KindNone {
		return nil
	}
	if dryRun {
		printMigrationPlan(e.Stdout, plan, true)
		return nil
	}
	o.Progress = io.Discard
	rec, err := migrate.FirstRun(layout, o)
	if err != nil || rec == nil {
		return err
	}
	switch rec.Kind {
	case migrate.KindAdopt:
		fmt.Fprintf(e.Stdout, "✓ capture mode: adopted %s in place (nothing moved)\n", rec.Root)
	default:
		fmt.Fprintf(e.Stdout, "✓ %s mode: workspace root %s\n", rec.ToMode, rec.Root)
	}
	if rec.Default != "" {
		fmt.Fprintf(e.Stdout, "✓ the live login is account %s\n", rec.Default)
	}
	return nil
}
