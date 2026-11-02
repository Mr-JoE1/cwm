//go:build windows

package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// execClaude starts claude as a child with the same console, waits for it
// and exits with its code: Windows has no exec(2) (ccam pattern).
func execClaude(bin string, args []string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.ExitCode())
	}
	if err != nil {
		return fmt.Errorf("launcher: run %s: %w", bin, err)
	}
	os.Exit(0)
	return nil
}
