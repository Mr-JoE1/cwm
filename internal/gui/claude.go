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

// claudePrecheck refuses a launch that cannot work: the terminal would run
// `cwm account add` (or the resume), fail to find claude and close before
// the user can read why (the v0.6.0 Windows report). 424 carries the
// install command as the copyable command. On success the result tells
// the caller whether to pass on the PATH hint.
func (s *Server) claudePrecheck() (claudebin.Result, int, error) {
	res, err := s.findClaude(true)
	if err == nil {
		return res, 0, nil
	}
	cmd := claudebin.InstallCommand(s.goos())
	if errors.Is(err, claudebin.ErrNotFound) {
		return res, http.StatusFailedDependency, &commandError{
			err:     errors.New("claude (Claude Code) is not installed on this machine, or cwm cannot find it — install it in a terminal with the command below, then try again"),
			command: cmd,
		}
	}
	return res, http.StatusFailedDependency, &commandError{err: err, command: cmd}
}

// withClaudeHint adds the off-PATH note to a successful launch so the
// dashboard can show the PATH command next to "terminal opened".
func (s *Server) withClaudeHint(body any, res claudebin.Result) any {
	lr, ok := body.(launchResult)
	if !ok || res.Path == "" || res.OnPath {
		return body
	}
	lr.ClaudePath = res.Path
	lr.PathHint = claudebin.PathHint(s.goos(), res.Dir())
	return lr
}

// claudeInfo is GET /api/claude: the resolver's answer plus everything the
// dashboard needs to tell the user what to do (D49). Field names are
// snake_case, as specified for this endpoint.
type claudeInfo struct {
	Found       bool                      `json:"found"`
	Path        string                    `json:"path,omitempty"`
	Source      claudebin.Source          `json:"source,omitempty"`
	SourceLabel string                    `json:"source_label,omitempty"`
	OnPath      bool                      `json:"on_path"`
	Hint        string                    `json:"hint"`
	PathCommand string                    `json:"path_command,omitempty"`
	Install     []claudebin.InstallOption `json:"install"`
	Error       string                    `json:"error,omitempty"`
	OS          string                    `json:"os"`
}

// apiClaude resolves afresh (no cache): someone asking explicitly wants
// the current answer, e.g. right after installing Claude Code.
func (s *Server) apiClaude(*http.Request) (any, int, error) {
	goos := s.goos()
	res, err := s.lookupClaude()
	out := claudeInfo{Install: claudebin.InstallOptions(goos), OS: goos}
	switch {
	case err != nil && errors.Is(err, claudebin.ErrNotFound):
		out.Error = err.Error()
		out.Hint = "Claude Code is not installed, or cwm cannot find it. Install it with the official command below, then try again."
	case err != nil:
		out.Error = err.Error()
		out.Hint = "The claude setting points at something that is not an executable file. Fix " + claudebin.EnvClaude + " (or claude_path in config.toml), or unset it."
	default:
		out.Found, out.Path, out.Source, out.SourceLabel, out.OnPath = true, res.Path, res.Source, res.Source.Label(), res.OnPath
		if res.OnPath {
			out.Hint = "claude is on PATH: cwm and your terminals use the same binary."
		} else {
			out.PathCommand = claudebin.PathHint(goos, res.Dir())
			out.Hint = "cwm uses claude at " + res.Path + ", but its folder is not on PATH, so a plain `claude` in a new terminal will not find it. Run the PATH command once, then open a new terminal."
		}
	}
	return out, http.StatusOK, nil
}

// lookupClaude is the read-only resolver (never pins claude_path).
func (s *Server) lookupClaude() (claudebin.Result, error) {
	if s.opts.FindClaude != nil {
		return s.opts.FindClaude()
	}
	return claudebin.Lookup(s.opts.Layout)
}
