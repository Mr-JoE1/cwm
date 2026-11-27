package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
)

// IsHandOff reports whether extra hands a session to the Claude desktop
// app (`claude --desktop …`, D113/D117).
func IsHandOff(extra []string) bool { return slices.Contains(extra, "--desktop") }

// HandOffCmd is the hand-off child. claude --desktop refuses to run unless
// both stdin and stdout are terminals (Claude Code 2.1.3xx: "can't run
// non-interactively (… or redirected output)" and "doesn't take piped or
// redirected input"), so it gets this process's own standard handles —
// never a pipe, never a buffer — and no window flags: it shares cwm's
// console or terminal.
func HandOffCmd(bin string, args []string) *exec.Cmd {
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd
}

// startClaude execs claude, except for the desktop hand-off: that runs as
// a child and returns, so the caller can fall back to opening the app in
// the folder when it fails, and a terminal opened for it closes itself
// once it succeeds.
func startClaude(bin string, args, extra []string) error {
	if !IsHandOff(extra) {
		return execClaude(bin, args)
	}
	if err := HandOffCmd(bin, args).Run(); err != nil {
		return fmt.Errorf("claude --desktop: %w", err)
	}
	return nil
}
