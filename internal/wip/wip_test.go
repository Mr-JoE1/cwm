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

	subject := strings.TrimSpace(gitRun(t, repo, "log", "-1", "--format=%s"))
	if want := regexp.MustCompile(`^wip: ` + regexp.QuoteMeta(host) + ` \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`); !want.MatchString(subject) {
		t.Errorf("commit subject = %q, want 'wip: %s <UTC>'", subject, host)
	}
	if got := strings.TrimSpace(gitRun(t, repo, "log", "-1", "--format=%an")); got != commitName {
		t.Errorf("commit author = %q, want %q", got, commitName)
	}
	if got := strings.TrimSpace(gitRun(t, repo, "log", "-1", "--format=%ae")); got != commitEmail {
		t.Errorf("commit email = %q, want %q", got, commitEmail)
	}
	if log := readLog(t, home, "wip.log"); !strings.Contains(log, "ok: pushed HEAD to sync/"+host) {
		t.Errorf("wip.log = %q, want ok push line", log)
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

func TestSessionEndPushFailureLeavesLocalCommit(t *testing.T) {
	repo, _ := initRepo(t, "alpha")
	// Point origin at a path that cannot be pushed to.
	gitRun(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "nowhere.git"))
	dirty(t, repo, "stranded wip")
	home := t.TempDir()

	res := SessionEnd(storage.NewLayoutAt(home), repo)
	if res.Pushed || res.Skipped != "" || !strings.Contains(res.Error, "push failed for sync/") {
		t.Fatalf("SessionEnd = %+v, want push-failure report", res)
	}
	// The WIP commit must survive locally (bash kit semantics).
	if got := strings.TrimSpace(gitRun(t, repo, "log", "-1", "--format=%an")); got != commitName {
		t.Errorf("local WIP commit author = %q, want %q", got, commitName)
	}
	if log := readLog(t, home, "wip.log"); !strings.Contains(log, "error: push failed for sync/") {
		t.Errorf("wip.log = %q, want push-failure line", log)
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
