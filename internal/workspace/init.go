package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// Init creates the cwm home skeleton (workspace items, accounts/,
// backups/, state/, logs/, workspace/.stignore, config.toml). When account
// is non-empty it registers that account first (interactive `claude auth
// login` via accounts.Add). Existing state is
// preserved: init is safe to re-run. dryRun prints the plan and touches
// nothing.
func Init(l *storage.Layout, account string, dryRun bool, stdout io.Writer) error {
	if dryRun {
		return initPlan(l, account, stdout)
	}

	// Capture mode's root is ~/.claude, Claude Code's own dir: cwm
	// creates the root but no placeholder items in it (claude makes what
	// it uses; login-only dirs create link targets on demand).
	skeleton := l.WorkspaceDir() == l.IsolatedWorkspaceDir()
	if err := os.MkdirAll(l.WorkspaceDir(), 0o755); err != nil {
		return fmt.Errorf("workspace: create %s: %w", l.WorkspaceDir(), err)
	}
	for _, d := range storage.WorkspaceDirs {
		if !skeleton {
			break
		}
		if err := os.MkdirAll(l.WorkspaceItemPath(d), 0o755); err != nil {
			return fmt.Errorf("workspace: create %s: %w", d, err)
		}
	}
	for _, d := range []string{l.AccountsDir()} {
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
		if !skeleton {
			break
		}
		p := l.WorkspaceItemPath(f)
		if _, err := os.Lstat(p); err == nil {
			continue
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			return fmt.Errorf("workspace: create %s: %w", p, err)
		}
	}
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	if _, err := WriteStIgnore(l, cfg); err != nil {
		return err
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
		fmt.Fprintf(stdout, "✓ default account: %s\n", cfg.DefaultAccount)
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
	fmt.Fprintf(stdout, "would create %s, %s, %s, %s\n",
		l.AccountsDir(), l.BackupsDir(), l.StateDir(), l.LogsDir())
	fmt.Fprintf(stdout, "would write %s (allow-list from [sync])\n", StIgnorePath(l))
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
		fmt.Fprintf(stdout, "note: found ~/.claude-me (legacy bash-kit workspace) — run `cwm init --migrate` to import it\n")
	}
}
