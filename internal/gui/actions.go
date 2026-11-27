package gui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/claudeopen"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/gui/term"
	"github.com/Mr-JoE1/cwm/internal/launcher"
	"github.com/Mr-JoE1/cwm/internal/manifest"
	"github.com/Mr-JoE1/cwm/internal/sessions"
	"github.com/Mr-JoE1/cwm/internal/status"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
	"github.com/Mr-JoE1/cwm/internal/upkeep"
)

// Input rules (SPEC-V0.5 B). Every value that ends up in a terminal argv
// or a config file is matched against an allowlist; nothing is "cleaned".
var (
	// accountNameRe is the GUI's account rule; config.ValidAccountName
	// (not all digits) applies on top.
	accountNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	// projectNameRe: repo-style names; no separators, no leading dot or
	// dash (so no traversal and nothing a CLI parser reads as a flag).
	projectNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,99}$`)
	// sessionIDRe: claude session IDs are UUIDs; the shape check only
	// bounds input before the real lookup decides.
	// folderNameRe: a capture-mode projects/ folder (Claude Code's
	// path-derived name: [A-Za-z0-9-], up to 200 chars plus a hash).
	folderNameRe = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,254}$`)
	sessionIDRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,127}$`)
	// deviceNameRe: optional human label for a syncthing peer.
	deviceNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)
)

// commandError carries the copyable command of a launch that did not
// happen; serveAPI copies it into apiError.Command.
type commandError struct {
	err     error
	command string
}

func (e *commandError) Error() string { return e.err.Error() }
func (e *commandError) Unwrap() error { return e.err }

// launchResult is the body of a successful terminal launch.
type launchResult struct {
	Launched bool   `json:"launched"`
	Command  string `json:"command"`
	// ClaudePath + PathHint: claude was found outside PATH (D49); the
	// dashboard offers the PATH command with a Copy button.
	ClaudePath string `json:"claudePath,omitempty"`
	PathHint   string `json:"pathHint,omitempty"`
	// Opened names the app a launch opened instead of a terminal (D113:
	// "VS Code", "Claude desktop app"); Note is what it said, or what it
	// cannot do.
	Opened string `json:"opened,omitempty"`
	Note   string `json:"note,omitempty"`
}

// decodeBody reads exactly one JSON object into v; unknown fields,
// trailing data and oversized bodies are 400s.
func decodeBody(r *http.Request, v any) (int, error) {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return http.StatusRequestEntityTooLarge, fmt.Errorf("request body too large (limit %d bytes)", maxBodyBytes)
		}
		if errors.Is(err, io.EOF) {
			return http.StatusBadRequest, errors.New("invalid JSON body: empty (send a JSON object)")
		}
		return http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return http.StatusBadRequest, errors.New("invalid JSON body: trailing data after the object")
	}
	return 0, nil
}

func (s *Server) goos() string {
	if s.opts.GOOS != "" {
		return s.opts.GOOS
	}
	return runtime.GOOS
}

// launch opens a terminal running `cwm <args...>`. No terminal → 501 with
// the copyable command; any other failure → 500 with it.
func (s *Server) launch(args ...string) (any, int, error) {
	bin := s.opts.CwmPath
	if bin == "" {
		bin = "cwm"
	}
	argv := append([]string{bin}, args...)
	cmd := term.Display(s.goos(), argv)
	if s.opts.Launcher == nil {
		return nil, http.StatusNotImplemented, &commandError{err: term.ErrNoTerminal, command: cmd}
	}
	// The hold flag travels in argv, not only in the environment: a
	// terminal that hands the command to an already-running instance
	// (wt.exe, gnome-terminal-server) starts it with that instance's
	// environment, and the window closed with the error unread (v0.6.0).
	held := append([]string{bin, term.HoldFlag}, args...)
	if err := s.opts.Launcher.Open(held); err != nil {
		if errors.Is(err, term.ErrNoTerminal) {
			return nil, http.StatusNotImplemented, &commandError{err: term.ErrNoTerminal, command: cmd}
		}
		return nil, http.StatusInternalServerError, &commandError{err: fmt.Errorf("could not open a terminal: %w", err), command: cmd}
	}
	return launchResult{Launched: true, Command: cmd}, http.StatusOK, nil
}

func (s *Server) reserved(name string) bool {
	return s.opts.Reserved != nil && s.opts.Reserved(name)
}

// apiCSRF hands the page its derived CSRF value (never the launch token).
func (s *Server) apiCSRF(*http.Request) (any, int, error) {
	_, csrf := s.currentToken()
	return map[string]string{"csrf": csrf}, http.StatusOK, nil
}

func (s *Server) apiState(*http.Request) (any, int, error) {
	st, err := status.Setup(s.opts.Layout, s.opts.SyncClient)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	st.Claude = s.claudeState()
	on := s.statsEnabled()
	st.Stats = &on
	return st, http.StatusOK, nil
}

// initRequest has no fields: init in the GUI is always the plain,
// non-interactive path (no account login, no migrate).
type initRequest struct{}

func (s *Server) apiInit(r *http.Request) (any, int, error) {
	var req initRequest
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	res, err := status.Init(s.opts.Layout, io.Discard)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return res, http.StatusOK, nil
}

type accountRequest struct {
	Name string `json:"name"`
}

// apiAccountAdd opens a terminal running `cwm account add <name>`; the
// login happens there, the page polls /api/state for the result.
func (s *Server) apiAccountAdd(r *http.Request) (any, int, error) {
	var req accountRequest
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	if !accountNameRe.MatchString(req.Name) || !config.ValidAccountName(req.Name) {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid account name %q (want lowercase letters, digits and dashes, 1-32 chars, starting with a letter or digit, not all digits)", req.Name)
	}
	cfg, err := config.Load(s.opts.Layout.ConfigPath())
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if _, exists := cfg.Account(req.Name); exists {
		return nil, http.StatusConflict, fmt.Errorf("account %q already exists", req.Name)
	}
	res, code, err := s.claudePrecheck()
	if err != nil {
		return nil, code, err
	}
	body, code, err := s.launch("account", "add", req.Name)
	if err != nil {
		return nil, code, err
	}
	return s.withClaudeHint(body, res), code, nil
}

type resumeRequest struct {
	Project string `json:"project"`
	ID      string `json:"id"`
	// Account resumes as another account, in parallel (v0.8, D80; ""
	// = the default). It must be a configured account.
	Account string `json:"account,omitempty"`
	// With overrides launch.claude for this click (D113).
	With string `json:"with,omitempty"`
}

// apiResume opens a terminal on the D20 passthrough for a session that is
// looked up, never trusted from input: the project must be in the
// manifest and the id a discovered session of that project.
func (s *Server) apiResume(r *http.Request) (any, int, error) {
	var req resumeRequest
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	if !sessionIDRe.MatchString(req.ID) {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid session id %q", req.ID)
	}
	if req.With != "" && !claudeopen.Valid(req.With) {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid with %q (want %s)", req.With, strings.Join(claudeopen.Options, ", "))
	}
	if _, err := status.RequireWorkspace(s.opts.Layout); err != nil {
		return nil, http.StatusConflict, err
	}
	// Capture mode files sessions under Claude Code's path-derived
	// folder (D86): the project is that folder (it may start with "-"),
	// and the resume goes through `cwm sessions resume <id>`, which
	// follows the session to this machine's clone (D91). The folder name
	// never reaches the argv.
	mirror := false
	if cfg, err := config.Load(s.opts.Layout.ConfigPath()); err == nil {
		mirror = upkeep.MirrorEnabled(cfg)
	}
	if mirror {
		if !folderNameRe.MatchString(req.Project) || strings.Contains(req.Project, "..") {
			return nil, http.StatusBadRequest, fmt.Errorf("invalid project folder %q", req.Project)
		}
	} else {
		if !projectNameRe.MatchString(req.Project) || s.reserved(req.Project) {
			return nil, http.StatusBadRequest, fmt.Errorf("invalid project name %q", req.Project)
		}
		m, err := manifest.Load(s.opts.Layout.ManifestPath())
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		if _, ok := m.Lookup(req.Project); !ok {
			return nil, http.StatusNotFound, fmt.Errorf("unknown project %q — it is not in the workspace manifest (open it once with `cwm open %s` from a terminal)", req.Project, req.Project)
		}
	}
	sess, ok, err := sessions.Find(status.ProjectsDir(s.opts.Layout), req.Project, req.ID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !ok {
		return nil, http.StatusNotFound, fmt.Errorf("unknown session %q in project %q", req.ID, req.Project)
	}
	choice, code, err := s.launchWith(req.With)
	if err != nil {
		return nil, code, err
	}
	// auto keeps the D20 passthrough outside capture mode; every other
	// option goes through `cwm sessions resume <id> --with <x>`.
	argv := []string{"sessions", "resume", sess.ID}
	if !mirror && choice.ID == claudeopen.Auto {
		argv = launcher.ResumeCLIArgs(sess.Project, sess.ID)
	}
	if req.Account != "" {
		cfg, err := config.Load(s.opts.Layout.ConfigPath())
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		if _, ok := cfg.Account(req.Account); !ok {
			return nil, http.StatusNotFound, fmt.Errorf("unknown account %q", req.Account)
		}
		argv = append([]string{"--account", req.Account}, argv...)
	}
	if argv[len(argv)-2] != "resume" { // the passthrough has no --with
		res, code, err := s.claudePrecheck()
		if err != nil {
			return nil, code, err
		}
		body, code, err := s.launch(argv...)
		if err != nil {
			return nil, code, err
		}
		return s.withClaudeHint(body, res), code, nil
	}
	return s.launchVia(choice, argv, sess.ID)
}

type pairRequest struct {
	DeviceID  string   `json:"deviceID"`
	Name      string   `json:"name"`
	Addresses []string `json:"addresses,omitempty"` // static, for private sync
}

// apiPair runs the same status.Pair as `cwm pair <id> [name]` in-process.
func (s *Server) apiPair(r *http.Request) (any, int, error) {
	var req pairRequest
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	if err := syncengine.ValidateDeviceID(req.DeviceID); err != nil {
		return nil, http.StatusBadRequest, err
	}
	if req.Name != "" && !deviceNameRe.MatchString(req.Name) {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid device name %q (letters, digits, space, dot, dash, underscore; 1-64 chars)", req.Name)
	}
	if len(req.Addresses) > 8 {
		return nil, http.StatusBadRequest, errors.New("at most 8 addresses")
	}
	for _, a := range req.Addresses {
		if err := syncengine.ValidateAddress(a); err != nil {
			return nil, http.StatusBadRequest, err
		}
	}
	ws, err := status.RequireWorkspace(s.opts.Layout)
	if err != nil {
		return nil, http.StatusConflict, err
	}
	if s.opts.SyncClient == nil {
		return nil, http.StatusServiceUnavailable, errors.New("syncthing not configured")
	}
	cl, err := s.opts.SyncClient()
	if err != nil {
		return nil, http.StatusServiceUnavailable, err
	}
	myID, err := syncengine.MyID(cl)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	if req.DeviceID == myID {
		return nil, http.StatusBadRequest, errors.New("that is THIS device's ID — paste the ID shown on the other device")
	}
	res, err := status.PairAt(cl, req.DeviceID, req.Name, ws, req.Addresses)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	res.DeviceID = myID
	return res, http.StatusOK, nil
}
