package config

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Workspace modes (D76).
const (
	// ModeCapture makes the standard Claude config dir (~/.claude) the
	// workspace: everything Claude Code does on the machine is captured.
	ModeCapture = "capture"
	// ModeIsolated keeps the workspace in ~/.cwm/workspace; only
	// cwm-launched sessions use it.
	ModeIsolated = "isolated"
)

// ValidMode reports whether m is a workspace mode.
func ValidMode(m string) bool { return m == ModeCapture || m == ModeIsolated }

// SyncItem is one entry of the synced-item allow-list (SPEC-V0.8): the
// `sync.<key>` switch and the workspace-root paths it covers. Dir items
// are directories; the others are single files.
type SyncItem struct {
	Key  string
	Path string
	Dir  bool
	// Help is the one-line consequence the dashboard and `cwm config list`
	// show next to the switch.
	Help string
}

// SyncItems is the allow-list in spec order. repos.tsv rides with
// sessions: it is the manifest `cwm open` needs to clone a session's repo
// on another device (D85); so does session-repos/, the per-device
// session -> repo record cross-device resume maps paths with (D91). Everything not listed here never syncs.
var SyncItems = []SyncItem{
	{Key: "sessions", Path: "projects", Dir: true, Help: "conversation transcripts, resumable on every device"},
	{Key: "sessions", Path: "repos.tsv", Help: "the repo manifest cwm open uses to clone a session's project"},
	{Key: "sessions", Path: "session-repos", Dir: true, Help: "which repo each session ran in, per device (cross-device resume)"},
	{Key: "history", Path: "history.jsonl", Help: "prompt history (up-arrow) shared between devices"},
	{Key: "memory", Path: "CLAUDE.md", Help: "your global memory file follows you"},
	{Key: "skills", Path: "skills", Dir: true, Help: "personal skills are available everywhere"},
	{Key: "agents", Path: "agents", Dir: true, Help: "personal subagents are available everywhere"},
	{Key: "commands", Path: "commands", Dir: true, Help: "personal slash commands are available everywhere"},
	{Key: "rules", Path: "rules", Dir: true, Help: "personal rules are available everywhere"},
	{Key: "output_styles", Path: "output-styles", Dir: true, Help: "output styles are available everywhere"},
	{Key: "scheduled_tasks", Path: "scheduled-tasks", Dir: true, Help: "scheduled tasks are shared between devices"},
	{Key: "plugins", Path: "plugins", Dir: true, Help: "installed plugins (never their caches) are shared"},
	{Key: "settings", Path: "settings.json", Help: "off: each machine keeps its own settings.json (cwm still adds its hooks)"},
	{Key: "mcp", Path: "mcp.json", Help: "MCP servers (bridged into every account's .claude.json)"},
}

// SyncKeys returns the distinct sync switch keys in spec order.
func SyncKeys() []string {
	var out []string
	for _, it := range SyncItems {
		if !slices.Contains(out, it.Key) {
			out = append(out, it.Key)
		}
	}
	return out
}

func syncHelp(key string) string {
	for _, it := range SyncItems {
		if it.Key == key {
			return it.Help
		}
	}
	return ""
}

// DefaultSync has every allow-list item on.
func DefaultSync() map[string]bool {
	m := map[string]bool{}
	for _, k := range SyncKeys() {
		m[k] = true
	}
	return m
}

// SyncEnabled reports sync.<key>; an unknown key is off.
func (c *Config) SyncEnabled(key string) bool {
	if v, ok := c.Sync[key]; ok {
		return v
	}
	return slices.Contains(SyncKeys(), key)
}

// Update-check settings.
const (
	UpdateCheckManual = "manual"
	UpdateCheckOff    = "off"
)

// Features is the [features] table. Every switch is honoured (SPEC-V0.8
// Modes): a switch that did nothing would be a lie in the Settings tab.
type Features struct {
	Hooks                bool   `toml:"hooks"`
	HandoffPush          bool   `toml:"handoff_push"`
	Stats                bool   `toml:"stats"`
	Autostart            bool   `toml:"autostart"`
	OpenDashboardAtLogin bool   `toml:"open_dashboard_at_login"`
	PrivateSync          bool   `toml:"private_sync"`
	UpdateCheck          string `toml:"update_check"`
	// PathMirror keeps sessions from other devices resumable here when the
	// same repo sits at a different path (D91); capture mode only.
	PathMirror bool `toml:"path_mirror"`
}

// DefaultFeatures matches what v0.7 did without any switch: hooks wired,
// handoff pushed, stats scanned, the agent autostarted by the installer,
// no browser at login, public Syncthing discovery, a manual update check;
// v0.8 adds path mirroring, on.
func DefaultFeatures() Features {
	return Features{Hooks: true, HandoffPush: true, Stats: true, Autostart: true, UpdateCheck: UpdateCheckManual, PathMirror: true}
}

