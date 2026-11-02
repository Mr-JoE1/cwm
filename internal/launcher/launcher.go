// Package launcher implements `cwm open <repo>` and the direct form
// `cwm <repo> [new|last]`: clone-on-demand from the workspace manifest,
// project-name pinning (the cross-OS session identity, D3), sync-branch
// hints, and the exec into claude. Stage B launches against the workspace
// itself; stage C assembles per-account launch dirs on top.
package launcher

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/manifest"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/workspace"
)

// Mode selects how claude is launched for the repo.
type Mode string

const (
	ModeResume Mode = "resume" // claude --resume (session picker)
	ModeNew    Mode = "new"    // plain claude (fresh session)
	ModeLast   Mode = "last"   // claude --continue (latest session)
)

// ReposRootEnv overrides the canonical clone root (default ~/repos).
const ReposRootEnv = "CWM_REPOS_ROOT"

// ReposRoot returns the canonical clone root: $CWM_REPOS_ROOT or ~/repos.
func ReposRoot() (string, error) {
	if v := os.Getenv(ReposRootEnv); v != "" {
		return storage.ExpandTilde(v), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("launcher: cannot determine home dir: %w", err)
	}
	return filepath.Join(home, "repos"), nil
}

// Open prepares repo for work and execs claude; it returns only on error.
// On success the process is replaced (unix) or exits with the claude exit
// code (windows). account selects the launch dir ("" = the configured
// default).
func Open(repo string, mode Mode, extra []string, account string, stdout, stderr io.Writer) error {
	layout, err := storage.NewLayout()
	if err != nil {
		return err
	}
	ws := layout.WorkspaceDir()
	if info, err := os.Stat(ws); err != nil || !info.IsDir() {
		return fmt.Errorf("workspace %s does not exist — run `cwm init` or pair Syncthing so the folder is restored ('cwm doctor' can diagnose)", ws)
	}

	root, err := ReposRoot()
	if err != nil {
		return err
	}
	repoDir := filepath.Join(root, repo)
	if _, err := os.Stat(repoDir); err != nil {
		m, err := manifest.Load(layout.ManifestPath())
		if err != nil {
			return fmt.Errorf("launcher: cannot read manifest: %w", err)
		}
		url, ok := m.Lookup(repo)
		if !ok {
			return fmt.Errorf("unknown project %q — no %s and no entry in %s\nknown projects:\n  %s",
				repo, repoDir, layout.ManifestPath(), strings.Join(m.Names(), "\n  "))
		}
		fmt.Fprintf(stdout, "cloning %s -> %s\n", url, repoDir)
		clone := exec.Command("git", "clone", url, repoDir)
		clone.Stdin, clone.Stdout, clone.Stderr = os.Stdin, stdout, stderr
		if err := clone.Run(); err != nil {
			return fmt.Errorf("launcher: git clone %s: %w", url, err)
		}
	} else if info, serr := os.Stat(repoDir); serr == nil && !info.IsDir() {
		return fmt.Errorf("%s exists but is not a directory", repoDir)
	}

	if err := os.Chdir(repoDir); err != nil {
		return fmt.Errorf("launcher: cannot enter %s: %w", repoDir, err)
	}

	syncHints(repoDir, stdout)

	// Stage C: with accounts configured, claude runs against the assembled
	// launch/<account> dir (workspace + credentials, D11). Without any
	// account the workspace itself is the config dir (stage B behavior).
	launchDir, err := resolveConfigDir(layout, account)
	if err != nil {
		return err
	}
	if err := os.Setenv("CLAUDE_CONFIG_DIR", launchDir); err != nil {
		return fmt.Errorf("launcher: set CLAUDE_CONFIG_DIR: %w", err)
	}
	if err := os.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", repo); err != nil {
		return fmt.Errorf("launcher: set CLAUDE_CODE_PROJECT_DIR_NAME: %w", err)
	}

	args, err := claudeArgs(layout, mode, extra)
	if err != nil {
		return err
	}
	bin, _, err := storage.ClaudeBin()
	if err != nil {
		return err
	}
	return execClaude(bin, args)
}

// resolveConfigDir picks the dir claude should treat as CLAUDE_CONFIG_DIR:
// the assembled launch/<account> dir when any account exists, the raw
// workspace dir when none does (pre-account stage B mode).
func resolveConfigDir(layout *storage.Layout, account string) (string, error) {
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		return "", fmt.Errorf("launcher: %w", err)
	}
	acct := account
	if acct == "" {
		acct = cfg.DefaultAccount
	}
	if len(cfg.Accounts) == 0 {
		if acct == "" {
			return layout.WorkspaceDir(), nil
		}
		return "", fmt.Errorf("launcher: unknown account %q (no accounts configured — run `cwm init --account <name>`)", acct)
	}
	if acct == "" {
		return "", fmt.Errorf("launcher: no default account — run `cwm account use <name>`")
	}
	if _, ok := cfg.Account(acct); !ok {
		return "", fmt.Errorf("launcher: unknown account %q (see: cwm account list)", acct)
	}
	if err := workspace.EnsureLaunch(layout, cfg, acct); err != nil {
		return "", fmt.Errorf("launcher: assemble launch dir: %w", err)
	}
	return filepath.Join(layout.LaunchDir(), acct), nil
}

// claudeArgs builds the claude argv: --mcp-config when the workspace has
// MCP servers configured (D13), then the mode flag, then caller extras
// verbatim.
func claudeArgs(layout *storage.Layout, mode Mode, extra []string) ([]string, error) {
	args := []string{}
	mcp := filepath.Join(layout.WorkspaceDir(), "mcp.json")
	if data, err := os.ReadFile(mcp); err == nil && strings.TrimSpace(string(data)) != "" {
		abs, err := filepath.Abs(mcp)
		if err != nil {
			return nil, fmt.Errorf("launcher: resolve %s: %w", mcp, err)
		}
		args = append(args, "--mcp-config", abs)
	}
	switch mode {
	case ModeNew:
		// plain claude
	case ModeLast:
		args = append(args, "--continue")
	case ModeResume:
		args = append(args, "--resume")
	default:
		return nil, fmt.Errorf("launcher: unknown mode %q", mode)
	}
	return append(args, extra...), nil
}

// syncHints prints one line per origin sync/<host> branch carrying commits
// HEAD lacks — the "another device pushed WIP" signal (port of bin/cwm
// sync_hint). Best-effort: offline or non-repo states print nothing.
func syncHints(dir string, w io.Writer) {
	mine := storage.HostName()
	_ = exec.Command("git", "fetch", "-q", "origin").Run()
	out, err := gitOut(dir, "for-each-ref", "--format=%(refname:short)", "refs/remotes/origin/sync/")
	if err != nil {
		return
	}
	for _, ref := range strings.Split(strings.TrimSpace(out), "\n") {
		if ref = strings.TrimSpace(ref); ref == "" {
			continue
		}
		host := strings.TrimPrefix(ref, "origin/sync/")
		if host == mine {
			continue
		}
		count := strings.TrimSpace(gitSafe(dir, "rev-list", "--count", "HEAD.."+ref))
		if count != "" && count != "0" && count != "?" {
			fmt.Fprintf(w, "hint: sync branch %s has commits not on this branch (run: git pull origin sync/%s)\n", host, host)
		}
	}
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

func gitSafe(dir string, args ...string) string {
	out, err := gitOut(dir, args...)
	if err != nil {
		return "?"
	}
	return out
}
