package pathmirror

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

// SessionReposDir is the synced workspace dir where each device records
// which repo its sessions ran in: session-repos/<host>.tsv, lines
// "<session id>\t<normalised remote>". One file per device, so two
// devices never write the same file and Syncthing never conflicts.
const SessionReposDir = "session-repos"

// RepoPathsFile is the machine-local (never synced) map of normalised
// remote -> this machine's clone path, under ~/.cwm/state.
const RepoPathsFile = "repo-paths.json"

// RecordSession appends id -> remote to root/session-repos/<host>.tsv
// unless that exact pair is already there.
func RecordSession(root, host, id, remote string) error {
	remote = NormalizeRemote(remote)
	if root == "" || id == "" || remote == "" || strings.ContainsAny(id+remote, "\t\n") {
		return nil
	}
	host = safeHost(host)
	p := filepath.Join(root, SessionReposDir, host+".tsv")
	if cur, err := readTSV(p); err == nil && cur[id] == remote {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("pathmirror: create %s: %w", filepath.Dir(p), err)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("pathmirror: open %s: %w", p, err)
	}
	if _, err := fmt.Fprintf(f, "%s\t%s\n", id, remote); err != nil {
		f.Close()
		return fmt.Errorf("pathmirror: write %s: %w", p, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("pathmirror: close %s: %w", p, err)
	}
	return nil
}

// safeHost keeps a hostname usable as a file name on every OS.
func safeHost(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func readTSV(p string) (map[string]string, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		id, remote, ok := strings.Cut(sc.Text(), "\t")
		if ok && id != "" && remote != "" {
			out[id] = strings.TrimSpace(remote) // a later line wins
		}
	}
	return out, sc.Err()
}

// SessionIndex is what every device recorded about its sessions.
type SessionIndex struct {
	Remote  map[string]string   // session id -> normalised remote
	Devices map[string][]string // session id -> sorted hosts that ran it
}

// LoadSessionIndex reads every root/session-repos/*.tsv. A missing dir
// is an empty index.
func LoadSessionIndex(root string) SessionIndex {
	idx := SessionIndex{Remote: map[string]string{}, Devices: map[string][]string{}}
	files, _ := filepath.Glob(filepath.Join(root, SessionReposDir, "*.tsv"))
	sort.Strings(files)
	for _, p := range files {
		m, err := readTSV(p)
		if err != nil {
			continue
		}
		host := strings.TrimSuffix(filepath.Base(p), ".tsv")
		for id, remote := range m {
			idx.Remote[id] = remote
			idx.Devices[id] = append(idx.Devices[id], host)
		}
	}
	return idx
}

// RepoPaths is the machine-local remote -> clone path map.
type RepoPaths struct {
	path  string
	Paths map[string]string `json:"paths"`
}

// RepoPathsPath is the map's location for l.
func RepoPathsPath(l *storage.Layout) string { return filepath.Join(l.StateDir(), RepoPathsFile) }

// LoadRepoPaths reads the map; a missing or unreadable file is empty
// (the map is a cache that hooks and resumes refill).
func LoadRepoPaths(p string) *RepoPaths {
	rp := &RepoPaths{path: p, Paths: map[string]string{}}
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, rp)
		if rp.Paths == nil {
			rp.Paths = map[string]string{}
		}
	}
	return rp
}

// Lookup returns the recorded local path of remote when it still exists.
func (rp *RepoPaths) Lookup(remote string) (string, bool) {
	p, ok := rp.Paths[NormalizeRemote(remote)]
	if !ok {
		return "", false
	}
	if info, err := os.Stat(p); err != nil || !info.IsDir() {
		return "", false
	}
	return p, true
}

// Record stores remote -> dir and saves the map when it changed.
func (rp *RepoPaths) Record(remote, dir string) error {
	remote = NormalizeRemote(remote)
	if remote == "" || dir == "" || rp.Paths[remote] == dir {
		return nil
	}
	rp.Paths[remote] = dir
	data, err := json.MarshalIndent(rp, "", "  ")
	if err != nil {
		return fmt.Errorf("pathmirror: encode repo paths: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(rp.path), 0o755); err != nil {
		return fmt.Errorf("pathmirror: create %s: %w", filepath.Dir(rp.path), err)
	}
	if err := storage.WriteFileAtomic(rp.path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("pathmirror: write %s: %w", rp.path, err)
	}
	return nil
}
