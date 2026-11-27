// Package wip implements the SessionStart/SessionEnd hook logic: recording
// repos into the workspace manifest and the WIP auto-push to sync/<host>
// branches (a port of the bash kit's hooks/session-*.sh). Hook contract,
// kept from the bash kit: a hook must never fail the session — every failure
// is logged under $CWM_HOME/logs and reported through the result struct,
// never as a returned error.
package wip

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/manifest"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// WIP commit identity (exact port of hooks/session-end.sh): -c overrides so
// commits never fail on machines without a configured git user.
const (
	commitName  = "claude-sync"
	commitEmail = "claude-sync@localhost"
)

// StartResult reports what SessionStart did.
type StartResult struct {
	// Recorded is true when the name→url entry landed in repos.tsv.
	Recorded bool
	// Skipped is the human-readable skip reason ("" when not skipped).
	Skipped string
	// Error is the logged failure reason ("" on success). The hook still
	// exits 0: skip/error details live here and in the log file only.
	Error string
}

// EndResult reports what SessionEnd did.
type EndResult struct {
	// Pushed is true when a WIP commit was created and pushed.
	Pushed bool
	// Branch is "sync/<host>" for the push target.
	Branch string
	// Skipped is the human-readable skip reason ("" when not skipped).
	Skipped string
	// Error is the logged failure reason ("" on success).
	Error string
}

type hookRun struct {
	layout  *storage.Layout
	dir     string
	logName string
	logFile *os.File
}

func (h *hookRun) log() (*os.File, error) {
	if h.logFile != nil {
		return h.logFile, nil
	}
	dir := h.layout.LogsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("wip: create log dir %s: %w", dir, err)
	}
	p := filepath.Join(dir, h.logName)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("wip: open log %s: %w", p, err)
	}
	h.logFile = f
	return f, nil
}

// logf writes one timestamped line, mirroring the bash kit's logline().
// Logging is best-effort: a broken log must not break the hook.
func (h *hookRun) logf(format string, args ...any) {
	f, err := h.log()
	if err != nil {
		return
	}
	stamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	fmt.Fprintf(f, "%s %s\n", stamp, fmt.Sprintf(format, args...))
}

func (h *hookRun) close() {
	if h.logFile != nil {
		_ = h.logFile.Close()
	}
}

