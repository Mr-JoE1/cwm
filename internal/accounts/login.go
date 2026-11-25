package accounts

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// LoginStatus is where an account's interactive login stands (D55).
type LoginStatus string

const (
	LoginNever      LoginStatus = "never"
	LoginInProgress LoginStatus = "in_progress"
	LoginDone       LoginStatus = "done"
	LoginFailed     LoginStatus = "failed"
)

// LoginState is the marker under state/account-login/<name>.json. The cwm
// process running the login writes it (in_progress → done | failed); the
// agent writes in_progress before it opens the terminal and persists the
// transitions it derives (a dead pid, a timeout).
type LoginState struct {
	Status  LoginStatus `json:"status"`
	Started time.Time   `json:"started,omitzero"`
	Updated time.Time   `json:"updated,omitzero"`
	// PID is the cwm process running `claude auth login` (0 = the
	// dashboard queued it and no cwm process has reported yet).
	PID int `json:"pid,omitempty"`
	// By is "cli" or "dashboard".
	By    string `json:"by,omitempty"`
	Error string `json:"error,omitempty"`
}

// Login-state timeouts: a terminal that never ran the command, and a
// login nobody finished.
const (
	LoginPickupTimeout = 90 * time.Second
	LoginTimeout       = 15 * time.Minute
)

// loginStateDir holds one marker per account.
func loginStateDir(l *storage.Layout) string {
	return filepath.Join(l.StateDir(), "account-login")
}

// LoginStatePath is the marker file for name.
func LoginStatePath(l *storage.Layout, name string) string {
	return filepath.Join(loginStateDir(l), name+".json")
}

// ReadLoginState loads name's marker (ok=false when there is none or it
// is unreadable — an unreadable marker carries no information).
func ReadLoginState(l *storage.Layout, name string) (LoginState, bool) {
	data, err := os.ReadFile(LoginStatePath(l, name))
	if err != nil {
		return LoginState{}, false
	}
	var st LoginState
	if err := json.Unmarshal(data, &st); err != nil || st.Status == "" {
		return LoginState{}, false
	}
	return st, true
}

