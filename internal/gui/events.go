package gui

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"slices"
	"time"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/status"
)

// Default /api/events cadence (SPEC-V0.6 A.7).
const (
	defaultEventsPoll = 5 * time.Second
	defaultHeartbeat  = 15 * time.Second
)

// Snapshot is the cheap state /api/events diffs: no network, no git — a
// config read and a projects directory scan (the `cwm list` data, D26).
type Snapshot struct {
	Initialized bool     `json:"initialized"`
	Accounts    []string `json:"accounts"`
	// Logins is "name=status" per account (D55), so a login finishing in
	// its terminal reaches open dashboards as an "accounts" change.
	Logins        []string  `json:"logins"`
	Projects      int       `json:"projects"`
	Sessions      int       `json:"sessions"`
	NewestSession time.Time `json:"newestSession,omitzero"`
}

// changed lists the snapshot sections that differ, in a fixed order; the
// page refetches only the matching endpoints.
func (a Snapshot) changed(b Snapshot) []string {
	var out []string
	if a.Initialized != b.Initialized {
		out = append(out, "initialized")
	}
	if !slices.Equal(a.Accounts, b.Accounts) || !slices.Equal(a.Logins, b.Logins) {
		out = append(out, "accounts")
	}
	if a.Projects != b.Projects {
		out = append(out, "projects")
	}
	if a.Sessions != b.Sessions || !a.NewestSession.Equal(b.NewestSession) {
		out = append(out, "sessions")
	}
	return out
}

func (s *Server) interval(v, def time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return def
}

// snapshot returns the server-wide cached Snapshot, recomputed at most
// once per half poll interval however many streams are open.
func (s *Server) snapshot() Snapshot {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	if !s.snapAt.IsZero() && time.Since(s.snapAt) < s.interval(s.opts.EventsPoll, defaultEventsPoll)/2 {
		return s.snap
	}
	snap := Snapshot{Initialized: status.Initialized(s.opts.Layout), Accounts: []string{}, Logins: []string{}}
	if cfg, err := config.Load(s.opts.Layout.ConfigPath()); err == nil {
		for _, a := range cfg.Accounts {
			snap.Accounts = append(snap.Accounts, a.Name)
			snap.Logins = append(snap.Logins, a.Name+"="+string(s.loginState(a).Status))
		}
	}
	if projects, err := status.Projects(s.opts.Layout); err == nil {
		snap.Projects = len(projects)
		for _, p := range projects {
			snap.Sessions += p.Sessions
			if p.Modified.After(snap.NewestSession) {
				snap.NewestSession = p.Modified
			}
		}
	}
	s.snap, s.snapAt = snap, time.Now()
	return snap
}

// serveEvents is GET /api/events: a Server-Sent Events stream with hello,
// heartbeat and state events. Auth already passed; it is re-checked on
// every poll so a rotated token also ends open streams.
func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeAPI(w, http.StatusInternalServerError, apiError{Error: "streaming unsupported"})
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // reverse proxies must not buffer the stream
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	send := func(event string, v any) bool {
		data, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	if _, err := fmt.Fprint(w, "retry: 3000\n\n"); err != nil {
		return
	}
	prev := s.snapshot()
	info, _, _ := s.apiAgent(r)
	if !send("hello", map[string]any{"snapshot": prev, "agent": info}) ||
		!send("heartbeat", map[string]any{"time": time.Now().UTC()}) {
		return
	}
	hb := time.NewTicker(s.interval(s.opts.Heartbeat, defaultHeartbeat))
	defer hb.Stop()
	poll := time.NewTicker(s.interval(s.opts.EventsPoll, defaultEventsPoll))
	defer poll.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-hb.C:
			if !send("heartbeat", map[string]any{"time": time.Now().UTC()}) {
				return
			}
		case <-poll.C:
			if !s.authenticated(r) {
				send("unauthorized", map[string]any{"error": "token rotated — sign in again"})
				return
			}
			cur := s.snapshot()
			if ch := prev.changed(cur); len(ch) > 0 {
				if !send("state", map[string]any{"changed": ch, "snapshot": cur}) {
					return
				}
				prev = cur
			}
		}
	}
}

// loginStyle is the only style on /login; its hash goes into that page's
// CSP so no 'unsafe-inline' is needed.
const loginStyle = `body{font:16px system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem;color:#1b1f24}` +
	`input{width:100%;font:inherit;padding:.5rem;margin:.5rem 0;box-sizing:border-box}` +
	`button{font:inherit;padding:.5rem 1rem}code{background:#eef1f4;padding:0 .25rem}` +
	`@media (prefers-color-scheme:dark){body{background:#0d1117;color:#e6edf3}code{background:#21262d}}`

var loginStyleHash = func() string {
	sum := sha256.Sum256([]byte(loginStyle))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}()

// serveLogin is GET /login (v0.6): a no-JS form that submits /?t=<token>
// — the way back in once the cookie is gone. The token exchange itself
// stays the only place a token is checked.
func (s *Server) serveLogin(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; style-src "+loginStyleHash+
		"; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>cwm — sign in</title><style>%s</style></head>
<body><h1>cwm dashboard</h1>
<p>Run <code>cwm dashboard</code> to open this page signed in, or paste the dashboard token
(<code>token</code> in <code>%s</code>):</p>
<form method="get" action="/"><label for="t">Token</label>
<input id="t" name="t" type="password" autocomplete="current-password" required minlength="64" maxlength="64" pattern="[0-9a-f]{64}" autofocus>
<button type="submit">Sign in</button></form></body></html>
`, loginStyle, html.EscapeString("~/.cwm/server.toml"))
}
