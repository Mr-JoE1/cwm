package doctor

// v0.8 health checks (SPEC-V0.8 "Health checks"): where claude reads
// from, whether the generated .stignore, the MCP bridge and cwm's hooks
// are current, conflict copies, the default account's vault entry and
// top-level items Claude Code added that stay local.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/mcp"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/upkeep"
	"github.com/Mr-JoE1/cwm/internal/workspace"
)

// FixCommand is the one command the fix hints point to: one agent
// maintenance pass now (the agent runs the same pass every minute).
const FixCommand = "cwm doctor --fix"

// EnvHit is one place a persistent CLAUDE_CONFIG_DIR is set for the user.
type EnvHit struct {
	// Where is a profile path with ~ ("~/.bashrc:12"), or "Windows user
	// environment (HKCU\Environment)" / "launchd user environment".
	Where string
	// Line is the offending line as written (home dir shown as ~).
	Line string
	// Fix is the command that removes it.
	Fix string
}

// profileNames are the shell profiles a login shell or an interactive
// shell reads; the spec's four plus the bash/zsh files that do the same.
var profileNames = []string{".bashrc", ".profile", ".zshrc", ".zprofile", ".bash_profile", ".zshenv"}

// configDirLineRe matches a line that sets CLAUDE_CONFIG_DIR in sh, csh or
// fish syntax; a commented-out line does not.
var configDirLineRe = regexp.MustCompile(`^\s*(?:export\s+CLAUDE_CONFIG_DIR=|CLAUDE_CONFIG_DIR=|setenv\s+CLAUDE_CONFIG_DIR\s|set\s+(?:-\w+\s+)*CLAUDE_CONFIG_DIR\s)`)

// Seams: the Windows registry and launchd probes. Tests replace both, so
// no test ever runs reg or launchctl.
var (
	regQueryUserEnv = func(name string) (string, bool) {
		out, err := exec.Command("reg", "query", `HKCU\Environment`, "/v", name).Output()
		if err != nil {
			return "", false
		}
		for line := range strings.SplitSeq(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) >= 3 && strings.EqualFold(f[0], name) {
				return strings.Join(f[2:], " "), true
			}
		}
		return "", false
	}
	launchctlGetenv = func(name string) (string, bool) {
		out, err := exec.Command("launchctl", "getenv", name).Output()
		v := strings.TrimSpace(string(out))
		return v, err == nil && v != ""
	}
)

// PersistentConfigDir lists every place CLAUDE_CONFIG_DIR is set for the
// user persistently on goos: the shell profiles, the Windows user
// environment, launchd.
func PersistentConfigDir(goos string) []EnvHit {
	var hits []EnvHit
	home, err := os.UserHomeDir()
	if err == nil {
		for _, name := range profileNames {
			data, err := os.ReadFile(filepath.Join(home, name))
			if err != nil {
				continue
			}
			for i, line := range strings.Split(string(data), "\n") {
				line = strings.TrimRight(line, "\r")
				if !configDirLineRe.MatchString(line) {
					continue
				}
				n := strconv.Itoa(i + 1)
				hits = append(hits, EnvHit{
					Where: "~/" + name + ":" + n,
					Line:  strings.TrimSpace(strings.ReplaceAll(line, home, "~")),
					Fix:   fmt.Sprintf("sed -i.cwm-bak '%sd' ~/%s", n, name),
				})
			}
		}
	}
	switch goos {
	case "windows":
		if v, ok := regQueryUserEnv(claudepaths.EnvConfigDir); ok {
			if home != "" {
				v = strings.ReplaceAll(v, home, "~")
			}
			hits = append(hits, EnvHit{
				Where: `Windows user environment (HKCU\Environment)`,
				Line:  claudepaths.EnvConfigDir + "=" + v,
				Fix:   `powershell -NoProfile -Command "[Environment]::SetEnvironmentVariable('CLAUDE_CONFIG_DIR', $null, 'User')"`,
			})
		}
	case "darwin":
		if v, ok := launchctlGetenv(claudepaths.EnvConfigDir); ok {
			if home != "" {
				v = strings.ReplaceAll(v, home, "~")
			}
			hits = append(hits, EnvHit{
				Where: "launchd user environment",
				Line:  claudepaths.EnvConfigDir + "=" + v,
				Fix:   "launchctl unsetenv CLAUDE_CONFIG_DIR (and remove the LaunchAgent or profile that sets it)",
			})
		}
	}
	return hits
}

