package gui

import (
	"errors"
	"net/http"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/status"
)

// claudeTTL bounds how stale the wizard's claude status may be: short
// enough that installing Claude Code mid-wizard shows up within one poll
// cycle or two, long enough that polling never spawns npm every 2 s.
const claudeTTL = 5 * time.Second

// findClaude is the cached resolver (D49). fresh skips the cache (the
// Add-account precheck must not act on a stale miss) and is the one call
// that pins an off-PATH hit in config.toml, right before the terminal's
// `cwm account add` reads it; the wizard's poll only looks.
func (s *Server) findClaude(fresh bool) (claudebin.Result, error) {
	s.claudeMu.Lock()
	defer s.claudeMu.Unlock()
	if !fresh && !s.claudeAt.IsZero() && time.Since(s.claudeAt) < claudeTTL {
		return s.claudeRes, s.claudeErr
	}
	find := s.opts.FindClaude
	if find == nil {
		layout := s.opts.Layout
		find = func() (claudebin.Result, error) { return claudebin.Lookup(layout) }
		if fresh {
			find = func() (claudebin.Result, error) { return claudebin.Find(layout) }
		}
	}
	s.claudeRes, s.claudeErr = find()
	s.claudeAt = time.Now()
	return s.claudeRes, s.claudeErr
}

// claudeState is the wizard's view of the claude lookup.
func (s *Server) claudeState() *status.Claude {
	res, err := s.findClaude(false)
	goos := s.goos()
	c := &status.Claude{Install: claudebin.InstallCommand(goos)}
	if err != nil {
		c.Error = err.Error()
		return c
	}
	c.Found, c.Path, c.OnPath = true, res.Path, res.OnPath
	if !res.OnPath {
		c.PathHint = claudebin.PathHint(goos, res.Dir())
	}
	return c
}

// claudePrecheck refuses an Add-account launch that cannot work: the
// terminal would run `cwm account add`, fail to find claude and close
// before the user can read why (the v0.6.0 Windows report). 424 carries
// the install command as the copyable command.
func (s *Server) claudePrecheck() (int, error) {
	_, err := s.findClaude(true)
	if err == nil {
		return 0, nil
	}
	cmd := claudebin.InstallCommand(s.goos())
	if errors.Is(err, claudebin.ErrNotFound) {
		return http.StatusFailedDependency, &commandError{
			err:     errors.New("claude (Claude Code) is not installed on this machine, or cwm cannot find it — install it in a terminal, then press Add account again"),
			command: cmd,
		}
	}
	return http.StatusFailedDependency, &commandError{err: err, command: cmd}
}
