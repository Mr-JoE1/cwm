//go:build !windows

package migrate

import "errors"

// errSharingViolation stands in for Windows' ERROR_SHARING_VIOLATION:
// Unix has no mandatory open locks, so only the test opener returns it.
var errSharingViolation = errors.New("the process cannot access the file because it is being used by another process (simulated)")

func isLocked(err error) bool { return errors.Is(err, errSharingViolation) }