// loadConfig reads root's config.toml; nil when it cannot be read.
func loadConfig(root string) *config.Config {
	cfg, err := config.Load(filepath.Join(root, "config.toml"))
	if err != nil {
		return nil
	}
	return cfg
}

// checkReadsFrom is "claude reads from": the mode, the resolved live
// paths, and in capture mode a FAIL for any persistent CLAUDE_CONFIG_DIR
// (it would point plain claude away from the workspace).
func checkReadsFrom(r *checkList, l *storage.Layout, cfg *config.Config) {
	if cfg == nil {
		cfg = config.Default()
	}
	live, err := claudepaths.LiveFor(l, cfg, "")
	if err != nil {
		r.warn("claude-reads-from", "cannot resolve the live paths: %v", err)
		return
	}
	creds := accounts.DefaultStore().Location(live)
	if hostOS != "darwin" {
		creds = claudepaths.Tilde(live.Credentials)
	}
	lines := []string{
		"workspace root: " + claudepaths.Tilde(live.Root),
		".claude.json:   " + claudepaths.Tilde(live.ClaudeJSON),
		"credentials:    " + creds,
	}
	mode := cfg.EffectiveMode()
	var hits []EnvHit
	if mode == config.ModeCapture {
		// Isolated mode does not care: cwm sets the variable per launch.
		hits = PersistentConfigDir(hostOS)
	}
	for _, h := range hits {
		lines = append(lines, h.Where+": "+h.Line, "  remove it: "+h.Fix)
	}
	switch {
	case mode == config.ModeIsolated:
		r.ok("claude-reads-from", "isolated mode: cwm-launched sessions read %s through CLAUDE_CONFIG_DIR; plain claude keeps ~/.claude", claudepaths.Tilde(live.Root))
	case len(hits) == 0:
		r.ok("claude-reads-from", "capture mode: every claude on this machine reads %s (no persistent CLAUDE_CONFIG_DIR)", claudepaths.Tilde(live.Root))
	case cfg.NeedsMigration():
		r.warn("claude-reads-from", "CLAUDE_CONFIG_DIR is set persistently (%d place(s)); after the migration to capture mode remove it so plain claude reads %s", len(hits), claudepaths.Tilde(live.Root))
	default:
		r.fail("claude-reads-from", "capture mode, but CLAUDE_CONFIG_DIR is set persistently (%d place(s)): plain claude and IDEs read that dir instead of %s — remove the line(s) below, then open a new terminal", len(hits), claudepaths.Tilde(live.Root))
	}
	r.last().Lines = lines
}

func (c *checkList) last() *Check { return &(*c)[len(*c)-1] }

// checkMigration warns about a config written before v0.8: nothing
// moves until the user reviewed the plan.
func checkMigration(r *checkList, cfg *config.Config) {
	if cfg != nil && cfg.NeedsMigration() {
		r.warn("migration", "cwm now captures all Claude Code activity — this config predates v0.8; review the migration: cwm migrate --dry-run")
	}
}

