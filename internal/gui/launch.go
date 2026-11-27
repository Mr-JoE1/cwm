package gui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/claudeopen"
	"github.com/Mr-JoE1/cwm/internal/config"
)

// launchersView is GET /api/launchers (D113): the launch.claude setting
// and every option with whether it can work on this machine.
type launchersView struct {
	Setting string              `json:"setting"`
	Choices []claudeopen.Choice `json:"choices"`
	// Install is this OS's Claude Code install command, for the warning
	// shown when the selected option can't work.
	Install string `json:"install"`
}

func (s *Server) launchers() []claudeopen.Choice {
	if s.opts.Launchers != nil {
		return s.opts.Launchers()
	}
	capture := true
	if cfg, err := config.Load(s.opts.Layout.ConfigPath()); err == nil {
		capture = cfg.EffectiveMode() == config.ModeCapture
	}
	return claudeopen.Detect(claudeopen.SystemEnv(func() (claudebin.Result, error) { return s.findClaude(false) }, capture))
}

func (s *Server) apiLaunchers(*http.Request) (any, int, error) {
	v := launchersView{Setting: config.LaunchAuto, Choices: s.launchers(), Install: claudebin.InstallCommand(runtime.GOOS)}
	if cfg, err := config.Load(s.opts.Layout.ConfigPath()); err == nil {
		v.Setting = cfg.Launch.Claude
	}
	return v, http.StatusOK, nil
}

// launchWith validates a request's optional `with` against the allow-list
// and resolves the option to use: the override, else the setting. An
// option that cannot work here is a 424 with the reason.
func (s *Server) launchWith(with string) (claudeopen.Choice, int, error) {
	if with != "" && !claudeopen.Valid(with) {
		return claudeopen.Choice{}, http.StatusBadRequest, fmt.Errorf("invalid with %q (want %s)", with, strings.Join(claudeopen.Options, ", "))
	}
	setting := ""
	if cfg, err := config.Load(s.opts.Layout.ConfigPath()); err == nil {
		setting = cfg.Launch.Claude
	}
	id := claudeopen.Effective(with, setting)
	if id == claudeopen.Auto {
		// The claude precheck explains a missing claude in full.
		return claudeopen.Choice{ID: id, Label: "Auto", Available: true, Resume: true}, 0, nil
	}
	c, _ := claudeopen.Pick(s.launchers(), id)
	if !c.Available {
		cmd := ""
		if id == claudeopen.CLI {
			cmd = claudebin.InstallCommand(s.goos())
		}
		return c, http.StatusFailedDependency, &commandError{err: claudeopen.Unavailable(c, s.goos()), command: cmd}
	}
	return c, 0, nil
}

// launchVia opens args (a cwm command line without the program) the way
// the option needs: auto and cli in a terminal, after the claude
// precheck; vscode and desktop as a background cwm run, since they open
// an app and need no terminal — except the desktop hand-off of a
// session, which does (D117). resumeID ("" = none) is the session, for
// the copyable fallback.
func (s *Server) launchVia(c claudeopen.Choice, args []string, resumeID string) (any, int, error) {
	args = append(args, "--with", c.ID)
	if c.ID == claudeopen.Auto || c.ID == claudeopen.CLI {
		res, code, err := s.claudePrecheck()
		if err != nil {
			return nil, code, err
		}
		body, code, err := s.launch(args...)
		if err != nil {
			return nil, code, err
		}
		return s.withClaudeHint(body, res), code, nil
	}
	if c.ID == claudeopen.Desktop && c.Resume && resumeID != "" {
		// claude --desktop needs a terminal on stdin and stdout (D117), so
		// the hand-off runs in one, like auto; the window closes itself
		// when it succeeds and holds the message when it fails.
		body, _, err := s.launch(args...)
		if err == nil {
			lr := body.(launchResult)
			lr.Opened, lr.Note = c.Label, NoteHandOffTerminal
			return lr, http.StatusOK, nil
		}
		// No terminal: the background run finds no TTY either and opens
		// the app in the session's folder, which it reports.
	}
	out, err := s.background(args)
	if err != nil {
		return nil, http.StatusInternalServerError, &commandError{err: fmt.Errorf("could not open %s: %w", c.Label, err), command: resumeCommand(resumeID)}
	}
	lr := launchResult{Launched: true, Command: resumeCommand(resumeID), Opened: c.Label, Note: lastLine(out)}
	if c.ID == claudeopen.Desktop && !c.Resume && resumeID != "" {
		lr.Note = claudeopen.NoteDesktopFolder
	}
	return lr, http.StatusOK, nil
}

// NoteHandOffTerminal is the toast note of a desktop hand-off (D117,
// D123): the terminal window that runs it closes on its own.
const NoteHandOffTerminal = "handed to the Claude app — if it doesn't show the session, see Health → Desktop app"

// resumeCommand is the copy-paste fallback shown with an app launch.
func resumeCommand(id string) string {
	if id == "" {
		return ""
	}
	return "claude --resume " + id
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return shortText(strings.TrimSpace(lines[len(lines)-1]), 300)
}

// backgroundCap bounds a background launch: it may clone the session's
// repo first (no prompt: GIT_TERMINAL_PROMPT=0).
const backgroundCap = 90 * time.Second

// background runs `cwm <args>` without a terminal and returns its output;
// argv goes to exec as an array, never through a shell.
func (s *Server) background(args []string) (string, error) {
	bin := s.opts.CwmPath
	if bin == "" {
		bin = "cwm"
	}
	argv := append([]string{bin}, args...)
	if s.opts.Background != nil {
		return s.opts.Background(argv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), backgroundCap)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	claudeopen.HideConsole(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := lastLine(string(out))
		if msg == "" || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			msg = err.Error()
		}
		return string(out), errors.New(msg)
	}
	return string(out), nil
}