// Tabs are the dashboard tab ids, in order. v0.9 (D105): Home replaced
// Stats, whose content is Home's Activity section.
var Tabs = []string{"home", "sessions", "projects", "devices", "handoff", "health", "accounts", "settings"}

// DisplaySchema is the [display] layout version: 9 = the v0.9 tabs.
const DisplaySchema = 9

// Display is the [display] table.
type Display struct {
	HiddenTabs []string `toml:"hidden_tabs"`
	DefaultTab string   `toml:"default_tab"`
	// HomeActivity shows Home's Activity section (the former Stats tab).
	HomeActivity bool `toml:"home_activity"`
	// Schema records that the v0.9 migration ran (D105); 0 = an older file.
	Schema int `toml:"schema,omitempty"`
}

// Launch is the [launch] table (D113): which Claude the dashboard's
// Resume and Start buttons, `cwm sessions resume` and `cwm run` open.
type Launch struct {
	Claude string `toml:"claude"`
}

// The launch.claude values (internal/claudeopen implements them).
const (
	LaunchAuto    = "auto"
	LaunchCLI     = "cli"
	LaunchVSCode  = "vscode"
	LaunchDesktop = "desktop"
)

// LaunchChoices are the launch.claude values in display order.
var LaunchChoices = []string{LaunchAuto, LaunchCLI, LaunchVSCode, LaunchDesktop}

// DefaultDisplay shows every tab and opens on Home.
func DefaultDisplay() Display {
	return Display{HiddenTabs: []string{}, DefaultTab: "home", HomeActivity: true, Schema: DisplaySchema}
}

// migrateDisplay maps a pre-v0.9 [display] table onto the v0.9 tabs (D105):
// "stats" is no longer a tab, so hiding it now hides Home's Activity
// section, and a default tab of "stats" (or "sessions", the v0.8 default
// that every v0.8 init wrote) becomes "home". It runs once per file: Save
// writes the schema marker, and a later explicit choice of "sessions"
// sticks. It reports whether anything changed.
func (d *Display) migrateDisplay() bool {
	if d.Schema >= DisplaySchema {
		return false
	}
	d.Schema = DisplaySchema
	if i := slices.Index(d.HiddenTabs, "stats"); i >= 0 {
		d.HiddenTabs = slices.Delete(slices.Clone(d.HiddenTabs), i, i+1)
		d.HomeActivity = false
	}
	if d.DefaultTab == "stats" || d.DefaultTab == "sessions" || d.DefaultTab == "" {
		d.DefaultTab = "home"
	}
	if slices.Contains(d.HiddenTabs, "home") && d.DefaultTab == "home" {
		d.HiddenTabs = slices.DeleteFunc(slices.Clone(d.HiddenTabs), func(t string) bool { return t == "home" })
	}
	return true
}

// NeedsMigration reports a config written before v0.8 (no mode key):
// it behaves as capture mode, but its data may still sit in the v0.7
// layout until `cwm migrate` records a mode (D78).
func (c *Config) NeedsMigration() bool { return c.Mode == "" }

// EffectiveMode is the mode the resolver uses: Mode, or capture for a
// config that predates v0.8.
func (c *Config) EffectiveMode() string {
	if ValidMode(c.Mode) {
		return c.Mode
	}
	return ModeCapture
}

// normalize fills defaults for missing switches and rejects invalid
// values; Load and Save both run it.
func (c *Config) normalize() error {
	if c.Mode != "" && !ValidMode(c.Mode) {
		return fmt.Errorf("invalid mode %q (want capture or isolated)", c.Mode)
	}
	if c.Sync == nil {
		c.Sync = map[string]bool{}
	}
	for k := range c.Sync {
		if !slices.Contains(SyncKeys(), k) {
			return fmt.Errorf("unknown [sync] key %q (want one of %s)", k, strings.Join(SyncKeys(), ", "))
		}
	}
	for _, k := range SyncKeys() {
		if _, ok := c.Sync[k]; !ok {
			c.Sync[k] = true
		}
	}
	switch c.Features.UpdateCheck {
	case "":
		c.Features.UpdateCheck = UpdateCheckManual
	case UpdateCheckManual, UpdateCheckOff:
	default:
		return fmt.Errorf("invalid features.update_check %q (want manual or off)", c.Features.UpdateCheck)
	}
	if c.Launch.Claude == "" {
		c.Launch.Claude = LaunchAuto
	}
	if !slices.Contains(LaunchChoices, c.Launch.Claude) {
		return fmt.Errorf("invalid launch.claude %q (want %s)", c.Launch.Claude, strings.Join(LaunchChoices, ", "))
	}
	if c.Display.HiddenTabs == nil {
		c.Display.HiddenTabs = []string{}
	}
	if c.Display.migrateDisplay() {
		c.displayMigrated = true
	}
	for _, t := range c.Display.HiddenTabs {
		if !slices.Contains(Tabs, t) {
			return fmt.Errorf("unknown display.hidden_tabs entry %q (want one of %s)", t, strings.Join(Tabs, ", "))
		}
		if t == "settings" {
			// Hiding the only place that can unhide tabs would strand the
			// dashboard user; the CLI could still fix it, but say so now.
			return fmt.Errorf("display.hidden_tabs cannot hide settings")
		}
	}
	if c.Display.DefaultTab == "" {
		c.Display.DefaultTab = "home"
	}
	if !slices.Contains(Tabs, c.Display.DefaultTab) {
		return fmt.Errorf("unknown display.default_tab %q (want one of %s)", c.Display.DefaultTab, strings.Join(Tabs, ", "))
	}
	return nil
}

