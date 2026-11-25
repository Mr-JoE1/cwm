package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/upkeep"
	"github.com/Mr-JoE1/cwm/internal/workspace"
)

// configRow is one `cwm config list --json` entry.
type configRow struct {
	config.KeyInfo
	Value string `json:"value"`
}

// cmdConfig is `cwm config list|get|set` (D78): every switch the Settings
// tab shows, reachable from the CLI.
func cmdConfig(e *Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cwm config list [--json] | get <key> | set <key> <value> (see: cwm help config)")
	}
	layout, err := storage.NewLayout()
	if err != nil {
		return err
	}
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		asJSON := false
		for _, a := range args[1:] {
			if a != "--json" {
				return fmt.Errorf("config list: unknown argument %q (want --json)", a)
			}
			asJSON = true
		}
		return configList(e, cfg, asJSON)
	case "get":
		if len(args) != 2 {
			return fmt.Errorf("usage: cwm config get <key> (see: cwm config list)")
		}
		v, err := cfg.Get(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintln(e.Stdout, v)
		return nil
	case "set":
		if len(args) != 3 {
			return fmt.Errorf("usage: cwm config set <key> <value> (see: cwm config list)")
		}
		key, value := args[1], args[2]
		if key == "mode" {
			// A mode change moves data: it goes through the one flow
			// that previews and applies it.
			return cmdMode(e, []string{"set", value})
		}
		if err := cfg.Set(key, value); err != nil {
			return err
		}
		got, _ := cfg.Get(key)
		if e.DryRun {
			fmt.Fprintf(e.Stdout, "would set %s = %s\n", key, got)
			return configEffects(e, layout, cfg, key, true)
		}
		if err := config.Save(layout.ConfigPath(), cfg); err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "✓ %s = %s\n", key, got)
		return configEffects(e, layout, cfg, key, false)
	case "-h", "--help":
		fmt.Fprint(e.Stdout, "usage: cwm config list [--json] | get <key> | set <key> <value>\n\n"+
			"  list        every setting with its value, default and what it does\n"+
			"  get <key>   print one value\n"+
			"  set <key> <value>\n"+
			"              change one value (bools take on/off; lists are comma-separated)\n\n"+
			"  `cwm config set mode …` is `cwm mode set …`.\n")
		return nil
	default:
		return fmt.Errorf("unknown config subcommand %q (want list, get or set)", args[0])
	}
}

