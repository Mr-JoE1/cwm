//go:build !windows

package term

import "syscall"

// startDetached runs the terminal in its own session so a Ctrl-C in the
// `cwm gui` terminal never reaches the windows it opened.
func startDetached(spec Spec) error {
	cmd := command(spec)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap; the terminal's exit status is irrelevant
	return nil
}
