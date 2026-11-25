// Package launcher implements `cwm open <repo>` and the direct form
// `cwm <repo> [new|last]`: clone-on-demand from the workspace manifest,
// project-name pinning (the cross-OS session identity, D3), sync-branch
// hints, and the exec into claude. v0.8 (D76/D80): the default account
// uses the workspace root directly — in capture mode with NO
// CLAUDE_CONFIG_DIR, exactly like a plain `claude` — and any other
// account runs from its login-only dir.
package launcher

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/manifest"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/upkeep"
	"github.com/Mr-JoE1/cwm/internal/workspace"
)

// Mode selects how claude is launched for the repo.
type Mode string

const (
	ModeResume Mode = "resume" // claude --resume (session picker)
	ModeNew    Mode = "new"    // plain claude (fresh session)
	ModeLast   Mode = "last"   // claude --continue (latest session)
)

// ResumeArgs is the D20 passthrough: resuming a known session is a plain
// launch (ModeNew) with `--resume <id>` handed to claude verbatim, so
// clone-on-demand, name pinning and sync hints apply unchanged.
func ResumeArgs(id string) (Mode, []string) {
	return ModeNew, []string{"--resume", id}
}

// ResumeCLIArgs is the cwm argv (without the program) that performs the
// same launch from a terminal: `cwm open <project> new --resume <id>`.
func ResumeCLIArgs(project, id string) []string {
	mode, extra := ResumeArgs(id)
	return append([]string{"open", project, string(mode)}, extra...)
}

// ReposRootEnv overrides the canonical clone root (default ~/repos).
const ReposRootEnv = storage.ReposRootEnv

// ReposRoot returns the canonical clone root: $CWM_REPOS_ROOT or ~/repos.
func ReposRoot() (string, error) { return storage.ReposRoot() }

// Open prepares repo for work and execs claude; it returns only on error.
// On success the process is replaced (unix) or exits with the claude exit
// code (windows). account selects the login ("" = the configured
// default).
func Open(repo string, mode Mode, extra []string, account string, stdout, stderr io.Writer) error {
	layout, err := claudepaths.NewLayout()
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

	if err := applyAccountEnv(layout, account); err != nil {
		return err
	}
	if err := os.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", repo); err != nil {
		return fmt.Errorf("launcher: set CLAUDE_CODE_PROJECT_DIR_NAME: %w", err)
	}

	fallback := prepareLaunch(layout, account, stderr)
	args, err := claudeArgs(layout, mode, extra, fallback)
	if err != nil {
		return err
	}
	res, err := claudebin.Find(layout)
	if err != nil {
		return fmt.Errorf("launcher: %w", err)
	}
	return execClaude(res.Path, args)
}

// OpenDir execs claude in dir with extra (cross-device resume, D91): the
// session's folder follows dir, so no project-name pin is set and an
// inherited one is removed. dir must exist.
func OpenDir(dir string, extra []string, account string, stdout, stderr io.Writer) error {
	layout, err := claudepaths.NewLayout()
	if err != nil {
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("launcher: cannot enter %s: %w", dir, err)
	}
	syncHints(dir, stdout)
	if err := applyAccountEnv(layout, account); err != nil {
		return err
	}
	if err := os.Unsetenv("CLAUDE_CODE_PROJECT_DIR_NAME"); err != nil {
		return fmt.Errorf("launcher: unset CLAUDE_CODE_PROJECT_DIR_NAME: %w", err)
	}
	fallback := prepareLaunch(layout, account, stderr)
	args, err := claudeArgs(layout, ModeNew, extra, fallback)
	if err != nil {
		return err
	}
	res, err := claudebin.Find(layout)
	if err != nil {
		return fmt.Errorf("launcher: %w", err)
	}
	return execClaude(res.Path, args)
}

