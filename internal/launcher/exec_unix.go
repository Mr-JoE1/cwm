//go:build unix

package launcher

import (
	"fmt"
	"os"
	"syscall"
)

// execClaude replaces this process with claude (ccam pattern): keystrokes,
// signals and the exit code belong to claude afterwards, and arguments
// stay an array instead of a shell string.
func execClaude(bin string, args []string) error {
	argv := append([]string{bin}, args...)
	if err := syscall.Exec(bin, argv, os.Environ()); err != nil {
		return fmt.Errorf("launcher: exec %s: %w", bin, err)
	}
	return nil
}
