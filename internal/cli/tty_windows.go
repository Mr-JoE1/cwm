//go:build windows

package cli

import (
	"os"
	"syscall"
)

// isConsole: f is a console (GetConsoleMode succeeds). NUL and pipes are
// not, which is what Node's isTTY sees too.
func isConsole(f *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil
}
