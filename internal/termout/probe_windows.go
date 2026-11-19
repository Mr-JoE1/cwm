//go:build windows

package termout

import (
	"os"
	"syscall"
	"unsafe"
)

// golang.org/x/sys is not a dependency (AGENT.md: stdlib + toml only), so
// the two kernel32 calls are bound by hand.
var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleOutputCP = kernel32.NewProc("GetConsoleOutputCP")
	procGetConsoleMode     = kernel32.NewProc("GetConsoleMode")
)

func probe(f *os.File) Probe {
	return Probe{
		GOOS:      "windows",
		CodePage:  consoleOutputCP(),
		IsConsole: isConsole(f),
		WTSession: os.Getenv("WT_SESSION"),
		Override:  os.Getenv(EnvGlyphs),
	}
}

func consoleOutputCP() uint32 {
	if procGetConsoleOutputCP.Find() != nil {
		return 0
	}
	cp, _, _ := procGetConsoleOutputCP.Call()
	return uint32(cp)
}

// isConsole: GetConsoleMode succeeds only on console handles.
func isConsole(f *os.File) bool {
	if f == nil || procGetConsoleMode.Find() != nil {
		return false
	}
	var mode uint32
	r, _, _ := procGetConsoleMode.Call(f.Fd(), uintptr(unsafe.Pointer(&mode)))
	return r != 0
}
