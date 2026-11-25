// Package gui is the embedded localhost dashboard (v0.5, D23/D24): stdlib
// net/http serving the static files under web/ plus a small JSON API whose
// bodies are the internal/status structs (D26). Security model (D24):
// bind 127.0.0.1 only, a random per-launch token, token→cookie exchange on
// first load, every /api/* request authenticated, state-changing requests
// POST-only with an X-CWM-CSRF header, and a Host allowlist as the
// DNS-rebinding guard. V0.5-B adds the setup/action endpoints (actions.go);
// interactive work (login, claude itself) runs in a real terminal window
// opened through the term.Launcher seam (D27).
package gui

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/debugbundle"
	"github.com/Mr-JoE1/cwm/internal/gui/term"
	"github.com/Mr-JoE1/cwm/internal/migrate"
	"github.com/Mr-JoE1/cwm/internal/stats"
	"github.com/Mr-JoE1/cwm/internal/status"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
)

// Default port range: the first free port wins (SPEC-V0.5 A.3).
const (
	DefaultPortFirst = 47800
	DefaultPortLast  = 47809
)

// tokenBytes is the D24 token size (hex-encoded to 64 chars).
const tokenBytes = 32

// CSRFHeader must carry the token on every state-changing request.
const CSRFHeader = "X-CWM-CSRF"

// maxBodyBytes bounds every POST body; the action payloads are tiny.
const maxBodyBytes = 16 << 10

// defaultSessionsLimit caps /api/sessions unless ?limit= says otherwise.
const defaultSessionsLimit = 50

//go:embed web
var webFS embed.FS

// persistentCookieAge is the agent-mode cookie lifetime (D31): long enough
// that a bookmarked dashboard keeps working, bounded so a stolen cookie
// does not live forever.
const persistentCookieAge = 30 * 24 * time.Hour

// Options carries everything the handlers read. SyncClient is a seam so
// tests inject a fake syncthing; it is called per request that needs it.
// Launcher opens terminal windows (D27; tests inject a fake, nil = none
// available → 501). CwmPath is argv[0] of every terminal command ("" =
// "cwm"). Reserved reports names cwm commands own (cli.IsReserved).
type Options struct {
	Version    status.Version
	Layout     *storage.Layout
	ReposRoot  string
	SyncClient func() (syncengine.Client, error)
	Launcher   term.Launcher
	CwmPath    string
	GOOS       string
	Reserved   func(name string) bool
	// FindClaude resolves claude for the wizard and the Add-account
	// precheck (D49); nil = claudebin.Lookup (poll) / claudebin.Find (precheck).
	FindClaude func() (claudebin.Result, error)
	// DebugSources seeds GET /api/debug with what only the CLI can build
	// (service status needs its environment); the server adds itself,
	// its API errors and anything left unset (D52).
	DebugSources func() debugbundle.Sources

	// v0.6 agent mode (D31-D33). Bind is the listen address ("" =
	// 127.0.0.1); AllowHosts extends the Host allow-list (H matches H:port,
	// H:P matches exactly); PersistentCookie gives the token cookie a
	// Max-Age so a bookmark survives browser restarts. EventsPoll and
	// Heartbeat tune /api/events (0 = 5s / 15s).
	Bind             string
	AllowHosts       []string
	PersistentCookie bool
	EventsPoll       time.Duration
	Heartbeat        time.Duration

	// v0.7.1 stats (D62, D64). StatsAccounts maps accountUuid → cwm
	// account name (nil = everything unattributed); StatsInterval is the
	// shortest time between two scans (0 = stats.MinInterval, 60 s).
	StatsAccounts func() map[string]string
	StatsInterval time.Duration

	// v0.7.2 About (D69). UpdateClient / UpdateURL are seams for the one
	// manual update check (nil / "" = a 5 s client / the GitHub API).
	UpdateClient *http.Client
	UpdateURL    string

	// v0.8 default-account switch (D79); nil = the platform credential
	// store and process finder.
	Switcher *accounts.Switcher

	// v0.8 Settings (D95). SettingEffects runs what `cwm config set` runs
	// after saving key (the .stignore, hooks, autostart service, Syncthing
	// privacy); dry only describes it into out. nil = save only. Migrate
	// tunes the engine for the dashboard's mode change (nil = defaults,
	// with the Syncthing folder re-pointed through SyncClient).
	SettingEffects func(cfg *config.Config, key string, dry bool, out io.Writer) error
	Migrate        *migrate.Options
}

