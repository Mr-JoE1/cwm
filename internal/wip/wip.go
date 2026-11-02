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

// git runs git in the hook dir and appends combined output to the log, the
// same redirection the bash kit used (`git ... >>"$log" 2>&1`).
func (h *hookRun) git(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = h.dir
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
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
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

// SessionEnd is the WIP auto-push: when the tree is dirty, commit everything
// as claude-sync and push (non-force) to sync/<host> on origin so another
// device can pick up in-flight work. Exact port of hooks/session-end.sh; it
// never returns an error — failures are logged and reported in the result.
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
	// `[[ -n "$(git status --porcelain 2>/dev/null)" ]]` guard.
	porcelain, _ := gitOutput(dir, "status", "--porcelain")
	if strings.TrimSpace(porcelain) == "" {
		h.logf("skip: tree is clean (%s)", dir)
		return EndResult{Skipped: "tree is clean"}
	}

	host := storage.HostName()
	branch := "sync/" + host
	stamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	if err := h.git("add", "-A"); err != nil {
		h.logf("error: git add failed (%s)", dir)
		return EndResult{Error: "git add failed", Branch: branch}
	}
	if err := h.git("-c", "user.name="+commitName, "-c", "user.email="+commitEmail,
		"commit", "-m", "wip: "+host+" "+stamp, "--no-verify"); err != nil {
		h.logf("error: commit failed (%s)", dir)
		return EndResult{Error: "commit failed", Branch: branch}
	}
	if err := h.git("push", "origin", "HEAD:refs/heads/"+branch); err != nil {
		h.logf("error: push failed for %s (non-force; WIP commit left local)", branch)
		return EndResult{Error: "push failed for " + branch + " (non-force; WIP commit left local)", Branch: branch}
	}
	h.logf("ok: pushed HEAD to %s (%s)", branch, url)
	return EndResult{Pushed: true, Branch: branch}
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
