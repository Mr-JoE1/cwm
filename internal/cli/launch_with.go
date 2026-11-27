package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/claudeopen"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/desktopapp"
	"github.com/Mr-JoE1/cwm/internal/gui/term"
	"github.com/Mr-JoE1/cwm/internal/launcher"
	"github.com/Mr-JoE1/cwm/internal/sessions"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// withFlag reads `--with <x>` / `--with=<x>` at args[i]; ok = it was one.
func withFlag(args []string, i int) (val string, next int, ok bool, err error) {
	a := args[i]
	switch {
	case a == "--with":
		if i+1 >= len(args) {
			return "", i, true, fmt.Errorf("--with needs a value (%s)", strings.Join(claudeopen.Options, ", "))
		}
		val, next = args[i+1], i+1
	case strings.HasPrefix(a, "--with="):
		val, next = strings.TrimPrefix(a, "--with="), i
	default:
		return "", i, false, nil
	}
	if !claudeopen.Valid(val) {
		return "", next, true, fmt.Errorf("invalid --with %q (want %s)", val, strings.Join(claudeopen.Options, ", "))
	}
	return val, next, true, nil
}

// launchChoice resolves the launch.claude option for this run (the flag,
// else config.toml) and checks it can work here. For auto and cli it
// also tells the claude lookup (launcher.EnvWith).
func launchChoice(layout *storage.Layout, cfg *config.Config, flag string) (claudeopen.Choice, error) {
	with := claudeopen.Effective(flag, cfg.Launch.Claude)
	env := claudeopen.SystemEnv(func() (claudebin.Result, error) { return claudebin.Lookup(layout) }, cfg.EffectiveMode() == config.ModeCapture)
	if with == claudeopen.Auto || with == claudeopen.CLI {
		// The terminal path reports a missing claude itself, with the
		// whole search; only cli needs the env hint.
		_ = os.Setenv(launcher.EnvWith, with)
		return claudeopen.Choice{ID: with}, nil
	}
	c, _ := claudeopen.Pick(claudeopen.Detect(env), with)
	if !c.Available {
		return c, claudeopen.Unavailable(c, env.GOOS)
	}
	return c, nil
}

// openApp opens folder (and session id, "" = none) in the editor or the
// desktop app (the options that need no terminal). desktopResume is the
// caller's way to hand a session to the app (`claude --desktop --resume`
// through the launcher, which sets the account env and the folder).
func (e *Env) openApp(c claudeopen.Choice, folder, id string, desktopResume func() error) error {
	goos := claudeopen.SystemEnv(nil, true).GOOS
	var steps []claudeopen.Step
	var done string
	switch c.ID {
	case claudeopen.VSCode:
		ed, _ := claudeopen.FindEditor(claudeopen.SystemEnv(nil, true))
		steps = claudeopen.EditorSteps(goos, ed, folder, id)
		done = "opened " + orHere(folder) + " in " + ed.Name
		if id != "" {
			done += "; its Claude Code tab resumes session " + sessionID8(id) +
				"\n  if it starts a new conversation instead, run this in that folder: claude --resume " + id
		}
	case claudeopen.Desktop:
		if id != "" && c.Resume {
			if e.DryRun {
				fmt.Fprintf(e.Stdout, "would run in %s: claude --desktop --resume %s\n", folder, id)
				return nil
			}
			return e.handOff(goos, folder, id, desktopResume)
		}
		steps = []claudeopen.Step{{Argv: claudeopen.OpenURL(goos, claudeopen.DesktopURI(folder))}}
		done = "opened the Claude desktop app in " + orHere(folder)
		if id != "" {
			done += " (" + claudeopen.NoteDesktopFolder + ")\n  to resume it in a terminal instead: claude --resume " + id
		}
	}
	if e.DryRun {
		for _, s := range steps {
			fmt.Fprintf(e.Stdout, "would run: %s\n", strings.Join(s.Argv, " "))
		}
		return nil
	}
	if err := runSteps(steps); err != nil {
		return err
	}
	if e.Account != "" {
		fmt.Fprintln(e.Stderr, "note: "+c.Label+" signs in on its own; --account applies to terminal launches only")
	}
	fmt.Fprintln(e.Stdout, done)
	return nil
}

