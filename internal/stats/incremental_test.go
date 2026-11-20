package stats

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func userLine(uuid, ts, sess, text string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"sessionId":%q,"uuid":%q,"message":{"role":"user","content":%q}}`+"\n", ts, sess, uuid, text)
}

func asstLine(id, ts, sess, model string, in, out, cw, cr int) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"sessionId":%q,"message":{"id":%q,"model":%q,"usage":{"input_tokens":%d,"output_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d}}}`+"\n",
		ts, sess, id, model, in, out, cw, cr)
}

type ws struct {
	t     *testing.T
	dir   string
	cache string
	tick  time.Time
}

func newWS(t *testing.T) *ws {
	return &ws{t: t, dir: t.TempDir(), cache: filepath.Join(t.TempDir(), "cache", "stats.json"), tick: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (w *ws) path(rel string) string {
	return filepath.Join(w.dir, "projects", filepath.FromSlash(rel))
}

// touch gives every write a distinct mtime, whatever the file system's
// timestamp granularity.
func (w *ws) touch(rel string) {
	w.tick = w.tick.Add(time.Minute)
	if err := os.Chtimes(w.path(rel), w.tick, w.tick); err != nil {
		w.t.Fatal(err)
	}
}

func (w *ws) write(rel, data string) {
	if err := os.MkdirAll(filepath.Dir(w.path(rel)), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(w.path(rel), []byte(data), 0o644); err != nil {
		w.t.Fatal(err)
	}
	w.touch(rel)
}

func (w *ws) appendData(rel, data string) {
	f, err := os.OpenFile(w.path(rel), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := f.WriteString(data); err != nil {
		w.t.Fatal(err)
	}
	f.Close()
	w.touch(rel)
}

// scan is a fresh process: the disk cache is the only memory.
func (w *ws) scan() (*Index, *Snapshot) {
	w.t.Helper()
	ix, err := Build(w.dir, Options{CachePath: w.cache})
	if err != nil {
		w.t.Fatal(err)
	}
	s, err := ix.Snapshot(Query{Range: "all", Loc: time.UTC, Now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		w.t.Fatal(err)
	}
	return ix, s
}

func TestAppendResumesAtOffset(t *testing.T) {
	w := newWS(t)
	w.write("p/a.jsonl", userLine("u1", "2026-11-20T09:00:00Z", "s", "hi")+
		asstLine("m1", "2026-11-20T09:00:01Z", "s", "claude-opus-5-5", 1, 5, 0, 0)+
		`{"type":"assistant","timestamp":"2026-11-20T09:00:02Z","sessionId":"s","message":{"id":"m1","model":"claude-opus-5-5","usa`)
	ix, s := w.scan()
	if info := ix.Info(); info.Parsed != 1 || info.Reused != 0 {
		t.Fatalf("first scan %+v", info)
	}
	if s.Totals.Messages != 2 || s.Totals.Tokens.Output != 5 {
		t.Fatalf("first totals %+v", s.Totals)
	}
	ix2, s2 := w.scan()
	if info := ix2.Info(); info.Reused != 1 || info.HitRatio() != 1 {
		t.Errorf("unchanged rescan %+v", info)
	}
	if s2.Totals != s.Totals {
		t.Errorf("cached totals differ: %+v vs %+v", s2.Totals, s.Totals)
	}
	// The writer finishes the partial line (the same message id, more
	// output) and appends a new turn.
	w.appendData("p/a.jsonl", `ge":{"input_tokens":1,"output_tokens":9,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n"+
		userLine("u2", "2026-11-20T10:00:00Z", "s", "more")+
		asstLine("m2", "2026-11-20T10:00:01Z", "s", "claude-opus-5-5", 2, 3, 4, 5))
	ix3, s3 := w.scan()
	if info := ix3.Info(); info.Appended != 1 || info.Parsed != 0 {
		t.Errorf("append scan %+v", info)
	}
	want := Tokens{Input: 3, Output: 12, CacheWrite: 4, CacheRead: 5, Total: 24}
	if s3.Totals.Messages != 4 || s3.Totals.Tokens != want {
		t.Errorf("after append %+v, want 4 messages %+v", s3.Totals, want)
	}
	// The resumed result equals a from-scratch parse.
	os.Remove(w.cache)
	ix4, s4 := w.scan()
	if ix4.Info().Parsed != 1 || s4.Totals != s3.Totals {
		t.Errorf("fresh parse %+v differs from resumed %+v", s4.Totals, s3.Totals)
	}
}

func TestOwnerStateSurvivesResume(t *testing.T) {
	w := newWS(t)
	w.write("p/a.jsonl", `{"type":"session-owner","accountUuid":"A","sessionId":"s"}`+"\n"+userLine("u1", "2026-11-20T09:00:00Z", "s", "hi"))
	w.scan()
	w.appendData("p/a.jsonl", userLine("u2", "2026-11-20T09:05:00Z", "s", "again"))
	ix, _ := w.scan()
	s, _ := ix.Snapshot(Query{Range: "all", Loc: time.UTC, Now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC), Accounts: map[string]string{"A": "work"}})
	if len(s.Accounts) != 1 || s.Accounts[0].Name != "work" || s.Accounts[0].Messages != 2 {
		t.Errorf("accounts after resume = %+v", s.Accounts)
	}
	// Without a mapping the uuid never shows: unattributed.
	s, _ = ix.Snapshot(Query{Range: "all", Loc: time.UTC, Now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)})
	if s.Accounts[0].Name != Unattributed {
		t.Errorf("unmapped = %+v", s.Accounts)
	}
}

func TestRewriteDetection(t *testing.T) {
	w := newWS(t)
	a := userLine("u1", "2026-11-20T09:00:00Z", "s", "aaaa")
	w.write("p/a.jsonl", a)
	w.scan()

	// Same size, new content: a rewrite (mtime changed), full parse.
	b := userLine("u9", "2026-11-19T09:00:00Z", "s", "bbbb")
	if len(a) != len(b) {
		t.Fatal("fixture lines must have equal length")
	}
	w.write("p/a.jsonl", b)
	ix, s := w.scan()
	if ix.Info().Parsed != 1 || s.Totals.FirstActivity != "2026-11-19T09:00:00Z" {
		t.Errorf("same-size rewrite: %+v %+v", ix.Info(), s.Totals)
	}
	// Longer but the head changed: not an append.
	w.write("p/a.jsonl", userLine("u7", "2026-11-18T09:00:00Z", "s", "cccc")+userLine("u8", "2026-11-18T10:00:00Z", "s", "dddd"))
	ix, s = w.scan()
	if ix.Info().Parsed != 1 || ix.Info().Appended != 0 || s.Totals.Messages != 2 || s.Totals.FirstActivity != "2026-11-18T09:00:00Z" {
		t.Errorf("head change: %+v %+v", ix.Info(), s.Totals)
	}
	// Shorter: a rewrite.
	w.write("p/a.jsonl", userLine("u1", "2026-11-20T09:00:00Z", "s", "e"))
	ix, s = w.scan()
	if ix.Info().Parsed != 1 || s.Totals.Messages != 1 {
		t.Errorf("shrink: %+v %+v", ix.Info(), s.Totals)
	}
	// Gone: dropped from the cache.
	os.Remove(w.path("p/a.jsonl"))
	ix, s = w.scan()
	if ix.Info().Dropped != 1 || ix.Info().Files != 0 || s.Totals.Messages != 0 {
		t.Errorf("delete: %+v %+v", ix.Info(), s.Totals)
	}
	if data, err := os.ReadFile(w.cache); err != nil || strings.Contains(string(data), "a.jsonl") {
		t.Errorf("cache still lists the deleted file: %v", err)
	}
}

func TestCorruptCacheIsAFullScan(t *testing.T) {
	w := newWS(t)
	w.write("p/a.jsonl", userLine("u1", "2026-11-20T09:00:00Z", "s", "x"))
	for _, junk := range []string{"{not json", `{"version":99,"files":[]}`, `{"version":1,"files":[{"path":"p/a.jsonl","events":"AQ=="}]}`} {
		if err := os.MkdirAll(filepath.Dir(w.cache), 0o700); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(w.cache, []byte(junk), 0o600)
		ix, s := w.scan()
		if ix.Info().Parsed != 1 || s.Totals.Messages != 1 {
			t.Errorf("cache %q: %+v %+v", junk, ix.Info(), s.Totals)
		}
	}
	if fi, err := os.Stat(w.cache); err != nil || (fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Errorf("cache perms: %v %v", fi, err)
	}
}

func TestCrossFileDedupeKeepsLargestOutput(t *testing.T) {
	w := newWS(t)
	// The later file holds the complete copy: it wins and carries the
	// message to its project.
	w.write("a/x.jsonl", asstLine("m", "2026-11-20T09:00:00Z", "s1", "claude-opus-5-5", 1, 2, 0, 0))
	w.write("b/y.jsonl", asstLine("m", "2026-11-20T09:00:00Z", "s2", "claude-opus-5-5", 1, 7, 0, 0))
	_, s := w.scan()
	if s.Totals.Messages != 1 || s.Totals.Tokens.Output != 7 || s.TopProjects[0].Name != "b" || s.Totals.Sessions != 1 {
		t.Errorf("dedupe: %+v %+v", s.Totals, s.TopProjects)
	}
}

func TestEngineServesCachedAndRefreshesInBackground(t *testing.T) {
	w := newWS(t)
	w.write("p/a.jsonl", userLine("u1", "2026-11-20T09:00:00Z", "s", "x"))
	e := NewEngine(w.dir, Options{CachePath: w.cache}, time.Hour)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	if fp := e.Fingerprint(); fp != "" {
		t.Errorf("fingerprint before any scan = %q", fp)
	}
	ix, err := e.Index()
	if err != nil || ix == nil {
		t.Fatal(err)
	}
	fp := e.Fingerprint()
	w.appendData("p/a.jsonl", userLine("u2", "2026-11-20T10:00:00Z", "s", "y"))
	// Within the interval: same index, no scan.
	if ix2, _ := e.Index(); ix2 != ix {
		t.Error("rescanned inside the interval")
	}
	now = now.Add(2 * time.Hour)
	if ix3, _ := e.Index(); ix3 != ix {
		t.Error("a stale index must still be answered at once")
	}
	e.Wait()
	if e.Fingerprint() == fp {
		t.Error("background refresh did not pick up the append")
	}
}