// git runs git in the hook dir (env nil = this process's) and appends combined output to the log, the
// same redirection the bash kit used (`git ... >>"$log" 2>&1`).
func (h *hookRun) git(env []string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = h.dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if f, lerr := h.log(); lerr == nil && len(out) > 0 {
		_, _ = f.Write(out)
	}
	if err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func gitOutput(dir string, args ...string) (string, error) {
	return gitOutputEnv(dir, nil, args...)
}

func gitOutputEnv(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return string(out), fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return string(out), fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// SessionStart records "this machine worked on repo <dir> at origin <url>"
// in the workspace manifest so `cwm open <name>` can clone it elsewhere.
// Port of hooks/session-start.sh; it never returns an error.
func SessionStart(layout *storage.Layout, dir string) StartResult {
	h := &hookRun{layout: layout, dir: dir, logName: "session-start.log"}
	defer h.close()

	url, err := gitOutput(dir, "remote", "get-url", "--push", "origin")
	if err != nil {
		h.logf("skip: cwd is not a git repo with an origin remote (%s)", dir)
		return StartResult{Skipped: "cwd is not a git repo with an origin remote"}
	}
	url = strings.TrimSpace(url)

	if err := os.MkdirAll(layout.WorkspaceDir(), 0o755); err != nil {
		h.logf("error: cannot create %s (%v)", layout.WorkspaceDir(), err)
		return StartResult{Error: fmt.Sprintf("cannot create %s", layout.WorkspaceDir())}
	}
	m, err := manifest.Load(layout.ManifestPath())
	if err != nil {
		h.logf("error: cannot read %s (%v)", layout.ManifestPath(), err)
		return StartResult{Error: fmt.Sprintf("cannot read %s", layout.ManifestPath())}
	}
	m.Set(filepath.Base(dir), url)
	if err := m.Save(); err != nil {
		h.logf("error: cannot write %s (%v)", layout.ManifestPath(), err)
		return StartResult{Error: fmt.Sprintf("cannot write %s", layout.ManifestPath())}
	}
	return StartResult{Recorded: true}
}

// SessionEnd is the WIP auto-push: when the tree is dirty, snapshot it as
// claude-sync and push (non-force) to sync/<host> on origin so another
// device can pick up in-flight work. It never returns an error — failures
// are logged and reported in the result.
//
// The snapshot never touches the user's branch, index or working tree
// (D119; cwm ≤ 0.9.1 committed onto the current branch): it is built in a
// temporary index from HEAD, without nested repositories or Claude Code's
// own worktrees, and committed with commit-tree, so only the pushed ref
// points at it. A rejected push leaves nothing behind but a log line.
func SessionEnd(layout *storage.Layout, dir string) EndResult {
	h := &hookRun{layout: layout, dir: dir, logName: "wip.log"}
	defer h.close()

	url, err := gitOutput(dir, "remote", "get-url", "--push", "origin")
	if err != nil {
		h.logf("skip: cwd is not a git repo with an origin remote (%s)", dir)
		return EndResult{Skipped: "cwd is not a git repo with an origin remote"}
	}
	url = strings.TrimSpace(url)

	// A failing status counts as clean, exactly like the bash hook's
	// guard. --no-optional-locks: status must not even refresh the
	// user's index.
	porcelain, _ := gitOutput(dir, "--no-optional-locks", "status", "--porcelain")
	if strings.TrimSpace(porcelain) == "" {
		h.logf("skip: tree is clean (%s)", dir)
		return EndResult{Skipped: "tree is clean"}
	}
	head, err := gitOutput(dir, "rev-parse", "--verify", "-q", "HEAD^{commit}")
	if err != nil {
		h.logf("skip: no commit yet to snapshot from (%s)", dir)
		return EndResult{Skipped: "no commit yet"}
	}
	head = strings.TrimSpace(head)

	host := storage.HostName()
	branch := "sync/" + host
	stamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	tmp, err := os.MkdirTemp("", "cwm-wip-")
	if err != nil {
		h.logf("error: cannot create a temporary index (%v)", err)
		return EndResult{Error: "cannot create a temporary index", Branch: branch}
	}
	defer os.RemoveAll(tmp)
	tree, skipped, err := h.snapshot(filepath.Join(tmp, "index"), filepath.Join(tmp, "pathspec"))
	if len(skipped) > 0 {
		h.logf("skipped %d nested repositories (%s)", len(skipped), summarize(skipped))
	}
	if err != nil {
		h.logf("error: %v (%s)", err, dir)
		return EndResult{Error: "snapshot failed", Branch: branch}
	}
	if headTree, _ := gitOutput(dir, "rev-parse", "HEAD^{tree}"); tree == strings.TrimSpace(headTree) {
		h.logf("skip: nothing to snapshot besides nested repositories (%s)", dir)
		return EndResult{Skipped: "nothing to snapshot besides nested repositories"}
	}

	args := []string{"-c", "user.name=" + commitName, "-c", "user.email=" + commitEmail, "commit-tree", tree}
	for _, p := range h.parents(head, branch) {
		args = append(args, "-p", p)
	}
	sha, err := gitOutput(dir, append(args, "-m", "wip: "+host+" "+stamp)...)
	if err != nil {
		h.logf("error: commit failed (%v)", err)
		return EndResult{Error: "commit failed", Branch: branch}
	}
	sha = strings.TrimSpace(sha)
	if err := h.git(nil, "push", "origin", sha+":refs/heads/"+branch); err != nil {
		h.logf("error: push failed for %s (non-force; nothing kept locally — your files are untouched)", branch)
		return EndResult{Error: "push failed for " + branch + " (non-force; nothing kept locally)", Branch: branch}
	}
	h.logf("ok: pushed a snapshot (%s) to %s (%s)", short(sha), branch, url)
	return EndResult{Pushed: true, Branch: branch}
}

// claudeWorktrees is Claude Code's own worktree folder, never snapshotted.
const claudeWorktrees = ":(exclude,glob)**/.claude/worktrees/**"

// snapshot builds the WIP tree in the temporary index idx: HEAD, then
// every change in the working tree except nested repositories (git lists
// an embedded repo or worktree it would add as a gitlink as "dir/") and
// Claude Code's worktrees, and minus any gitlink HEAD does not already
// have. The user's index file only seeds idx's stat cache (a copy), so
// unchanged files are not re-hashed.
func (h *hookRun) snapshot(idx, specFile string) (tree string, skipped []string, err error) {
	env := append(os.Environ(), "GIT_INDEX_FILE="+idx)
	if p, err := gitOutput(h.dir, "rev-parse", "--path-format=absolute", "--git-path", "index"); err == nil {
		_ = copyFile(strings.TrimSpace(p), idx)
	}
	if err := h.git(env, "read-tree", "-m", "HEAD"); err != nil {
		_ = os.Remove(idx)
		if err := h.git(env, "read-tree", "HEAD"); err != nil {
			return "", nil, fmt.Errorf("git read-tree failed")
		}
	}
	others, err := gitOutputEnv(h.dir, env, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return "", nil, fmt.Errorf("git ls-files failed")
	}
	spec := []string{".", claudeWorktrees}
	for p := range strings.SplitSeq(others, "\x00") {
		if strings.HasSuffix(p, "/") {
			p = strings.TrimSuffix(p, "/")
			skipped = append(skipped, p)
			spec = append(spec, ":(exclude,literal)"+p)
		}
	}
	if err := os.WriteFile(specFile, []byte(strings.Join(spec, "\x00")), 0o600); err != nil {
		return "", skipped, fmt.Errorf("write pathspec: %w", err)
	}
	if err := h.git(env, "add", "-A", "--pathspec-from-file="+specFile, "--pathspec-file-nul"); err != nil {
		return "", skipped, fmt.Errorf("git add failed")
	}
	staged, _ := gitOutputEnv(h.dir, env, "ls-files", "-z", "-s")
	for e := range strings.SplitSeq(staged, "\x00") {
		meta, path, ok := strings.Cut(e, "\t")
		if !ok || !strings.HasPrefix(meta, "160000 ") {
			continue
		}
		if in, _ := gitOutput(h.dir, "ls-tree", "HEAD", "--", path); strings.HasPrefix(in, "160000 ") {
			continue
		}
		if err := h.git(env, "update-index", "--force-remove", "--", path); err != nil {
			return "", skipped, fmt.Errorf("git update-index failed")
		}
		skipped = append(skipped, path)
	}
	tree, err = gitOutputEnv(h.dir, env, "write-tree")
	if err != nil {
		return "", skipped, fmt.Errorf("git write-tree failed")
	}
	return strings.TrimSpace(tree), skipped, nil
}

// parents keeps the push a fast-forward of the remote sync branch: HEAD
// alone when there is none (or it is behind HEAD), the remote tip when it
// is an earlier snapshot on top of HEAD, and both when the branch moved on
// since (the snapshot then merges them; its tree is still the working
// tree). An unreachable remote leaves HEAD, and the push reports it.
func (h *hookRun) parents(head, branch string) []string {
	ref := "refs/heads/" + branch
	out, err := gitOutput(h.dir, "ls-remote", "origin", ref)
	tip, _, _ := strings.Cut(strings.TrimSpace(out), "\t")
	if err != nil || tip == "" || tip == head {
		return []string{head}
	}
	if _, err := gitOutput(h.dir, "cat-file", "-e", tip+"^{commit}"); err != nil {
		if h.git(nil, "fetch", "-q", "--no-tags", "origin", ref) != nil {
			return []string{head}
		}
	}
	switch {
	case isAncestor(h.dir, tip, head):
		return []string{head}
	case isAncestor(h.dir, head, tip):
		return []string{tip}
	}
	return []string{head, tip}
}

func isAncestor(dir, a, b string) bool {
	_, err := gitOutput(dir, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

func short(sha string) string { return sha[:min(len(sha), 12)] }

// summarize names at most five paths.
func summarize(paths []string) string {
	if len(paths) <= 5 {
		return strings.Join(paths, ", ")
	}
	return strings.Join(paths[:5], ", ") + fmt.Sprintf(", … %d more", len(paths)-5)
}

// SyncStatus lists origin/sync/* branches with the number of commits each
// carries that HEAD lacks — the "another host pushed WIP we don't have"
// signal. A failing fetch is ignored so offline runs still report the
// last-fetched view. Unlike the hooks, this is a status command: errors
// (e.g. cwd not a git repo) are returned to the caller.
func SyncStatus(dir string, w io.Writer) error {
	_, _ = gitOutput(dir, "fetch", "-q", "origin")

	out, err := gitOutput(dir, "for-each-ref", "--format=%(refname:short)", "refs/remotes/origin/sync/")
	if err != nil {
		return fmt.Errorf("wip: cannot list sync branches in %s: %w", dir, err)
	}
	var refs []string
	for ref := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if ref = strings.TrimSpace(ref); ref != "" {
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		fmt.Fprintln(w, "  (no sync branches on origin yet)")
		return nil
	}

	mine := storage.HostName()
	fmt.Fprintln(w, "sync branches on origin:")
	for _, ref := range refs {
		host := strings.TrimPrefix(ref, "origin/sync/")
		n := strings.TrimSpace(mustCount(dir, "HEAD.."+ref))
		fmt.Fprintf(w, "  %-24s %s commit(s) ahead", host, n)
		if host != mine && n != "0" && n != "?" {
			fmt.Fprintf(w, " (run: git pull origin sync/%s)", host)
		}
		fmt.Fprintln(w)
	}
	return nil
}

func mustCount(dir, rev string) string {
	out, err := gitOutput(dir, "rev-list", "--count", rev)
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(out)
}
