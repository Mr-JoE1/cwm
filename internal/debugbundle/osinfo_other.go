//go:build !windows

package debugbundle

import (
	"os"
	"regexp"
	"runtime"
	"strings"
)

var plistVersionRe = regexp.MustCompile(`<key>ProductVersion</key>\s*<string>([^<]+)</string>`)

// OSInfo is a one-line description of the OS release, read from files
// (no subprocess): /etc/os-release + the kernel on Linux, SystemVersion
// on macOS.
func OSInfo() string {
	switch runtime.GOOS {
	case "darwin":
		data, err := os.ReadFile("/System/Library/CoreServices/SystemVersion.plist")
		if m := plistVersionRe.FindSubmatch(data); err == nil && m != nil {
			return "macOS " + string(m[1])
		}
		return "macOS"
	case "linux":
		name := "Linux"
		if data, err := os.ReadFile("/etc/os-release"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
					name = strings.Trim(v, `"'`)
				}
			}
		}
		if k, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
			name += ", kernel " + strings.TrimSpace(string(k))
		}
		if os.Getenv("WSL_DISTRO_NAME") != "" {
			name += " (WSL)"
		}
		return name
	}
	return runtime.GOOS
}
