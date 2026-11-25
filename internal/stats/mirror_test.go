package stats

import (
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/pathmirror"
)

// A session mirrored for another clone path (D91) is one session with
// the same messages: stats must not count the copy.
func TestMirroredCopiesDoNotDoubleCount(t *testing.T) {
	w := newWS(t)
	srcRepo, localRepo := "/A/home/projects/api", "/B/work/api"
	withCwd := func(line, cwd string) string {
		return strings.Replace(line, `{"type":"assistant",`, `{"type":"assistant","cwd":"`+cwd+`",`, 1)
	}
	folder := pathmirror.FolderName(srcRepo)
	w.write(folder+"/s1.jsonl",
		withCwd(asstLine("m1", "2026-11-20T09:00:00Z", "s1", "claude-opus-5-5", 1, 2, 0, 0), srcRepo)+
			withCwd(asstLine("m2", "2026-11-20T09:01:00Z", "s1", "claude-opus-5-5", 3, 4, 0, 0), srcRepo))
	_, before := w.scan()
	res, err := pathmirror.Materialise(w.dir, "s1", srcRepo, localRepo)
	if err != nil || !res.Wrote {
		t.Fatalf("materialise = %+v %v", res, err)
	}
	_, after := w.scan()
	if before.Totals.Messages != 2 || after.Totals.Messages != 2 || after.Totals.Sessions != 1 ||
		after.Totals.Tokens.Output != before.Totals.Tokens.Output {
		t.Errorf("before %+v after %+v", before.Totals, after.Totals)
	}
}
