package pathmirror

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/testsafe"
)

func TestMain(m *testing.M) { os.Exit(testsafe.Main(m)) }

// Vectors computed by running Claude Code 2.1.319's own fL/rne functions
// (copied verbatim from the bundle) in node.
func TestFolderNameMatchesClaudeCode(t *testing.T) {
	long := "/home/me/" + strings.Repeat("verylongdirectoryname/", 12) + "api"
	for in, want := range map[string]string{
		"/home/me/projects/api":    "-home-me-projects-api",
		`C:\Users\me\projects\api`: "C--Users-me-projects-api",
		"/tmp/é😀x":                 "-tmp----x",
		long:                       "-home-me" + strings.Repeat("-verylongdirectoryname", 8) + "-verylongdirecto-csi888",
	} {
		if got := FolderName(in); got != want {
			t.Errorf("FolderName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeRemote(t *testing.T) {
	want := "github.com/me/api"
	for _, in := range []string{
		"git@github.com:me/api.git", "https://github.com/me/api", "https://user@GitHub.com/me/api.git/",
		"ssh://git@github.com:22/me/api.git", "github.com:me/api",
	} {
		if got := NormalizeRemote(in); got != want {
			t.Errorf("NormalizeRemote(%q) = %q", in, got)
		}
	}
}

func TestRewriteCwd(t *testing.T) {
	local := filepath.Join(string(filepath.Separator)+"b", "api")
	for in, want := range map[string]string{
		`C:\Users\me\api`:       local,
		`c:\users\me\api\src\x`: filepath.Join(local, "src", "x"),
		`C:\Users\me\apiary`:    `C:\Users\me\apiary`,
		"/elsewhere":            "/elsewhere",
	} {
		if got := RewriteCwd(in, `C:\Users\me\api`, local); got != want {
			t.Errorf("RewriteCwd(%q) = %q, want %q", in, got, want)
		}
	}
	if got := RewriteCwd("/a/api/x", "/a/api", "/b/api"); got != filepath.Join("/b/api", "x") {
		t.Errorf("unix rewrite = %q", got)
	}
	if got := RewriteCwd("/a/API", "/a/api", "/b/api"); got != "/a/API" {
		t.Errorf("unix paths are case-sensitive: %q", got)
	}
}

func write(t *testing.T, p string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ln(uuid, cwd, ts string) string {
	return `{"type":"user","uuid":"` + uuid + `","cwd":"` + cwd + `","timestamp":"` + ts + `"}`
}

func TestMaterialisePrefixRuleAndDivergence(t *testing.T) {
	root := t.TempDir()
	srcRepo, localRepo := "/A/home/projects/api", "/B/work/api"
	srcFolder, localFolder := FolderName(srcRepo), FolderName(localRepo)
	src := filepath.Join(root, "projects", srcFolder, "s1.jsonl")
	write(t, src, ln("1", srcRepo, "2026-11-24T10:00:00Z"), ln("2", srcRepo+"/sub", "2026-11-24T10:00:01Z"))
	write(t, filepath.Join(root, "projects", srcFolder, "s1", "subagents", "agent-1.jsonl"), ln("x", srcRepo, "2026-11-24T10:00:02Z"))
	before, _ := os.ReadFile(src)

	res, err := Materialise(root, "s1", srcRepo, localRepo)
	if err != nil || !res.Wrote || res.Diverged || res.Sidecars != 1 {
		t.Fatalf("first = %+v %v", res, err)
	}
	local := filepath.Join(root, "projects", localFolder, "s1.jsonl")
	got, _ := os.ReadFile(local)
	if strings.Contains(string(got), srcRepo) || !strings.Contains(string(got), `"cwd":`+js(filepath.Join(localRepo, "sub"))) {
		t.Errorf("cwd not rewritten: %s", got)
	}
	if after, _ := os.ReadFile(src); string(after) != string(before) {
		t.Error("source copy modified")
	}
	if _, err := os.Stat(filepath.Join(root, "projects", localFolder, "s1", "subagents", "agent-1.jsonl")); err != nil {
		t.Error("sidecar not mirrored")
	}
	// Idempotent: nothing to write the second time.
	if res, _ := Materialise(root, "s1", srcRepo, localRepo); res.Wrote {
		t.Error("second run rewrote an up-to-date copy")
	}
	// B continues: the local copy grows. Mirroring back onto A's folder
	// (A's view) extends A's copy by the prefix rule.
	f, _ := os.OpenFile(local, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(ln("3", localRepo, "2026-11-24T11:00:00Z") + "\n")
	f.Close()
	res, err = Materialise(root, "s1", localRepo, srcRepo)
	if err != nil || !res.Wrote || res.Source != local {
		t.Fatalf("mirror back = %+v %v", res, err)
	}
	if got, _ := os.ReadFile(src); !strings.Contains(string(got), `"uuid":"3"`) || !strings.Contains(string(got), `"cwd":`+js(srcRepo)) {
		t.Errorf("A's copy not extended with A's paths: %s", got)
	}
	// Both sides continue: diverged, both kept, nothing overwritten.
	f, _ = os.OpenFile(src, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(ln("4a", srcRepo, "2026-11-24T12:00:00Z") + "\n")
	f.Close()
	f, _ = os.OpenFile(local, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(ln("4b", localRepo, "2026-11-24T12:30:00Z") + "\n")
	f.Close()
	a1, _ := os.ReadFile(src)
	b1, _ := os.ReadFile(local)
	res, err = Materialise(root, "s1", srcRepo, localRepo)
	if err != nil || !res.Diverged || res.Wrote {
		t.Fatalf("diverged = %+v %v", res, err)
	}
	a2, _ := os.ReadFile(src)
	b2, _ := os.ReadFile(local)
	if string(a1) != string(a2) || string(b1) != string(b2) {
		t.Error("a diverged copy was overwritten")
	}
	copies, _ := FindCopies(root, "s1")
	if best, div := Pick(copies); !div || best.Path != local {
		t.Errorf("pick = %s %v (want the newest last entry)", best.Path, div)
	}
}

func js(s string) string { b, _ := json.Marshal(s); return string(b) }
