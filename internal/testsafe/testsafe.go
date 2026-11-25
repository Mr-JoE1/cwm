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

// homeVars are every variable a cwm or Claude Code path can derive from.
var homeVars = []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"}

// IsolateHome points HOME, USERPROFILE, LOCALAPPDATA, APPDATA, the XDG
// dirs and CWM_HOME at a fresh temp dir, so a test that forgets its own
// t.Setenv can never reach the machine's real ~/.claude, ~/.claude.json or
// ~/.cwm: since v0.8 capture mode resolves the workspace to ~/.claude
// (D76). It returns the dir and a cleanup.
func IsolateHome() (string, func()) {
	dir, err := os.MkdirTemp("", "cwm-test-home-")
	if err != nil {
		// No temp dir means no safe HOME: point everything at a path that
		// cannot exist rather than at the real one.
		dir = filepath.Join(string(filepath.Separator), "nonexistent-cwm-test-home")
	}
	for _, k := range homeVars {
		os.Setenv(k, dir)
	}
	os.Setenv("CWM_HOME", filepath.Join(dir, ".cwm"))
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	return dir, func() { _ = os.RemoveAll(dir) }
}

// Main is the TestMain body every package uses: no real claude, no real
// home, then the tests.
func Main(m interface{ Run() int }) int {
	NoRealClaude()
	_, cleanup := IsolateHome()
	defer cleanup()
	return m.Run()
}
