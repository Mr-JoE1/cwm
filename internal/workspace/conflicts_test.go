package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

func conflictLayout(t *testing.T) (*storage.Layout, string) {
	t.Helper()
	l := storage.NewLayoutAt(filepath.Join(t.TempDir(), "cwm"))
	root := filepath.Join(t.TempDir(), "claude")
	l.SetWorkspaceDir(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return l, root
}

func put(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseConflictName(t *testing.T) {
	cases := []struct{ in, orig, dev string }{
		{"history.sync-conflict-20261124-101010-ABCDEF2.jsonl", "history.jsonl", "ABCDEF2"},
		{"CLAUDE.sync-conflict-20261124-101010-ZZZZZZZ.md", "CLAUDE.md", "ZZZZZZZ"},
		{"Makefile.sync-conflict-20261124-101010-AAAAAAA", "Makefile", "AAAAAAA"},
	}
	for _, c := range cases {
		orig, dev, _, ok := ParseConflictName(c.in)
		if !ok || orig != c.orig || dev != c.dev {
			t.Errorf("%s: got %q %q %v", c.in, orig, dev, ok)
		}
	}
	for _, no := range []string{"history.jsonl", "x.sync-conflict-2026-ABC.md", "CLAUDE.md"} {
		if _, _, _, ok := ParseConflictName(no); ok {
			t.Errorf("%s parsed as a conflict", no)
		}
	}
}

func TestMergeHistoryConflicts(t *testing.T) {
	l, root := conflictLayout(t)
	put(t, filepath.Join(root, "history.jsonl"),
		`{"display":"a","timestamp":100}`+"\n"+`{"display":"c","timestamp":300}`+"\n")
	conflict := filepath.Join(root, "history.sync-conflict-20261124-101010-ABCDEF2.jsonl")
	put(t, conflict,
		`{"display":"a","timestamp":100}`+"\n"+`{"display":"b","timestamp":200}`+"\n"+`{"display":"d","timestamp":400}`+"\n")
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	n, err := MergeHistoryConflicts(l, now)
	if err != nil || n != 1 {
		t.Fatalf("merged %d, %v", n, err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "history.jsonl"))
	want := `{"display":"a","timestamp":100}` + "\n" + `{"display":"b","timestamp":200}` + "\n" +
		`{"display":"c","timestamp":300}` + "\n" + `{"display":"d","timestamp":400}` + "\n"
	if string(got) != want {
		t.Fatalf("history =\n%s\nwant\n%s", got, want)
	}
	if _, err := os.Stat(conflict); !os.IsNotExist(err) {
		t.Fatal("conflict copy still in the workspace")
	}
	moved := filepath.Join(ConflictBackupDir(l, now), filepath.Base(conflict))
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("conflict copy not in backups: %v", err)
	}
	// A second tick with no copies writes nothing.
	if n, err := MergeHistoryConflicts(l, now); err != nil || n != 0 {
		t.Fatalf("second merge %d, %v", n, err)
	}
}

func TestFindAndResolveConflicts(t *testing.T) {
	l, root := conflictLayout(t)
	cfg := config.Default()
	put(t, filepath.Join(root, "CLAUDE.md"), "mine\nshared\n")
	put(t, filepath.Join(root, "CLAUDE.sync-conflict-20261124-101010-ABCDEF2.md"), "theirs\nshared\n")
	put(t, filepath.Join(root, "skills", "x", "SKILL.sync-conflict-20261124-101010-ABCDEF2.md"), "s2\n")
	put(t, filepath.Join(root, "skills", "x", "SKILL.md"), "s1\n")
	put(t, filepath.Join(root, "settings.sync-conflict-20261124-101010-ABCDEF2.json"), `{"a":2}`)
	put(t, filepath.Join(root, "settings.json"), `{"a":1}`)
	// Not synced: never reported.
	put(t, filepath.Join(root, "todos", "t.sync-conflict-20261124-101010-ABCDEF2.json"), `{}`)

	cs, err := FindConflicts(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, c := range cs {
		paths = append(paths, c.Path)
	}
	if strings.Join(paths, ",") != "CLAUDE.md,settings.json,skills/x/SKILL.md" {
		t.Fatalf("conflicts = %v", paths)
	}
	if !cs[0].Mergeable || cs[1].Mergeable {
		t.Fatalf("mergeable flags wrong: %+v", cs)
	}

	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	if _, err := ResolveConflict(l, cfg, "settings.json", ResolveMerge, now); err == nil {
		t.Fatal("merging a JSON document must be refused")
	}
	// Keep both merged.
	if _, err := ResolveConflict(l, cfg, "CLAUDE.md", ResolveMerge, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md")); string(got) != "mine\nshared\ntheirs\n" {
		t.Fatalf("merged CLAUDE.md = %q", got)
	}
	bdir := ConflictBackupDir(l, now)
	if got, _ := os.ReadFile(filepath.Join(bdir, "CLAUDE.md")); string(got) != "mine\nshared\n" {
		t.Fatalf("pre-merge CLAUDE.md not backed up: %q", got)
	}
	// Pick the other device's version.
	if _, err := ResolveConflict(l, cfg, "skills/x/SKILL.md", "skills/x/SKILL.sync-conflict-20261124-101010-ABCDEF2.md", now); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "skills", "x", "SKILL.md")); string(got) != "s2\n" {
		t.Fatalf("SKILL.md = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(bdir, "skills", "x", "SKILL.md")); string(got) != "s1\n" {
		t.Fatalf("losing SKILL.md not in backups: %q", got)
	}
	// Keep the current settings.json.
	if _, err := ResolveConflict(l, cfg, "settings.json", ResolveKeepCurrent, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "settings.json")); string(got) != `{"a":1}` {
		t.Fatalf("settings.json = %q", got)
	}
	cs, err = FindConflicts(root, cfg)
	if err != nil || len(cs) != 0 {
		t.Fatalf("conflicts left: %+v %v", cs, err)
	}
	if _, err := ResolveConflict(l, cfg, "CLAUDE.md", ResolveKeepCurrent, now); err == nil {
		t.Fatal("resolving a settled file must fail")
	}
}
