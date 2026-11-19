// Package testsafe keeps tests away from a real Claude Code install. It
// is imported only by test files (TestMain), never by cwm itself.
package testsafe

import (
	"os"
	"path/filepath"
	"strings"
)

// NoRealClaude disables the resolver's folder search
// (CWM_CLAUDE_NO_SEARCH=1) and drops every PATH entry that holds a
// `claude` executable (and repeated entries), so a test that prepends its own stub to the
// inherited PATH can never fall through to the machine's real claude —
// which, since v0.6.2, the resolver does when a stub fails verification.
func NoRealClaude() {
	os.Setenv("CWM_CLAUDE_NO_SEARCH", "1")
	os.Setenv("CWM_CLAUDE", "")
	// Duplicates go too, so doctor's PATH-duplicates note (D58) only
	// shows up in the tests that ask for it.
	var keep []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || seen[dir] || hasClaude(dir) {
			continue
		}
		seen[dir] = true
		keep = append(keep, dir)
	}
	os.Setenv("PATH", strings.Join(keep, string(os.PathListSeparator)))
}

func hasClaude(dir string) bool {
	for _, name := range []string{"claude", "claude.exe", "claude.cmd", "claude.bat", "claude.com"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}
