package storage

import (
	"os"
	"strings"
)

// Slugify maps any string onto [A-Za-z0-9-] the same way Claude Code slugs
// OS-specific project paths (e.g. /home/u/repos/foo -> -home-u-repos-foo).
// Doctor uses it to recognize legacy unpinned session folders.
func Slugify(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// SanitizeHost normalizes a hostname for use in sync/<host> branch names:
// strip the domain, map onto [A-Za-z0-9-], trim edge dashes; empty → unknown.
func SanitizeHost(host string) string {
	if i := strings.IndexByte(host, '.'); i >= 0 {
		host = host[:i]
	}
	h := strings.Trim(Slugify(host), "-")
	if h == "" {
		return "unknown"
	}
	return h
}

// HostName returns the sanitized local hostname for WIP branch names.
func HostName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "unknown"
	}
	return SanitizeHost(host)
}
