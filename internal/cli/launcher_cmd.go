// Stage B2: the launcher commands — `cwm open <repo>` and the direct
// launch form `cwm <repo> [new|last]`.
package cli

import (
	"errors"
	"fmt"

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
