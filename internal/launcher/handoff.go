package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/storage"
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
// once it succeeds. Each hand-off is appended to logs/handoff.log (D123).
func startClaude(layout *storage.Layout, bin string, args, extra []string) error {
	if !IsHandOff(extra) {
		return execClaude(bin, args)
	}
	err := HandOffCmd(bin, args).Run()
	LogHandOff(HandOffLogPath(layout), bin, args, err, time.Now())
	if err != nil {
		return fmt.Errorf("claude --desktop: %w", err)
	}
	return nil
}

// HandOffLogName is the hand-off log in ~/.cwm/logs (in the debug bundle).
const HandOffLogName = "handoff.log"

// HandOffLogPath is layout's logs/handoff.log.
func HandOffLogPath(layout *storage.Layout) string {
	return filepath.Join(layout.LogsDir(), HandOffLogName)
}

// handOffLogMax caps handoff.log; past it the file starts over (the
// previous one is kept as handoff.log.1).
const handOffLogMax = 256 << 10

// HandOffNote says what the log cannot hold: claude --desktop refuses
// redirected stdout and stdin, so its own messages ("Opening session … in
// Claude Desktop", "Session … is open in another terminal") are only in
// its window.
const HandOffNote = "claude's own messages are only in its window (it refuses redirected output)"

// LogHandOff appends one hand-off — the command, its folder, exit code and
// error — to path, best effort.
func LogHandOff(path, bin string, args []string, runErr error, now time.Time) {
	code := 0
	var ee *exec.ExitError
	switch {
	case errors.As(runErr, &ee):
		code = ee.ExitCode()
	case runErr != nil:
		code = -1
	}
	cwd, _ := os.Getwd()
	line := fmt.Sprintf("%s %s %s (in %s) exit %d", now.Format(time.RFC3339), bin, strings.Join(args, " "), cwd, code)
	if runErr != nil && ee == nil {
		line += " (" + runErr.Error() + ")"
	}
	line += " — " + HandOffNote + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > handOffLogMax {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}
