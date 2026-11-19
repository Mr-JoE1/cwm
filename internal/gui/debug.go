package gui

import (
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/debugbundle"
	"github.com/Mr-JoE1/cwm/internal/status"
)

// apiErrorKeep is how many recent API errors the agent remembers for the
// debug bundle (D52). Memory only: they are gone with the process.
const apiErrorKeep = 100

// errorRing is a fixed-size ring of recent API errors.
type errorRing struct {
	mu   sync.Mutex
	buf  []debugbundle.APIError
	next int
	full bool
}

func (r *errorRing) add(e debugbundle.APIError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.buf == nil {
		r.buf = make([]debugbundle.APIError, apiErrorKeep)
	}
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// list returns the errors oldest first (non-nil).
func (r *errorRing) list() []debugbundle.APIError {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []debugbundle.APIError{}
	if r.buf == nil {
		return out
	}
	if r.full {
		out = append(out, r.buf[r.next:]...)
	}
	return append(out, r.buf[:r.next]...)
}

// recordError remembers a non-2xx API answer. 401s are not recorded: any
// local process can produce them without a token, and they would push
// the useful entries out.
func (s *Server) recordError(r *http.Request, code int, msg string) {
	if code == http.StatusUnauthorized {
		return
	}
	s.apiErrors.add(debugbundle.APIError{Time: time.Now().UTC(), Method: r.Method, Endpoint: r.URL.Path, Code: code, Message: msg})
}

// plainText is an endpoint body served as text/plain instead of JSON.
type plainText string

// apiDebug is GET /api/debug: the redacted plain-text bundle (D52).
func (s *Server) apiDebug(*http.Request) (any, int, error) {
	return plainText(debugbundle.Build(s.debugSources())), http.StatusOK, nil
}

// debugSources starts from the caller's sources (service status needs the
// CLI's environment) and fills in what only this process knows.
func (s *Server) debugSources() debugbundle.Sources {
	var src debugbundle.Sources
	if s.opts.DebugSources != nil {
		src = s.opts.DebugSources()
	}
	src.From = "dashboard agent"
	src.Version = s.opts.Version
	if src.Layout == nil {
		src.Layout = s.opts.Layout
	}
	if src.SyncClient == nil {
		src.SyncClient = s.opts.SyncClient
	}
	if src.Claude == nil {
		src.Claude = s.lookupClaude
	}
	if src.Doctor == nil {
		root := s.opts.Layout.Root
		src.Doctor = func() []status.Check { return status.Doctor(root, false) }
	}
	if src.GOOS == "" {
		src.GOOS = s.goos()
	}
	bind := s.opts.Bind
	if bind == "" {
		bind = agent.DefaultBind
	}
	src.Agent = &agent.Info{Agent: true, PID: os.Getpid(), Port: s.port, Bind: bind, Started: s.started, Version: s.opts.Version.Version}
	src.APIErrors = s.apiErrors.list()
	return src
}
