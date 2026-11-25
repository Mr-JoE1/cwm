package accounts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

func vaultSandbox(t *testing.T) (home string, l *storage.Layout) {
	t.Helper()
	home = t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		t.Setenv(k, home)
	}
	l = storage.NewLayoutAt(filepath.Join(home, ".cwm"))
	return home, l
}

func put(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func topKeys(t *testing.T, p string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// The save-back-then-swap switch in capture mode: the live login moves
// into the old default's vault entry, the new one into the live files,
// and no non-identity key of ~/.claude.json changes.
func TestSwitchSavesBackThenSwaps(t *testing.T) {
	home, l := vaultSandbox(t)
	live := filepath.Join(home, ".claude", ".credentials.json")
	liveJSON := filepath.Join(home, ".claude.json")
	put(t, live, `{"claudeAiOauth":{"accessToken":"fake-main-REFRESHED"}}`)
	put(t, liveJSON, `{"userID":"fake-user","projects":{"/x":{"allowedTools":[]}},"mcpServers":{"a":{"command":"a"}},"oauthAccount":{"emailAddress":"main@example.invalid"},"modelAccessCache":{"m":1}}`)
	put(t, filepath.Join(l.AccountsDir(), "main", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"fake-main-STALE"}}`)
	put(t, filepath.Join(l.AccountsDir(), "work", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"fake-work"}}`)
	put(t, filepath.Join(l.AccountsDir(), "work", ".claude.json"), `{"oauthAccount":{"emailAddress":"work@example.invalid"},"mcpServers":{"ignored":{}}}`)

	cfg := config.Default()
	for _, n := range []string{"main", "work"} {
		if err := cfg.AddAccount(n, filepath.Join(l.AccountsDir(), n)); err != nil {
			t.Fatal(err)
		}
	}
	before := topKeys(t, liveJSON)
	sw := Switcher{Store: FileStore{}, Running: func() []int { return []int{42} }, Now: func() time.Time { return time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC) }}
	res, err := sw.Use(l, cfg, "work")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultAccount != "work" || !res.SavedBack || res.Backup == "" {
		t.Fatalf("result %+v default %q", res, cfg.DefaultAccount)
	}
	if got, _ := os.ReadFile(live); !bytes.Contains(got, []byte("fake-work")) {
		t.Errorf("live creds = %s", got)
	}
	if got, _ := os.ReadFile(filepath.Join(l.AccountsDir(), "main", ".credentials.json")); !bytes.Contains(got, []byte("REFRESHED")) {
		t.Errorf("save-back missed the refreshed token: %s", got)
	}
	after := topKeys(t, liveJSON)
	for _, k := range []string{"userID", "projects", "mcpServers"} {
		if !reflect.DeepEqual(before[k], after[k]) {
			t.Errorf("non-identity key %s changed: %v -> %v", k, before[k], after[k])
		}
	}
	if acct := after["oauthAccount"].(map[string]any); acct["emailAddress"] != "work@example.invalid" {
		t.Errorf("identity = %v", acct)
	}
	if _, ok := after["modelAccessCache"]; ok {
		t.Error("main's account cache leaked into work's live file")
	}
	if saved := topKeys(t, filepath.Join(l.AccountsDir(), "main", ".claude.json")); saved["modelAccessCache"] == nil || saved["oauthAccount"] == nil {
		t.Errorf("main's identity block not saved back: %v", saved)
	}
	if _, err := os.Stat(filepath.Join(res.Backup, ".credentials.json")); err != nil {
		t.Errorf("no backup of the replaced credentials: %v", err)
	}
	if w := res.Warning(); !strings.Contains(w, "keep using main until restarted") {
		t.Errorf("warning = %q", w)
	}
	// Switching back restores main's (refreshed) login.
	if _, err := sw.Use(l, cfg, "main"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(live); !bytes.Contains(got, []byte("REFRESHED")) {
		t.Errorf("switch back: live = %s", got)
	}
}

func TestSwitchRefusesAccountWithoutLogin(t *testing.T) {
	home, l := vaultSandbox(t)
	put(t, filepath.Join(home, ".claude", ".credentials.json"), `{"fake":"main"}`)
	cfg := config.Default()
	_ = cfg.AddAccount("main", filepath.Join(l.AccountsDir(), "main"))
	_ = cfg.AddAccount("empty", filepath.Join(l.AccountsDir(), "empty"))
	if _, err := (Switcher{Store: FileStore{}, Running: func() []int { return nil }}).Use(l, cfg, "empty"); err == nil || !strings.Contains(err.Error(), "cwm account login empty") {
		t.Fatalf("err = %v", err)
	}
	if cfg.DefaultAccount != "main" {
		t.Error("default changed on a refused switch")
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json")); string(got) != `{"fake":"main"}` {
		t.Errorf("live creds touched: %s", got)
	}
}

// Isolated mode swaps inside the workspace root.
func TestSwitchIsolatedLivePaths(t *testing.T) {
	_, l := vaultSandbox(t)
	ws := l.IsolatedWorkspaceDir()
	put(t, filepath.Join(ws, ".credentials.json"), `{"fake":"a"}`)
	put(t, filepath.Join(l.AccountsDir(), "b", ".credentials.json"), `{"fake":"b"}`)
	cfg := config.Default()
	cfg.Mode = config.ModeIsolated
	_ = cfg.AddAccount("a", filepath.Join(l.AccountsDir(), "a"))
	_ = cfg.AddAccount("b", filepath.Join(l.AccountsDir(), "b"))
	if _, err := (Switcher{Store: FileStore{}, Running: func() []int { return nil }}).Use(l, cfg, "b"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(ws, ".credentials.json")); string(got) != `{"fake":"b"}` {
		t.Errorf("isolated live = %s", got)
	}
}

func TestKeychainStoreFake(t *testing.T) {
	_, l := vaultSandbox(t)
	items := map[string]string{}
	var calls [][]string
	fake := func(stdin []byte, args ...string) ([]byte, error) {
		calls = append(calls, args)
		switch args[0] {
		case "find-generic-password":
			if v, ok := items[args[len(args)-1]]; ok {
				return []byte(v + "\n"), nil
			}
			return nil, os.ErrNotExist
		case "-i":
			_, rest, _ := strings.Cut(string(stdin), `-s "`)
			svc, rest, _ := strings.Cut(rest, `"`)
			_, raw, _ := strings.Cut(rest, "-X ")
			raw = strings.TrimSpace(raw)
			b := make([]byte, len(raw)/2)
			for i := range b {
				var x byte
				for _, c := range raw[2*i : 2*i+2] {
					x <<= 4
					switch {
					case c >= '0' && c <= '9':
						x |= byte(c - '0')
					default:
						x |= byte(c-'a') + 10
					}
				}
				b[i] = x
			}
			items[svc] = string(b)
			return nil, nil
		}
		return nil, os.ErrInvalid
	}
	ks := KeychainStore{Run: fake, User: "tester"}
	def, _ := claudepaths.Live(l, config.ModeCapture, "", "")
	work, _ := VaultPaths(l, config.ModeCapture, "work")
	if KeychainService(def) != "Claude Code-credentials" {
		t.Errorf("default service = %q", KeychainService(def))
	}
	if s := KeychainService(work); !strings.HasPrefix(s, "Claude Code-credentials-") || len(s) != len("Claude Code-credentials-")+8 {
		t.Errorf("work service = %q", s)
	}
	if _, err := ks.Read(work); err != ErrNoCredentials {
		t.Errorf("empty read: %v", err)
	}
	if err := ks.Write(work, []byte(`{"fake":"work token"}`)); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if strings.Contains(strings.Join(c, " "), "fake") {
			t.Errorf("secret in argv: %v", c)
		}
	}
	if got, err := ks.Read(work); err != nil || string(got) != `{"fake":"work token"}` {
		t.Errorf("read back %q %v", got, err)
	}
	// The fallback file wins when it exists.
	put(t, def.Credentials, `{"fake":"file"}`)
	if got, _ := ks.Read(def); string(got) != `{"fake":"file"}` {
		t.Errorf("fallback file not preferred: %s", got)
	}
}