// Server is the dashboard http.Handler bound to one port and one token.
type Server struct {
	opts    Options
	port    int
	started time.Time
	static  http.Handler
	// tokMu guards token/csrf: `cwm service rotate-token` swaps them on a
	// running agent (SetToken).
	tokMu sync.RWMutex
	token string
	csrf  string
	// snapshot cache shared by every /api/events stream.
	snapMu sync.Mutex
	snapAt time.Time
	snap   Snapshot
	// actionMu serializes state-changing actions (init, pair, launches):
	// two tabs double-clicking must not race the config files.
	actionMu sync.Mutex
	// claude lookup cache: /api/state is polled every 2 s while the wizard
	// waits for a login, and a miss can cost an `npm prefix -g`.
	claudeMu  sync.Mutex
	claudeAt  time.Time
	claudeRes claudebin.Result
	claudeErr error
	// apiErrors remembers the last API errors for the debug bundle.
	apiErrors errorRing
	// logins is the in-memory account login state (D55).
	logins loginTracker
	// stats is the lazily created stats engine (D64).
	statsOnce sync.Once
	stats     *stats.Engine
	// update remembers the last manual update check (D69).
	update updateState
	// migration is the dashboard's mode-change job (D95).
	migration migrationJob
}

// NewToken returns a fresh 32-byte crypto/rand token, hex-encoded.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gui: generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// New builds a Server for port with the given token (see NewToken).
func New(opts Options, port int, token string) (*Server, error) {
	if len(token) != 2*tokenBytes {
		return nil, fmt.Errorf("gui: token must be %d hex chars, got %d", 2*tokenBytes, len(token))
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("gui: invalid port %d", port)
	}
	if opts.Layout == nil {
		return nil, errors.New("gui: options need a layout")
	}
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, fmt.Errorf("gui: embedded web assets: %w", err)
	}
	return &Server{opts: opts, port: port, started: time.Now().UTC(), token: token, csrf: deriveCSRF(token), static: http.FileServerFS(sub)}, nil
}

// SetToken replaces the token (and its derived CSRF value) on a running
// server: every cookie, bookmark and Bearer header carrying the old one is
// rejected from now on.
func (s *Server) SetToken(token string) error {
	if len(token) != 2*tokenBytes {
		return fmt.Errorf("gui: token must be %d hex chars, got %d", 2*tokenBytes, len(token))
	}
	s.tokMu.Lock()
	defer s.tokMu.Unlock()
	s.token, s.csrf = token, deriveCSRF(token)
	return nil
}

// Token is the current dashboard token.
func (s *Server) Token() string {
	tok, _ := s.currentToken()
	return tok
}

func (s *Server) currentToken() (token, csrf string) {
	s.tokMu.RLock()
	defer s.tokMu.RUnlock()
	return s.token, s.csrf
}

// deriveCSRF is the value the dashboard's JS sends as X-CWM-CSRF. It is an
// HMAC of the launch token, so the page can be handed it (GET /api/csrf)
// without the HttpOnly launch token ever appearing in a response body.
// The raw launch token stays accepted for scripted (curl) callers.
func deriveCSRF(token string) string {
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte("cwm-gui-csrf-v1"))
	return hex.EncodeToString(m.Sum(nil))
}

// URL is the launch URL carrying the token (stripped after first load).
func (s *Server) URL() string {
	tok, _ := s.currentToken()
	return agent.URL(agent.ProbeHost(s.opts.Bind), s.port, tok)
}

// CookieName is per-port: cookies are not port-scoped, so two dashboards
// on different ports must not overwrite each other's token.
func (s *Server) CookieName() string {
	return "cwm_gui_" + strconv.Itoa(s.port)
}

