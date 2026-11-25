// Package storage resolves the cwm home layout (D11) and provides shared
// path/slug/version helpers used by the rest of the toolkit.
package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// EnvHome overrides the cwm home dir. config.toml must NOT be able to set it:
// env only, so a wrong config can never redirect every cwm binary at once.
const EnvHome = "CWM_HOME"

const defaultHomeSuffix = ".cwm"

// WorkspaceDirs are the synced workspace directories (canonical item list, D11).
var WorkspaceDirs = []string{
	"projects",
	"skills",
	"agents",
	"commands",
	"rules",
	"plugins",
	"output-styles",
	"scheduled-tasks",
}

// WorkspaceFiles are the synced workspace files (canonical item list, D11).
var WorkspaceFiles = []string{
	"CLAUDE.md",
	"history.jsonl",
	"repos.tsv",
}

// WorkspaceItems returns dirs followed by files, the full linked-into-launch
// item list. mcp.json is deliberately absent: managed, but never linked.
func WorkspaceItems() []string {
	out := make([]string, 0, len(WorkspaceDirs)+len(WorkspaceFiles))
	out = append(out, WorkspaceDirs...)
	out = append(out, WorkspaceFiles...)
	return out
}

// HomeDir returns the cwm home: $CWM_HOME if set, else ~/.cwm.
func HomeDir() (string, error) {
	if h := os.Getenv(EnvHome); h != "" {
		return ExpandTilde(h), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("storage: cannot determine user home dir: %w", err)
	}
	return filepath.Join(home, defaultHomeSuffix), nil
}

// Layout maps the cwm home layout onto concrete paths.
type Layout struct {
	// Root is the cwm home dir ($CWM_HOME or ~/.cwm).
	Root string

	// mu guards workspace: the agent re-points a live layout when the
	// workspace mode changes (D77).
	mu        sync.RWMutex
	workspace string
}

// NewLayout resolves the layout from the environment.
func NewLayout() (*Layout, error) {
	root, err := HomeDir()
	if err != nil {
		return nil, err
	}
	return &Layout{Root: root}, nil
}

// NewLayoutAt builds a layout rooted at an explicit path (tests, dry-runs).
func NewLayoutAt(root string) *Layout {
	return &Layout{Root: root}
}

func (l *Layout) ConfigPath() string { return filepath.Join(l.Root, "config.toml") }

// WorkspaceDir is the workspace root every caller reads and writes:
// sessions, history, the manifest, mcp.json. internal/claudepaths sets it
// from the configured mode (D77); a layout nobody resolved (tests,
// planners) uses the isolated workspace.
func (l *Layout) WorkspaceDir() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.workspace != "" {
		return l.workspace
	}
	return l.IsolatedWorkspaceDir()
}

// SetWorkspaceDir points the layout at a resolved workspace root ("" =
// back to the isolated workspace). Only internal/claudepaths calls it.
func (l *Layout) SetWorkspaceDir(dir string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.workspace = dir
}

// IsolatedWorkspaceDir is ~/.cwm/workspace: the isolated-mode root and
// the v0.7 workspace, whatever the current mode.
func (l *Layout) IsolatedWorkspaceDir() string { return filepath.Join(l.Root, "workspace") }
func (l *Layout) AccountsDir() string          { return filepath.Join(l.Root, "accounts") }

// LaunchDir is the v0.7 per-account launch-dir parent (D11, superseded
// by D76); v0.8 only reads it to migrate it into a backup.
func (l *Layout) LaunchDir() string  { return filepath.Join(l.Root, "launch") }
func (l *Layout) BackupsDir() string { return filepath.Join(l.Root, "backups") }
func (l *Layout) StateDir() string   { return filepath.Join(l.Root, "state") }
func (l *Layout) LogsDir() string    { return filepath.Join(l.Root, "logs") }
func (l *Layout) CacheDir() string   { return filepath.Join(l.Root, "cache") }

// StatsCachePath is the incremental stats cache (D61); derived data, never
// synced, safe to delete.
func (l *Layout) StatsCachePath() string { return filepath.Join(l.CacheDir(), "stats.json") }

// ManifestPath is the workspace repos manifest (repos.tsv, bash-kit format).
func (l *Layout) ManifestPath() string {
	return filepath.Join(l.WorkspaceDir(), "repos.tsv")
}

// WorkspaceItemPath resolves a workspace item by name (see WorkspaceItems).
func (l *Layout) WorkspaceItemPath(name string) string {
	return filepath.Join(l.WorkspaceDir(), name)
}

// IsWorkspaceItem reports whether name is part of the canonical item list.
func IsWorkspaceItem(name string) bool {
	for _, n := range WorkspaceItems() {
		if n == name {
			return true
		}
	}
	return false
}

// ExpandTilde expands a leading ~ to the user home dir. Paths that do not
// start with ~ are returned unchanged.
func ExpandTilde(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// ReposRootEnv overrides the canonical clone root (default ~/repos).
const ReposRootEnv = "CWM_REPOS_ROOT"

// ReposRoot returns the canonical clone root: $CWM_REPOS_ROOT or ~/repos.
func ReposRoot() (string, error) {
	if v := os.Getenv(ReposRootEnv); v != "" {
		return ExpandTilde(v), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("storage: cannot determine home dir: %w", err)
	}
	return filepath.Join(home, "repos"), nil
}
