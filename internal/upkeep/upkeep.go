// Package upkeep is the one place that keeps a v0.8 workspace consistent
// (D87, D89): the allow-list .stignore, the history.jsonl conflict merge,
// the login-only dir links, the MCP bridge into every live .claude.json
// and cwm's hooks in settings.json. The agent runs Tick once a minute;
// every cwm command that launches claude runs BeforeLaunch.
package upkeep

import (
	"fmt"
	"os"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/mcp"
	"github.com/Mr-JoE1/cwm/internal/pathmirror"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/workspace"
)

// Report says what one run changed.
type Report struct {
	Skipped       string                  // why nothing ran ("" = it ran)
	StIgnore      bool                    // the .stignore was regenerated
	HistoryMerged int                     // history.jsonl conflict copies merged
	TextConflicts int                     // other conflict files waiting for a choice
	Relinked      []string                // "<account>: <item>" links repaired
	Bridge        mcp.BridgeReport        // the MCP bridge run
	Hooks         bool                    // settings.json hooks rewritten
	Mirror        pathmirror.MirrorReport // cross-device path mirroring (D91)
	Errors        []string                // non-fatal step failures
}

// Ready reports whether upkeep may touch the workspace root: the config
// records a v0.8 mode (never before the user reviewed a migration) and
// the root exists (upkeep never creates ~/.claude).
func Ready(l *storage.Layout, cfg *config.Config) string {
	if cfg.NeedsMigration() {
		return "config predates v0.8 (review: cwm migrate --dry-run)"
	}
	if info, err := os.Stat(l.WorkspaceDir()); err != nil || !info.IsDir() {
		return "workspace root " + claudepaths.Tilde(l.WorkspaceDir()) + " does not exist"
	}
	return ""
}

// Targets are the live .claude.json files the MCP bridge keeps in sync:
// the default account's and every other account's login-only dir that
// exists.
func Targets(l *storage.Layout, cfg *config.Config) ([]mcp.BridgeTarget, error) {
	def, err := claudepaths.LiveFor(l, cfg, "")
	if err != nil {
		return nil, err
	}
	out := []mcp.BridgeTarget{{Name: "default", Path: def.ClaudeJSON}}
	for _, a := range cfg.Accounts {
		if a.Name == cfg.DefaultAccount {
			continue
		}
		if info, err := os.Stat(claudepaths.LoginDir(l, a.Name)); err != nil || !info.IsDir() {
			continue
		}
		p, err := claudepaths.LiveFor(l, cfg, a.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, mcp.BridgeTarget{Name: a.Name, Path: p.ClaudeJSON})
	}
	return out, nil
}

// Tick is the agent's periodic run. l must already be resolved for cfg.
// Each step is independent: one failing is reported and the rest still
// run.
func Tick(l *storage.Layout, cfg *config.Config, now time.Time) Report {
	var rep Report
	if rep.Skipped = Ready(l, cfg); rep.Skipped != "" {
		return rep
	}
	fail := func(step string, err error) {
		rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", step, err))
	}
	var err error
	if rep.StIgnore, err = workspace.WriteStIgnore(l, cfg); err != nil {
		fail("stignore", err)
	}
	if cfg.SyncEnabled("history") {
		if rep.HistoryMerged, err = workspace.MergeHistoryConflicts(l, now); err != nil {
			fail("history merge", err)
		}
	}
	if cs, err := workspace.FindConflicts(l.WorkspaceDir(), cfg); err != nil {
		fail("conflicts", err)
	} else {
		rep.TextConflicts = len(cs)
	}
	for _, a := range cfg.Accounts {
		if a.Name == cfg.DefaultAccount {
			continue
		}
		if info, err := os.Stat(claudepaths.LoginDir(l, a.Name)); err != nil || !info.IsDir() {
			continue
		}
		res, err := workspace.EnsureLoginDir(l, cfg, a.Name)
		if err != nil {
			fail("login dir "+a.Name, err)
			continue
		}
		for _, it := range res.Relinked {
			rep.Relinked = append(rep.Relinked, a.Name+": "+it)
		}
	}
	rep.Bridge, rep.Hooks = bridgeAndHooks(l, cfg, fail)
	if MirrorEnabled(cfg) {
		rep.Mirror = mirrorer.Tick(pathmirror.NewEnv(l), now)
		for _, e := range rep.Mirror.Errors {
			fail("path mirror", fmt.Errorf("%s", e))
		}
	}
	return rep
}

// mirrorer keeps the agent's mirroring signatures between ticks; a full
// pass at most every 30 seconds, and only sessions whose copies changed
// size or mtime are read.
var mirrorer = &pathmirror.Mirrorer{MinInterval: 30 * time.Second}

// MirrorEnabled reports whether path mirroring runs: capture mode (in
// isolated mode cwm pins the folder name, D86) with features.path_mirror,
// on a config that records a v0.8 mode (a pre-v0.8 workspace still has
// pinned folders until it is migrated).
func MirrorEnabled(cfg *config.Config) bool {
	return !cfg.NeedsMigration() && cfg.Mode == config.ModeCapture && cfg.Features.PathMirror
}

func bridgeAndHooks(l *storage.Layout, cfg *config.Config, fail func(string, error)) (mcp.BridgeReport, bool) {
	var br mcp.BridgeReport
	targets, err := Targets(l, cfg)
	if err != nil {
		fail("mcp bridge", err)
	} else if br, err = mcp.Bridge(l, targets); err != nil {
		fail("mcp bridge", err)
	}
	hooks := false
	// D89: with features.hooks off the tick leaves settings.json alone
	// (`cwm config set features.hooks off` removed cwm's entries once);
	// a device that still has them on would otherwise fight over the
	// synced file.
	if cfg.Features.Hooks {
		if hooks, err = workspace.EnsureHooks(l, true); err != nil {
			fail("hooks", err)
		}
	}
	return br, hooks
}

// LaunchResult is what BeforeLaunch did for one launch.
type LaunchResult struct {
	Report
	// MCPFallback asks the launcher to pass --mcp-config workspace/mcp.json
	// because the account's .claude.json could not carry the servers yet
	// (it does not exist before claude's first start, or the bridge
	// failed, or upkeep did not run).
	MCPFallback bool
}

// BeforeLaunch runs the MCP bridge and the hook merge before claude
// starts as account ("" = default), so the session sees the workspace
// servers and cwm's hooks. It never fails the launch: problems are
// returned as Errors for the launcher to print.
func BeforeLaunch(l *storage.Layout, cfg *config.Config, account string) LaunchResult {
	var res LaunchResult
	fail := func(step string, err error) {
		res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", step, err))
	}
	if res.Skipped = Ready(l, cfg); res.Skipped != "" {
		res.MCPFallback = true
		return res
	}
	res.Bridge, res.Hooks = bridgeAndHooks(l, cfg, fail)
	live, err := claudepaths.LiveFor(l, cfg, account)
	if err != nil {
		fail("resolve", err)
		res.MCPFallback = true
		return res
	}
	if _, err := os.Stat(live.ClaudeJSON); err != nil || len(res.Errors) > 0 {
		res.MCPFallback = true
	}
	return res
}

// ApplyHooks is `cwm config set features.hooks on|off`: add or remove
// cwm's entries now (the tick only ever adds).
func ApplyHooks(l *storage.Layout, cfg *config.Config) (bool, error) {
	if reason := Ready(l, cfg); reason != "" {
		return false, nil
	}
	return workspace.EnsureHooks(l, cfg.Features.Hooks)
}