// WriteLoginState stores name's marker atomically (0600).
func WriteLoginState(l *storage.Layout, name string, st LoginState) error {
	if !config.ValidAccountName(name) {
		return fmt.Errorf("accounts: invalid account name %q", name)
	}
	if st.Updated.IsZero() {
		st.Updated = time.Now().UTC()
	}
	if err := os.MkdirAll(loginStateDir(l), 0o700); err != nil {
		return fmt.Errorf("accounts: create %s: %w", loginStateDir(l), err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("accounts: encode login state: %w", err)
	}
	if err := storage.WriteFileAtomic(LoginStatePath(l, name), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("accounts: write login state: %w", err)
	}
	return nil
}

// ClearLoginState removes name's marker (missing is fine).
func ClearLoginState(l *storage.Layout, name string) {
	_ = os.Remove(LoginStatePath(l, name))
}

// MarkLogin records a state transition for name, best-effort: the login
// itself matters more than its bookkeeping.
func MarkLogin(l *storage.Layout, name string, status LoginStatus, by string, err error) {
	now := time.Now().UTC()
	st := LoginState{Status: status, Updated: now, By: by}
	prev, ok := ReadLoginState(l, name)
	switch status {
	case LoginInProgress:
		st.Started = now
		if by == "cli" {
			st.PID = os.Getpid()
		}
	default:
		if ok {
			st.Started = prev.Started
		}
	}
	if err != nil {
		st.Error = shortError(err)
	}
	_ = WriteLoginState(l, name, st)
}

// shortError keeps a marker's error to one readable line.
func shortError(err error) string {
	s := strings.TrimSpace(err.Error())
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 300 {
		s = string(r[:299]) + "…"
	}
	return s
}

// HasRecordedLogin is the CLI's signal that an account logged in: an
// identity with an e-mail in config.toml.
func HasRecordedLogin(a config.Account) bool {
	return a.Login != nil && a.Login.Email != ""
}

// Clock and liveness seams for DeriveLogin.
type deriveEnv struct {
	now   time.Time
	alive func(pid int) bool
}

// DeriveLogin turns name's marker plus the recorded-login signal into
// the current state; changed reports that the result differs from the
// stored marker, and the caller should persist it (WriteLoginState).
func DeriveLogin(l *storage.Layout, a config.Account) (st LoginState, changed bool) {
	return deriveLogin(l, a, deriveEnv{now: time.Now().UTC(), alive: processAlive})
}

func deriveLogin(l *storage.Layout, a config.Account, env deriveEnv) (LoginState, bool) {
	recorded := HasRecordedLogin(a)
	stored, ok := ReadLoginState(l, a.Name)
	if !ok {
		if recorded {
			st := LoginState{Status: LoginDone}
			if fi, err := os.Stat(filepath.Join(a.ExpandedDir(), ".credentials.json")); err == nil {
				st.Updated = fi.ModTime().UTC()
			}
			return st, false
		}
		return LoginState{Status: LoginNever}, false
	}
	if stored.Status != LoginInProgress {
		return stored, false
	}
	// In progress: did it finish, die, or stall?
	if recorded {
		if fi, err := os.Stat(filepath.Join(a.ExpandedDir(), ".credentials.json")); err == nil &&
			!fi.ModTime().Before(stored.Started.Add(-2*time.Second)) {
			return LoginState{Status: LoginDone, Started: stored.Started, Updated: env.now, By: stored.By}, true
		}
	}
	fail := func(msg string) (LoginState, bool) {
		return LoginState{Status: LoginFailed, Started: stored.Started, Updated: env.now, By: stored.By, PID: stored.PID, Error: msg}, true
	}
	switch {
	case stored.PID > 0 && env.alive != nil && !env.alive(stored.PID):
		return fail("the login window was closed before the login finished")
	case stored.PID == 0 && env.now.Sub(stored.Started) > LoginPickupTimeout:
		return fail("the terminal did not start the login (no cwm process reported back)")
	case env.now.Sub(stored.Started) > LoginTimeout:
		return fail("the login did not finish within 15 minutes")
	}
	return stored, false
}

// LoginRunning reports whether name has a login whose cwm process is
// still alive (a second login into the same dir would race it).
func LoginRunning(l *storage.Layout, a config.Account) bool {
	st, _ := DeriveLogin(l, a)
	return st.Status == LoginInProgress && st.PID > 0 && processAlive(st.PID)
}

// ReservedAccountWords are the `cwm account` actions; an account named
// like one reads ambiguously in every help text and log line.
var ReservedAccountWords = []string{"add", "list", "use", "remove", "rm", "login"}

// NameError is a rejected account name; the GUI maps it to 400.
type NameError struct{ msg string }

func (e *NameError) Error() string { return e.msg }

// ValidateNewName checks a name for a new account, most specific rule
// first, so the message says what to change. reserved reports cwm's own
// command names (cli.IsReserved); nil skips that rule.
func ValidateNewName(name string, cfg *config.Config, reserved func(string) bool) error {
	bad := func(format string, args ...any) error { return &NameError{msg: fmt.Sprintf(format, args...)} }
	switch {
	case strings.TrimSpace(name) == "":
		return bad("account name is required")
	case len(name) > 32:
		return bad("account name %q is too long (at most 32 characters)", name)
	case strings.ContainsAny(name, `/\`):
		return bad("account name %q must not contain / or \\", name)
	}
	for _, w := range ReservedAccountWords {
		if name == w {
			return bad("account name %q is reserved (a `cwm account` action) — pick another name", name)
		}
	}
	if reserved != nil && reserved(name) {
		return bad("account name %q is reserved (a cwm command) — pick another name", name)
	}
	if !config.ValidAccountName(name) {
		return bad("invalid account name %q (want lowercase letters, digits, dashes and underscores, starting with a letter or digit, not all digits)", name)
	}
	if cfg != nil {
		if _, exists := cfg.Account(name); exists {
			return &ExistsError{Name: name}
		}
	}
	return nil
}

// ExistsError is a duplicate account name; the GUI maps it to 409.
type ExistsError struct{ Name string }

func (e *ExistsError) Error() string {
	return fmt.Sprintf("account %q already exists — to log it in again: cwm account login %s", e.Name, e.Name)
}

// CreateSkeleton registers name with its empty, private account dir and
// no login yet (the dashboard's Add account, D56): the account is visible
// with login status "never" from this moment, and `cwm account login`
// fills it in. cfg is mutated in memory; the caller persists it.
func CreateSkeleton(l *storage.Layout, cfg *config.Config, name string, reserved func(string) bool) error {
	if err := ValidateNewName(name, cfg, reserved); err != nil {
		return err
	}
	dir := filepath.Join(l.AccountsDir(), name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("accounts: create %s: %w", dir, err)
	}
	if err := cfg.AddAccount(name, dir); err != nil {
		return fmt.Errorf("accounts: %w", err)
	}
	return nil
}

// Login re-runs `claude auth login` for an existing account into its own
// dir (recreated when missing) and refreshes the identity in cfg. cfg is
// mutated in memory; the caller persists it.
func Login(l *storage.Layout, cfg *config.Config, name string, stdout io.Writer) error {
	acct, ok := cfg.Account(name)
	if !ok {
		return fmt.Errorf("accounts: account %q not found — add it with: cwm account add %s", name, name)
	}
	res, err := FindClaude(l)
	if err != nil {
		return fmt.Errorf("accounts: %w", err)
	}
	claudebin.Remember(cfg, res)
	claudebin.Hint(stdout, runtime.GOOS, res)
	dir := acct.ExpandedDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("accounts: create %s: %w", dir, err)
	}
	if err := runLogin(res.Path, dir); err != nil {
		return err
	}
	return recordIdentity(cfg, name, dir, stdout, "logged in")
}

// runLogin is the interactive `claude auth login` with CLAUDE_CONFIG_DIR
// pointing at the account dir (stdio inherited).
func runLogin(claude, dir string) error {
	login := exec.Command(claude, "auth", "login")
	login.Env = append(os.Environ(), ClaudeConfigDirEnv+"="+dir)
	login.Stdin, login.Stdout, login.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := login.Run(); err != nil {
		// err already reads "exit status N" for a failed login.
		return fmt.Errorf("accounts: claude auth login failed: %w", err)
	}
	return nil
}

// recordIdentity stores the identity the login left in dir. A login that
// succeeded without an identity is kept with a warning (spec: warn, keep).
func recordIdentity(cfg *config.Config, name, dir string, stdout io.Writer, verb string) error {
	id, idErr := ReadIdentity(filepath.Join(dir, ".claude.json"))
	if idErr != nil || id.Email == "" {
		fmt.Fprintf(stdout, "⚠ %s %s (no login identity found — re-run the login if this looks wrong)\n", name, verb)
		return nil
	}
	if err := cfg.SetAccountLogin(name, config.Login{
		Email:            id.Email,
		AccountUUID:      id.AccountUUID,
		OrganizationName: id.OrganizationName,
		Plan:             id.Plan,
	}); err != nil {
		return fmt.Errorf("accounts: %w", err)
	}
	fmt.Fprintf(stdout, "✓ %s (%s) %s\n", name, id.Email, verb)
	return nil
}

// LastUsed is when name's login-only dir last ran claude (Claude Code
// rewrites .claude.json on every start, D80), or, for a v0.7 layout, when
// its launch dir was last assembled; zero = never launched.
func LastUsed(l *storage.Layout, name string) time.Time {
	if fi, err := os.Stat(filepath.Join(l.AccountsDir(), name, ".claude.json")); err == nil {
		return fi.ModTime().UTC()
	}
	fi, err := os.Stat(filepath.Join(l.LaunchDir(), name))
	if err != nil || !fi.IsDir() {
		return time.Time{}
	}
	return fi.ModTime().UTC()
}

// ConfigDirReady reports whether the account dir exists, so a launch can
// assemble launch/<name> from it.
func ConfigDirReady(a config.Account) bool {
	fi, err := os.Stat(a.ExpandedDir())
	return err == nil && fi.IsDir()
}