// Key kinds of the settings registry.
const (
	KindBool   = "bool"
	KindEnum   = "enum"
	KindList   = "list"
	KindString = "string"
)

// KeyInfo describes one `cwm config` key.
type KeyInfo struct {
	Name    string   `json:"key"`
	Kind    string   `json:"kind"`
	Choices []string `json:"choices,omitempty"`
	Default string   `json:"default"`
	Help    string   `json:"help"`
}

// Keys lists every settable key in display order: mode, sync.*,
// features.*, display.*, link_mode.
func Keys() []KeyInfo {
	keys := []KeyInfo{{
		Name: "mode", Kind: KindEnum, Choices: []string{ModeCapture, ModeIsolated}, Default: ModeCapture,
		Help: "capture: ~/.claude is the workspace and every claude run is captured; isolated: only cwm-launched sessions use ~/.cwm/workspace",
	}}
	for _, k := range SyncKeys() {
		keys = append(keys, KeyInfo{Name: "sync." + k, Kind: KindBool, Default: "true", Help: syncHelp(k)})
	}
	keys = append(keys,
		KeyInfo{Name: "features.hooks", Kind: KindBool, Default: "true", Help: "cwm's session-start/end hooks in settings.json (repo manifest, handoff push)"},
		KeyInfo{Name: "features.handoff_push", Kind: KindBool, Default: "true", Help: "the session-end hook pushes uncommitted work to sync/<host>"},
		KeyInfo{Name: "features.stats", Kind: KindBool, Default: "true", Help: "scan transcripts for Home's Activity section (the former Stats tab); off hides it and scans nothing"},
		KeyInfo{Name: "features.autostart", Kind: KindBool, Default: "true", Help: "start the cwm agent at login (installs or removes the per-user service)"},
		KeyInfo{Name: "features.open_dashboard_at_login", Kind: KindBool, Default: "false", Help: "open the dashboard in the browser when the agent starts at login"},
		KeyInfo{Name: "features.private_sync", Kind: KindBool, Default: "false", Help: "Syncthing without global discovery or relays; pair devices by address"},
		KeyInfo{Name: "features.update_check", Kind: KindEnum, Choices: []string{UpdateCheckManual, UpdateCheckOff}, Default: UpdateCheckManual, Help: "manual: a check-for-updates button in About; off: no button, never asks"},
		KeyInfo{Name: "features.path_mirror", Kind: KindBool, Default: "true", Help: "capture mode: copy sessions from other devices under this machine's folder for the same repo, so plain claude --resume finds them"},
		KeyInfo{Name: "display.hidden_tabs", Kind: KindList, Choices: tabsExceptSettings(), Default: "", Help: "tabs the dashboard hides (a comma-separated list from the CLI)"},
		KeyInfo{Name: "display.default_tab", Kind: KindEnum, Choices: Tabs, Default: "home", Help: "the tab the dashboard opens on"},
		KeyInfo{Name: "display.home_activity", Kind: KindBool, Default: "true", Help: "show the Activity section (sessions, tokens, heatmap) on Home"},
		KeyInfo{Name: "launch.claude", Kind: KindEnum, Choices: LaunchChoices, Default: LaunchAuto, Help: "which Claude Resume and Start open: auto (the claude doctor picks, in a terminal), cli (a standalone install only), vscode (the editor's Claude Code tab), desktop (the Claude app)"},
		KeyInfo{Name: "link_mode", Kind: KindEnum, Choices: []string{LinkModeAuto, LinkModeSymlink, LinkModeJunction, LinkModeCopy}, Default: LinkModeAuto, Help: "how login-only account dirs link shared items"},
	)
	return keys
}

