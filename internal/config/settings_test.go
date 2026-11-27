package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDefaultHasModeAndEverySwitch(t *testing.T) {
	c := Default()
	if c.Mode != ModeCapture || c.NeedsMigration() {
		t.Fatalf("fresh config: mode %q needs migration %v", c.Mode, c.NeedsMigration())
	}
	for _, k := range SyncKeys() {
		if !c.SyncEnabled(k) {
			t.Errorf("sync.%s off by default", k)
		}
	}
	f := c.Features
	if !f.Hooks || !f.HandoffPush || !f.Stats || !f.Autostart || f.OpenDashboardAtLogin || f.PrivateSync || f.UpdateCheck != UpdateCheckManual {
		t.Errorf("feature defaults = %+v", f)
	}
	if c.Display.DefaultTab != "home" || len(c.Display.HiddenTabs) != 0 || !c.Display.HomeActivity || c.Display.Schema != DisplaySchema {
		t.Errorf("display defaults = %+v", c.Display)
	}
}

// A v0.7 config (no mode key) loads as capture, flagged for migration,
// keeps its accounts, and a re-save does not invent a mode.
func TestLegacyConfigNeedsMigration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	old := "version = 1\ndefault_account = \"work\"\nlink_mode = \"auto\"\n\n[[account]]\nname = \"work\"\ndir = \"~/.cwm/accounts/work\"\n"
	if err := os.WriteFile(p, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.NeedsMigration() || c.EffectiveMode() != ModeCapture {
		t.Fatalf("legacy: needs %v mode %q", c.NeedsMigration(), c.EffectiveMode())
	}
	if !c.SyncEnabled("sessions") || !c.Features.Hooks || c.Display.DefaultTab != "home" {
		t.Errorf("legacy defaults not filled: %+v", c)
	}
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if strings.HasPrefix(string(data), "mode =") || strings.Contains(string(data), "\nmode =") {
		t.Errorf("re-save invented a mode:\n%s", data)
	}
	c2, err := Load(p)
	if err != nil || !c2.NeedsMigration() || len(c2.Accounts) != 1 {
		t.Fatalf("reload: %v %+v", err, c2)
	}
}

func TestPartialTablesKeepDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	body := "mode = \"isolated\"\n[sync]\nsettings = false\n[features]\nstats = false\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.EffectiveMode() != ModeIsolated || c.NeedsMigration() {
		t.Errorf("mode %q", c.Mode)
	}
	if c.SyncEnabled("settings") || !c.SyncEnabled("history") {
		t.Errorf("sync = %v", c.Sync)
	}
	if c.Features.Stats || !c.Features.Hooks {
		t.Errorf("features = %+v", c.Features)
	}
}

func TestInvalidValuesRejectedOnLoad(t *testing.T) {
	for _, body := range []string{
		"mode = \"cloud\"\n",
		"[sync]\ntodos = true\n",
		"[features]\nupdate_check = \"auto\"\n",
		"[display]\nhidden_tabs = [\"nope\"]\n",
		"[display]\nhidden_tabs = [\"settings\"]\n",
		"[display]\ndefault_tab = \"nope\"\n",
	} {
		if _, err := parse(body); err == nil {
			t.Errorf("accepted %q", body)
		}
	}
}

func TestGetSetEveryKey(t *testing.T) {
	c := Default()
	for _, k := range Keys() {
		v, err := c.Get(k.Name)
		if err != nil {
			t.Fatalf("get %s: %v", k.Name, err)
		}
		if v != k.Default {
			t.Errorf("%s = %q, default %q", k.Name, v, k.Default)
		}
		var next string
		switch k.Kind {
		case KindBool:
			next = "off"
			if v == "false" {
				next = "on"
			}
		case KindEnum:
			for _, ch := range k.Choices {
				if ch != v {
					next = ch
					break
				}
			}
		case KindList:
			next = "handoff, devices,handoff"
		}
		if err := c.Set(k.Name, next); err != nil {
			t.Fatalf("set %s %q: %v", k.Name, next, err)
		}
		got, _ := c.Get(k.Name)
		if got == v {
			t.Errorf("set %s %q: still %q", k.Name, next, got)
		}
		if k.Kind == KindList && got != "devices,handoff" {
			t.Errorf("list = %q", got)
		}
	}
	// The default tab moved off sessions above; hiding it is refused and
	// leaves the config as it was.
	before, _ := c.Get("display.hidden_tabs")
	if err := c.Set("display.hidden_tabs", c.Display.DefaultTab); err == nil {
		t.Error("hid the default tab")
	}
	if after, _ := c.Get("display.hidden_tabs"); after != before {
		t.Errorf("rejected set changed hidden_tabs: %q -> %q", before, after)
	}
	for _, bad := range [][2]string{{"nope", "1"}, {"sync.sessions", "maybe"}, {"mode", "cloud"}, {"display.hidden_tabs", "settings"}} {
		if err := c.Set(bad[0], bad[1]); err == nil {
			t.Errorf("set %v accepted", bad)
		}
	}
}

