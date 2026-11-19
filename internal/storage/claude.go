package storage

import "strings"

// MinVersion is the oldest claude release supporting project-name pinning
// (CLAUDE_CODE_PROJECT_DIR_NAME), the feature the whole kit depends on.
// Locating the binary lives in internal/claudebin (D49).
const MinVersion = "2.1.234"

// CompareVersions compares dotted numeric versions a and b and returns -1, 0
// or 1. Missing or non-numeric segments count as 0, so "2.1" == "2.1.0".
// Anything unparseable as a leading number sorts first.
func CompareVersions(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := max(len(as), len(bs))
	for i := range n {
		av, bv := 0, 0
		if i < len(as) {
			av = numericPrefix(as[i])
		}
		if i < len(bs) {
			bv = numericPrefix(bs[i])
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

func numericPrefix(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	v := 0
	for _, r := range s[:end] {
		v = v*10 + int(r-'0')
	}
	return v
}

// VersionAtLeast reports whether v meets MinVersion.
func VersionAtLeast(v string) bool {
	return CompareVersions(v, MinVersion) >= 0
}