func tabsExceptSettings() []string {
	out := []string{}
	for _, t := range Tabs {
		if t != "settings" {
			out = append(out, t)
		}
	}
	return out
}

// LookupKey finds a registry entry.
func LookupKey(name string) (KeyInfo, bool) {
	for _, k := range Keys() {
		if k.Name == name {
			return k, true
		}
	}
	return KeyInfo{}, false
}

// Get renders a key's current value as `cwm config get` prints it.
func (c *Config) Get(name string) (string, error) {
	if _, ok := LookupKey(name); !ok {
		return "", fmt.Errorf("config: unknown key %q (see: cwm config list)", name)
	}
	f := c.Features
	switch name {
	case "mode":
		return c.EffectiveMode(), nil
	case "features.hooks":
		return strconv.FormatBool(f.Hooks), nil
	case "features.handoff_push":
		return strconv.FormatBool(f.HandoffPush), nil
	case "features.stats":
		return strconv.FormatBool(f.Stats), nil
	case "features.autostart":
		return strconv.FormatBool(f.Autostart), nil
	case "features.open_dashboard_at_login":
		return strconv.FormatBool(f.OpenDashboardAtLogin), nil
	case "features.private_sync":
		return strconv.FormatBool(f.PrivateSync), nil
	case "features.update_check":
		return f.UpdateCheck, nil
	case "features.path_mirror":
		return strconv.FormatBool(f.PathMirror), nil
	case "display.hidden_tabs":
		return strings.Join(c.Display.HiddenTabs, ","), nil
	case "display.default_tab":
		return c.Display.DefaultTab, nil
	case "display.home_activity":
		return strconv.FormatBool(c.Display.HomeActivity), nil
	case "launch.claude":
		return c.Launch.Claude, nil
	case "link_mode":
		return c.LinkMode, nil
	}
	if k, ok := strings.CutPrefix(name, "sync."); ok {
		return strconv.FormatBool(c.SyncEnabled(k)), nil
	}
	return "", fmt.Errorf("config: unknown key %q", name)
}

// Set validates and applies one key. mode is settable here for the
// dashboard's apply step; the CLI routes `cwm config set mode` through
// `cwm mode set`, which migrates first. The receiver is unchanged when
// the value is rejected.
func (c *Config) Set(name, value string) error {
	k, ok := LookupKey(name)
	if !ok {
		return fmt.Errorf("config: unknown key %q (see: cwm config list)", name)
	}
	value = strings.TrimSpace(value)
	var b bool
	switch k.Kind {
	case KindBool:
		v, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("config: %s: %w", name, err)
		}
		b = v
	case KindEnum:
		if !slices.Contains(k.Choices, value) {
			return fmt.Errorf("config: %s: invalid value %q (want %s)", name, value, strings.Join(k.Choices, " or "))
		}
	}
	next := c.clone()
	f := &next.Features
	switch name {
	case "mode":
		next.Mode = value
	case "features.hooks":
		f.Hooks = b
	case "features.handoff_push":
		f.HandoffPush = b
	case "features.stats":
		f.Stats = b
	case "features.autostart":
		f.Autostart = b
	case "features.open_dashboard_at_login":
		f.OpenDashboardAtLogin = b
	case "features.private_sync":
		f.PrivateSync = b
	case "features.update_check":
		f.UpdateCheck = value
	case "features.path_mirror":
		f.PathMirror = b
	case "display.hidden_tabs":
		next.Display.HiddenTabs = splitList(value)
	case "display.default_tab":
		next.Display.DefaultTab = value
	case "display.home_activity":
		next.Display.HomeActivity = b
	case "launch.claude":
		next.Launch.Claude = value
	case "link_mode":
		next.LinkMode = value
	default:
		sk, _ := strings.CutPrefix(name, "sync.")
		next.Sync[sk] = b
	}
	if err := next.normalize(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if slices.Contains(next.Display.HiddenTabs, next.Display.DefaultTab) {
		return fmt.Errorf("config: the default tab %q cannot be hidden", next.Display.DefaultTab)
	}
	*c = *next
	return nil
}

// clone copies c deeply enough for Set to validate a candidate.
func (c *Config) clone() *Config {
	n := *c
	n.Sync = map[string]bool{}
	for k, v := range c.Sync {
		n.Sync[k] = v
	}
	n.Display.HiddenTabs = slices.Clone(c.Display.HiddenTabs)
	n.Accounts = slices.Clone(c.Accounts)
	return &n
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "true", "on", "yes", "1":
		return true, nil
	case "false", "off", "no", "0":
		return false, nil
	}
	return false, fmt.Errorf("invalid value %q (want on or off)", s)
}

// splitList parses "a, b,,c" into a sorted, de-duplicated list.
func splitList(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
