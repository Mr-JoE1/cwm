//go:build !windows

package termout

import (
	"os"
	"runtime"
)

func probe(*os.File) Probe {
	return Probe{GOOS: runtime.GOOS, Override: os.Getenv(EnvGlyphs)}
}
