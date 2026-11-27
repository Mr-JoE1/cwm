package gui

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/claudeopen"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/desktopapp"
	"github.com/Mr-JoE1/cwm/internal/gui/term"
	"github.com/Mr-JoE1/cwm/internal/status"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// accountView is one GET /api/accounts row (D56): the v0.5 identity row
// (D12: no account dir, no credential content) plus login state and
// freshness. New fields are snake_case, like /api/claude.
type accountView struct {
	accounts.Row
	IsDefault        bool                 `json:"is_default"`
	HasRecordedLogin bool                 `json:"has_recorded_login"`
	LastUsed         string               `json:"last_used,omitempty"`
	LoginStatus      accounts.LoginStatus `json:"login_status"`
	LoginError       string               `json:"login_error,omitempty"`
	LoginUpdated     string               `json:"login_updated,omitempty"`
	// ConfigDirReady is set on the default account only.
	ConfigDirReady *bool `json:"config_dir_ready,omitempty"`
	// RunningNow is set when cwm can tell which account a running claude
	// uses (Linux: its CLAUDE_CONFIG_DIR); absent = unknown (D95).
	RunningNow *bool `json:"running_now,omitempty"`
	// Parallel is true for an account that is not the default: it runs
	// beside the default from its login-only dir (`cwm run --account`).
	Parallel bool `json:"parallel"`
}

// runningNow attributes running claude processes to accounts by the
// CLAUDE_CONFIG_DIR they run with. nil = not known on this platform.
func runningNow(procs []accounts.ClaudeProc, live claudepaths.Paths) *bool {
	on := false
	for _, p := range procs {
		if !p.EnvKnown {
			return nil
		}
		dir := p.ConfigDir
		switch {
		case live.Env == "" && (dir == "" || sameDir(dir, live.Root)):
			on = true
		case live.Env != "" && dir != "" && sameDir(dir, live.Env):
			on = true
		}
	}
	return &on
}

func sameDir(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// loginTracker is the agent's in-memory view of every account's login
// (D55). The marker files are the shared truth (the CLI in the terminal
// writes them); the tracker keeps the last derived state, persists the
// transitions only the agent can see (a dead pid, a timeout) and lets the
// event stream notice changes.
type loginTracker struct {
	mu    sync.Mutex
	state map[string]accounts.LoginState
}

func (t *loginTracker) set(name string, st accounts.LoginState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == nil {
		t.state = map[string]accounts.LoginState{}
	}
	t.state[name] = st
}

func (t *loginTracker) forget(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.state, name)
}

// loginState derives a's state, persists a transition and remembers it.
func (s *Server) loginState(a config.Account) accounts.LoginState {
	st, changed := accounts.DeriveLogin(s.opts.Layout, a)
	if changed {
		_ = accounts.WriteLoginState(s.opts.Layout, a.Name, st)
	}
	s.logins.set(a.Name, st)
	return st
}