// Listen binds 127.0.0.1 only (D24: never 0.0.0.0). port 0 picks the first
// free port in DefaultPortFirst..DefaultPortLast.
func Listen(port int) (net.Listener, error) {
	if port != 0 {
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("gui: invalid port %d (want 1-65535)", port)
		}
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			return nil, fmt.Errorf("gui: listen on 127.0.0.1:%d: %w", port, err)
		}
		return ln, nil
	}
	var lastErr error
	for p := DefaultPortFirst; p <= DefaultPortLast; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err == nil {
			return ln, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("gui: no free port in %d-%d (pass --port N): %w", DefaultPortFirst, DefaultPortLast, lastErr)
}

// Port reports the TCP port ln is bound to (0 if it is not TCP).
func Port(ln net.Listener) int {
	if a, ok := ln.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// Serve runs h on ln until ctx is cancelled, then shuts down gracefully
// (in-flight requests get up to 5s).
func Serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		// Request contexts derive from ctx, so long-lived /api/events
		// streams end as soon as shutdown starts instead of holding it up.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("gui: serve: %w", err)
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		return fmt.Errorf("gui: shutdown: %w", err)
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("gui: serve: %w", err)
	}
	return nil
}

// ServeHTTP applies the D24 gates in order: Host allowlist (403), token
// exchange on /?t=, then either the authenticated API or static assets.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; "+
		"connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")

	if !s.hostOK(r.Host) {
		http.Error(w, "forbidden: unexpected Host header", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		s.serveAPI(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/healthz":
		w.Header().Set("Cache-Control", "no-store")
		writeAPI(w, http.StatusOK, map[string]any{"app": "cwm", "ok": true})
		return
	case "/login":
		s.serveLogin(w)
		return
	}
	if r.URL.Path == "/" && r.URL.Query().Has("t") {
		s.exchangeToken(w, r)
		return
	}
	s.static.ServeHTTP(w, r)
}

// hostOK is the DNS-rebinding guard: only the exact loopback authorities
// this server is reachable at are accepted.
func (s *Server) hostOK(host string) bool {
	p := ":" + strconv.Itoa(s.port)
	if host == "127.0.0.1"+p || strings.EqualFold(host, "localhost"+p) {
		return true
	}
	// A concrete non-wildcard bind IP is reachable under its own literal.
	if ip := net.ParseIP(s.opts.Bind); ip != nil && !ip.IsUnspecified() &&
		strings.EqualFold(host, net.JoinHostPort(s.opts.Bind, strconv.Itoa(s.port))) {
		return true
	}
	for _, a := range s.opts.AllowHosts {
		want := a
		if _, _, err := net.SplitHostPort(a); err != nil {
			// Bare host: only with our port (browsers always send the
			// port for a non-default one).
			want = net.JoinHostPort(strings.Trim(a, "[]"), strconv.Itoa(s.port))
		}
		if a != "" && strings.EqualFold(host, want) {
			return true
		}
	}
	return false
}

// exchangeToken turns ?t=<token> into the HttpOnly cookie and redirects to
// "/" so the token leaves the address bar and history.
func (s *Server) exchangeToken(w http.ResponseWriter, r *http.Request) {
	if !s.tokenEqual(r.URL.Query().Get("t")) {
		http.Error(w, "unauthorized: invalid token — run `cwm dashboard`, or sign in at /login", http.StatusUnauthorized)
		return
	}
	tok, _ := s.currentToken()
	c := &http.Cookie{
		Name:     s.CookieName(),
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
	if s.opts.PersistentCookie {
		c.MaxAge = int(persistentCookieAge / time.Second)
	}
	http.SetCookie(w, c)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) tokenEqual(got string) bool {
	tok, _ := s.currentToken()
	return subtle.ConstantTimeCompare([]byte(got), []byte(tok)) == 1
}

// csrfOK accepts the launch token or its derived CSRF value.
func (s *Server) csrfOK(got string) bool {
	_, csrf := s.currentToken()
	return s.tokenEqual(got) || subtle.ConstantTimeCompare([]byte(got), []byte(csrf)) == 1
}

// authenticated accepts the cookie or an Authorization: Bearer header.
func (s *Server) authenticated(r *http.Request) bool {
	if c, err := r.Cookie(s.CookieName()); err == nil && s.tokenEqual(c.Value) {
		return true
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return s.tokenEqual(strings.TrimPrefix(auth, "Bearer "))
	}
	return false
}

// apiError is the body of every non-2xx /api/* response. Command is the
// copyable terminal command when a launch could not happen (501).
type apiError struct {
	Error   string `json:"error"`
	Command string `json:"command,omitempty"`
}

// endpoint produces a status struct (or an HTTP status + error).
type endpoint func(r *http.Request) (any, int, error)

// route is one /api path: a read-only GET handler, a POST action, or both.
type route struct {
	get  endpoint
	post endpoint
	// put is a settings write (v0.8 PUT /api/settings): same CSRF, JSON
	// and serialisation rules as post.
	put endpoint
}

func (s *Server) routes() map[string]route {
	return map[string]route{
		"/api/version":              {get: s.apiVersion},
		"/api/projects":             {get: s.apiProjects},
		"/api/sessions":             {get: s.apiSessions},
		"/api/devices":              {get: s.apiDevices},
		"/api/wip":                  {get: s.apiWip},
		"/api/doctor":               {get: s.apiDoctor},
		"/api/csrf":                 {get: s.apiCSRF},
		"/api/state":                {get: s.apiState},
		"/api/accounts":             {get: s.apiAccounts, post: s.apiAccountAdd},
		"/api/accounts/add":         {post: s.apiAccountsAdd},
		"/api/accounts/login":       {post: s.apiAccountsLogin},
		"/api/accounts/set-default": {post: s.apiAccountsSetDefault},
		"/api/accounts/remove":      {post: s.apiAccountsRemove},
		"/api/init":                 {post: s.apiInit},
		"/api/sessions/resume":      {post: s.apiResume},
		"/api/pair":                 {post: s.apiPair},
		"/api/agent":                {get: s.apiAgent},
		"/api/claude":               {get: s.apiClaude},
		"/api/debug":                {get: s.apiDebug},
		"/api/stats":                {get: s.apiStats},
		"/api/about":                {get: s.apiAbout},
		"/api/about/check-update":   {post: s.apiCheckUpdate},
		"/api/conflicts":            {get: s.apiConflicts},
		"/api/conflicts/resolve":    {post: s.apiConflictsResolve},
		"/api/settings":             {get: s.apiSettings, put: s.apiSettingsPut},
		"/api/migrate":              {get: s.apiMigrateStatus},
		"/api/migrate/preview":      {post: s.apiMigratePreview},
		"/api/migrate/apply":        {post: s.apiMigrateApply},
		"/api/accounts/run":         {post: s.apiAccountsRun},
	}
}

func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authenticated(r) {
		writeAPI(w, http.StatusUnauthorized, apiError{Error: "unauthorized — run `cwm dashboard`, or sign in at /login"})
		return
	}
	readOnly := r.Method == http.MethodGet || r.Method == http.MethodHead
	if !readOnly {
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			w.Header().Set("Allow", "GET, HEAD, POST")
			s.writeError(w, r, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
			return
		}
		if !s.csrfOK(r.Header.Get(CSRFHeader)) {
			s.writeError(w, r, http.StatusForbidden, apiError{Error: "forbidden: missing or invalid " + CSRFHeader + " header"})
			return
		}
	}
	if r.URL.Path == "/api/events" && readOnly {
		s.serveEvents(w, r)
		return
	}
	rt, ok := s.routes()[r.URL.Path]
	if !ok {
		s.writeError(w, r, http.StatusNotFound, apiError{Error: "no such endpoint " + r.URL.Path})
		return
	}
	ep := rt.get
	switch r.Method {
	case http.MethodPost:
		ep = rt.post
	case http.MethodPut:
		ep = rt.put
	}
	if ep == nil && r.Method == http.MethodPut {
		w.Header().Set("Allow", "GET, HEAD, POST")
		s.writeError(w, r, http.StatusMethodNotAllowed, apiError{Error: "method not allowed (PUT is only for /api/settings)"})
		return
	}
	if ep == nil {
		if readOnly {
			w.Header().Set("Allow", "POST")
			s.writeError(w, r, http.StatusMethodNotAllowed, apiError{Error: "method not allowed (action endpoint: POST only)"})
		} else {
			w.Header().Set("Allow", "GET, HEAD")
			s.writeError(w, r, http.StatusMethodNotAllowed, apiError{Error: "method not allowed (read-only endpoint)"})
		}
		return
	}
	if !readOnly {
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			s.writeError(w, r, http.StatusUnsupportedMediaType, apiError{Error: r.Method + " body must be Content-Type: application/json"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		s.actionMu.Lock()
		defer s.actionMu.Unlock()
	}
	body, code, err := ep(r)
	if err != nil {
		resp := apiError{Error: err.Error()}
		var ce *commandError
		if errors.As(err, &ce) {
			resp.Command = ce.command
		}
		s.writeError(w, r, code, resp)
		return
	}
	writeAPI(w, code, body)
}

// writeError answers with an apiError and remembers it for the debug
// bundle (D52).
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, code int, e apiError) {
	s.recordError(r, code, e.Error)
	writeAPI(w, code, e)
}

func writeAPI(w http.ResponseWriter, code int, v any) {
	if text, ok := v.(plainText); ok {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, string(text))
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		code = http.StatusInternalServerError
		data = []byte(`{"error":"encode response"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(append(data, '\n'))
}

// apiAgent reports who serves this dashboard (v0.6): the probe a second
// launch, `cwm dashboard` and `cwm service status|stop` use.
func (s *Server) apiAgent(*http.Request) (any, int, error) {
	bind := s.opts.Bind
	if bind == "" {
		bind = agent.DefaultBind
	}
	return agent.Info{
		Agent:   true,
		PID:     os.Getpid(),
		Port:    s.port,
		Bind:    bind,
		Started: s.started,
		Version: s.opts.Version.Version,
	}, http.StatusOK, nil
}

func (s *Server) apiVersion(*http.Request) (any, int, error) {
	return s.opts.Version, http.StatusOK, nil
}

func (s *Server) apiProjects(*http.Request) (any, int, error) {
	list, err := status.Projects(s.opts.Layout)
	if err != nil {
		var nw *status.NoWorkspaceError
		if errors.As(err, &nw) {
			return nil, http.StatusConflict, err
		}
		return nil, http.StatusInternalServerError, err
	}
	return list, http.StatusOK, nil
}

func (s *Server) apiSessions(r *http.Request) (any, int, error) {
	q := r.URL.Query()
	limit := defaultSessionsLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, http.StatusBadRequest, fmt.Errorf("limit must be a non-negative number, got %q", v)
		}
		limit = n
	}
	return status.Sessions(status.ProjectsDir(s.opts.Layout), q.Get("project"), limit), http.StatusOK, nil
}

// devicesNotConfigured is GET /api/devices while Syncthing is not set up
// (or not running) on this machine: a normal pre-pairing state, so 200
// and not an API error (D58). A reachable Syncthing still answers the
// device array.
type devicesNotConfigured struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Hint   string `json:"hint"`
}

const devicesHint = "Syncthing keeps the workspace in sync between your devices, peer to peer. It is optional: install Syncthing, then run `cwm pair --start` on both devices."

func (s *Server) apiDevices(*http.Request) (any, int, error) {
	if s.opts.SyncClient == nil {
		return devicesNotConfigured{Status: "not_configured", Error: "syncthing not configured", Hint: devicesHint}, http.StatusOK, nil
	}
	cl, err := s.opts.SyncClient()
	if err != nil {
		return devicesNotConfigured{Status: "not_configured", Error: err.Error(), Hint: devicesHint}, http.StatusOK, nil
	}
	list, err := status.Devices(cl)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	return list, http.StatusOK, nil
}

func (s *Server) apiWip(*http.Request) (any, int, error) {
	list, err := status.Dirty(s.opts.ReposRoot)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return list, http.StatusOK, nil
}

// apiDoctor never runs the SSH probe: the GUI stays local-only (D9).
func (s *Server) apiDoctor(*http.Request) (any, int, error) {
	return status.Doctor(s.opts.Layout.Root, false), http.StatusOK, nil
}
