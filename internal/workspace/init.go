package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// stIgnoreLines is the managed workspace/.stignore content: machine-local
// clutter that must never sync between devices.
var stIgnoreLines = []string{
	"settings.json",
	"settings.local.json",
	"cache",
	"debug",
	"telemetry",
	"statsig",
	"shell-snapshots",
	"session-env",
	"paste-cache",
	"file-history",
	"backups",
	"downloads",
	"ide",
	"chrome",
	"todos",
	"tasks",
	"sessions",
	"worktrees",
	"logs",
	".DS_Store",
	"Thumbs.db",
}

// StIgnoreContent renders workspace/.stignore (one "(?d)pattern" line per
// entry, trailing newline).
func StIgnoreContent() string {
	var b strings.Builder
	for _, line := range stIgnoreLines {
		fmt.Fprintf(&b, "(?d)%s\n", line)
	}
	return b.String()
}

// Init creates the cwm home skeleton (workspace items, accounts/, launch/,
// backups/, state/, logs/, workspace/.stignore, config.toml). When account
// is non-empty it registers that account first (interactive `claude auth
// login` via accounts.Add) and assembles its launch dir. Existing state is
// preserved: init is safe to re-run. dryRun prints the plan and touches
// nothing.
func Init(l *storage.Layout, account string, dryRun bool, stdout io.Writer) error {
	if dryRun {
		return initPlan(l, account, stdout)
	}

	for _, d := range storage.WorkspaceDirs {
		if err := os.MkdirAll(l.WorkspaceItemPath(d), 0o755); err != nil {
			return fmt.Errorf("workspace: create %s: %w", d, err)
		}
	}
	for _, d := range []string{l.AccountsDir(), l.LaunchDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("workspace: create %s: %w", d, err)
		}
	}
	for _, d := range []string{l.Root, l.BackupsDir(), l.StateDir(), l.LogsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("workspace: create %s: %w", d, err)
		}
	}
	for _, f := range storage.WorkspaceFiles {
		p := l.WorkspaceItemPath(f)
		if _, err := os.Lstat(p); err == nil {
			continue
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			return fmt.Errorf("workspace: create %s: %w", p, err)
		}
	}
	if err := storage.WriteFileAtomic(l.WorkspaceItemPath(".stignore"), []byte(StIgnoreContent()), 0o644); err != nil {
		return fmt.Errorf("workspace: write .stignore: %w", err)
	}

	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	if account != "" {
		if err := accounts.Add(l, cfg, account, stdout); err != nil {
			return err
		}
	}
	if err := config.Save(l.ConfigPath(), cfg); err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	fmt.Fprintf(stdout, "✓ cwm home initialized at %s\n", l.Root)

	if len(cfg.Accounts) > 0 && cfg.DefaultAccount != "" {
		if err := EnsureLaunch(l, cfg, cfg.DefaultAccount); err != nil {
			return fmt.Errorf("workspace: assemble launch dir: %w", err)
		}
		fmt.Fprintf(stdout, "✓ launch dir assembled at %s\n", filepath.Join(l.LaunchDir(), cfg.DefaultAccount))
	} else {
		fmt.Fprintf(stdout, "next: add an account with `cwm account add <name>`\n")
	}

	printMigrateHint(stdout)
	return nil
}

func initPlan(l *storage.Layout, account string, stdout io.Writer) error {
	fmt.Fprintf(stdout, "would create %s\n", l.WorkspaceDir())
	for _, d := range storage.WorkspaceDirs {
		fmt.Fprintf(stdout, "would create   %s%c%s\n", l.WorkspaceDir(), filepath.Separator, d)
	}
	fmt.Fprintf(stdout, "would create %s, %s, %s, %s, %s\n",
		l.AccountsDir(), l.LaunchDir(), l.BackupsDir(), l.StateDir(), l.LogsDir())
	fmt.Fprintf(stdout, "would write %s (%d ignore rules)\n", l.WorkspaceItemPath(".stignore"), len(stIgnoreLines))
	if account != "" {
		fmt.Fprintf(stdout, "would run `claude auth login` into %s%c%s and register it\n",
			l.AccountsDir(), filepath.Separator, account)
	}
	fmt.Fprintf(stdout, "would write %s\n", l.ConfigPath())
	return nil
}

// printMigrateHint points bash-kit users at the (upcoming) migrate flow.
func printMigrateHint(stdout io.Writer) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	if _, err := os.Stat(filepath.Join(home, ".claude-me")); err == nil {
		fmt.Fprintf(stdout, "note: found ~/.claude-me (legacy bash-kit workspace) — `cwm init --migrate` (v0.2-C2) will be able to import it\n")
	}
}
