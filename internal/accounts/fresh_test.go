package accounts

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// cred is a fake credentials blob whose renewal generation is exp.
func cred(token string, exp int64) string {
	return `{"claudeAiOauth":{"accessToken":"fake-` + token + `","refreshToken":"fake-r-` + token + `","expiresAt":` + strconv.FormatInt(exp, 10) + `}}`
}

func TestExpiresAt(t *testing.T) {
	if got := ExpiresAt([]byte(cred("a", 1764000000000))); got != 1764000000000 {
		t.Fatalf("ExpiresAt = %d", got)
	}
	for _, bad := range []string{"", "{", `{"claudeAiOauth":{}}`, `{"primaryApiKey":"k"}`} {
		if ExpiresAt([]byte(bad)) != 0 {
			t.Errorf("ExpiresAt(%q) != 0", bad)
		}
	}
}

type freshFix struct {
	l           *storage.Layout
	cfg         *config.Config
	live, vault string // main's live and vault credentials
}

// freshSandbox: capture mode, default account main with a live login and
// a vault copy, plus account work.
func freshSandbox(t *testing.T, liveCred, vaultCred string) freshFix {
	t.Helper()
	home, l := vaultSandbox(t)
	f := freshFix{l: l, live: filepath.Join(home, ".claude", ".credentials.json"), vault: filepath.Join(l.AccountsDir(), "main", ".credentials.json")}
	put(t, f.live, liveCred)
	put(t, f.vault, vaultCred)
	put(t, filepath.Join(l.AccountsDir(), "work", ".credentials.json"), cred("work", 5))
	f.cfg = config.Default()
	for _, n := range []string{"main", "work"} {
		if err := f.cfg.AddAccount(n, filepath.Join(l.AccountsDir(), n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.cfg.SetDefaultAccount("main"); err != nil {
		t.Fatal(err)
	}
	return f
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSwitchKeepsNewerVault (D126): main ran from its vault dir and
// renewed there after it was put live; a switch away must not save the
// live (spent) copy over it.
func TestSwitchKeepsNewerVault(t *testing.T) {
	f := freshSandbox(t, cred("main-old", 100), cred("main-renewed", 200))
	res, err := Switcher{Store: FileStore{}, Running: func() []int { return nil }}.Use(f.l, f.cfg, "work")
	if err != nil {
		t.Fatal(err)
	}
	if !res.KeptVault || res.SavedBack {
		t.Fatalf("result %+v", res)
	}
	if got := read(t, f.vault); !strings.Contains(got, "main-renewed") {
		t.Fatalf("vault lost the renewed login: %s", got)
	}
	if got := read(t, f.live); !strings.Contains(got, "fake-work") {
		t.Fatalf("live = %s", got)
	}
}

// TestSwitchSavesNewerLive: the usual case, live renewed in place.
func TestSwitchSavesNewerLive(t *testing.T) {
	f := freshSandbox(t, cred("main-renewed", 300), cred("main-old", 100))
	res, err := Switcher{Store: FileStore{}, Running: func() []int { return nil }}.Use(f.l, f.cfg, "work")
	if err != nil || !res.SavedBack || res.KeptVault {
		t.Fatalf("result %+v %v", res, err)
	}
	if got := read(t, f.vault); !strings.Contains(got, "main-renewed") {
		t.Fatalf("vault = %s", got)
	}
}

func TestReconcile(t *testing.T) {
	cases := []struct {
		name            string
		live, vault     string
		want            Reconciled
		wantLive, wantV string
	}{
		{"live newer", cred("L", 300), cred("V", 100), ReconcileToVault, "fake-L", "fake-L"},
		{"vault newer", cred("L", 100), cred("V", 300), ReconcileToLive, "fake-V", "fake-V"},
		{"same", cred("S", 100), cred("S", 100), ReconcileNone, "fake-S", "fake-S"},
		{"cannot tell", `{"claudeAiOauth":{"accessToken":"fake-L"}}`, `{"claudeAiOauth":{"accessToken":"fake-V"}}`, ReconcileNone, "fake-L", "fake-V"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := freshSandbox(t, tc.live, tc.vault)
			got, err := Reconcile(f.l, f.cfg, FileStore{})
			if err != nil || got != tc.want {
				t.Fatalf("Reconcile = %q %v", got, err)
			}
			if l, v := read(t, f.live), read(t, f.vault); !strings.Contains(l, tc.wantLive) || !strings.Contains(v, tc.wantV) {
				t.Fatalf("live %s vault %s", l, v)
			}
		})
	}
}

// TestReconcileMtimeFallback: without expiresAt the clearly later file
// wins; writes in the same moment are a tie.
func TestReconcileMtimeFallback(t *testing.T) {
	f := freshSandbox(t, `{"claudeAiOauth":{"accessToken":"fake-L"}}`, `{"claudeAiOauth":{"accessToken":"fake-V"}}`)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(f.live, old, old); err != nil {
		t.Fatal(err)
	}
	if got, _ := Reconcile(f.l, f.cfg, FileStore{}); got != ReconcileToLive || !strings.Contains(read(t, f.live), "fake-V") {
		t.Fatalf("Reconcile = %q, live %s", got, read(t, f.live))
	}
}

func TestReconcileGuards(t *testing.T) {
	// The live login is another account (plain `claude /login`): no copy.
	f := freshSandbox(t, cred("L", 300), cred("V", 100))
	put(t, filepath.Join(filepath.Dir(filepath.Dir(f.live)), ".claude.json"), `{"oauthAccount":{"accountUuid":"u-other"}}`)
	put(t, filepath.Join(filepath.Dir(f.vault), ".claude.json"), `{"oauthAccount":{"accountUuid":"u-main"}}`)
	if got, err := Reconcile(f.l, f.cfg, FileStore{}); got != ReconcileOtherAccount || err != nil || !strings.Contains(read(t, f.vault), "fake-V") {
		t.Fatalf("other account: %q %v vault %s", got, err, read(t, f.vault))
	}
	// No vault copy, no default account: nothing to do, no error.
	g := freshSandbox(t, cred("L", 300), cred("V", 100))
	if err := os.Remove(g.vault); err != nil {
		t.Fatal(err)
	}
	if got, err := Reconcile(g.l, g.cfg, FileStore{}); got != ReconcileNone || err != nil {
		t.Fatalf("no vault: %q %v", got, err)
	}
	if got, err := Reconcile(g.l, config.Default(), FileStore{}); got != ReconcileNone || err != nil {
		t.Fatalf("no default: %q %v", got, err)
	}
}
