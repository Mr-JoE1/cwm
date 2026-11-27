// Stage B2: the launcher commands — `cwm open <repo>` and the direct
// launch form `cwm <repo> [new|last]`.
package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/claudeopen"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/desktopapp"
	"github.com/Mr-JoE1/cwm/internal/launcher"
)

// parseMode consumes an optional leading mode word (new|last) from args.
func parseMode(args []string) (launcher.Mode, []string) {
	if len(args) > 0 {
		switch args[0] {
		case "new":
			return launcher.ModeNew, args[1:]
		case "last":
			return launcher.ModeLast, args[1:]
		}
	}
	return launcher.ModeResume, args
}

func cmdOpen(e *Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cwm open <repo> [new|last] [claude args...]")
	}
	repo := args[0]
	if IsReserved(repo) {
		return fmt.Errorf("repo name %q is reserved by cwm commands — rename the repo (an explicit escape flag arrives in v0.3)", repo)
	}
	mode, extra := parseMode(args[1:])
	return launcher.Open(repo, mode, extra, e.Account, e.Stdout, e.Stderr)
}

// runDirect implements `cwm <repo> [new|last] ...` for non-command words.
func (e *Env) runDirect(repo string, rest []string) int {
	if err := cmdOpen(e, append([]string{repo}, rest...)); err != nil {
		var se silentExit
		if errors.As(err, &se) {
			return se.code
		}
		fmt.Fprintf(e.Stderr, "cwm: %v\n", err)
		return ExitError
	}
	return ExitOK
}

// cmdRun is `cwm run [--account <name>] [-- claude args]` (D80): claude in
// the current directory as an account. The default account uses the
// workspace root (capture mode: no CLAUDE_CONFIG_DIR at all); any other
// runs in parallel from its login-only dir.
func cmdRun(e *Env, args []string) error {
	account := e.Account
	var extra []string
	with := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if v, next, ok, err := withFlag(args, i); ok {
			if err != nil {
				return err
			}
			with, i = v, next
			continue
		}
		switch {
		case a == "--":
			extra = append(extra, args[i+1:]...)
			i = len(args)
		case a == "--account":
			if i+1 >= len(args) {
				return fmt.Errorf("--account needs a value (try: cwm run --account <name>)")
			}
			i++
			account = args[i]
		case strings.HasPrefix(a, "--account="):
			account = strings.TrimPrefix(a, "--account=")
		case a == "-h" || a == "--help":
			fmt.Fprint(e.Stdout, "usage: cwm run [--account <name>] [--with auto|cli|vscode|desktop] [-- claude args]\n\n"+
				"  Starts claude in the current directory. The default account uses the\n"+
				"  workspace directly; another account runs in parallel from its\n"+
				"  login-only dir, sharing every session, memory and setting.\n")
			return nil
		default:
			return fmt.Errorf("cwm run: unexpected %q (claude arguments go after --: cwm run -- %s)", a, a)
		}
	}
	layout, err := claudepaths.NewLayout()
	if err != nil {
		return err
	}
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		return err
	}
	choice, err := launchChoice(layout, cfg, with)
	if err != nil {
		return err
	}
	if choice.ID == claudeopen.Desktop {
		opened, err := e.runDesktop(cfg, account, choice)
		if opened || err != nil {
			return err
		}
		choice = claudeopen.Choice{ID: claudeopen.Auto}
	}
	if choice.ID == claudeopen.VSCode {
		e.Account = account
		return e.openApp(choice, runFolder(), "", nil)
	}
	if e.DryRun {
		who := account
		if who == "" {
			who = "the default account"
		}
		fmt.Fprintf(e.Stdout, "would run claude %s as %s\n", strings.Join(extra, " "), who)
		return nil
	}
	return launcher.Run(account, extra)
}

// runDesktop is `cwm run --with desktop` (D122). The app has its own
// sign-in, so an account opens there only when the app is signed in to
// it; otherwise it reports false and the caller opens the account where
// cwm's account env applies, a terminal (this one).
func (e *Env) runDesktop(cfg *config.Config, account string, choice claudeopen.Choice) (bool, error) {
	r := claudeopen.RouteAccount(account, desktopAccount(cfg))
	if !r.Desktop {
		fmt.Fprintln(e.Stderr, "note: "+r.Note)
		return false, nil
	}
	if err := e.openApp(choice, runFolder(), "", nil); err != nil {
		return true, err
	}
	if r.Note != "" && !e.DryRun {
		fmt.Fprintln(e.Stdout, r.Note)
	}
	return true, nil
}

// desktopAccount is who the desktop app is signed in to (a seam for
// tests).
var desktopAccount = func(cfg *config.Config) claudeopen.AppAccount {
	return desktopapp.ForConfig(cfg).AppAccount()
}
