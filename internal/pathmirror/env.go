package pathmirror

import (
	"os"
	"os/exec"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/manifest"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// NewEnv is the real-machine Env for l (already resolved for its mode):
// the workspace's session index and repos.tsv, this machine's repo-path
// map and clone root, and git for a clone's origin.
func NewEnv(l *storage.Layout) *Env {
	e := &Env{
		Root:     l.WorkspaceDir(),
		Paths:    LoadRepoPaths(RepoPathsPath(l)),
		Index:    LoadSessionIndex(l.WorkspaceDir()),
		RemoteOf: GitRemote,
	}
	e.ReposRoot, _ = storage.ReposRoot()
	if m, err := manifest.Load(l.ManifestPath()); err == nil {
		e.Manifest = func(name string) string {
			u, _ := m.Lookup(name)
			return u
		}
	}
	return e
}

// GitRemote is the origin URL of the clone at dir ("" when dir is not a
// git repo with an origin).
func GitRemote(dir string) string {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
