package desktopapp

import (
	"errors"
	"syscall"
)

// errSharingViolation is ERROR_SHARING_VIOLATION.
const errSharingViolation = syscall.Errno(32)

// fileLocked opens path asking for no sharing at all, which fails while
// any other process holds it open (the running app's cookie store). The
// handle is closed at once; nothing is read.
func fileLocked(path string) bool {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return errors.Is(err, errSharingViolation)
	}
	_ = syscall.CloseHandle(h)
	return false
}
