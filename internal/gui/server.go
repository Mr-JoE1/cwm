// Package gui is the embedded localhost dashboard (v0.5, D23/D24): stdlib
// net/http serving the static files under web/ plus a small JSON API whose
// bodies are the internal/status structs (D26). Security model (D24):
// bind 127.0.0.1 only, a random per-launch token, token→cookie exchange on
// first load, every /api/* request authenticated, state-changing requests
// POST-only with an X-CWM-CSRF header, and a Host allowlist as the
// DNS-rebinding guard.
package gui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

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

// defaultSessionsLimit caps /api/sessions unless ?limit= says otherwise.
const defaultSessionsLimit = 50

//go:embed web
var webFS embed.FS

// Options carries everything the handlers read. SyncClient is a seam so
// tests inject a fake syncthing; it is called per /api/devices request.
type Options struct {
	Version    status.Version
	Layout     *storage.Layout
	ReposRoot  string
	SyncClient func() (syncengine.Client, error)
}

// Server is the dashboard http.Handler bound to one port and one token.
type Server struct {
	opts   Options
	port   int
	token  string
	static http.Handler
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
	return &Server{opts: opts, port: port, token: token, static: http.FileServerFS(sub)}, nil
}

// URL is the launch URL carrying the token (stripped after first load).
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/?t=%s", s.port, s.token)
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
	return host == "127.0.0.1"+p || strings.EqualFold(host, "localhost"+p)
}

// exchangeToken turns ?t=<token> into the HttpOnly cookie and redirects to
// "/" so the token leaves the address bar and history.
func (s *Server) exchangeToken(w http.ResponseWriter, r *http.Request) {
	if !s.tokenEqual(r.URL.Query().Get("t")) {
		http.Error(w, "unauthorized: invalid token — open the URL printed by `cwm gui`", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.CookieName(),
		Value:    s.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) tokenEqual(got string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
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

// apiError is the body of every non-2xx /api/* response.
type apiError struct {
	Error string `json:"error"`
}

// endpoint produces a status struct (or an HTTP status + error).
type endpoint func(r *http.Request) (any, int, error)

func (s *Server) endpoints() map[string]endpoint {
	return map[string]endpoint{
		"/api/version":  s.apiVersion,
		"/api/projects": s.apiProjects,
		"/api/sessions": s.apiSessions,
		"/api/devices":  s.apiDevices,
		"/api/wip":      s.apiWip,
		"/api/doctor":   s.apiDoctor,
	}
}

func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.authenticated(r) {
		writeAPI(w, http.StatusUnauthorized, apiError{Error: "unauthorized — open the URL printed by `cwm gui`"})
		return
	}
	readOnly := r.Method == http.MethodGet || r.Method == http.MethodHead
	if !readOnly {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, HEAD, POST")
			writeAPI(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
			return
		}
		if !s.tokenEqual(r.Header.Get(CSRFHeader)) {
			writeAPI(w, http.StatusForbidden, apiError{Error: "forbidden: missing or invalid " + CSRFHeader + " header"})
			return
		}
	}
	ep, ok := s.endpoints()[r.URL.Path]
	if !ok {
		writeAPI(w, http.StatusNotFound, apiError{Error: "no such endpoint " + r.URL.Path})
		return
	}
	// Every V0.5-A endpoint is a read-only snapshot; POST actions arrive
	// with V0.5-B.
	if !readOnly {
		w.Header().Set("Allow", "GET, HEAD")
		writeAPI(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed (read-only endpoint)"})
		return
	}
	body, code, err := ep(r)
	if err != nil {
		writeAPI(w, code, apiError{Error: err.Error()})
		return
	}
	writeAPI(w, http.StatusOK, body)
}

func writeAPI(w http.ResponseWriter, code int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		code = http.StatusInternalServerError
		data = []byte(`{"error":"encode response"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(append(data, '\n'))
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

func (s *Server) apiDevices(*http.Request) (any, int, error) {
	if s.opts.SyncClient == nil {
		return nil, http.StatusServiceUnavailable, errors.New("syncthing not configured")
	}
	cl, err := s.opts.SyncClient()
	if err != nil {
		return nil, http.StatusServiceUnavailable, err
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