func TestRunningParsers(t *testing.T) {
	if got := parseTasklist("\"claude.exe\",\"1234\",\"Console\",\"1\",\"10 K\"\r\n\"other.exe\",\"9\"\r\n"); !reflect.DeepEqual(got, []int{1234}) {
		t.Errorf("tasklist = %v", got)
	}
	if got := parsePids("12\n34\n"); !reflect.DeepEqual(got, []int{12, 34}) {
		t.Errorf("pgrep = %v", got)
	}
	proc := t.TempDir()
	put(t, filepath.Join(proc, "100", "comm"), "claude\n")
	put(t, filepath.Join(proc, "101", "comm"), "bash\n")
	put(t, filepath.Join(proc, "101", "cmdline"), "bash\x00-c\x00")
	put(t, filepath.Join(proc, "102", "comm"), "node\n")
	put(t, filepath.Join(proc, "102", "cmdline"), "/usr/local/bin/claude\x00--resume\x00")
	if got := procClaude(proc); !reflect.DeepEqual(got, []int{100, 102}) {
		t.Errorf("proc = %v", got)
	}
	// Only CLAUDE_CONFIG_DIR is read from the environment; an unreadable
	// environ leaves the process unattributed.
	put(t, filepath.Join(proc, "102", "environ"), "TOKEN_LIKE=fake-not-kept\x00CLAUDE_CONFIG_DIR=/x/accounts/home\x00")
	want := []ClaudeProc{{PID: 100}, {PID: 102, EnvKnown: true, ConfigDir: "/x/accounts/home"}}
	if got := procClaudeEnv(proc); !reflect.DeepEqual(got, want) {
		t.Errorf("procClaudeEnv = %+v", got)
	}
}

