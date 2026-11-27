//go:build !windows

package desktopapp

// fileLocked: macOS and Linux have no mandatory locks to observe, so the
// app's running state is unknown there (mtimes still pick the root).
func fileLocked(string) bool { return false }