// markLogin records what the agent itself started (or failed to start).
func (s *Server) markLogin(name string, status accounts.LoginStatus, err error) {
	accounts.MarkLogin(s.opts.Layout, name, status, "dashboard", err)
	if st, ok := accounts.ReadLoginState(s.opts.Layout, name); ok {
		s.logins.set(name, st)
	}
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// accountViews builds the GET /api/accounts rows in config order.
func (s *Server) accountViews() ([]accountView, error) {
	cfg, err := config.Load(s.opts.Layout.ConfigPath())
	if err != nil {
		return nil, err
	}
	rows := accounts.Rows(accounts.Gather(cfg, s.opts.Layout))
	out := make([]accountView, 0, len(rows))
	procs := s.switcher().ListProcs()
	// A copy: the live paths only need the mode's root, and this read
	// must not re-point the server's layout.
	l := storage.NewLayoutAt(s.opts.Layout.Root)
	_ = claudepaths.Apply(l, cfg)
	for i, a := range cfg.Accounts {
		st := s.loginState(a)
		v := accountView{
			Row:              rows[i],
			IsDefault:        a.Name == cfg.DefaultAccount,
			HasRecordedLogin: accounts.HasRecordedLogin(a),
			LastUsed:         rfc3339(accounts.LastUsed(s.opts.Layout, a.Name)),
			LoginStatus:      st.Status,
			LoginError:       st.Error,
			LoginUpdated:     rfc3339(st.Updated),
		}
		if v.IsDefault {
			ready := accounts.ConfigDirReady(a)
			v.ConfigDirReady = &ready
		}
		v.Parallel = !v.IsDefault
		if live, err := claudepaths.LiveFor(l, cfg, a.Name); err == nil {
			v.RunningNow = runningNow(procs, live)
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Server) apiAccounts(*http.Request) (any, int, error) {
	list, err := s.accountViews()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return list, http.StatusOK, nil
}

// accountLaunch is the body of a successful add/login launch.
type accountLaunch struct {
	launchResult
	Account     string               `json:"account"`
	LoginStatus accounts.LoginStatus `json:"login_status"`
}

// startLogin marks the login in progress and opens the terminal running
// `cwm account login <name>`. The command in the window reports back
// through the marker itself (D55). A launch that cannot happen leaves no
// in-progress state behind: 501 removes the marker (nothing runs until
// the user runs the command), any other failure marks it failed.
func (s *Server) startLogin(name string, res claudebin.Result) (any, int, error) {
	s.markLogin(name, accounts.LoginInProgress, nil)
	body, code, err := s.launch("account", "login", name)
	if err != nil {
		if errors.Is(err, term.ErrNoTerminal) {
			accounts.ClearLoginState(s.opts.Layout, name)
			s.logins.forget(name)
		} else {
			s.markLogin(name, accounts.LoginFailed, err)
		}
		return nil, code, err
	}
	lr, _ := s.withClaudeHint(body, res).(launchResult)
	return accountLaunch{launchResult: lr, Account: name, LoginStatus: accounts.LoginInProgress}, code, nil
}

type accountNameRequest struct {
	Name string `json:"name"`
}

// apiAccountsAdd is POST /api/accounts/add: validate, create the account
// skeleton (dir + config entry, login "never"), then start the login in a
// terminal (D56). A missing claude refuses before anything is created.
func (s *Server) apiAccountsAdd(r *http.Request) (any, int, error) {
	var req accountNameRequest
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	cfg, err := config.Load(s.opts.Layout.ConfigPath())
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if err := accounts.ValidateNewName(req.Name, cfg, s.opts.Reserved); err != nil {
		var ee *accounts.ExistsError
		if errors.As(err, &ee) {
			return nil, http.StatusConflict, err
		}
		return nil, http.StatusBadRequest, err
	}
	if _, err := status.RequireWorkspace(s.opts.Layout); err != nil || !status.Initialized(s.opts.Layout) {
		return nil, http.StatusConflict, errors.New("the workspace is not initialised yet — run the setup (Initialize) first, or `cwm init` in a terminal")
	}
	res, code, err := s.claudePrecheck()
	if err != nil {
		return nil, code, err
	}
	if err := accounts.CreateSkeleton(s.opts.Layout, cfg, req.Name, s.opts.Reserved); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if err := config.Save(s.opts.Layout.ConfigPath(), cfg); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	accounts.ClearLoginState(s.opts.Layout, req.Name) // a stale marker from a CLI attempt
	return s.startLogin(req.Name, res)
}

// apiAccountsLogin is POST /api/accounts/login: re-run the login in a
// fresh terminal — after a failure, a closed window, or to switch the
// login behind an account.
func (s *Server) apiAccountsLogin(r *http.Request) (any, int, error) {
	var req accountNameRequest
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	cfg, err := config.Load(s.opts.Layout.ConfigPath())
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	a, ok := cfg.Account(req.Name)
	if !ok {
		return nil, http.StatusNotFound, fmt.Errorf("unknown account %q", req.Name)
	}
	if accounts.LoginRunning(s.opts.Layout, a) {
		return nil, http.StatusConflict, fmt.Errorf("a login for %q is still running in another terminal window — finish or close it first", req.Name)
	}
	res, code, err := s.claudePrecheck()
	if err != nil {
		return nil, code, err
	}
	return s.startLogin(req.Name, res)
}

// apiAccountsSetDefault is POST /api/accounts/set-default.
func (s *Server) apiAccountsSetDefault(r *http.Request) (any, int, error) {
	var req accountNameRequest
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	cfg, err := config.Load(s.opts.Layout.ConfigPath())
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if _, ok := cfg.Account(req.Name); !ok {
		return nil, http.StatusNotFound, fmt.Errorf("unknown account %q", req.Name)
	}
	res, err := s.switcher().Use(s.opts.Layout, cfg, req.Name)
	if err != nil {
		return nil, http.StatusConflict, err
	}
	if err := config.Save(s.opts.Layout.ConfigPath(), cfg); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	// "default" keeps the v0.6 shape; the rest is additive (D79).
	return map[string]any{"default": req.Name, "saved_back": res.SavedBack, "warning": res.Warning(), "running": len(res.Running)}, http.StatusOK, nil
}

type accountRemoveRequest struct {
	Name                string `json:"name"`
	AlsoDeleteConfigDir bool   `json:"also_delete_config_dir"`
}

type accountRemoveResult struct {
	Removed          string `json:"removed"`
	Backup           string `json:"backup,omitempty"`
	DeletedConfigDir bool   `json:"deleted_config_dir"`
	Default          string `json:"default"`
}

// apiAccountsRemove is POST /api/accounts/remove: the same
// accounts.RemoveWith as `cwm account remove [--delete-dir]` — back up
// first, refuse the last account, promote a new default (D56).
func (s *Server) apiAccountsRemove(r *http.Request) (any, int, error) {
	var req accountRemoveRequest
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	cfg, err := config.Load(s.opts.Layout.ConfigPath())
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	a, ok := cfg.Account(req.Name)
	if !ok {
		return nil, http.StatusNotFound, fmt.Errorf("unknown account %q", req.Name)
	}
	if len(cfg.Accounts) == 1 {
		return nil, http.StatusConflict, errors.New("this is the last account — add another account before removing it")
	}
	if accounts.LoginRunning(s.opts.Layout, a) {
		return nil, http.StatusConflict, fmt.Errorf("a login for %q is still running in a terminal window — finish or close it first", req.Name)
	}
	res, err := accounts.RemoveWith(s.opts.Layout, cfg, req.Name, accounts.RemoveOptions{DeleteDir: req.AlsoDeleteConfigDir, Switch: s.switcher().SwitchFunc(s.opts.Layout), Store: s.switcher().CredStore()})
	if err != nil {
		if errors.Is(err, accounts.ErrLastAccount) {
			return nil, http.StatusConflict, err
		}
		return nil, http.StatusInternalServerError, err
	}
	if err := config.Save(s.opts.Layout.ConfigPath(), cfg); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	s.logins.forget(req.Name)
	return accountRemoveResult{Removed: req.Name, Backup: res.Backup, DeletedConfigDir: res.DeletedDir, Default: cfg.DefaultAccount}, http.StatusOK, nil
}

// apiAccountsRun is POST /api/accounts/run: open a terminal running
// `cwm run --account <name>` — the default account in the workspace root,
// any other one in parallel from its login-only dir (D80).
func (s *Server) apiAccountsRun(r *http.Request) (any, int, error) {
	var req struct {
		Name string `json:"name"`
		With string `json:"with,omitempty"` // D113
	}
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	choice, code, err := s.launchWith(req.With)
	if err != nil {
		return nil, code, err
	}
	cfg, err := config.Load(s.opts.Layout.ConfigPath())
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if _, ok := cfg.Account(req.Name); !ok {
		return nil, http.StatusNotFound, fmt.Errorf("unknown account %q", req.Name)
	}
	note := ""
	if choice.ID == claudeopen.Desktop {
		// The app has its own sign-in (D122): unless it is signed in to
		// this account, the account opens in a terminal (auto).
		r := claudeopen.RouteAccount(req.Name, s.desktopAccount(cfg))
		if note = r.Note; !r.Desktop {
			choice = claudeopen.Choice{ID: claudeopen.Auto, Label: "Auto", Available: true, Resume: true}
		}
	}
	body, code, err := s.launchVia(choice, []string{"run", "--account", req.Name}, "")
	if lr, ok := body.(launchResult); ok && note != "" {
		lr.Note = note
		body = lr
	}
	return body, code, err
}

func (s *Server) desktopAccount(cfg *config.Config) claudeopen.AppAccount {
	if s.opts.DesktopAccount != nil {
		return s.opts.DesktopAccount()
	}
	return desktopapp.ForConfig(cfg).AppAccount()
}

// switcher is the default-account switch seam (Options.Switcher).
func (s *Server) switcher() accounts.Switcher {
	if s.opts.Switcher != nil {
		return *s.opts.Switcher
	}
	return accounts.Switcher{}
}
