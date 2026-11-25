package cli

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/pathmirror"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// hookPayloadMax bounds what the hook reads from Claude Code's stdin.
const hookPayloadMax = 1 << 20

// hookStdinWait bounds the wait for a payload that never arrives (a hook
// run by hand from a shell whose stdin stays open).
var hookStdinWait = 2 * time.Second

// readHookPayload returns the session id and cwd of the JSON Claude Code
// pipes into a hook ("" when there is none: a terminal, no payload).
func readHookPayload(r io.Reader) (id, cwd string) {
	if r == nil {
		return "", ""
	}
	if f, ok := r.(*os.File); ok {
		if info, err := f.Stat(); err != nil || info.Mode()&os.ModeCharDevice != 0 {
			return "", ""
		}
	}
	done := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(io.LimitReader(r, hookPayloadMax))
		done <- data
	}()
	var data []byte
	select {
	case data = <-done:
	case <-time.After(hookStdinWait):
		return "", ""
	}
	var p struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
	}
	if json.Unmarshal(data, &p) != nil {
		return "", ""
	}
	return p.SessionID, p.Cwd
}

// recordSessionRepo is the session-start hook's path-mirror bookkeeping
// (D91): session id -> remote in the synced session-repos/<host>.tsv,
// and remote -> this clone's path in the machine-local map. Best-effort,
// like every hook step.
func recordSessionRepo(layout *storage.Layout, dir, id, cwd string) {
	if cwd != "" {
		dir = cwd
	}
	remote := pathmirror.GitRemote(dir)
	if remote == "" {
		return
	}
	top := dir
	if out := gitTop(dir); out != "" {
		top = out
	}
	_ = pathmirror.LoadRepoPaths(pathmirror.RepoPathsPath(layout)).Record(remote, top)
	if id != "" {
		host, _ := os.Hostname()
		_ = pathmirror.RecordSession(layout.WorkspaceDir(), host, id, remote)
	}
}

// gitTop is the clone's top-level dir ("" when dir is not in one).
func gitTop(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return filepath.FromSlash(strings.TrimSpace(string(out)))
}
