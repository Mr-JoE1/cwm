//go:build !windows

package cli

// consoleOwnedBySelf is a Windows-only notion (D35).
func consoleOwnedBySelf() bool { return false }

// alert has no GUI on POSIX: the message is already in the agent log.
func alert(string, string) {}
