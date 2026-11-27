package wip

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

// TestMain isolates every test in this package from the host's git config,
// so commits never pick up a real identity or signing key.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "wip-git-cfg")
	if err != nil {
		panic(err)
	}
	os.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(tmp, "no-config"))
	os.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(tmp, "no-config"))
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitOutput(dir, args...)
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return out
}

// initRepo creates a local repo named name with an empty initial commit and
// a bare origin sibling — file:// transport only, no network.
func initRepo(t *testing.T, name string) (repo, origin string) {
	t.Helper()
	base := t.TempDir()
	origin = filepath.Join(base, "origin.git")
	repo = filepath.Join(base, name)
	gitRun(t, base, "init", "-q", "--bare", origin)
	gitRun(t, base, "init", "-q", repo)
	gitRun(t, repo, "remote", "add", "origin", origin)
	gitRun(t, repo, "-c", "user.name=tester", "-c", "user.email=tester@test",
		"commit", "-q", "--allow-empty", "-m", "initial")
	return repo, origin
}

func dirty(t *testing.T, repo, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "wip.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
}

func readLog(t *testing.T, home, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "logs", name))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return string(data)
}

func TestSessionStartRecordsManifest(t *testing.T) {
	repo, origin := initRepo(t, "alpha")
	home := t.TempDir()
	layout := storage.NewLayoutAt(home)

	res := SessionStart(layout, repo)
	if !res.Recorded || res.Skipped != "" || res.Error != "" {
		t.Fatalf("SessionStart = %+v, want Recorded only", res)
	}
	want := "alpha\t" + origin + "\n"
	data, err := os.ReadFile(filepath.Join(home, "workspace", "repos.tsv"))
	if err != nil {
		t.Fatalf("read repos.tsv: %v", err)
	}
	if string(data) != want {
		t.Errorf("repos.tsv = %q, want %q", string(data), want)
	}

	// Re-running must dedupe, not append a second line.
	res = SessionStart(layout, repo)
	if !res.Recorded {
		t.Fatalf("second SessionStart = %+v, want Recorded", res)
	}
	data, err = os.ReadFile(filepath.Join(home, "workspace", "repos.tsv"))
	if err != nil {
		t.Fatalf("re-read repos.tsv: %v", err)
	}
	if string(data) != want {
		t.Errorf("repos.tsv after re-run = %q, want %q", string(data), want)
	}
}

func TestSessionStartOutsideRepo(t *testing.T) {
	home := t.TempDir()
	layout := storage.NewLayoutAt(home)

	res := SessionStart(layout, home)
	if res.Recorded || res.Error != "" || !strings.Contains(res.Skipped, "not a git repo") {
		t.Fatalf("SessionStart = %+v, want skip outside a repo", res)
	}
	log := readLog(t, home, "session-start.log")
	if !strings.Contains(log, "skip: cwd is not a git repo with an origin remote") {
		t.Errorf("log = %q, want skip line", log)
	}
	if _, err := os.Stat(filepath.Join(home, "workspace", "repos.tsv")); !os.IsNotExist(err) {
		t.Errorf("repos.tsv should not exist, stat err = %v", err)
	}
}

func TestSessionEndPushesWIPBranch(t *testing.T) {
	repo, origin := initRepo(t, "alpha")
	dirty(t, repo, "dirty wip")
	home := t.TempDir()
	before := repoState(t, repo)

	res := SessionEnd(storage.NewLayoutAt(home), repo)
	host := storage.HostName()
	if !res.Pushed || res.Branch != "sync/"+host || res.Skipped != "" || res.Error != "" {
		t.Fatalf("SessionEnd = %+v, want push to sync/%s", res, host)
	}

	gitRun(t, origin, "rev-parse", "--verify", "--quiet", "refs/heads/sync/"+host)
	out := gitRun(t, origin, "show", "sync/"+host+":wip.txt")
	if strings.TrimSpace(out) != "dirty wip" {
		t.Errorf("wip.txt in sync/%s = %q, want %q", host, out, "dirty wip")
	}
	ref := "sync/" + host
	subject := strings.TrimSpace(gitRun(t, origin, "log", "-1", "--format=%s", ref))
	if want := regexp.MustCompile(`^wip: ` + regexp.QuoteMeta(host) + ` \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`); !want.MatchString(subject) {
		t.Errorf("commit subject = %q, want 'wip: %s <UTC>'", subject, host)
	}
	if got := strings.TrimSpace(gitRun(t, origin, "log", "-1", "--format=%an <%ae>", ref)); got != commitName+" <"+commitEmail+">" {
		t.Errorf("commit identity = %q", got)
	}
	if got := strings.TrimSpace(gitRun(t, origin, "rev-parse", ref+"^")); got != before.head {
		t.Errorf("snapshot parent = %s, want HEAD %s", got, before.head)
	}
	if after := repoState(t, repo); after != before {
		t.Errorf("the snapshot changed the repo:\n before %+v\n after  %+v", before, after)
	}
	if log := readLog(t, home, "wip.log"); !strings.Contains(log, "ok: pushed a snapshot (") || !strings.Contains(log, "to sync/"+host) {
		t.Errorf("wip.log = %q, want ok push line", log)
	}
}

