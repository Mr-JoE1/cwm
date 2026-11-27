package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/claudeopen"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/launcher"
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
			return desktopResume()
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
	if err := claudeopen.Run(steps, nil); err != nil {
		return err
	}
	if e.Account != "" {
		fmt.Fprintln(e.Stderr, "note: "+c.Label+" signs in on its own; --account applies to terminal launches only")
	}
	fmt.Fprintln(e.Stdout, done)
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
