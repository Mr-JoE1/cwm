package config

import (
	"os"
	"path/filepath"
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
	if c.Display.DefaultTab != "sessions" || len(c.Display.HiddenTabs) != 0 {
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
	if !c.SyncEnabled("sessions") || !c.Features.Hooks || c.Display.DefaultTab != "sessions" {
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
			next = "stats, devices,stats"
		}
		if err := c.Set(k.Name, next); err != nil {
			t.Fatalf("set %s %q: %v", k.Name, next, err)
		}
		got, _ := c.Get(k.Name)
		if got == v {
			t.Errorf("set %s %q: still %q", k.Name, next, got)
		}
		if k.Kind == KindList && got != "devices,stats" {
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
	for _, kv := range [][2]string{{"mode", "isolated"}, {"sync.plugins", "off"}, {"features.private_sync", "on"}, {"display.hidden_tabs", "stats"}} {
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
