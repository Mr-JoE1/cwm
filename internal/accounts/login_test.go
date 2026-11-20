package accounts

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

func isolateHome(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		t.Setenv(k, t.TempDir())
	}
}

func TestValidateNewName(t *testing.T) {
	cfg := config.Default()
	_ = cfg.AddAccount("taken", "/x")
	reserved := func(n string) bool { return n == "gui" || n == "debug" }
	cases := map[string]string{
		"":                      "is required",
		"  ":                    "is required",
		strings.Repeat("a", 33): "too long",
		"a/b":                   "must not contain",
		`a\b`:                   "must not contain",
		"login":                 "`cwm account` action",
		"rm":                    "`cwm account` action",
		"gui":                   "a cwm command",
		"Work":                  "invalid account name",
		"123":                   "invalid account name",
		"-x":                    "invalid account name",
		"taken":                 "already exists",
	}
	for name, want := range cases {
		err := ValidateNewName(name, cfg, reserved)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ValidateNewName(%q) = %v, want ~%q", name, err, want)
		}
	}
	var ee *ExistsError
	if !errors.As(ValidateNewName("taken", cfg, nil), &ee) {
		t.Error("a duplicate must be an *ExistsError")
	}
	for _, ok := range []string{"work", "a", "w_2", strings.Repeat("z", 32)} {
		if err := ValidateNewName(ok, cfg, reserved); err != nil {
			t.Errorf("ValidateNewName(%q) = %v", ok, err)
		}
	}
}

func TestDeriveLogin(t *testing.T) {
	isolateHome(t)
	l := storage.NewLayoutAt(t.TempDir())
	dir := filepath.Join(l.AccountsDir(), "work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	a := config.Account{Name: "work", Dir: dir}
	now := time.Now().UTC()
	alive := map[int]bool{100: true}
	env := deriveEnv{now: now, alive: func(p int) bool { return alive[p] }}
	write := func(st LoginState) {
		t.Helper()
		if err := WriteLoginState(l, "work", st); err != nil {
			t.Fatal(err)
		}
	}
	// No marker: never, or done when a login was recorded.
	if st, ch := deriveLogin(l, a, env); st.Status != LoginNever || ch {
		t.Errorf("fresh = %+v %v", st, ch)
	}
	logged := a
	logged.Login = &config.Login{Email: "w@example.com"}
	if st, _ := deriveLogin(l, logged, env); st.Status != LoginDone {
		t.Errorf("recorded = %+v", st)
	}
	cases := []struct {
		name   string
		marker LoginState
		acct   config.Account
		creds  bool
		want   LoginStatus
		errHas string
	}{
		{"running cli", LoginState{Status: LoginInProgress, Started: now.Add(-time.Minute), PID: 100}, a, false, LoginInProgress, ""},
		{"dead pid", LoginState{Status: LoginInProgress, Started: now.Add(-time.Minute), PID: 200}, a, false, LoginFailed, "closed"},
		{"dashboard, picked up soon", LoginState{Status: LoginInProgress, Started: now.Add(-30 * time.Second)}, a, false, LoginInProgress, ""},
		{"dashboard, never picked up", LoginState{Status: LoginInProgress, Started: now.Add(-2 * time.Minute)}, a, false, LoginFailed, "did not start"},
		{"timed out", LoginState{Status: LoginInProgress, Started: now.Add(-16 * time.Minute), PID: 100}, a, false, LoginFailed, "15 minutes"},
		{"finished meanwhile", LoginState{Status: LoginInProgress, Started: now.Add(-time.Minute), PID: 100}, logged, true, LoginDone, ""},
		{"old login, new attempt", LoginState{Status: LoginInProgress, Started: now.Add(time.Hour), PID: 100}, logged, true, LoginInProgress, ""},
		{"failed stays failed", LoginState{Status: LoginFailed, Error: "exit status 1"}, logged, true, LoginFailed, "exit status 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_ = os.Remove(filepath.Join(dir, ".credentials.json"))
			if c.creds {
				if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write(c.marker)
			st, changed := deriveLogin(l, c.acct, env)
			if st.Status != c.want || !strings.Contains(st.Error, c.errHas) {
				t.Errorf("got %+v, want %s ~%q", st, c.want, c.errHas)
			}
			if changed != (st.Status != c.marker.Status) {
				t.Errorf("changed = %v for %s → %s", changed, c.marker.Status, st.Status)
			}
		})
	}
	// Markers are private files.
	if fi, err := os.Stat(LoginStatePath(l, "work")); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) {
		t.Errorf("marker: %v %v", fi, err)
	}
	if err := WriteLoginState(l, "../evil", LoginState{Status: LoginDone}); err == nil {
		t.Error("a marker name must be a valid account name")
	}
}

