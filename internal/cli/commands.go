// Stage B1 commands: hook entry points (wip), doctor, list and wip.
// The launcher (`open` / direct repo form) arrives with stage B2.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Mr-JoE1/cwm/internal/doctor"
	"github.com/Mr-JoE1/cwm/internal/status"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/wip"
)

// silentExit lets a command control its exit code without the dispatcher
// printing an error line — doctor reports its own FAIL lines, exactly like
// bin/doctor.sh (summary + exit 1, nothing on stderr).
type silentExit struct{ code int }

func (e silentExit) Error() string { return fmt.Sprintf("exit %d", e.code) }

// layoutAndDir resolves the cwm home plus the caller's working dir — the
// hooks and `cwm wip` both operate on the repo in the current directory.
func layoutAndDir() (*storage.Layout, string, error) {
	layout, err := storage.NewLayout()
	if err != nil {
		return nil, "", err
	}
	dir, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("cannot determine working dir: %w", err)
	}
	return layout, dir, nil
}

func cmdHook(e *Env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: cwm hook session-start|session-end")
	}
	layout, dir, err := layoutAndDir()
	if err != nil {
		return err
	}
	switch args[0] {
	case "session-start":
		// Hook contract: never fail the session; result is log-only.
		wip.SessionStart(layout, dir)
	case "session-end":
		wip.SessionEnd(layout, dir)
	default:
		return fmt.Errorf("unknown hook %q (want: session-start or session-end)", args[0])
	}
	return nil
}

func cmdWip(e *Env, args []string) error {
	if len(args) > 0 && args[0] == "dirty" {
		return e.runWipDirty(args[1:])
	}
	layout, dir, err := layoutAndDir()
	if err != nil {
		return err
	}
	switch {
	case len(args) == 0:
		res := wip.SessionEnd(layout, dir)
		fmt.Fprintln(e.Stdout, wipSummary(res))
		return nil // the bash kit's WIP hook never fails the session
	case len(args) == 1 && args[0] == "status":
		return wip.SyncStatus(dir, e.Stdout)
	default:
		return fmt.Errorf("usage: cwm wip [status]")
	}
}

func wipSummary(res wip.EndResult) string {
	switch {
	case res.Pushed:
		return "wip: pushed HEAD to " + res.Branch
	case res.Error != "":
		return "wip: error: " + res.Error + " (logged to logs/wip.log)"
	case res.Skipped != "":
		return "wip: skip: " + res.Skipped
	}
	return "wip: done"
}

func cmdDoctor(e *Env, args []string) error {
	net, asJSON := false, false
	for _, a := range args {
		switch a {
		case "--net":
			net = true
		case "--json":
			asJSON = true
		default:
			return fmt.Errorf("usage: cwm doctor [--net] [--json]")
		}
	}
	checks := status.Doctor("", net)
	healthy := true
	if asJSON {
		if err := writeJSON(e.Stdout, checks); err != nil {
			return err
		}
		_, _, nFail := doctor.Counts(checks)
		healthy = nFail == 0
	} else {
		healthy = doctor.Render(e.Stdout, checks)
	}
	if !healthy {
		return silentExit{code: ExitError}
	}
	return nil
}

func cmdList(e *Env, args []string) error {
	asJSON, err := onlyJSONFlag(args, "usage: cwm list [--json]")
	if err != nil {
		return err
	}
	layout, err := storage.NewLayout()
	if err != nil {
		return err
	}
	projects, err := status.Projects(layout)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(e.Stdout, projects)
	}
	fmt.Fprintf(e.Stdout, "workspace projects (%s):\n", status.ProjectsDir(layout))
	if len(projects) == 0 {
		fmt.Fprintln(e.Stdout, "  (none yet — open a repo via 'cwm open <name> new' to create one)")
		return nil
	}
	for _, p := range projects {
		when := "never"
		if !p.Modified.IsZero() {
			when = p.Modified.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(e.Stdout, "%-24s %4d session(s)  last: %s\n", p.Name, p.Sessions, when)
	}
	return nil
}

// onlyJSONFlag accepts an empty arg list or a lone --json.
func onlyJSONFlag(args []string, usage string) (bool, error) {
	switch {
	case len(args) == 0:
		return false, nil
	case len(args) == 1 && args[0] == "--json":
		return true, nil
	default:
		return false, errors.New(usage)
	}
}

// writeJSON emits v as indented JSON plus a trailing newline — the shape is
// the status struct itself, identical to the GUI's /api/* body (D26).
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}