// state is what the WIP snapshot must never change (D119).
type state struct{ head, branch, index, status string }

func repoState(t *testing.T, repo string) state {
	t.Helper()
	idx, _ := os.ReadFile(filepath.Join(repo, ".git", "index"))
	return state{
		head:   strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD")),
		branch: strings.TrimSpace(gitRun(t, repo, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/")),
		index:  string(idx),
		status: gitRun(t, repo, "--no-optional-locks", "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none"),
	}
}

// TestSessionEndSkipsNestedRepos: the v0.9.1 field report — a project
// with a nested repo and Claude Code's own worktrees under
// <sub>/.claude/worktrees. HEAD, the branches and the index stay as they
// were, and the pushed tree holds the user's change but no nested repo.
func TestSessionEndSkipsNestedRepos(t *testing.T) {
	repo, origin := initRepo(t, "alpha")
	gitRun(t, repo, "-c", "user.name=tester", "-c", "user.email=tester@test", "commit", "-q", "--allow-empty", "-m", "second")
	dirty(t, repo, "real change")
	for _, sub := range []string{"vendor/lib", "hardware/x/.claude/worktrees/remote-control-1", ".claude/worktrees/test-2"} {
		d := filepath.Join(repo, filepath.FromSlash(sub))
		gitRun(t, repo, "init", "-q", d)
		if err := os.WriteFile(filepath.Join(d, "f.txt"), []byte("nested"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRun(t, d, "add", "f.txt")
		gitRun(t, d, "-c", "user.name=tester", "-c", "user.email=tester@test", "commit", "-q", "-m", "n")
	}
	if err := os.WriteFile(filepath.Join(repo, "hardware", "x", "board.txt"), []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A staged-but-uncommitted change in the user's index stays theirs.
	if err := os.WriteFile(filepath.Join(repo, "staged.txt"), []byte("staged"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "staged.txt")
	home := t.TempDir()
	before := repoState(t, repo)

	res := SessionEnd(storage.NewLayoutAt(home), repo)
	if !res.Pushed {
		t.Fatalf("SessionEnd = %+v\n%s", res, readLog(t, home, "wip.log"))
	}
	if after := repoState(t, repo); after != before {
		t.Errorf("the snapshot changed the repo:\n before %+v\n after  %+v", before, after)
	}
	files := gitRun(t, origin, "ls-tree", "-r", "--format=%(objectmode) %(path)", "sync/"+storage.HostName())
	for _, want := range []string{"100644 wip.txt", "100644 staged.txt", "100644 hardware/x/board.txt"} {
		if !strings.Contains(files, want) {
			t.Errorf("pushed tree lacks %q:\n%s", want, files)
		}
	}
	if strings.Contains(files, "160000") || strings.Contains(files, "vendor/lib") || strings.Contains(files, "worktrees") {
		t.Errorf("pushed tree holds a nested repo:\n%s", files)
	}
	log := readLog(t, home, "wip.log")
	if !strings.Contains(log, "skipped 3 nested repositories (") || strings.Contains(log, "embedded git repository") {
		t.Errorf("wip.log = %q", log)
	}
}

// TestSessionEndChainsSnapshots: HEAD no longer moves, so each snapshot
// must still fast-forward sync/<host>: on top of an earlier snapshot, and
// as a merge once the user committed on their branch.
func TestSessionEndChainsSnapshots(t *testing.T) {
	repo, origin := initRepo(t, "alpha")
	home := t.TempDir()
	layout := storage.NewLayoutAt(home)
	ref := "sync/" + storage.HostName()
	tips := []string{}
	for i, step := range []func(){
		func() { dirty(t, repo, "one") },
		func() { dirty(t, repo, "two") },
		func() {
			gitRun(t, repo, "add", "wip.txt")
			gitRun(t, repo, "-c", "user.name=tester", "-c", "user.email=tester@test", "commit", "-q", "-m", "user work")
			dirty(t, repo, "three")
		},
	} {
		step()
		if res := SessionEnd(layout, repo); !res.Pushed {
			t.Fatalf("run %d: %+v\n%s", i, res, readLog(t, home, "wip.log"))
		}
		tips = append(tips, strings.TrimSpace(gitRun(t, origin, "rev-parse", ref)))
		if got := strings.TrimSpace(gitRun(t, origin, "show", ref+":wip.txt")); got != []string{"one", "two", "three"}[i] {
			t.Errorf("run %d: wip.txt = %q", i, got)
		}
	}
	if p := strings.TrimSpace(gitRun(t, origin, "rev-parse", tips[1]+"^")); p != tips[0] {
		t.Errorf("second snapshot's parent = %s, want the first %s", p, tips[0])
	}
	userHead := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	if ps := strings.Fields(gitRun(t, origin, "log", "-1", "--format=%P", tips[2])); len(ps) != 2 || ps[0] != userHead || ps[1] != tips[1] {
		t.Errorf("third snapshot's parents = %v, want [%s %s]", ps, userHead, tips[1])
	}
	if strings.Contains(gitRun(t, repo, "log", "--format=%an", "HEAD"), commitName) {
		t.Error("a WIP commit reached the user's branch")
	}
}

func TestSessionEndCleanTreeSkips(t *testing.T) {
	repo, _ := initRepo(t, "alpha")
	home := t.TempDir()
	head := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))

	res := SessionEnd(storage.NewLayoutAt(home), repo)
	if res.Pushed || res.Error != "" || !strings.Contains(res.Skipped, "tree is clean") {
		t.Fatalf("SessionEnd = %+v, want clean-tree skip", res)
	}
	if log := readLog(t, home, "wip.log"); !strings.Contains(log, "skip: tree is clean") {
		t.Errorf("wip.log = %q, want clean skip line", log)
	}
	if after := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD")); after != head {
		t.Errorf("HEAD moved %s -> %s on a clean tree; no commit expected", head, after)
	}
}

func TestSessionEndNoOriginSkips(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "alpha")
	gitRun(t, base, "init", "-q", repo)
	dirty(t, repo, "never committed")
	home := t.TempDir()

	res := SessionEnd(storage.NewLayoutAt(home), repo)
	if res.Pushed || res.Error != "" || !strings.Contains(res.Skipped, "not a git repo with an origin remote") {
		t.Fatalf("SessionEnd = %+v, want no-origin skip", res)
	}
	if log := readLog(t, home, "wip.log"); !strings.Contains(log, "skip: cwd is not a git repo") {
		t.Errorf("wip.log = %q, want skip line", log)
	}
}

func TestSessionEndRejectedPushKeepsNothing(t *testing.T) {
	repo, origin := initRepo(t, "alpha")
	// The remote sync branch holds history this repo has never seen and
	// cannot fetch (a non-fast-forward the push must not force), then an
	// unreachable origin.
	other := filepath.Join(t.TempDir(), "other")
	gitRun(t, filepath.Dir(other), "clone", "-q", origin, other)
	gitRun(t, other, "-c", "user.name=tester", "-c", "user.email=tester@test", "commit", "-q", "--allow-empty", "-m", "elsewhere")
	gitRun(t, other, "push", "-q", "origin", "HEAD:refs/heads/sync/"+storage.HostName())
	for i, url := range []string{origin, filepath.Join(t.TempDir(), "nowhere.git")} {
		gitRun(t, repo, "remote", "set-url", "origin", url)
		if i == 0 {
			// Unreachable tip: keep the fetch from succeeding.
			gitRun(t, repo, "config", "remote.origin.uploadpack", "false")
		}
		dirty(t, repo, "stranded wip")
		home := t.TempDir()
		before := repoState(t, repo)
		res := SessionEnd(storage.NewLayoutAt(home), repo)
		if res.Pushed || res.Skipped != "" || !strings.Contains(res.Error, "push failed for sync/") {
			t.Fatalf("%s: SessionEnd = %+v\n%s", url, res, readLog(t, home, "wip.log"))
		}
		if after := repoState(t, repo); after != before {
			t.Errorf("%s: a rejected push changed the repo:\n before %+v\n after  %+v", url, before, after)
		}
		if refs := gitRun(t, repo, "for-each-ref", "--format=%(refname)", "--contains", "HEAD"); strings.Contains(gitRun(t, repo, "log", "--all", "--format=%an"), commitName) {
			t.Errorf("%s: a WIP commit is reachable from a local ref (%s)", url, refs)
		}
		if log := readLog(t, home, "wip.log"); !strings.Contains(log, "error: push failed for sync/") {
			t.Errorf("wip.log = %q, want push-failure line", log)
		}
	}
}

func TestSyncStatus(t *testing.T) {
	repo, _ := initRepo(t, "alpha")
	orig := strings.TrimSpace(gitRun(t, repo, "symbolic-ref", "--short", "HEAD"))
	gitRun(t, repo, "checkout", "-q", "-b", "wipwork")
	dirty(t, repo, "other host wip")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "-c", "user.name=tester", "-c", "user.email=tester@test", "commit", "-q", "-m", "extra")
	gitRun(t, repo, "push", "-q", "origin", "HEAD:refs/heads/sync/otherhost")
	gitRun(t, repo, "checkout", "-q", orig)
	gitRun(t, repo, "push", "-q", "origin", "HEAD:refs/heads/sync/"+storage.HostName())

	var buf bytes.Buffer
	if err := SyncStatus(repo, &buf); err != nil {
		t.Fatalf("SyncStatus: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "otherhost") || !strings.Contains(out, "1 commit(s) ahead") {
		t.Errorf("SyncStatus output missing otherhost ahead count:\n%s", out)
	}
	if !strings.Contains(out, "git pull origin sync/otherhost") {
		t.Errorf("SyncStatus output missing pull hint for otherhost:\n%s", out)
	}
	mine := storage.HostName()
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, mine) && strings.Contains(line, "git pull") {
			t.Errorf("own sync branch must not carry a pull hint: %s", line)
		}
	}
}

