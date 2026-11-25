package pathmirror

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionIndexAndRepoPaths(t *testing.T) {
	root := t.TempDir()
	if err := RecordSession(root, "Box A", "s1", "git@github.com:me/api.git"); err != nil {
		t.Fatal(err)
	}
	_ = RecordSession(root, "Box A", "s1", "https://github.com/me/api") // same pair: not appended
	_ = RecordSession(root, "b", "s1", "ssh://git@github.com/me/api.git")
	data, _ := os.ReadFile(filepath.Join(root, SessionReposDir, "box-a.tsv"))
	if strings.Count(string(data), "\n") != 1 {
		t.Errorf("duplicate record: %q", data)
	}
	idx := LoadSessionIndex(root)
	if idx.Remote["s1"] != "github.com/me/api" || strings.Join(idx.Devices["s1"], ",") != "b,box-a" {
		t.Errorf("index = %+v", idx)
	}
	state := filepath.Join(t.TempDir(), "state", RepoPathsFile)
	clone := t.TempDir()
	rp := LoadRepoPaths(state)
	if err := rp.Record("git@github.com:me/api.git", clone); err != nil {
		t.Fatal(err)
	}
	if got, ok := LoadRepoPaths(state).Lookup("https://github.com/me/api"); !ok || got != clone {
		t.Errorf("lookup = %q %v", got, ok)
	}
	if _, ok := LoadRepoPaths(state).Lookup("github.com/me/other"); ok {
		t.Error("unknown remote found")
	}
}

// fakeEnv is device B: its clone of github.com/me/api sits at localRepo,
// a different path than device A's srcRepo.
func fakeEnv(t *testing.T, root, reposRoot string) *Env {
	t.Helper()
	return &Env{
		Root:      root,
		Paths:     LoadRepoPaths(filepath.Join(t.TempDir(), RepoPathsFile)),
		Index:     LoadSessionIndex(root),
		ReposRoot: reposRoot,
		Manifest: func(name string) string {
			if name == "api" {
				return "git@github.com:me/api.git"
			}
			return ""
		},
		RemoteOf: func(dir string) string {
			if filepath.Base(dir) == "api" {
				return "https://github.com/me/api"
			}
			return ""
		},
	}
}

func TestPlanResumeFindsTheLocalClone(t *testing.T) {
	root, repos := t.TempDir(), t.TempDir()
	srcRepo := "/nowhere-A/home/projects/api"
	write(t, filepath.Join(root, "projects", FolderName(srcRepo), "s1.jsonl"), ln("1", srcRepo, "2026-11-24T10:00:00Z"))
	e := fakeEnv(t, root, repos)

	// No clone yet: clone target under the repos root, URL from repos.tsv.
	p, err := e.PlanResume("s1")
	if err != nil || p.Local != filepath.Join(repos, "api") || p.Exists || p.URL == "" || p.Remote != "github.com/me/api" {
		t.Fatalf("plan (no clone) = %+v %v", p, err)
	}
	// The clone exists: resume there, mirroring needed.
	if err := os.MkdirAll(filepath.Join(repos, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err = e.PlanResume("s1")
	if err != nil || !p.Exists || !p.NeedsMirror() {
		t.Fatalf("plan (clone) = %+v %v", p, err)
	}
	res, err := e.Apply(p)
	if err != nil || !res.Wrote {
		t.Fatalf("apply = %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(root, "projects", FolderName(filepath.Join(repos, "api")), "s1.jsonl")); err != nil {
		t.Error("local copy missing")
	}
	// A clone with another origin is not this repo.
	e.RemoteOf = func(string) string { return "git@github.com:someone/else.git" }
	if p, _ := e.PlanResume("s1"); p.Exists {
		t.Errorf("foreign clone used: %+v", p)
	}
	if _, err := e.PlanResume("nope"); err == nil {
		t.Error("unknown session planned")
	}
}

func TestTickMirrorsOnceAndNeverPingPongs(t *testing.T) {
	root, repos := t.TempDir(), t.TempDir()
	local := filepath.Join(repos, "api")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	srcRepo := "/nowhere-A/home/projects/api"
	src := filepath.Join(root, "projects", FolderName(srcRepo), "s1.jsonl")
	write(t, src, ln("1", srcRepo, "2026-11-24T10:00:00Z"))
	// A session made on this machine is left alone without being parsed.
	write(t, filepath.Join(root, "projects", FolderName(local), "own.jsonl"), ln("o", local, "2026-11-24T09:00:00Z"))
	e := fakeEnv(t, root, repos)
	m := &Mirrorer{MinInterval: time.Minute}
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	rep := m.Tick(e, now)
	if !rep.Ran || strings.Join(rep.Mirrored, ",") != "s1" || len(rep.Errors) > 0 {
		t.Fatalf("tick 1 = %+v", rep)
	}
	if rep := m.Tick(e, now.Add(time.Second)); rep.Ran {
		t.Error("rate limit ignored")
	}
	if rep := m.Tick(e, now.Add(2*time.Minute)); rep.Checked != 0 || len(rep.Mirrored) != 0 {
		t.Errorf("unchanged sessions re-read: %+v", rep)
	}
	// A fresh agent (no signatures) compares by content: nothing to write.
	if rep := (&Mirrorer{}).Tick(e, now); len(rep.Mirrored) != 0 {
		t.Errorf("ping-pong: %+v", rep)
	}
	// The source grows: the local copy is extended.
	f, _ := os.OpenFile(src, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(ln("2", srcRepo, "2026-11-24T10:05:00Z") + "\n")
	f.Close()
	if rep := m.Tick(e, now.Add(4*time.Minute)); strings.Join(rep.Mirrored, ",") != "s1" {
		t.Errorf("growth not mirrored: %+v", rep)
	}
	if div, _ := Diverged(root); len(div) != 0 || Mirrored(root) != 1 {
		t.Errorf("diverged = %v mirrored = %d", div, Mirrored(root))
	}
	// Both continue: diverged, reported, nothing written.
	f, _ = os.OpenFile(src, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(ln("3a", srcRepo, "2026-11-24T11:00:00Z") + "\n")
	f.Close()
	lp := filepath.Join(root, "projects", FolderName(local), "s1.jsonl")
	f, _ = os.OpenFile(lp, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(ln("3b", local, "2026-11-24T11:30:00Z") + "\n")
	f.Close()
	before, _ := os.ReadFile(lp)
	rep = m.Tick(e, now.Add(6*time.Minute))
	if strings.Join(rep.Diverged, ",") != "s1" || len(rep.Mirrored) != 0 {
		t.Errorf("divergence = %+v", rep)
	}
	if after, _ := os.ReadFile(lp); string(after) != string(before) {
		t.Error("diverged local copy overwritten")
	}
	if div, _ := Diverged(root); strings.Join(div, ",") != "s1" {
		t.Errorf("Diverged = %v", div)
	}
}

func TestFirstCwd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, p, `{"type":"summary"}`, ln("1", "/x/y", "t"))
	if got := FirstCwd(p); got != "/x/y" {
		t.Errorf("FirstCwd = %q", got)
	}
}
