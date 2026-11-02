// Stage C1: `cwm init` — workspace skeleton + optional first account.
package cli

import (
	"fmt"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/config"
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
	layout, err := storage.NewLayout()
	if err != nil {
		return err
	}
	return workspace.Init(layout, account, dryRun, e.Stdout)
}