// Has never reads the secret (no -w), and SaveLive fills an empty vault
// entry from the live login but leaves a filled one alone.
func TestHasAndSaveLive(t *testing.T) {
	home, l := vaultSandbox(t)
	var calls [][]string
	fake := func(_ []byte, args ...string) ([]byte, error) {
		calls = append(calls, args)
		return nil, os.ErrNotExist
	}
	work, _ := VaultPaths(l, config.ModeCapture, "work")
	if Has(KeychainStore{Run: fake, User: "tester"}, work) {
		t.Error("empty keychain: Has = true")
	}
	for _, c := range calls {
		if strings.Contains(strings.Join(c, " "), "-w") {
			t.Errorf("Has must not ask for the secret: %v", c)
		}
	}
	cfg := config.Default()
	if err := cfg.AddAccount("main", filepath.Join(l.AccountsDir(), "main")); err != nil {
		t.Fatal(err)
	}
	if saved, err := SaveLive(l, cfg, FileStore{}); err != nil || saved {
		t.Fatalf("no live login: saved=%v err=%v", saved, err)
	}
	put(t, filepath.Join(home, ".claude", ".credentials.json"), `{"fake":"live"}`)
	put(t, filepath.Join(home, ".claude.json"), `{"oauthAccount":{"emailAddress":"main@example.invalid"},"projects":{}}`)
	if saved, err := SaveLive(l, cfg, FileStore{}); err != nil || !saved {
		t.Fatalf("empty vault: saved=%v err=%v", saved, err)
	}
	main, _ := VaultPaths(l, config.ModeCapture, "main")
	if !Has(FileStore{}, main) {
		t.Fatal("vault entry not written")
	}
	if k := topKeys(t, main.ClaudeJSON); k["oauthAccount"] == nil || k["projects"] != nil {
		t.Errorf("vault .claude.json = %v, want identity keys only", k)
	}
	put(t, filepath.Join(home, ".claude", ".credentials.json"), `{"fake":"other"}`)
	if saved, _ := SaveLive(l, cfg, FileStore{}); saved {
		t.Error("a filled vault entry must be left alone")
	}
	if got, _ := os.ReadFile(main.Credentials); string(got) != `{"fake":"live"}` {
		t.Errorf("vault overwritten: %s", got)
	}
}

// The first account added on a machine without a Claude login becomes
// the default: its vault login must become the live one, or plain claude
// runs logged out. A live login is never replaced without force; force
// (the default logged in again) backs the old files up first.
func TestInstallDefault(t *testing.T) {
	home, l := vaultSandbox(t)
	cfg := config.Default()
	if err := cfg.AddAccount("main", filepath.Join(l.AccountsDir(), "main")); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddAccount("work", filepath.Join(l.AccountsDir(), "work")); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	if put, err := InstallDefault(l, cfg, "main", FileStore{}, false, now); err != nil || put {
		t.Fatalf("empty vault: put=%v err=%v", put, err)
	}
	put(t, filepath.Join(l.AccountsDir(), "main", ".credentials.json"), `{"fake":"main-1"}`)
	put(t, filepath.Join(l.AccountsDir(), "main", ".claude.json"), `{"oauthAccount":{"emailAddress":"main@example.invalid"}}`)
	put(t, filepath.Join(l.AccountsDir(), "work", ".credentials.json"), `{"fake":"work"}`)
	if put, _ := InstallDefault(l, cfg, "work", FileStore{}, true, now); put {
		t.Fatal("a non-default account must never be installed live")
	}
	liveCreds := filepath.Join(home, ".claude", ".credentials.json")
	liveJSON := filepath.Join(home, ".claude.json")
	if put, err := InstallDefault(l, cfg, "main", FileStore{}, false, now); err != nil || !put {
		t.Fatalf("no live login: put=%v err=%v", put, err)
	}
	if got, _ := os.ReadFile(liveCreds); string(got) != `{"fake":"main-1"}` {
		t.Errorf("live credentials = %s", got)
	}
	if k := topKeys(t, liveJSON); k["oauthAccount"] == nil {
		t.Errorf("live .claude.json = %v, want the identity", k)
	}
	put(t, liveJSON, `{"oauthAccount":{"emailAddress":"main@example.invalid"},"projects":{"/x":{}},"userID":"fake"}`)
	put(t, filepath.Join(l.AccountsDir(), "main", ".credentials.json"), `{"fake":"main-2"}`)
	if put, _ := InstallDefault(l, cfg, "main", FileStore{}, false, now); put {
		t.Fatal("without force a live login must be left alone")
	}
	if put, err := InstallDefault(l, cfg, "main", FileStore{}, true, now); err != nil || !put {
		t.Fatalf("force: put=%v err=%v", put, err)
	}
	if got, _ := os.ReadFile(liveCreds); string(got) != `{"fake":"main-2"}` {
		t.Errorf("forced live credentials = %s", got)
	}
	if k := topKeys(t, liveJSON); k["projects"] == nil || k["userID"] != "fake" {
		t.Errorf("non-identity keys changed: %v", k)
	}
	old, err := os.ReadFile(filepath.Join(l.BackupsDir(), "20261125T100000Z-login-main", ".credentials.json"))
	if err != nil || string(old) != `{"fake":"main-1"}` {
		t.Errorf("backup = %q, %v", old, err)
	}
	if put, _ := InstallDefault(l, cfg, "main", FileStore{}, true, now); put {
		t.Error("identical login: nothing to install")
	}
}