// Clone clones url into dir (clone on demand, as Open does).
func Clone(url, dir string, stdout, stderr io.Writer) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("launcher: create %s: %w", filepath.Dir(dir), err)
	}
	fmt.Fprintf(stdout, "cloning %s -> %s\n", url, dir)
	clone := exec.Command("git", "clone", url, dir)
	clone.Stdin, clone.Stdout, clone.Stderr = os.Stdin, stdout, stderr
	if err := clone.Run(); err != nil {
		return fmt.Errorf("launcher: git clone %s: %w", url, err)
	}
	return nil
}

// applyAccountEnv sets (or clears) CLAUDE_CONFIG_DIR for account ("" =
// the default) through the one resolver (D77): the capture-mode default
// must see exactly what a plain `claude` sees, so an inherited
// CLAUDE_CONFIG_DIR is removed; every other account gets its login-only
// dir, re-verified before each launch.
func applyAccountEnv(layout *storage.Layout, account string) error {
	env, err := ResolveAccountEnv(layout, account)
	if err != nil {
		return err
	}
	if env == "" {
		if err := os.Unsetenv(claudepaths.EnvConfigDir); err != nil {
			return fmt.Errorf("launcher: unset %s: %w", claudepaths.EnvConfigDir, err)
		}
		return nil
	}
	if err := os.Setenv(claudepaths.EnvConfigDir, env); err != nil {
		return fmt.Errorf("launcher: set %s: %w", claudepaths.EnvConfigDir, err)
	}
	return nil
}

// ResolveAccountEnv is the CLAUDE_CONFIG_DIR value a launch of account
// needs ("" = leave it unset), preparing the login-only dir of a
// non-default account.
func ResolveAccountEnv(layout *storage.Layout, account string) (string, error) {
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		return "", fmt.Errorf("launcher: %w", err)
	}
	if account != "" && account != cfg.DefaultAccount {
		if _, ok := cfg.Account(account); !ok {
			return "", fmt.Errorf("launcher: unknown account %q (see: cwm account list)", account)
		}
	}
	live, err := claudepaths.LiveFor(layout, cfg, account)
	if err != nil {
		return "", fmt.Errorf("launcher: %w", err)
	}
	if !live.Default {
		if _, err := workspace.EnsureLoginDir(layout, cfg, live.Account); err != nil {
			return "", fmt.Errorf("launcher: prepare login-only dir: %w", err)
		}
	}
	return live.Env, nil
}

// Run is `cwm run [--account <name>] [-- claude args]`: claude in the
// current directory, as account, with no repo handling.
func Run(account string, extra []string) error {
	layout, err := claudepaths.NewLayout()
	if err != nil {
		return err
	}
	if err := applyAccountEnv(layout, account); err != nil {
		return err
	}
	fallback := prepareLaunch(layout, account, os.Stderr)
	args, err := claudeArgs(layout, ModeNew, extra, fallback)
	if err != nil {
		return err
	}
	res, err := claudebin.Find(layout)
	if err != nil {
		return fmt.Errorf("launcher: %w", err)
	}
	return execClaude(res.Path, args)
}

// prepareLaunch runs the MCP bridge and the hook merge (upkeep, D82/D89)
// and reports whether claude still needs --mcp-config because the
// account's .claude.json could not carry the servers. Problems are
// printed, never fatal: the session must start.
func prepareLaunch(layout *storage.Layout, account string, stderr io.Writer) bool {
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		fmt.Fprintf(stderr, "cwm: %v\n", err)
		return true
	}
	res := upkeep.BeforeLaunch(layout, cfg, account)
	for _, e := range res.Errors {
		fmt.Fprintf(stderr, "cwm: %s\n", e)
	}
	return res.MCPFallback
}

// claudeArgs builds the claude argv: --mcp-config only when mcpFallback
// says the bridge could not put the workspace servers into the live
// .claude.json (D82; passing both would list every server twice), then
// the mode flag, then caller extras verbatim.
func claudeArgs(layout *storage.Layout, mode Mode, extra []string, mcpFallback bool) ([]string, error) {
	args := []string{}
	mcp := filepath.Join(layout.WorkspaceDir(), "mcp.json")
	if data, err := os.ReadFile(mcp); mcpFallback && err == nil && strings.TrimSpace(string(data)) != "" {
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
