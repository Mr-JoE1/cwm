// Stage C1: `cwm account add|list|use|remove`; v0.6.2 adds `login` and
// the login-state markers the dashboard reads (D55).
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
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
		case "--delete-config", "--delete-dir":
			deleteDir = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("unknown account %s flag %q", action, a)
			}
			pos = append(pos, a)
		}
	}
	dryRun = dryRun || e.DryRun

	layout, err := claudepaths.NewLayout()
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
		return e.installDefaultLogin(layout, cfg, name, false)

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
		return e.installDefaultLogin(layout, cfg, name, true)

	case "list":
		if len(pos) != 0 {
			return fmt.Errorf("usage: cwm account list [--json]")
		}
		return accounts.List(cfg, layout, asJSON, e.Stdout)

	case "use":
		if len(pos) != 1 {
			return fmt.Errorf("usage: cwm account use <name>")
		}
		if dryRun {
			fmt.Fprintf(e.Stdout, "would save the live login back into %q, then swap %q's credentials and identity into the live files\n", cfg.DefaultAccount, pos[0])
			return nil
		}
		res, err := accountSwitcher.Use(layout, cfg, pos[0])
		if err != nil {
			return err
		}
		if err := config.Save(layout.ConfigPath(), cfg); err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "✓ default account: %s\n", pos[0])
		if res.Backup != "" {
			fmt.Fprintf(e.Stdout, "  replaced files backed up to %s\n", res.Backup)
		}
		if w := res.Warning(); w != "" {
			fmt.Fprintf(e.Stdout, "! %s\n", w)
		}
		return nil

	case "remove", "rm":
		if len(pos) != 1 {
			return fmt.Errorf("usage: cwm account remove <name> [--delete-config] [--yes]")
		}
		name := pos[0]
		acctDir := filepath.Join(layout.AccountsDir(), name)
		if a, ok := cfg.Account(name); ok {
			acctDir = a.ExpandedDir()
		}
		if dryRun {
			fmt.Fprintf(e.Stdout, "would back up %s to %s\n", acctDir, layout.BackupsDir())
			fmt.Fprintf(e.Stdout, "would remove account %q from %s\n", name, layout.ConfigPath())
			if deleteDir {
				if accounts.Deletable(layout, acctDir) {
					fmt.Fprintf(e.Stdout, "would delete %s and %s after the backup\n", acctDir, filepath.Join(layout.LaunchDir(), name))
				} else {
					fmt.Fprintf(e.Stdout, "would delete %s after the backup and keep %s (outside the cwm home)\n", filepath.Join(layout.LaunchDir(), name), acctDir)
				}
			}
			return nil
		}
		// Refuse before asking: the question would be pointless.
		if _, ok := cfg.Account(name); ok && len(cfg.Accounts) == 1 {
			return accounts.ErrLastAccount
		}
		if !yes {
			confirmed, err := confirmRemove(e, accountStdinIsTTY)
			if err != nil {
				return err
			}
			if !confirmed {
				fmt.Fprintln(e.Stdout, "aborted")
				return nil
			}
			// Both questions come before any work. Only an interactive
			// run gets here (confirmRemove refuses a non-TTY), and a dir
			// outside the cwm home is never deletable, so no question.
			if _, ok := cfg.Account(name); ok && !deleteDir && accounts.Deletable(layout, acctDir) {
				del, err := askConfirm(e, "also delete its config folder "+acctDir)
				if err != nil {
					return err
				}
				deleteDir = del
			}
		}
		res, err := accounts.RemoveWith(layout, cfg, name, accounts.RemoveOptions{DeleteDir: deleteDir, Switch: accountSwitcher.SwitchFunc(layout), Store: accountSwitcher.CredStore()})
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
		if res.KeptDir != "" {
			fmt.Fprintf(e.Stdout, "kept its config folder %s (outside the cwm home; delete it yourself if you no longer need it)\n", res.KeptDir)
		}
		if res.NewDefault != "" {
			fmt.Fprintf(e.Stdout, "✓ default account is now %s\n", res.NewDefault)
		}
		return nil

	default:
		return fmt.Errorf("unknown account action %q (want add, login, list, use or remove)", action)
	}
}

// accountStdinIsTTY decides whether `account remove` may prompt; a seam
// so tests can drive the interactive path.
var accountStdinIsTTY = stdinIsTTY

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
	line, err := readLine(e.Stdin)
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

// readLine reads one line byte by byte. A bufio.Reader would swallow the
// answers to later prompts (remove asks twice) along with the first one.
func readLine(r io.Reader) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				return b.String(), nil
			}
			b.WriteByte(buf[0])
		}
		if err != nil {
			return b.String(), err
		}
	}
}

// accountSwitcher performs `cwm account use` (D79); tests swap in a fake
// credential store and process finder.
var accountSwitcher = accounts.Switcher{}

// installDefaultLogin makes a fresh login of the default account the live
// one (accounts.InstallDefault): after `account add` only when there is
// no live login yet (the first account on a machine), after `account
// login` always, so plain claude and cwm's default launches use it.
func (e *Env) installDefaultLogin(layout *storage.Layout, cfg *config.Config, name string, force bool) error {
	ok, err := accounts.InstallDefault(layout, cfg, name, accountSwitcher.CredStore(), force, time.Now())
	if err != nil {
		return err
	}
	if ok {
		live, err := claudepaths.LiveFor(layout, cfg, "")
		if err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "✓ %s is the live login now (%s): plain claude uses it\n", name, accountSwitcher.CredStore().Location(live))
	}
	return nil
}
