package cli

import (
	"encoding/json"
	"fmt"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/desktopapp"
)

const desktopUsage = "usage: cwm desktop status [--json] | sync-sessions [--dry-run]"

// desktopStatus reads the Claude desktop app's profile on this machine
// with cfg's accounts (a seam for tests).
var desktopStatus = func(cfg *config.Config) desktopapp.Status { return desktopapp.ForConfig(cfg) }

// cmdDesktop is `cwm desktop status|sync-sessions` (v0.9.3, D121/D124).
func cmdDesktop(e *Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", desktopUsage)
	}
	layout, err := claudepaths.NewLayout()
	if err != nil {
		return err
	}
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		return err
	}
	sub, rest := args[0], args[1:]
	asJSON, dry := false, e.DryRun
	for _, a := range rest {
		switch {
		case a == "--json" && sub == "status":
			asJSON = true
		case a == "--dry-run" && sub == "sync-sessions":
			dry = true
		default:
			return fmt.Errorf("unexpected %q (%s)", a, desktopUsage)
		}
	}
	st := desktopStatus(cfg)
	switch sub {
	case "status":
		if asJSON {
			data, err := json.Marshal(st)
			if err != nil {
				return err
			}
			fmt.Fprintln(e.Stdout, string(data))
			return nil
		}
		fmt.Fprintln(e.Stdout, "desktop app: "+st.Line())
		if st.Root != "" {
			fmt.Fprintln(e.Stdout, "profile:     "+claudepaths.Tilde(st.Root))
		}
		share := "on"
		if !cfg.Features.DesktopSessionShare {
			share = "off"
		}
		fmt.Fprintln(e.Stdout, "Code-tab sharing: "+share+" (features.desktop_session_share)")
		return nil
	case "sync-sessions":
		if !cfg.Features.DesktopSessionShare {
			return fmt.Errorf("Code-tab sharing is off — turn it on with: cwm config set features.desktop_session_share true")
		}
		res, line, err := desktopapp.Sync(st, desktopapp.ShareOptions{DryRun: dry})
		switch {
		case res.Skipped != "":
			fmt.Fprintln(e.Stdout, "nothing to share: "+res.Skipped)
		case dry:
			fmt.Fprintf(e.Stdout, "would add %d Code-tab entr%s for %s\n", res.Added, map[bool]string{true: "y", false: "ies"}[res.Added == 1], st.Who())
		case line != "":
			fmt.Fprintln(e.Stdout, line)
		default:
			fmt.Fprintln(e.Stdout, "every Code-tab entry is already listed for "+st.Who())
		}
		return err
	}
	return fmt.Errorf("unknown desktop subcommand %q (%s)", sub, desktopUsage)
}
