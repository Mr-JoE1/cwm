//go:build windows

package cli

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	user32                    = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW           = user32.NewProc("MessageBoxW")
)

// consoleOwnedBySelf: a console attached to exactly one process is the one
// Windows created for us — Explorer started cwm.exe. From cmd/PowerShell
// the shell is attached too (count >= 2).
func consoleOwnedBySelf() bool {
	if procGetConsoleProcessList.Find() != nil {
		return false
	}
	var pids [4]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return n == 1
}

// mbIconError | mbOK | mbSetForeground
const mbFlags = 0x10 | 0x0 | 0x10000

func alert(title, msg string) {
	if procMessageBoxW.Find() != nil {
		return
	}
	t, err1 := syscall.UTF16PtrFromString(title)
	m, err2 := syscall.UTF16PtrFromString(msg)
	if err1 != nil || err2 != nil {
		return
	}
	_, _, _ = procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbFlags)
}
