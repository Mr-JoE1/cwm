package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudeopen"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/launcher"
	"github.com/Mr-JoE1/cwm/internal/pathmirror"
	"github.com/Mr-JoE1/cwm/internal/sessions"
	"github.com/Mr-JoE1/cwm/internal/upkeep"
)

// cmdSessionsResume is `cwm sessions resume <id> [--dry-run] [--json]`:
// resume a session by id (or a unique prefix) on this machine. In capture
// mode with features.path_mirror the session is first materialised under
// the folder this machine's clone of its repo produces, cwd rewritten
// (D91), and claude starts in that clone (cloned on demand). Otherwise it
// is the D20 passthrough `cwm open <project> new --resume <id>`.
func cmdSessionsResume(e *Env, args []string) error {
	var id, with string
	dry, asJSON := e.DryRun, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if v, next, ok, err := withFlag(args, i); ok {
			if err != nil {
				return err
			}
			with, i = v, next
			continue
		}
		switch {
		case a == "--dry-run":
			dry = true
		case a == "--json":
			asJSON = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown sessions resume flag %q (usage: cwm sessions resume <id> [--with auto|cli|vscode|desktop] [--dry-run] [--json])", a)
		case id != "":
			return fmt.Errorf("unexpected argument %q (usage: cwm sessions resume <id>)", a)
		default:
			id = a
		}
	}
	if id == "" {
		return fmt.Errorf("usage: cwm sessions resume <id> [--dry-run] [--json]")
	}
	layout, err := claudepaths.NewLayout()
	if err != nil {
		return err
	}
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		return err
	}
	projectsDir := filepath.Join(layout.WorkspaceDir(), "projects")
	sess, err := resolveSessionID(projectsDir, id)
	if err != nil {
		return err
	}
	choice, err := launchChoice(layout, cfg, with)
	if err != nil {
		return err
	}
	app := choice.ID == claudeopen.VSCode || choice.ID == claudeopen.Desktop
	if !upkeep.MirrorEnabled(cfg) {
		if app {
			e.DryRun = e.DryRun || dry
			return e.openApp(choice, sessionFolder(sess), sess.ID, func() error {
				return launcher.Open(sess.Project, launcher.ModeNew, desktopResumeArgs(sess.ID), e.Account, e.Stdout, e.Stderr)
			})
		}
		if dry {
			fmt.Fprintf(e.Stdout, "would run: cwm %s\n", strings.Join(launcher.ResumeCLIArgs(sess.Project, sess.ID), " "))
			return nil
		}
		return openSession(e, sess)
	}
	env := pathmirror.NewEnv(layout)
	plan, err := env.PlanResume(sess.ID)
	if err != nil {
		return err
	}
	if asJSON {
		data, err := json.Marshal(plan)
		if err != nil {
			return fmt.Errorf("encode plan: %w", err)
		}
		fmt.Fprintln(e.Stdout, string(data))
	}
	if plan.Local == "" {
		if plan.SrcRepo != "" && plan.Name != "" {
			return fmt.Errorf("session %s ran in %s, which has no clone here and no repos.tsv entry for %q — clone it under %s (or open it once with claude) and retry",
				sessionID8(sess.ID), plan.SrcRepo, plan.Name, env.ReposRoot)
		}
		return fmt.Errorf("session %s records no working directory; resume it with `claude --resume %s` in its repo", sessionID8(sess.ID), sess.ID)
	}
	if plan.Diverged {
		fmt.Fprintf(e.Stderr, "cwm: session %s was continued on two devices; both copies are kept (cwm doctor) — resuming the newest\n", sessionID8(sess.ID))
	}
	if dry && app {
		e.DryRun = true
		return e.openApp(choice, plan.Local, sess.ID, nil)
	}
	if dry {
		if !asJSON {
			act := "resume in place"
			if plan.NeedsMirror() {
				act = "mirror " + plan.Source.Path + " -> " + filepath.Join(projectsDir, pathmirror.FolderName(plan.Local), sess.ID+".jsonl")
			}
			if !plan.Exists {
				act = "clone " + plan.URL + " -> " + plan.Local + "; " + act
			}
			fmt.Fprintf(e.Stdout, "would %s; then run claude --resume %s in %s\n", act, sess.ID, plan.Local)
		}
		return nil
	}
	if !plan.Exists {
		if err := launcher.Clone(plan.URL, plan.Local, e.Stdout, e.Stderr); err != nil {
			return err
		}
	}
	if plan.Remote != "" {
		_ = env.Paths.Record(plan.Remote, plan.Local)
	}
	res, err := env.Apply(plan)
	if err != nil {
		return err
	}
	if res.Wrote && !asJSON {
		fmt.Fprintf(e.Stdout, "mirrored session %s for %s\n", sessionID8(sess.ID), plan.Local)
	}
	if app {
		return e.openApp(choice, plan.Local, sess.ID, func() error {
			return launcher.OpenDir(plan.Local, desktopResumeArgs(sess.ID), e.Account, e.Stdout, e.Stderr)
		})
	}
	_, extra := launcher.ResumeArgs(sess.ID)
	return launcher.OpenDir(plan.Local, extra, e.Account, e.Stdout, e.Stderr)
}

