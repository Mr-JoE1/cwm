// Stage C1: `cwm account add|list|use|remove`.
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
		return fmt.Errorf("usage: cwm account add|list|use|remove")
	}
	action, rest := args[0], args[1:]

	var asJSON, yes, dryRun bool
	var pos []string
	for _, a := range rest {
		switch a {
		case "--json":
			asJSON = true
		case "--yes", "-y":
			yes = true
		case "--dry-run":
			dryRun = true
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
		if err := accounts.Add(layout, cfg, name, e.Stdout); err != nil {
			return err
		}
		return config.Save(layout.ConfigPath(), cfg)

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
			return fmt.Errorf("usage: cwm account remove <name> [--yes]")
		}
		name := pos[0]
		if dryRun {
			fmt.Fprintf(e.Stdout, "would back up %s to %s\n", filepath.Join(layout.AccountsDir(), name), layout.BackupsDir())
			fmt.Fprintf(e.Stdout, "would remove account %q from %s\n", name, layout.ConfigPath())
			return nil
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
		if err := accounts.Remove(layout, cfg, name); err != nil {
			return err
		}
		if err := config.Save(layout.ConfigPath(), cfg); err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "✓ account %s removed (backup in %s)\n", name, layout.BackupsDir())
		return nil

	default:
		return fmt.Errorf("unknown account action %q (want add, list, use or remove)", action)
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