func configList(e *Env, cfg *config.Config, asJSON bool) error {
	rows := []configRow{}
	for _, k := range config.Keys() {
		v, err := cfg.Get(k.Name)
		if err != nil {
			return err
		}
		rows = append(rows, configRow{KeyInfo: k, Value: v})
	}
	if asJSON {
		enc := json.NewEncoder(e.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	tw := tabwriter.NewWriter(e.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "key\tvalue\tdefault\t")
	for _, r := range rows {
		v := r.Value
		if v == "" {
			v = "-"
		}
		d := r.Default
		if d == "" {
			d = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t\n", r.Name, v, d)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if cfg.NeedsMigration() {
		fmt.Fprintln(e.Stdout, "\nnote: this config predates v0.8 (no mode recorded) — review the migration: cwm migrate --dry-run")
	}
	return nil
}

// modeReport is `cwm mode show --json`.
type modeReport struct {
	Mode           string            `json:"mode"`
	NeedsMigration bool              `json:"needs_migration"`
	Root           string            `json:"root"`
	Live           claudepaths.Paths `json:"live"`
}

// cmdMode is `cwm mode show|set`.
func cmdMode(e *Env, args []string) error {
	if len(args) == 0 {
		args = []string{"show"}
	}
	layout, err := storage.NewLayout()
	if err != nil {
		return err
	}
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		return err
	}
	switch args[0] {
	case "show":
		asJSON := len(args) == 2 && args[1] == "--json"
		if len(args) > 1 && !asJSON {
			return fmt.Errorf("usage: cwm mode show [--json]")
		}
		live, err := claudepaths.LiveFor(layout, cfg, "")
		if err != nil {
			return err
		}
		rep := modeReport{Mode: cfg.EffectiveMode(), NeedsMigration: cfg.NeedsMigration(), Root: live.Root, Live: live}
		if asJSON {
			rep.Root = claudepaths.Tilde(rep.Root)
			rep.Live = tildePaths(rep.Live)
			enc := json.NewEncoder(e.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rep)
		}
		fmt.Fprintf(e.Stdout, "mode: %s\n", rep.Mode)
		fmt.Fprintf(e.Stdout, "  workspace root:  %s\n", claudepaths.Tilde(live.Root))
		fmt.Fprintf(e.Stdout, "  .claude.json:    %s\n", claudepaths.Tilde(live.ClaudeJSON))
		fmt.Fprintf(e.Stdout, "  credentials:     %s\n", claudepaths.Tilde(live.Credentials))
		if live.Env == "" {
			fmt.Fprintln(e.Stdout, "  CLAUDE_CONFIG_DIR: not set (every claude on this machine uses the workspace)")
		} else {
			fmt.Fprintf(e.Stdout, "  CLAUDE_CONFIG_DIR: %s (cwm-launched sessions only)\n", claudepaths.Tilde(live.Env))
		}
		if rep.NeedsMigration {
			fmt.Fprintln(e.Stdout, "note: this config predates v0.8 — review the migration: cwm migrate --dry-run")
		}
		return nil
	case "set":
		to, yes, dry := "", false, e.DryRun
		for _, a := range args[1:] {
			switch a {
			case "--yes", "-y":
				yes = true
			case "--dry-run":
				dry = true
			default:
				if to != "" || strings.HasPrefix(a, "-") {
					return fmt.Errorf("usage: cwm mode set capture|isolated [--dry-run] [--yes]")
				}
				to = strings.TrimSpace(a)
			}
		}
		if !config.ValidMode(to) {
			return fmt.Errorf("invalid mode %q (want capture or isolated)", to)
		}
		return setMode(e, layout, cfg, to, dry, yes)
	case "-h", "--help":
		fmt.Fprint(e.Stdout, "usage: cwm mode show [--json] | set capture|isolated [--dry-run] [--yes]\n\n"+
			"  capture   ~/.claude is the workspace: every claude on this machine is captured\n"+
			"  isolated  ~/.cwm/workspace is the workspace: only cwm-launched sessions use it\n")
		return nil
	default:
		return fmt.Errorf("unknown mode subcommand %q (want show or set)", args[0])
	}
}

// setMode changes the workspace mode through the migration flow
// (SPEC-V0.8 Modes): preview the plan, then apply it — the synced items
// move or merge into the new root and the Syncthing folder follows.
func setMode(e *Env, layout *storage.Layout, cfg *config.Config, to string, dry, yes bool) error {
	if cfg.Mode == to {
		fmt.Fprintf(e.Stdout, "mode is already %s\n", to)
		return nil
	}
	if err := claudepaths.Apply(layout, cfg); err != nil {
		return err
	}
	return runMigration(e, layout, cfg, to, dry, yes, false)
}

func tildePaths(p claudepaths.Paths) claudepaths.Paths {
	p.Root = claudepaths.Tilde(p.Root)
	p.ConfigDir = claudepaths.Tilde(p.ConfigDir)
	p.ClaudeJSON = claudepaths.Tilde(p.ClaudeJSON)
	p.Credentials = claudepaths.Tilde(p.Credentials)
	if p.Env != "" {
		p.Env = claudepaths.Tilde(p.Env)
	}
	return p
}

// configEffects runs what a saved setting changes besides config.toml —
// the same for `cwm config set` and the dashboard's PUT /api/settings
// (D95). dry only describes the change, where there is something to
// describe (autostart, private sync).
func configEffects(e *Env, layout *storage.Layout, cfg *config.Config, key string, dry bool) error {
	// The login opener belongs to the autostart; with autostart off
	// the switch is only recorded.
	autostartKey := key == "features.autostart" || (key == "features.open_dashboard_at_login" && cfg.Features.Autostart)
	if dry {
		switch {
		case autostartKey:
			// Show what the autostart change would write and run.
			return applyAutostart(e, cfg, true)
		case key == "features.open_dashboard_at_login":
			fmt.Fprintln(e.Stdout, "  only recorded: takes effect once features.autostart is on")
		case key == "features.private_sync" && cfg.Features.PrivateSync:
			fmt.Fprintln(e.Stdout, "  Syncthing: global discovery off, relays off, local (LAN) discovery stays on")
			fmt.Fprintln(e.Stdout, "  devices outside this network need an address: cwm pair <id> --address tcp://host:22000")
		case key == "features.private_sync":
			fmt.Fprintln(e.Stdout, "  Syncthing: global discovery and relays back on (devices are found from anywhere)")
		}
		return nil
	}
	if strings.HasPrefix(key, "sync.") {
		// D81: the allow-list follows the switches at once; turning
		// one off only ignores the item, nothing is deleted.
		if err := claudepaths.Apply(layout, cfg); err != nil {
			return err
		}
		if wrote, err := workspace.WriteStIgnore(layout, cfg); err != nil {
			return err
		} else if wrote {
			fmt.Fprintf(e.Stdout, "  regenerated %s\n", claudepaths.Tilde(workspace.StIgnorePath(layout)))
		}
	}
	if key == "features.hooks" {
		if err := claudepaths.Apply(layout, cfg); err != nil {
			return err
		}
		if wrote, err := upkeep.ApplyHooks(layout, cfg); err != nil {
			return err
		} else if wrote {
			verb := "added cwm's hooks to"
			if !cfg.Features.Hooks {
				verb = "removed cwm's hooks from"
			}
			fmt.Fprintf(e.Stdout, "  %s %s\n", verb, claudepaths.Tilde(workspace.SettingsPath(layout)))
		}
	}
	if key == "features.open_dashboard_at_login" && !autostartKey {
		fmt.Fprintln(e.Stdout, "  takes effect once features.autostart is on")
	}
	if autostartKey {
		if err := applyAutostart(e, cfg, false); err != nil {
			return fmt.Errorf("%s is saved, but the autostart change failed: %w", key, err)
		}
	}
	if key == "features.private_sync" {
		// Best effort: Syncthing may not be running; the next
		// `cwm pair` or agent tick applies it then.
		if cl, err := connectSyncClient(false); err != nil {
			fmt.Fprintln(e.Stdout, "  Syncthing not reachable now; the options are applied on the next cwm pair or agent start")
		} else if err := applyPrivateSync(e, cl, true); err != nil {
			return err
		}
	}
	return nil
}