// runSteps starts app steps (claudeopen.Run); a seam for tests.
var runSteps = func(steps []claudeopen.Step) error { return claudeopen.Run(steps, nil) }

// handOffTTY reports whether `claude --desktop` can run here: it needs a
// terminal on both stdin and stdout (D117). A seam for tests.
var handOffTTY = func() bool { return isConsole(os.Stdin) && isConsole(os.Stdout) }

// handOffLinger is how long a dashboard-opened window stays after a
// successful hand-off, so its messages can be read.
var handOffLinger = 4 * time.Second

// handOffHeld: this window was opened by the dashboard (term.HoldFlag or
// term.HoldEnv), so it closes on its own when cwm exits.
var handOffHeld = func() bool { return holdRequested.Load() || os.Getenv(term.HoldEnv) == "1" }

// desktopPrep runs right before a desktop hand-off (D123): it shares the
// session's Code-tab entry into the list of the account the app is
// signed in to (D124), and notes when the app is signed in to another
// account than the one the session ran under.
func (e *Env) desktopPrep(cfg *config.Config, sess sessions.Session) {
	st := desktopStatus(cfg)
	if st.State == desktopapp.StateNone {
		return
	}
	if cfg.Features.DesktopSessionShare {
		_, line, err := desktopapp.Sync(st, desktopapp.ShareOptions{Session: sess.ID})
		if line != "" {
			fmt.Fprintln(e.Stdout, line)
		}
		if err != nil {
			fmt.Fprintln(e.Stderr, "note: Code-tab sharing: "+err.Error())
		}
	}
	owner := sessions.TranscriptOwner(sess.Path)
	if owner == "" || owner == st.UUID() {
		return
	}
	ran := accounts.UUIDMap(cfg)[owner]
	if ran == "" {
		ran = "another account"
	}
	fmt.Fprintln(e.Stderr, "note: the Claude app is signed in as "+st.Who()+", but this session ran as "+ran)
}

// handOff runs `claude --desktop --resume <id>` (resume) when this process
// has a terminal. Without one (the dashboard's no-terminal fallback, a
// pipe) or when the hand-off fails, it opens the app in the folder
// instead and says so; a failed hand-off still returns an error, so a
// held terminal window stays open with the message.
func (e *Env) handOff(goos, folder, id string, resume func() error) error {
	tty := handOffTTY()
	why := "no terminal to hand the session over from"
	if tty {
		err := resume()
		if err == nil {
			fmt.Fprintln(e.Stdout, "handed session "+sessionID8(id)+" to the Claude desktop app")
			if handOffHeld() {
				// A window the dashboard opened would close with claude's
				// "Opening session …" unread (D123).
				fmt.Fprintf(e.Stdout, "this window closes in %d s\n", int(handOffLinger/time.Second))
				time.Sleep(handOffLinger)
			}
			return nil
		}
		why = err.Error()
	}
	if err := runSteps([]claudeopen.Step{{Argv: claudeopen.OpenURL(goos, claudeopen.DesktopURI(folder))}}); err != nil {
		return fmt.Errorf("could not hand the session to the Claude desktop app (%s), nor open the app: %w — resume it in a terminal: claude --resume %s", why, err, id)
	}
	msg := "couldn't hand the session over (" + why + "); opened the Claude app in " + orHere(folder) + " — to resume it: claude --resume " + id
	if tty {
		return errors.New(msg)
	}
	fmt.Fprintln(e.Stdout, msg)
	return nil
}

func orHere(folder string) string {
	if folder == "" {
		return "a new window"
	}
	return folder
}

// runFolder is the folder `cwm run --with vscode|desktop` opens: the
// current one, unless that is the home or root folder (the dashboard's
// background run), where a new window is the better guess.
func runFolder() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	home, _ := os.UserHomeDir()
	if wd == home || filepath.Dir(wd) == wd {
		return ""
	}
	return wd
}
