package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/workspace"
)

// migrateOptions translates the cli env into the shared migrate/import
// option set.
func (e *Env) migrateOptions(yes bool) workspace.MigrateOptions {
	return workspace.MigrateOptions{
		Stdout: e.Stdout,
		Stdin:  e.Stdin,
		DryRun: e.DryRun,
		Yes:    yes,
	}
}

// initWithMigrate runs `cwm init` and then, when --migrate is given,
// imports the legacy bash-kit workspace from ~/.claude-me (stage C2).
func initWithMigrate(e *Env, args []string) error {
	migrate, yes := false, false
	rest := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--migrate":
			migrate = true
		case "--yes":
			yes = true
		default:
			rest = append(rest, a)
		}
	}
	if yes && !migrate {
		return fmt.Errorf("--yes is only valid together with --migrate")
	}
	if err := cmdInit(e, rest); err != nil {
		return err
	}
	if !migrate {
		return nil
	}
	l, err := storage.NewLayout()
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return workspace.MigrateFromBashKit(l, cfg, e.migrateOptions(yes))
}

// cmdImport implements `cwm import [--from <dir>] [--ccam] [--account
// <name>] [--yes]`.
func cmdImport(e *Env, args []string) error {
	var from, account string
	ccam, yes := false, false
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--from":
			if i+1 >= len(args) {
				return fmt.Errorf("--from needs a value (try: --from <dir>)")
			}
			i++
			from = args[i]
		case strings.HasPrefix(a, "--from="):
			from = strings.TrimPrefix(a, "--from=")
		case a == "--ccam":
			ccam = true
		case a == "--account":
			if i+1 >= len(args) {
				return fmt.Errorf("--account needs a value (try: --account <name>)")
			}
			i++
			account = args[i]
		case strings.HasPrefix(a, "--account="):
			account = strings.TrimPrefix(a, "--account=")
		case a == "--yes":
			yes = true
		case a == "-h" || a == "--help":
			fmt.Fprintf(e.Stdout, "usage: cwm import [--from <dir>] [--ccam] [--account <name>] [--yes]\n\n"+
				"  import an existing claude config dir (default ~/.claude) or, with\n"+
				"  --ccam, all ccam profiles and shared groups. Sources are never modified.\n")
			return nil
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q (see: cwm help import)", a)
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) > 0 {
		return fmt.Errorf("unexpected argument %q (see: cwm help import)", pos[0])
	}
	if ccam && from != "" {
		return fmt.Errorf("--from and --ccam are mutually exclusive")
	}
	if ccam && account != "" {
		return fmt.Errorf("--account is not valid with --ccam (profile dirs name the accounts)")
	}
	l, err := storage.NewLayout()
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}
	if _, err := os.Stat(l.WorkspaceDir()); err != nil {
		return fmt.Errorf("cwm home not initialized — run `cwm init` first")
	}
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}
	if account != "" && !config.ValidAccountName(account) {
		return fmt.Errorf("import: invalid account name %q (want [a-z0-9][a-z0-9_-]{0,31}, not all digits)", account)
	}
	if ccam {
		return workspace.ImportCcam(l, cfg, e.migrateOptions(yes))
	}
	if from == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("import: resolve home dir: %w", err)
		}
		from = filepath.Join(home, ".claude")
	}
	return workspace.ImportDir(l, cfg, from, account, e.migrateOptions(yes))
}