// checkStIgnore: the root's .stignore must be the allow-list generated
// from the [sync] switches.
func checkStIgnore(r *checkList, l *storage.Layout, cfg *config.Config) {
	p := claudepaths.Tilde(workspace.StIgnorePath(l))
	switch {
	case !fileExists(workspace.StIgnorePath(l)):
		r.warn("stignore", ".stignore missing at %s — sync would share everything; fix: %s", p, fixHint(cfg))
	case cfg == nil:
		r.ok("stignore", ".stignore present (config.toml unreadable: not compared with [sync])")
	case !workspace.StIgnoreCurrent(l, cfg):
		r.warn("stignore", ".stignore does not match the [sync] switches — fix: %s", fixHint(cfg))
	default:
		n := 0
		for _, k := range config.SyncKeys() {
			if cfg.SyncEnabled(k) {
				n++
			}
		}
		r.ok("stignore", ".stignore matches the [sync] switches (%d of %d items sync)", n, len(config.SyncKeys()))
	}
}

// fixHint is the fix for what upkeep maintains: a pre-v0.8 config is
// migrated first (upkeep does not touch it before).
func fixHint(cfg *config.Config) string {
	if cfg != nil && cfg.NeedsMigration() {
		return "review the migration first: cwm migrate --dry-run"
	}
	return FixCommand
}

// checkBridge: every live .claude.json carries the workspace MCP servers.
// Shown once mcp.json exists (nothing to bridge before).
func checkBridge(r *checkList, l *storage.Layout, cfg *config.Config) {
	if cfg == nil || !fileExists(filepath.Join(l.WorkspaceDir(), "mcp.json")) {
		return
	}
	targets, err := upkeep.Targets(l, cfg)
	if err != nil {
		r.warn("mcp-bridge", "cannot resolve the .claude.json files: %v", err)
		return
	}
	ok, off, err := mcp.InSync(l, targets)
	switch {
	case err != nil:
		r.warn("mcp-bridge", "cannot compare MCP servers: %v", err)
	case !ok:
		r.warn("mcp-bridge", "MCP servers in .claude.json differ from the workspace mcp.json for: %s — fix: %s", strings.Join(off, ", "), fixHint(cfg))
	default:
		r.ok("mcp-bridge", "MCP bridge in sync (%d .claude.json file(s))", len(targets))
	}
}

// checkHooks: cwm's hook entries are in the workspace settings.json when
// features.hooks is on (and absent when off).
func checkHooks(r *checkList, l *storage.Layout, cfg *config.Config) {
	if cfg == nil || cfg.NeedsMigration() {
		return
	}
	// Any command running `… hook session-start|session-end` counts: the
	// settings check above already verifies the binary it names.
	data, err := os.ReadFile(workspace.SettingsPath(l))
	if err != nil {
		return // the settings check already says the hooks are added next tick
	}
	raw := string(data)
	wired := workspace.HooksWired(l) ||
		(strings.Contains(raw, "hook session-start") && strings.Contains(raw, "hook session-end"))
	switch {
	case cfg.Features.Hooks && !wired:
		r.warn("cwm-hooks", "cwm's session hooks are not in %s (features.hooks is on) — fix: %s", claudepaths.Tilde(workspace.SettingsPath(l)), FixCommand)
	case cfg.Features.Hooks:
		r.ok("cwm-hooks", "cwm's session hooks are wired in settings.json")
	case wired:
		r.warn("cwm-hooks", "features.hooks is off but settings.json still wires cwm's hooks — fix: %s", FixCommand)
	default:
		r.ok("cwm-hooks", "cwm's session hooks are off (features.hooks)")
	}
}

// checkConflicts: Syncthing conflict copies of synced text files wait for
// the user ("2 versions of CLAUDE.md — pick one").
func checkConflicts(r *checkList, l *storage.Layout, cfg *config.Config) {
	if cfg == nil {
		cfg = config.Default()
	}
	cs, err := workspace.FindConflicts(l.WorkspaceDir(), cfg)
	if err != nil {
		r.warn("conflicts", "cannot look for conflict copies: %v", err)
		return
	}
	if len(cs) == 0 {
		r.ok("conflicts", "no sync conflict copies")
		return
	}
	parts := make([]string, len(cs))
	var lines []string
	for i, c := range cs {
		n := len(c.Copies)
		if c.Exists {
			n++
		}
		parts[i] = fmt.Sprintf("%d versions of %s", n, c.Path)
		choices := "current|<copy>"
		if c.Mergeable {
			choices = "current|merge|<copy>"
		}
		lines = append(lines, "cwm conflicts resolve "+c.Path+" "+choices)
	}
	r.warn("conflicts", "%s — pick one (dashboard Health, or the command below)", strings.Join(parts, "; "))
	r.last().Lines = lines
}

