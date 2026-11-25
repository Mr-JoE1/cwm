package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/migrate"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
)

// migrateSync re-points the Syncthing folder at a new root (seam for
// tests: never a real Syncthing there).
var migrateSync = func(root string) error {
	cl, err := connectSyncClient(false)
	if err != nil {
		return err
	}
	_, err = syncengine.EnsureFolder(cl, root)
	return err
}

// migrateOpts is what every migrate entry point (cwm migrate, cwm mode
// set, cwm init's first run) hands the engine.
func (e *Env) migrateEngineOpts(to string) migrate.Options {
	return migrate.Options{To: to, Account: e.Account, SyncFolder: migrateSync, Progress: e.Stdout}
}

// cmdMigrate is `cwm migrate [--dry-run] [--to capture|isolated] [--yes]
// [--json]`, `cwm migrate rollback [<id>] [--yes]` and `cwm migrate list`.
func cmdMigrate(e *Env, args []string) error {
	var to string
	yes, asJSON, dry := false, false, e.DryRun
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--to":
			if i+1 >= len(args) {
				return fmt.Errorf("--to needs a value (capture or isolated)")
			}
			i++
			to = args[i]
		case strings.HasPrefix(a, "--to="):
			to = strings.TrimPrefix(a, "--to=")
		case a == "--yes" || a == "-y":
			yes = true
		case a == "--json":
			asJSON = true
		case a == "--dry-run":
			dry = true
		case a == "--help" || a == "-h":
			fmt.Fprint(e.Stdout, migrateUsage)
			return nil
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown migrate flag %q", a)
		default:
			pos = append(pos, a)
		}
	}
	if to != "" && !config.ValidMode(to) {
		return fmt.Errorf("invalid mode %q (want capture or isolated)", to)
	}
	layout, err := claudepaths.NewLayout()
	if err != nil {
		return err
	}
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		switch pos[0] {
		case "rollback":
			if len(pos) > 2 {
				return fmt.Errorf("usage: cwm migrate rollback [<id>] [--yes]")
			}
			id := ""
			if len(pos) == 2 {
				id = pos[1]
			}
			return migrateRollback(e, layout, id, yes, dry)
		case "list":
			return migrateList(e, layout, asJSON)
		default:
			return fmt.Errorf("unknown migrate subcommand %q (want rollback or list)", pos[0])
		}
	}
	return runMigration(e, layout, cfg, to, dry, yes, asJSON)
}

const migrateUsage = `usage: cwm migrate [--dry-run] [--to capture|isolated] [--yes] [--json]
       cwm migrate rollback [<id>] [--yes]
       cwm migrate list [--json]

Moves a v0.7.x layout, an existing ~/.claude or the other mode's root into
the workspace root of the target mode: backup (sha256 manifest), plan,
apply, verify, record. Nothing is deleted; rollback restores the backup
byte for byte.
`

// runMigration previews the plan, then (unless dry) confirms and applies.
func runMigration(e *Env, layout *storage.Layout, cfg *config.Config, to string, dry, yes, asJSON bool) error {
	o := e.migrateEngineOpts(to)
	if asJSON {
		o.Progress = io.Discard
	}
	plan, err := migrate.BuildPlan(layout, cfg, o)
	if err != nil {
		return err
	}
	if asJSON && dry {
		return writeJSONOut(e.Stdout, plan)
	}
	if plan.Kind == migrate.KindNone {
		if asJSON {
			return writeJSONOut(e.Stdout, plan)
		}
		fmt.Fprintf(e.Stdout, "nothing to migrate: mode is already %s (%s)\n", plan.ToMode, claudepaths.Tilde(plan.Root))
		return nil
	}
	if !asJSON {
		printMigrationPlan(e.Stdout, plan, dry)
	}
	if dry {
		return nil
	}
	if !yes {
		ok, err := confirm(e, "apply this migration?")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(e.Stdout, "aborted — nothing changed")
			return nil
		}
	}
	rec, err := migrate.Apply(layout, cfg, plan, o)
	if asJSON && rec != nil {
		_ = writeJSONOut(e.Stdout, rec)
	}
	if err != nil {
		return err
	}
	if !asJSON {
		fmt.Fprintf(e.Stdout, "✓ migrated to %s mode (%s); %d of %d sessions verified\n", rec.ToMode, rec.Root, rec.SessionsAfter, rec.SessionsBefore)
		if rec.Conflicts > 0 {
			fmt.Fprintf(e.Stdout, "  %d conflict(s) kept side by side — see `cwm conflicts` and the Health tab\n", rec.Conflicts)
		}
		fmt.Fprintf(e.Stdout, "  undo: cwm migrate rollback %s\n", rec.ID)
	}
	return nil
}

