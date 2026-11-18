package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// LogPath is where detached agents write their output.
func LogPath(root string) string { return filepath.Join(root, "logs", "agent.log") }

// OpenLog opens (appending) the agent log, creating its directory.
func OpenLog(root string) (*os.File, error) {
	p := LogPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, fmt.Errorf("agent: create log dir: %w", err)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("agent: open log %s: %w", p, err)
	}
	return f, nil
}

// StartDetached starts exe args as a background process that outlives the
// caller: its own session (POSIX) / hidden console + own process group
// (Windows), stdin from the null device, stdout+stderr appended to the
// agent log. It does not wait for the child.
func StartDetached(root, exe string, args []string) error {
	logf, err := OpenLog(root)
	if err != nil {
		return err
	}
	defer logf.Close() // the child keeps its own copy of the handle
	null, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("agent: open %s: %w", os.DevNull, err)
	}
	defer null.Close()
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, logf, logf
	cmd.SysProcAttr = DetachAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("agent: start %s: %w", filepath.Base(exe), err)
	}
	// Release, not Wait: the agent is meant to outlive us; on POSIX the
	// orphan is re-parented and reaped by init.
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("agent: release child: %w", err)
	}
	return nil
}