func TestSyncStatusNoBranchesYet(t *testing.T) {
	repo, _ := initRepo(t, "alpha")
	var buf bytes.Buffer
	if err := SyncStatus(repo, &buf); err != nil {
		t.Fatalf("SyncStatus: %v", err)
	}
	if !strings.Contains(buf.String(), "no sync branches on origin yet") {
		t.Errorf("output = %q, want none-yet note", buf.String())
	}
}

func TestSyncStatusNotARepo(t *testing.T) {
	var buf bytes.Buffer
	err := SyncStatus(t.TempDir(), &buf)
	if err == nil {
		t.Fatal("SyncStatus outside a repo should error")
	}
	if !strings.Contains(err.Error(), "cannot list sync branches") {
		t.Errorf("err = %v, want cannot-list message", err)
	}
}

// TestFindStranded: a `wip: <host>` commit by cwm's identity at HEAD (what
// cwm ≤ 0.9.1 left on a branch) is found with its branch; the user's own
// commits, another host's and non-repos are not.
func TestFindStranded(t *testing.T) {
	host := storage.HostName()
	old, _ := initRepo(t, "old")
	dirty(t, old, "x")
	gitRun(t, old, "add", "-A")
	gitRun(t, old, "-c", "user.name="+commitName, "-c", "user.email="+commitEmail, "commit", "-q", "-m", "wip: "+host+" 2026-11-20T10:00:00Z")
	branch := strings.TrimSpace(gitRun(t, old, "symbolic-ref", "--short", "HEAD"))
	clean, _ := initRepo(t, "clean")
	other, _ := initRepo(t, "other")
	gitRun(t, other, "-c", "user.name="+commitName, "-c", "user.email="+commitEmail, "commit", "-q", "--allow-empty", "-m", "wip: elsewhere 2026-11-20T10:00:00Z")
	got := FindStranded([]string{old, clean, other, t.TempDir(), filepath.Join(t.TempDir(), "missing"), old}, host)
	if len(got) != 1 || got[0].Dir != old || got[0].Branch != branch {
		t.Fatalf("FindStranded = %+v, want [%s on %s]", got, old, branch)
	}
	// The new SessionEnd never strands one.
	dirty(t, clean, "y")
	if res := SessionEnd(storage.NewLayoutAt(t.TempDir()), clean); !res.Pushed {
		t.Fatalf("SessionEnd = %+v", res)
	}
	if got := FindStranded([]string{clean}, host); len(got) != 0 {
		t.Errorf("after a v0.9.2 snapshot: %+v", got)
	}
}
