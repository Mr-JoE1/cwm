// Package status is the single data layer behind every machine-readable
// view (D26): `cwm <cmd> --json` and the GUI's /api/* handlers serialize the
// same structs these snapshot functions return, and the CLI text renderers
// format them — no view re-derives the data on its own.
package status

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/doctor"
	"github.com/Mr-JoE1/cwm/internal/sessions"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
)

// NoWorkspaceError marks a missing workspace dir; its message is the hint
// the launcher prints, so CLI output stays unchanged.
type NoWorkspaceError struct{ Dir string }

func (e *NoWorkspaceError) Error() string {
	return fmt.Sprintf("workspace %s does not exist — run `cwm init` or pair Syncthing so the folder is restored ('cwm doctor' can diagnose)", e.Dir)
}

// requireWorkspace returns the workspace dir or a *NoWorkspaceError.
func requireWorkspace(layout *storage.Layout) (string, error) {
	ws := layout.WorkspaceDir()
	if info, err := os.Stat(ws); err != nil || !info.IsDir() {
		return "", &NoWorkspaceError{Dir: ws}
	}
	return ws, nil
}

// ProjectsDir is where session folders live inside the workspace.
func ProjectsDir(layout *storage.Layout) string {
	return filepath.Join(layout.WorkspaceDir(), "projects")
}

// Project is one `cwm list` row. Modified is the newest session file mtime;
// zero (omitted from JSON) means the project has no sessions yet.
type Project struct {
	Name     string    `json:"name"`
	Sessions int       `json:"sessions"`
	Modified time.Time `json:"modified,omitzero"`
}

// Projects lists the workspace session folders with session counts, sorted
// by name. A workspace without a projects dir yet has no projects (empty,
// non-nil slice); a missing workspace is a *NoWorkspaceError.
func Projects(layout *storage.Layout) ([]Project, error) {
	if _, err := requireWorkspace(layout); err != nil {
		return nil, err
	}
	out := []Project{}
	entries, err := os.ReadDir(ProjectsDir(layout))
	if err != nil {
		return out, nil
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		count, latest := projectStats(filepath.Join(ProjectsDir(layout), ent.Name()))
		out = append(out, Project{Name: ent.Name(), Sessions: count, Modified: latest})
	}
	return out, nil
}

// projectStats counts *.jsonl session files at depth 1 and returns the
// newest mtime (zero when there are none).
func projectStats(dir string) (int, time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, time.Time{}
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
	return count, latest
}

// Session is one `cwm sessions` row; the JSON shape is the v0.4
// `cwm sessions --json` contract (modified as RFC 3339, second precision).
type Session struct {
	Project     string `json:"project"`
	ID          string `json:"id"`
	FirstPrompt string `json:"firstPrompt"`
	Summary     string `json:"summary"`
	Messages    int    `json:"messages"`
	Branch      string `json:"branch"`
	Modified    string `json:"modified"`
}

// FromSession converts a discovered session to its snapshot row.
func FromSession(s sessions.Session) Session {
	return Session{
		Project:     s.Project,
		ID:          s.ID,
		FirstPrompt: s.FirstPrompt,
		Summary:     s.Summary,
		Messages:    s.Messages,
		Branch:      s.Branch,
		Modified:    s.Modified.Format(time.RFC3339),
	}
}

// Sessions lists sessions newest first via internal/sessions (project ""
// = all, limit 0 = no cap). A workspace without a projects dir yet simply
// has no sessions.
func Sessions(projectsDir, project string, limit int) []Session {
	out := []Session{}
	if _, err := os.Stat(projectsDir); err != nil {
		return out
	}
	list, err := sessions.List(projectsDir, project, limit)
	if err != nil {
		return out
	}
	for _, s := range list {
		out = append(out, FromSession(s))
	}
	return out
}

// Device is one `cwm devices` row (a paired peer and its sync health).
type Device struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Connected  bool    `json:"connected"`
	Address    string  `json:"address"`
	InBytes    int64   `json:"inBytes"`
	OutBytes   int64   `json:"outBytes"`
	Completion float64 `json:"completion"`
}

// Devices wraps syncengine.DevicesReport.
func Devices(cl syncengine.Client) ([]Device, error) {
	rows, err := syncengine.DevicesReport(cl)
	if err != nil {
		return nil, err // already syncengine-prefixed and %w-wrapped
	}
	out := make([]Device, 0, len(rows))
	for _, r := range rows {
		out = append(out, Device{
			ID:         r.ID,
			Name:       r.Name,
			Connected:  r.Connected,
			Address:    r.Address,
			InBytes:    r.InBytes,
			OutBytes:   r.OutBytes,
			Completion: r.Completion,
		})
	}
	return out, nil
}

// Check is one doctor outcome (alias: doctor builds them, D26).
type Check = doctor.Check

// Doctor runs the doctor checks against root ("" = $CWM_HOME / ~/.cwm);
// net adds the SSH probe (D9: never without it).
func Doctor(root string, net bool) []Check {
	return doctor.Checks(root, net)
}

// DirtyRepo is one `cwm wip dirty` row.
type DirtyRepo struct {
	Name       string `json:"name"`
	Changed    int    `json:"changed"`
	LastCommit string `json:"lastCommit"`
}

const gitProbeTimeout = 2 * time.Second

// Dirty walks reposRoot (D21: local clones only): only real directories
// containing .git are probed, and a repo that cannot answer git status is
// skipped (never a false "dirty"). A missing root means nothing is dirty.
func Dirty(reposRoot string) ([]DirtyRepo, error) {
	rows := []DirtyRepo{}
	entries, err := os.ReadDir(reposRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return rows, nil
		}
		return nil, fmt.Errorf("read repos root %s: %w", reposRoot, err)
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(reposRoot, ent.Name())
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			continue
		}
		out, err := gitProbe(dir, "status", "--porcelain")
		if err != nil {
			continue
		}
		changed := 0
		for _, line := range strings.Split(out, "\n") {
			if strings.TrimSpace(line) != "" {
				changed++
			}
		}
		if changed == 0 {
			continue
		}
		subject := "(no commits)"
		if out, err := gitProbe(dir, "log", "-1", "--format=%s"); err == nil {
			if s := strings.TrimSpace(out); s != "" {
				subject = s
			}
		}
		rows = append(rows, DirtyRepo{Name: ent.Name(), Changed: changed, LastCommit: subject})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

// gitProbe runs git in dir with a hard timeout so a wedged repo cannot
// stall the whole scan.
func gitProbe(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("git %s in %s timed out after %s: %w", args[0], dir, gitProbeTimeout, ctx.Err())
	}
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %w", args[0], dir, err)
	}
	return string(out), nil
}

// Version is the build identity served by /api/version.
type Version struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	// v0.7.2 (D69): a build without a release version is a dev build;
	// the dashboard then shows "dev build <commit>".
	Dev       bool   `json:"dev,omitempty"`
	Dirty     bool   `json:"dirty,omitempty"`
	BuildDate string `json:"built_at,omitempty"`
	Go        string `json:"go,omitempty"`
}