func TestSaveRoundTripsSwitches(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	c := Default()
	for _, kv := range [][2]string{{"mode", "isolated"}, {"sync.plugins", "off"}, {"features.private_sync", "on"}, {"display.hidden_tabs", "handoff"}} {
		if err := c.Set(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	r, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != ModeIsolated || r.SyncEnabled("plugins") || !r.Features.PrivateSync || len(r.Display.HiddenTabs) != 1 {
		t.Errorf("round trip = %+v", r)
	}
}

// D105: a v0.8 [display] table maps onto the v0.9 tabs once. Hidden
// "stats" hides Home's Activity section, a stats or sessions (the v0.8
// default) default tab becomes home, and after the marker is saved an
// explicit sessions choice sticks.
func TestDisplayMigratesStatsToHomeOnce(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	cases := map[string]Display{
		"[display]\nhidden_tabs = [\"stats\", \"devices\"]\ndefault_tab = \"sessions\"\n": {HiddenTabs: []string{"devices"}, DefaultTab: "home", HomeActivity: false},
		"[display]\nhidden_tabs = []\ndefault_tab = \"stats\"\n":                          {HiddenTabs: []string{}, DefaultTab: "home", HomeActivity: true},
		"[display]\nhidden_tabs = [\"handoff\"]\ndefault_tab = \"health\"\n":              {HiddenTabs: []string{"handoff"}, DefaultTab: "health", HomeActivity: true},
		"version = 1\n": {HiddenTabs: []string{}, DefaultTab: "home", HomeActivity: true},
	}
	for in, want := range cases {
		if err := os.WriteFile(p, []byte("version = 1\nmode = \"capture\"\n"+strings.TrimPrefix(in, "version = 1\n")), 0o644); err != nil {
			t.Fatal(err)
		}
		wrote, err := MigrateFile(p)
		if err != nil || !wrote {
			t.Fatalf("%q: MigrateFile = %v, %v", in, wrote, err)
		}
		c, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		got := c.Display
		if !slices.Equal(got.HiddenTabs, want.HiddenTabs) || got.DefaultTab != want.DefaultTab || got.HomeActivity != want.HomeActivity || got.Schema != DisplaySchema {
			t.Errorf("%q: display = %+v, want %+v", in, got, want)
		}
		if again, err := MigrateFile(p); err != nil || again {
			t.Errorf("%q: second MigrateFile = %v, %v (want a no-op)", in, again, err)
		}
		if err := c.Set("display.default_tab", "sessions"); err != nil {
			t.Fatal(err)
		}
		if err := Save(p, c); err != nil {
			t.Fatal(err)
		}
		if c2, _ := Load(p); c2.Display.DefaultTab != "sessions" {
			t.Errorf("%q: an explicit sessions default was migrated again: %q", in, c2.Display.DefaultTab)
		}
	}
	if wrote, err := MigrateFile(filepath.Join(t.TempDir(), "absent.toml")); wrote || err != nil {
		t.Errorf("missing file: %v, %v", wrote, err)
	}
}

func TestLaunchClaudeSetting(t *testing.T) {
	c := Default()
	if v, _ := c.Get("launch.claude"); v != "auto" {
		t.Fatalf("default = %q", v)
	}
	for _, v := range []string{"cli", "vscode", "desktop", "auto"} {
		if err := c.Set("launch.claude", v); err != nil {
			t.Fatalf("set %s: %v", v, err)
		}
		if got, _ := c.Get("launch.claude"); got != v {
			t.Errorf("get = %q, want %q", got, v)
		}
	}
	for _, v := range []string{"", "code", "AUTO", "cli,vscode"} {
		if err := c.Set("launch.claude", v); err == nil {
			t.Errorf("set %q accepted", v)
		}
	}
	bad := &Config{Launch: Launch{Claude: "terminal"}}
	if err := bad.normalize(); err == nil || !strings.Contains(err.Error(), "launch.claude") {
		t.Errorf("normalize bad = %v", err)
	}
	old := &Config{}
	if err := old.normalize(); err != nil || old.Launch.Claude != "auto" {
		t.Errorf("a config without [launch] = %q, %v", old.Launch.Claude, err)
	}
}
