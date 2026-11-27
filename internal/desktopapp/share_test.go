package desktopapp

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// shareFixture: a live profile where work (signed in) has one list and
// default has another; returns root and the two list dirs.
func shareFixture(t *testing.T) (root, workList, defList string) {
	t.Helper()
	root = t.TempDir()
	profile(t, root, uuidWork, now)
	workList = filepath.Join(root, SessionsDir, uuidWork, "orgW")
	defList = filepath.Join(root, SessionsDir, uuidDefault, "orgD")
	entry(t, workList, "local_w1", "cli-w1")
	entry(t, defList, "local_d1", "cli-d1")
	entry(t, defList, "local_d2", "cli-d2")
	return root, workList, defList
}

func entry(t *testing.T, dir, name, cli string) {
	t.Helper()
	write(t, filepath.Join(dir, name+".json"), `{"title":"t `+name+`","cwd":"/x","cliSessionId":"`+cli+`"}`, mid)
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func TestShareAddOnly(t *testing.T) {
	root, work, def := shareFixture(t)
	// An existing entry with the same file name keeps its own content.
	write(t, filepath.Join(work, "local_d2.json"), `{"cliSessionId":"cli-d2","title":"mine"}`, mid)
	res, err := Share(root, uuidWork, ShareOptions{})
	if err != nil || res.Added != 1 || res.Lists != 1 {
		t.Fatalf("Share = %+v %v", res, err)
	}
	if got := strings.Join(dirNames(t, work), ","); got != "local_d1.json,local_d2.json,local_w1.json" {
		t.Fatalf("work list = %s", got)
	}
	if b, _ := os.ReadFile(filepath.Join(work, "local_d2.json")); !strings.Contains(string(b), "mine") {
		t.Fatalf("existing entry overwritten: %s", b)
	}
	src, _ := os.ReadFile(filepath.Join(def, "local_d1.json"))
	dst, _ := os.ReadFile(filepath.Join(work, "local_d1.json"))
	if string(src) != string(dst) {
		t.Fatal("copied entry differs")
	}
	if fi, _ := os.Stat(filepath.Join(work, "local_d1.json")); !fi.ModTime().Equal(mid.Truncate(fi.ModTime().Sub(mid.Truncate(0))/1e18)) && fi.ModTime().Unix() != mid.Unix() {
		t.Fatalf("mtime not kept: %v", fi.ModTime())
	}
	// The source account's list is never written to.
	if got := strings.Join(dirNames(t, def), ","); got != "local_d1.json,local_d2.json" {
		t.Fatalf("default list changed: %s", got)
	}
	// Idempotent.
	if res, _ := Share(root, uuidWork, ShareOptions{}); res.Added != 0 {
		t.Fatalf("second run added %d", res.Added)
	}
}

func TestShareDedupeByCliSessionID(t *testing.T) {
	root, work, def := shareFixture(t)
	// default lists cli-w1 under another entry id.
	entry(t, def, "local_other", "cli-w1")
	res, _ := Share(root, uuidWork, ShareOptions{})
	if res.Added != 2 {
		t.Fatalf("Added = %d, want d1 and d2 only", res.Added)
	}
	if _, err := os.Stat(filepath.Join(work, "local_other.json")); err == nil {
		t.Fatal("duplicate conversation copied")
	}
}

func TestShareHonoursDeletedMarkers(t *testing.T) {
	root, work, def := shareFixture(t)
	write(t, filepath.Join(work, "deleted_d1"), "", mid)          // by entry id
	write(t, filepath.Join(work, "deleted_cli-d2.json"), "", mid) // by cliSessionId
	entry(t, def, "local_d3", "cli-d3")                           // deleted in its own list
	write(t, filepath.Join(def, "deleted_local_d3.json"), "", mid)
	res, _ := Share(root, uuidWork, ShareOptions{})
	if res.Added != 0 {
		t.Fatalf("Added = %d, deleted entries came back: %v", res.Added, dirNames(t, work))
	}
}

func TestShareNoTargetIsNoop(t *testing.T) {
	root, _, _ := shareFixture(t)
	if err := os.RemoveAll(filepath.Join(root, SessionsDir, uuidWork)); err != nil {
		t.Fatal(err)
	}
	res, err := Share(root, uuidWork, ShareOptions{})
	if err != nil || res.Added != 0 || res.Skipped != SkipNoList {
		t.Fatalf("Share = %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(root, SessionsDir, uuidWork)); err == nil {
		t.Fatal("created a list for an account that has none")
	}
	if res, _ := Share(root, "", ShareOptions{}); res.Skipped != SkipNoAccount {
		t.Fatalf("no uuid: %+v", res)
	}
	if res, _ := Share(root, "../x", ShareOptions{}); res.Skipped != SkipNoAccount {
		t.Fatalf("path uuid: %+v", res)
	}
}

func TestShareOtherFilesUntouched(t *testing.T) {
	root, work, def := shareFixture(t)
	write(t, filepath.Join(def, "tasks.json"), "{}", mid)
	write(t, filepath.Join(def, "settings.json"), "{}", mid)
	write(t, filepath.Join(def, "local_big.txt"), "x", mid)
	if err := os.MkdirAll(filepath.Join(def, "local_dir.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, "config.json"))
	res, _ := Share(root, uuidWork, ShareOptions{})
	if res.Added != 2 {
		t.Fatalf("Added = %d", res.Added)
	}
	if got := strings.Join(dirNames(t, work), ","); got != "local_d1.json,local_d2.json,local_w1.json" {
		t.Fatalf("work list = %s (only local_*.json may be copied, no temp files left)", got)
	}
	after, _ := os.ReadFile(filepath.Join(root, "config.json"))
	if string(before) != string(after) {
		t.Fatal("config.json changed")
	}
}

func TestShareDryRunAndSession(t *testing.T) {
	root, work, _ := shareFixture(t)
	res, _ := Share(root, uuidWork, ShareOptions{DryRun: true})
	if res.Added != 2 || len(dirNames(t, work)) != 1 {
		t.Fatalf("dry run: %+v %v", res, dirNames(t, work))
	}
	res, _ = Share(root, uuidWork, ShareOptions{Session: "cli-d2"})
	if res.Added != 1 || strings.Join(dirNames(t, work), ",") != "local_d2.json,local_w1.json" {
		t.Fatalf("session run: %+v %v", res, dirNames(t, work))
	}
}

func TestSyncSummary(t *testing.T) {
	root, _, _ := shareFixture(t)
	st := Status{Root: root, State: StateAccount, Account: "work", uuid: uuidWork}
	res, line, err := Sync(st, ShareOptions{DryRun: true})
	if err != nil || res.Added != 2 || line != "added 2 Code-tab entries for work" {
		t.Fatalf("Sync = %+v %q %v", res, line, err)
	}
	st.Running = true
	if line := (ShareResult{Added: 1}).Summary(st); line != "added 1 Code-tab entry for work — the Claude app is running and lists them after it restarts" {
		t.Fatalf("Summary = %q", line)
	}
	if _, line, _ := Sync(Status{State: StateNone}, ShareOptions{}); line != "" {
		t.Fatalf("none: %q", line)
	}
}
