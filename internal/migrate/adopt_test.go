package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/config"
)

// adoptWorld is the owner's machine: a v0.7 layout whose config already
// names accounts, and a live ~/.claude login no vault entry matches.
func adoptWorld(t *testing.T, cfgBody func(w *world) string, liveIdentity string) *world {
	t.Helper()
	w := newWorld(t)
	ws := filepath.Join(w.cwm, "workspace")
	for i := 0; i < 15; i++ {
		write(t, filepath.Join(ws, "projects", "p", "s-"+string(rune('a'+i))+".jsonl"), `{"uuid":"x"}`+"\n")
	}
	write(t, filepath.Join(ws, "CLAUDE.md"), "# memory\n")
	write(t, filepath.Join(w.cwm, "config.toml"), "version = 1\n"+cfgBody(w))
	write(t, filepath.Join(w.claude, ".credentials.json"), fakeLiveCreds)
	if liveIdentity != "" {
		write(t, filepath.Join(w.home, ".claude.json"), liveIdentity)
	}
	return w
}

func acct(name, dir string) string {
	return "\n[[account]]\nname = \"" + name + "\"\ndir = \"" + filepath.ToSlash(dir) + "\"\n"
}

func importAction(t *testing.T, p *Plan) Action {
	t.Helper()
	var found []Action
	for _, a := range p.Actions {
		if a.Kind == ActImport {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		t.Fatalf("import actions = %+v", found)
	}
	return found[0]
}

func TestAdoptLiveLoginIntoOnlyAccount(t *testing.T) {
	w := adoptWorld(t, func(w *world) string {
		return "default_account = \"tat\"\n" + acct("tat", filepath.Join(w.cwm, "accounts", "tat"))
	}, identity("uuid-tat", "tat@example.com"))
	cfg, p, rec := run(t, w, "")
	a := importAction(t, p)
	if a.Account != "tat" || !strings.Contains(a.Detail, "the live login is tat's (the only account, no saved login yet)") {
		t.Fatalf("import = %+v", a)
	}
	if strings.Contains(a.Detail, "FAKE") || strings.Contains(a.Src+a.Dst, "FAKE") {
		t.Fatal("a credential value in the plan")
	}
	if p.Default != "tat" || rec.Default != "tat" || cfg.DefaultAccount != "tat" {
		t.Fatalf("default = %q / %q / %q", p.Default, rec.Default, cfg.DefaultAccount)
	}
	if len(cfg.Accounts) != 1 || cfg.Accounts[0].Name != "tat" {
		t.Fatalf("accounts = %+v", cfg.Accounts)
	}
	if cfg.Accounts[0].Login == nil || cfg.Accounts[0].Login.AccountUUID != "uuid-tat" {
		t.Errorf("tat's recorded identity = %+v", cfg.Accounts[0].Login)
	}
	if got := read(t, filepath.Join(w.cwm, "accounts", "tat", ".credentials.json")); got != fakeLiveCreds {
		t.Error("tat's vault does not hold the live login")
	}
	if _, err := os.Stat(filepath.Join(w.cwm, "accounts", "default")); err == nil {
		t.Error(`an account "default" was created`)
	}
	if rec.SessionsAfter != 15 {
		t.Errorf("sessions = %d", rec.SessionsAfter)
	}
}

func TestAdoptLiveLoginIntoDefaultOfSeveral(t *testing.T) {
	w := adoptWorld(t, func(w *world) string {
		return "default_account = \"tat\"\n" + acct("work", filepath.Join(w.cwm, "accounts", "work")) + acct("tat", filepath.Join(w.cwm, "accounts", "tat"))
	}, "")
	work := filepath.Join(w.cwm, "accounts", "work")
	write(t, filepath.Join(work, ".credentials.json"), fakeWorkCreds)
	write(t, filepath.Join(work, ".claude.json"), identity("uuid-work", "work@example.com"))
	cfg, p, _ := run(t, w, "")
	a := importAction(t, p)
	if a.Account != "tat" || !strings.Contains(a.Detail, "the default account") {
		t.Fatalf("import = %+v", a)
	}
	if len(cfg.Accounts) != 2 || cfg.DefaultAccount != "tat" {
		t.Fatalf("accounts = %+v default %s", cfg.Accounts, cfg.DefaultAccount)
	}
	if read(t, filepath.Join(work, ".credentials.json")) != fakeWorkCreds {
		t.Error("work's saved login changed")
	}
}

func TestAdoptNoAccountsMakesDefault(t *testing.T) {
	w := adoptWorld(t, func(*world) string { return "" }, identity("uuid-live", "me@example.com"))
	cfg, p, _ := run(t, w, "")
	if a := importAction(t, p); a.Account != "default" || !strings.Contains(a.Detail, "becomes account default") {
		t.Fatalf("import = %+v", a)
	}
	if _, ok := cfg.Account("default"); !ok || cfg.DefaultAccount != "default" {
		t.Fatalf("accounts = %+v", cfg.Accounts)
	}
}

func TestAdoptDifferentIdentityMakesNewAccount(t *testing.T) {
	w := adoptWorld(t, func(w *world) string {
		return "default_account = \"work\"\n" + acct("work", filepath.Join(w.cwm, "accounts", "work"))
	}, identity("uuid-live", "me@example.com"))
	work := filepath.Join(w.cwm, "accounts", "work")
	write(t, filepath.Join(work, ".credentials.json"), fakeWorkCreds)
	write(t, filepath.Join(work, ".claude.json"), identity("uuid-work", "work@example.com"))
	cfg, err := config.Load(w.l.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(w.l, cfg, Options{Store: accounts.FileStore{}, Now: clock(), Host: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if a := importAction(t, p); a.Account != "default" {
		t.Fatalf("import = %+v", a)
	}
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "different Claude account than work's saved login") {
		t.Fatalf("warnings = %q", p.Warnings)
	}
	for _, wn := range p.Warnings {
		if strings.Contains(wn, "@") {
			t.Errorf("an email in a warning: %q", wn)
		}
	}
}