func writeJSONOut(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// printMigrationPlan is the dry-run preview: counts per action kind, every
// login move and conflict in full, the first copies.
func printMigrationPlan(w io.Writer, p *migrate.Plan, dry bool) {
	head := "migration plan"
	if dry {
		head += " (dry run — nothing will change)"
	}
	from := p.FromMode
	if from == "" {
		from = map[string]string{migrate.KindV07: "v0.7 layout", migrate.KindAdopt: "first run", migrate.KindFresh: "first run", migrate.KindSwitch: "first run"}[p.Kind]
	}
	fmt.Fprintf(w, "%s: %s -> %s mode (%s)\n", head, from, p.ToMode, p.Kind)
	fmt.Fprintf(w, "  root:    %s\n", claudepaths.Tilde(p.Root))
	for _, s := range p.Sources {
		fmt.Fprintf(w, "  source:  %s\n", claudepaths.Tilde(s))
	}
	fmt.Fprintf(w, "  backup:  %s (%s) -> ~/.cwm/backups/migrate-<id>/ with a sha256 manifest\n", strings.Join(p.BackupRoots, ", "), humanBytes(p.BackupBytes))
	counts := map[string]int{}
	for _, a := range p.Actions {
		counts[a.Kind]++
	}
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var parts []string
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%s %d", k, counts[k]))
	}
	if p.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("identical %d", p.Skipped))
	}
	fmt.Fprintf(w, "  actions: %s\n", strings.Join(parts, ", "))
	fmt.Fprintf(w, "  verify:  %d session(s) must be present afterwards\n", len(p.Sessions))
	shown := 0
	for _, a := range p.Actions {
		switch a.Kind {
		case migrate.ActCopy, migrate.ActExtend:
			if shown >= 10 {
				continue
			}
			shown++
		}
		line := ""
		switch {
		case a.Account != "":
			line = fmt.Sprintf("%s (%s -> %s)", a.Account, a.Src, a.Dst)
		case a.Src != "" && a.Dst != "":
			line = claudepaths.Tilde(a.Src) + " -> " + claudepaths.Tilde(a.Dst)
		case a.Src != "":
			line = claudepaths.Tilde(a.Src)
		default:
			line = claudepaths.Tilde(a.Dst)
		}
		if a.Detail != "" {
			line += " — " + a.Detail
		}
		fmt.Fprintf(w, "  %-11s %s\n", a.Kind, line)
	}
	if n := counts[migrate.ActCopy] + counts[migrate.ActExtend] - shown; n > 0 {
		fmt.Fprintf(w, "  …and %d more file(s)\n", n)
	}
	if p.Default != "" {
		fmt.Fprintf(w, "  default account: %s\n", p.Default)
	}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func confirm(e *Env, question string) (bool, error) {
	if e.Stdin == nil {
		return false, fmt.Errorf("confirmation required — pass --yes (non-interactive)")
	}
	fmt.Fprintf(e.Stdout, "%s [y/N] ", question)
	line, err := bufio.NewReader(e.Stdin).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "" && err != nil {
		return false, fmt.Errorf("not confirmed — pass --yes (non-interactive)")
	}
	return answer == "y" || answer == "yes", nil
}

func migrateRollback(e *Env, layout *storage.Layout, id string, yes, dry bool) error {
	recs, err := migrate.Records(layout)
	if err != nil {
		return err
	}
	target := ""
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Status != migrate.StatusRolledBack && (id == "" || recs[i].ID == id) {
			target = recs[i].ID
			break
		}
	}
	if target == "" {
		if id == "" {
			return errors.New("no migration to roll back (see: cwm migrate list)")
		}
		return fmt.Errorf("migration %q not found or already rolled back (see: cwm migrate list)", id)
	}
	fmt.Fprintf(e.Stdout, "rollback %s: ~/.claude, ~/.claude.json and ~/.cwm return to their state before it; anything newer is moved aside into ~/.cwm/backups/\n", target)
	if dry {
		return nil
	}
	if !yes {
		ok, err := confirm(e, "roll back?")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(e.Stdout, "aborted — nothing changed")
			return nil
		}
	}
	rec, err := migrate.Rollback(layout, target, migrate.Options{Progress: e.Stdout})
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "✓ rolled back %s\n", rec.ID)
	return nil
}

func migrateList(e *Env, layout *storage.Layout, asJSON bool) error {
	recs, err := migrate.Records(layout)
	if err != nil {
		return err
	}
	if asJSON {
		if recs == nil {
			recs = []migrate.Record{}
		}
		return writeJSONOut(e.Stdout, recs)
	}
	if len(recs) == 0 {
		fmt.Fprintln(e.Stdout, "no migrations recorded")
		return nil
	}
	for _, r := range recs {
		fmt.Fprintf(e.Stdout, "%s  %-12s %-7s -> %-8s %-13s sessions %d/%d  backup %s\n",
			r.ID, r.Kind, orDash(r.FromMode), r.ToMode, r.Status, r.SessionsAfter, r.SessionsBefore, r.Backup)
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