// checkVault: the default account's login is saved in its vault entry, so
// a switch can always come back to it.
func checkVault(r *checkList, l *storage.Layout, cfg *config.Config) {
	if cfg == nil {
		return
	}
	if cfg.DefaultAccount == "" {
		if len(cfg.Accounts) == 0 {
			r.ok("vault", "no accounts yet (plain claude keeps its own login)")
		} else {
			r.warn("vault", "no default account set — pick one: cwm account use <name>")
		}
		return
	}
	mode := cfg.EffectiveMode()
	store := accounts.DefaultStore()
	vault, err := accounts.VaultPaths(l, mode, cfg.DefaultAccount)
	if err != nil {
		r.warn("vault", "cannot resolve the vault entry: %v", err)
		return
	}
	live, err := claudepaths.Live(l, mode, "", "")
	if vaultHas(store, vault) {
		if err == nil && !vaultHas(store, live) {
			r.warn("vault", "plain claude is not logged in: %s's login is only in the vault — fix: %s", cfg.DefaultAccount, FixCommand)
			return
		}
		r.ok("vault", "vault holds the login of the default account %s", cfg.DefaultAccount)
		return
	}
	if err == nil && vaultHas(store, live) {
		r.warn("vault", "the vault has no saved copy of %s's login (it is only live) — fix: %s", cfg.DefaultAccount, FixCommand)
		return
	}
	r.warn("vault", "the default account %s has no login — log in again: cwm account login %s", cfg.DefaultAccount, cfg.DefaultAccount)
}

// vaultHas is accounts.Has; a seam so tests never query a Keychain.
var vaultHas = accounts.Has

// knownTopLevel are the ~/.claude items cwm knows: the allow-list, the
// never-synced list of the spec, Syncthing's and cwm's own files and
// Claude Code's machine-local extras.
var knownTopLevel = func() map[string]bool {
	m := map[string]bool{}
	for _, it := range config.SyncItems {
		m[it.Path] = true
	}
	for _, n := range []string{
		".credentials.json", ".claude.json", "cache", "debug", "telemetry", "statsig",
		"shell-snapshots", "session-env", "paste-cache", "file-history", "backups",
		"downloads", "ide", "chrome", "todos", "tasks", "sessions", "worktrees", "logs",
		".stignore", ".stfolder", ".stversions", ".stfolder.removed", "hooks",
		"settings.local.json", "local", "plans", "keybindings.json", "statsig.json",
		".DS_Store", "Thumbs.db",
	} {
		m[n] = true
	}
	return m
}()

// checkUnknownItems is INFO only (an ok line): top-level items of
// ~/.claude that are on neither list stay on this machine.
func checkUnknownItems(r *checkList, l *storage.Layout, cfg *config.Config) {
	if cfg == nil || cfg.EffectiveMode() != config.ModeCapture {
		return
	}
	entries, err := os.ReadDir(l.WorkspaceDir())
	if err != nil {
		return
	}
	var unknown []string
	for _, e := range entries {
		n := e.Name()
		if knownTopLevel[n] || strings.HasSuffix(n, ".lock") || strings.Contains(n, ".sync-conflict-") ||
			strings.HasPrefix(n, ".claude.json") || strings.HasPrefix(n, ".syncthing.") || strings.HasSuffix(n, ".tmp") {
			continue
		}
		unknown = append(unknown, n)
	}
	if len(unknown) == 0 {
		return
	}
	sort.Strings(unknown)
	r.ok("unknown-items", "Claude added %s; it stays local (not on the sync allow-list)", strings.Join(unknown, ", "))
}
