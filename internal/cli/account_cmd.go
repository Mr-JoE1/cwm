// Stage C1: `cwm account add|list|use|remove`; v0.6.2 adds `login` and
// the login-state markers the dashboard reads (D55).
package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

func cmdAccount(e *Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cwm account add|login|list|use|remove")
	}
	action, rest := args[0], args[1:]

	var asJSON, yes, dryRun, deleteDir bool
	var pos []string
	for _, a := range rest {
		switch a {
		case "--json":
			asJSON = true
		case "--yes", "-y":
			yes = true
		case "--dry-run":
			dryRun = true
		case "--delete-dir":
			deleteDir = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("unknown account %s flag %q", action, a)
			}
			pos = append(pos, a)
		}
	}
	dryRun = dryRun || e.DryRun

	layout, err := storage.NewLayout()
	if err != nil {
		return err
	}
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		return err
	}

	switch action {
	case "add":
		if len(pos) != 1 {
			return fmt.Errorf("usage: cwm account add <name>")
		}
		name := pos[0]
		if dryRun {
			fmt.Fprintf(e.Stdout, "would create %s\n", filepath.Join(layout.AccountsDir(), name))
			fmt.Fprintf(e.Stdout, "would run `claude auth login` (CLAUDE_CONFIG_DIR = the new account dir)\n")
			fmt.Fprintf(e.Stdout, "would register account %q in %s\n", name, layout.ConfigPath())
			return nil
		}
		// Validate before the marker: a typo must not leave state behind.
		if err := accounts.ValidateNewName(name, cfg, IsReserved); err != nil {
			return fmt.Errorf("accounts: %w", err)
		}
		accounts.MarkLogin(layout, name, accounts.LoginInProgress, "cli", nil)
		if err := accounts.AddChecked(layout, cfg, name, IsReserved, e.Stdout); err != nil {
			accounts.MarkLogin(layout, name, accounts.LoginFailed, "cli", err)
			return err
		}
		if err := config.Save(layout.ConfigPath(), cfg); err != nil {
			accounts.MarkLogin(layout, name, accounts.LoginFailed, "cli", err)
			return err
		}
		accounts.MarkLogin(layout, name, accounts.LoginDone, "cli", nil)
		return nil

	case "login":
		if len(pos) != 1 {
			return fmt.Errorf("usage: cwm account login <name>")
		}
		name := pos[0]
		if _, ok := cfg.Account(name); !ok {
			return fmt.Errorf("accounts: account %q not found — add it with: cwm account add %s", name, name)
		}
		if dryRun {
			fmt.Fprintf(e.Stdout, "would run `claude auth login` for account %q (CLAUDE_CONFIG_DIR = its account dir)\n", name)
			return nil
		}
		accounts.MarkLogin(layout, name, accounts.LoginInProgress, "cli", nil)
		if err := accounts.Login(layout, cfg, name, e.Stdout); err != nil {
			accounts.MarkLogin(layout, name, accounts.LoginFailed, "cli", err)
			return err
		}
		if err := config.Save(layout.ConfigPath(), cfg); err != nil {
			accounts.MarkLogin(layout, name, accounts.LoginFailed, "cli", err)
			return err
		}
		accounts.MarkLogin(layout, name, accounts.LoginDone, "cli", nil)
		return nil

	case "list":
		if len(pos) != 0 {
			return fmt.Errorf("usage: cwm account list [--json]")
		}
		return accounts.List(cfg, layout, asJSON, e.Stdout)

	case "use":
		if len(pos) != 1 {
			return fmt.Errorf("usage: cwm account use <name>")
		}
		if err := accounts.Use(cfg, pos[0]); err != nil {
			return err
		}
		if err := config.Save(layout.ConfigPath(), cfg); err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "✓ default account: %s\n", pos[0])
		return nil

	case "remove", "rm":
		if len(pos) != 1 {
			return fmt.Errorf("usage: cwm account remove <name> [--delete-dir] [--yes]")
		}
		name := pos[0]
		if dryRun {
			fmt.Fprintf(e.Stdout, "would back up %s to %s\n", filepath.Join(layout.AccountsDir(), name), layout.BackupsDir())
			fmt.Fprintf(e.Stdout, "would remove account %q from %s\n", name, layout.ConfigPath())
			if deleteDir {
				fmt.Fprintf(e.Stdout, "would delete %s and %s after the backup\n", filepath.Join(layout.AccountsDir(), name), filepath.Join(layout.LaunchDir(), name))
			}
			return nil
		}
		// Refuse before asking: the question would be pointless.
		if _, ok := cfg.Account(name); ok && len(cfg.Accounts) == 1 {
			return accounts.ErrLastAccount
		}
		if !yes {
			confirmed, err := confirmRemove(e, stdinIsTTY)
			if err != nil {
				return err
			}
			if !confirmed {
				fmt.Fprintln(e.Stdout, "aborted")
				return nil
			}
		}
		res, err := accounts.RemoveWith(layout, cfg, name, accounts.RemoveOptions{DeleteDir: deleteDir})
		if err != nil {
			return err
		}
		if err := config.Save(layout.ConfigPath(), cfg); err != nil {
			return err
		}
		where := layout.BackupsDir()
		if res.Backup != "" {
			where = res.Backup
		}
		fmt.Fprintf(e.Stdout, "✓ account %s removed (backup in %s)\n", name, where)
		if res.DeletedDir {
			fmt.Fprintf(e.Stdout, "✓ deleted its account dir and launch dir\n")
		}
		if res.NewDefault != "" {
			fmt.Fprintf(e.Stdout, "✓ default account is now %s\n", res.NewDefault)
		}
		return nil

	default:
		return fmt.Errorf("unknown account action %q (want add, login, list, use or remove)", action)
	}
}

func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	// /dev/null is a char device too; redirected-from-null stdin must count
	// as non-interactive.
	if null, err := os.Open(os.DevNull); err == nil {
		defer null.Close()
		if ni, err := null.Stat(); err == nil && os.SameFile(fi, ni) {
			return false
		}
	}
	return true
}

// confirmRemove decides whether a removal may proceed: --yes skips the
// question, a non-TTY without --yes is an error, a TTY gets a y/N prompt.
func confirmRemove(e *Env, isTTY func() bool) (bool, error) {
	if !isTTY() {
		return false, fmt.Errorf("account remove requires --yes in non-interactive mode")
	}
	return askConfirm(e, "back up and remove this account")
}

// askConfirm prompts on a TTY and accepts y/yes (any case).
func askConfirm(e *Env, prompt string) (bool, error) {
	fmt.Fprintf(e.Stdout, "cwm: %s? [y/N]: ", prompt)
	line, err := bufio.NewReader(e.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false, fmt.Errorf("could not read confirmation (pass --yes to skip the prompt): %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
