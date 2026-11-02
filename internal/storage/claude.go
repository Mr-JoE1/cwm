package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// MinVersion is the oldest claude release supporting project-name pinning
// (CLAUDE_CODE_PROJECT_DIR_NAME), the feature the whole kit depends on.
const MinVersion = "2.1.234"

const claudeExe = "claude"

var versionRe = regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)

// pathHash fingerprints $PATH so a stale cached claude path is re-resolved
// whenever PATH order or content changes (ccam pattern).
func pathHash(pathEnv string) string {
	sum := sha256.Sum256([]byte(pathEnv))
	return hex.EncodeToString(sum[:])
}

type claudeCache struct {
	PathHash string
	Path     string
}

func readClaudeCache(cachePath string) (claudeCache, bool) {
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return claudeCache{}, false
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		return claudeCache{}, false
	}
	return claudeCache{PathHash: strings.TrimSpace(lines[0]), Path: strings.TrimSpace(lines[1])}, true
}

func writeClaudeCache(cachePath, hash, path string) {
	_ = os.MkdirAll(filepath.Dir(cachePath), 0o755)
	_ = WriteFileAtomic(cachePath, []byte(hash+"\n"+path+"\n"), 0o644)
}

func claudeCachePath() (string, error) {
	layout, err := NewLayout()
	if err != nil {
		return "", err
	}
	return filepath.Join(layout.StateDir(), "claude-path"), nil
}

// ClaudeBin resolves the claude binary on PATH (memoized in
// $CWM_HOME/state/claude-path, keyed by a hash of PATH) and returns its
// parsed version. An unparseable version yields "" without error: callers
// like doctor report that case themselves.
func ClaudeBin() (path string, version string, err error) {
	hash := pathHash(os.Getenv("PATH"))

	if cachePath, cerr := claudeCachePath(); cerr == nil {
		if c, ok := readClaudeCache(cachePath); ok && c.PathHash == hash {
			if _, serr := os.Stat(c.Path); serr == nil {
				return c.Path, claudeVersion(c.Path), nil
			}
		}
	}

	path, err = exec.LookPath(claudeExe)
	if err != nil {
		return "", "", fmt.Errorf("storage: claude not found on PATH: %w", err)
	}
	if cachePath, cerr := claudeCachePath(); cerr == nil {
		writeClaudeCache(cachePath, hash, path)
	}
	return path, claudeVersion(path), nil
}

func claudeVersion(path string) string {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			out = append(out, ee.Stderr...)
		}
	}
	return versionRe.FindString(string(out))
}

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