func TestMarkLoginKeepsStart(t *testing.T) {
	l := storage.NewLayoutAt(t.TempDir())
	MarkLogin(l, "work", LoginInProgress, "cli", nil)
	first, ok := ReadLoginState(l, "work")
	if !ok || first.PID != os.Getpid() || first.By != "cli" || first.Started.IsZero() {
		t.Fatalf("in progress = %+v", first)
	}
	MarkLogin(l, "work", LoginFailed, "cli", errors.New("accounts: claude auth login failed (exit status 1)\nmore"))
	st, _ := ReadLoginState(l, "work")
	if st.Status != LoginFailed || !st.Started.Equal(first.Started) || st.Error != "accounts: claude auth login failed (exit status 1)" {
		t.Errorf("failed = %+v", st)
	}
	MarkLogin(l, "home", LoginInProgress, "dashboard", nil)
	if st, _ := ReadLoginState(l, "home"); st.PID != 0 {
		t.Errorf("the dashboard has no pid of its own to record: %+v", st)
	}
	ClearLoginState(l, "work")
	if _, ok := ReadLoginState(l, "work"); ok {
		t.Error("ClearLoginState left the marker")
	}
}

func TestLoginRefreshesIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	isolateHome(t)
	stubClaude(t, "new@example.com")
	l, cfg := newTestEnv(t)
	if err := CreateSkeleton(l, cfg, "work", nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(l.AccountsDir(), "work")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Login(l, cfg, "work", &out); err != nil {
		t.Fatalf("Login: %v", err)
	}
	a, _ := cfg.Account("work")
	if a.Login == nil || a.Login.Email != "new@example.com" || !strings.Contains(out.String(), "✓ work (new@example.com) logged in") {
		t.Errorf("after login: %+v / %q", a.Login, out.String())
	}
	if err := Login(l, cfg, "ghost", &out); err == nil || !strings.Contains(err.Error(), "cwm account add ghost") {
		t.Errorf("unknown account: %v", err)
	}
}

func TestLoginFailureReturnsExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	isolateHome(t)
	bin := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = --version ] && { echo '2.1.400 (Claude Code)'; exit 0; }\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	l, cfg := newTestEnv(t)
	_ = CreateSkeleton(l, cfg, "work", nil)
	err := Login(l, cfg, "work", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("err = %v", err)
	}
	if a, _ := cfg.Account("work"); a.Login != nil {
		t.Error("a failed login must not record an identity")
	}
}

func TestRemoveDeleteDir(t *testing.T) {
	l, cfg := seededConfig(t)
	if err := os.MkdirAll(filepath.Join(l.LaunchDir(), "main"), 0o700); err != nil {
		t.Fatal(err)
	}
	MarkLogin(l, "main", LoginDone, "cli", nil)
	res, err := RemoveWith(l, cfg, "main", RemoveOptions{DeleteDir: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Backup == "" || !res.DeletedDir || res.NewDefault != "second" || cfg.DefaultAccount != "second" {
		t.Errorf("res = %+v, default %q", res, cfg.DefaultAccount)
	}
	for _, gone := range []string{filepath.Join(l.AccountsDir(), "main"), filepath.Join(l.LaunchDir(), "main"), LoginStatePath(l, "main")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s still there (%v)", gone, err)
		}
	}
	if _, err := os.Stat(res.Backup); err != nil {
		t.Errorf("backup: %v", err)
	}
	// An account dir outside accounts/ (imported) is never deleted.
	outside := t.TempDir()
	_ = cfg.AddAccount("imported", outside)
	if res, err := RemoveWith(l, cfg, "imported", RemoveOptions{DeleteDir: true}); err != nil || res.DeletedDir || res.KeptDir != outside {
		t.Errorf("imported: %+v %v", res, err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("imported dir deleted: %v", err)
	}
}

func TestDeletable(t *testing.T) {
	l := storage.NewLayoutAt(t.TempDir())
	for _, tt := range []struct {
		dir  string
		want bool
	}{
		{filepath.Join(l.AccountsDir(), "a"), true},
		{filepath.Join(l.LaunchDir(), "a"), true},
		{l.AccountsDir(), false},
		{l.Root, false},
		{filepath.Join(l.WorkspaceDir(), "a"), false},
		{filepath.Join(t.TempDir(), "elsewhere"), false},
		{filepath.Join(l.AccountsDir(), "..", "x"), false},
	} {
		if got := Deletable(l, tt.dir); got != tt.want {
			t.Errorf("Deletable(%s) = %v, want %v", tt.dir, got, tt.want)
		}
	}
}
