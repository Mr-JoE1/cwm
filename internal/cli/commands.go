// Stage B1 commands: hook entry points (wip), doctor, list and wip.
// The launcher (`open` / direct repo form) arrives with stage B2.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/doctor"
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
	net := false
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "--net":
		net = true
	default:
		return fmt.Errorf("usage: cwm doctor [--net]")
	}
	if !doctor.Run(e.Stdout, "", net) {
		return silentExit{code: ExitError}
	}
	return nil
}

func cmdList(e *Env, _ []string) error {
	layout, err := storage.NewLayout()
	if err != nil {
		return err
	}
	ws := layout.WorkspaceDir()
	if info, err := os.Stat(ws); err != nil || !info.IsDir() {
		return fmt.Errorf("workspace %s does not exist — run `cwm init` or pair Syncthing so the folder is restored ('cwm doctor' can diagnose)", ws)
	}
	projects := filepath.Join(ws, "projects")
	fmt.Fprintf(e.Stdout, "workspace projects (%s):\n", projects)
	entries, err := os.ReadDir(projects)
	if err != nil {
		fmt.Fprintln(e.Stdout, "  (none yet — open a repo via 'cwm open <name> new' to create one)")
		return nil
	}
	found := false
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		found = true
		count, when := projectStats(filepath.Join(projects, ent.Name()))
		fmt.Fprintf(e.Stdout, "%-24s %4d session(s)  last: %s\n", ent.Name(), count, when)
	}
	if !found {
		fmt.Fprintln(e.Stdout, "  (none yet — open a repo via 'cwm open <name> new' to create one)")
	}
	return nil
}

// projectStats counts *.jsonl session files at depth 1 and returns the
// newest mtime formatted like the bash kit's listing.
func projectStats(dir string) (int, string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, "never"
	}
	count := 0
	var latest time.Time
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".jsonl") {
			continue
		}
		count++
		if info, err := ent.Info(); err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	if latest.IsZero() {
		return count, "never"
	}
	return count, latest.Format("2006-01-02 15:04")
}
