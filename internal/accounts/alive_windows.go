//go:build windows

package accounts

import "syscall"

// stillActive is GetExitCodeProcess's "has not exited" value.
const stillActive = 259

// processQueryLimitedInformation is the least access that allows
// GetExitCodeProcess.
const processQueryLimitedInformation = 0x1000

// processAlive reports whether pid still runs. An exited process can
// still be opened while someone holds a handle, so the exit code decides.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
