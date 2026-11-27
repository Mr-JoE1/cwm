package claudeopen

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// waitCap bounds a waited step: an editor CLI hands the folder to a
// running window and exits in about a second; a cold start may take
// longer, and the URI then simply queues behind it.
const waitCap = 10 * time.Second

// settle is the pause after opening a folder by URI (no process to wait
// for), so the session URI reaches the new window.
const settle = 2 * time.Second

// Run starts steps in order with start (nil = the real exec).
func Run(steps []Step, start func(Step) error) error {
	if start == nil {
		start = startStep
	}
	for _, s := range steps {
		if err := start(s); err != nil {
			return err
		}
	}
	return nil
}

func startStep(s Step) error {
	if len(s.Argv) == 0 {
		return fmt.Errorf("claudeopen: empty command")
	}
	path, err := exec.LookPath(s.Argv[0])
	if err != nil {
		return fmt.Errorf("%s not found: %w", s.Argv[0], err)
	}
	if !s.Wait {
		cmd := exec.Command(path, s.Argv[1:]...)
		hideWindow(cmd)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start %s: %w", s.Argv[0], err)
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitCap)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, s.Argv[1:]...)
	hideWindow(cmd)
	t0 := time.Now()
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("%s: %w", s.Argv[0], err)
	}
	if time.Since(t0) < settle {
		time.Sleep(settle - time.Since(t0))
	}
	return nil
}

// HideConsole keeps a child started from the windowless agent from
// flashing a console window (Windows; a no-op elsewhere).
func HideConsole(cmd *exec.Cmd) { hideWindow(cmd) }