// desktopResumeArgs hands a session to the Claude desktop app (D113).
func desktopResumeArgs(id string) []string { return []string{"--desktop", "--resume", id} }

// sessionFolder is where a session ran, when that folder exists here
// ("" otherwise: the editor or the app then opens without one).
func sessionFolder(s sessions.Session) string {
	cwd := s.Cwd
	if cwd == "" {
		cwd = sessions.TranscriptCwd(s.Path)
	}
	if fi, err := os.Stat(cwd); cwd != "" && err == nil && fi.IsDir() {
		return cwd
	}
	return ""
}

// resolveSessionID finds a session by full id or unique prefix.
func resolveSessionID(projectsDir, id string) (sessions.Session, error) {
	if _, err := os.Stat(projectsDir); err != nil {
		return sessions.Session{}, fmt.Errorf("no sessions yet (%s does not exist)", projectsDir)
	}
	list, err := sessions.List(projectsDir, "", 0)
	if err != nil {
		return sessions.Session{}, err
	}
	var hits []sessions.Session
	for _, s := range list {
		if s.ID == id {
			return s, nil
		}
		if strings.HasPrefix(s.ID, id) {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 0:
		return sessions.Session{}, fmt.Errorf("unknown session %q (see: cwm sessions)", id)
	case 1:
		return hits[0], nil
	}
	return sessions.Session{}, fmt.Errorf("session prefix %q matches %d sessions — give more of the id", id, len(hits))
}

// cmdSessionsMirror is `cwm sessions mirror [--json]`: one path-mirror
// pass now (the agent runs the same pass on its tick, D91).
func cmdSessionsMirror(e *Env, args []string) error {
	asJSON := false
	for _, a := range args {
		if a != "--json" {
			return fmt.Errorf("usage: cwm sessions mirror [--json]")
		}
		asJSON = true
	}
	layout, err := claudepaths.NewLayout()
	if err != nil {
		return err
	}
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		return err
	}
	if !upkeep.MirrorEnabled(cfg) {
		return fmt.Errorf("path mirroring is off (it runs in capture mode with features.path_mirror on; see: cwm config get features.path_mirror)")
	}
	rep := (&pathmirror.Mirrorer{}).Tick(pathmirror.NewEnv(layout), time.Now())
	if asJSON {
		data, err := json.Marshal(rep)
		if err != nil {
			return fmt.Errorf("encode report: %w", err)
		}
		fmt.Fprintln(e.Stdout, string(data))
	} else {
		fmt.Fprintf(e.Stdout, "checked %d session(s); mirrored %d; diverged %d\n", rep.Checked, len(rep.Mirrored), len(rep.Diverged))
		for _, id := range rep.Diverged {
			fmt.Fprintf(e.Stdout, "  session %s was continued on two devices — both copies kept\n", sessionID8(id))
		}
	}
	if len(rep.Errors) > 0 {
		return fmt.Errorf("path mirror: %s", strings.Join(rep.Errors, "; "))
	}
	return nil
}
