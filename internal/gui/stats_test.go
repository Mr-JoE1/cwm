package gui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/status"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// statsLines is a tiny generated transcript: an owner line, one prompt
// and one reply streamed over two lines (same message.id).
func statsLines(owner, sess string, ts time.Time, out int) string {
	t := ts.UTC().Format(time.RFC3339Nano)
	return fmt.Sprintf(`{"type":"session-owner","accountUuid":%q}`+"\n", owner) +
		fmt.Sprintf(`{"type":"user","uuid":"u-%s","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"hi"}}`+"\n", sess, sess, t) +
		fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"id":"msg-%s","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000}}}`+"\n", sess, t, sess) +
		fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"id":"msg-%s","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":%d,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000}}}`+"\n", sess, t, sess, out)
}

func TestAPIStats(t *testing.T) {
	f := newFixture(t, okSync)
	ws := filepath.Join(f.root, "workspace", "projects")
	now := time.Now()
	writeFile(t, filepath.Join(ws, "alpha", "s1.jsonl"), statsLines("uuid-main", "s1", now.Add(-time.Hour), 40))
	writeFile(t, filepath.Join(ws, "beta", "s2.jsonl"), statsLines("uuid-stranger", "s2", now.Add(-26*time.Hour), 60))
	f.srv.opts.StatsAccounts = func() map[string]string { return map[string]string{"uuid-main": "main"} }

	if rec := f.do(t, "GET", "/api/stats", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /api/stats = %d", rec.Code)
	}
	auth := func(r *http.Request) { r.AddCookie(f.cookie()) }
	for _, bad := range []string{"/api/stats?range=2w", "/api/stats?tz=Mars/Olympus"} {
		if rec := f.do(t, "GET", bad, auth); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, rec.Code)
		}
	}
	if rec := f.do(t, "POST", "/api/stats", auth); rec.Code != http.StatusForbidden && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/stats = %d", rec.Code)
	}

	rec := f.do(t, "GET", "/api/stats?range=7d&tz=UTC", auth)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/stats = %d %s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"range", "tz", "from", "to", "totals", "streaks", "daily", "calendar", "top_projects",
		"top_models", "accounts", "cost_estimate", "unpriced", "scanned_files", "scan_ms", "cache_hit_ratio"} {
		if _, ok := body[k]; !ok {
			t.Errorf("/api/stats lacks %q", k)
		}
	}
	if body["range"] != "7d" || body["tz"] != "UTC" || len(body["daily"].([]any)) != 7 || len(body["calendar"].([]any)) != 378 {
		t.Errorf("range/tz/daily/calendar = %v %v %d %d", body["range"], body["tz"], len(body["daily"].([]any)), len(body["calendar"].([]any)))
	}
	totals := body["totals"].(map[string]any)
	// 2 sessions; 2 prompts + 2 deduped replies; tokens 10+out+100+1000 each.
	if totals["sessions"] != 2.0 || totals["messages"] != 4.0 || totals["tokens"].(map[string]any)["total"] != 2320.0 {
		t.Errorf("totals = %v", totals)
	}
	var names []string
	for _, a := range body["accounts"].([]any) {
		names = append(names, a.(map[string]any)["name"].(string))
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "main,unattributed" {
		t.Errorf("accounts = %v", names)
	}
	if strings.Contains(rec.Body.String(), "uuid-") {
		t.Error("raw account uuids leaked into /api/stats")
	}
	ce := body["cost_estimate"].(map[string]any)
	if !strings.Contains(ce["note"].(string), "est.") || ce["usd"].(float64) <= 0 {
		t.Errorf("cost_estimate = %v", ce)
	}
	if body["scanned_files"] != 3.0 { // alpha/s1, beta/s2 and the fixture's alpha session
		t.Errorf("scanned_files = %v", body["scanned_files"])
	}
}

func TestSnapshotChangedStats(t *testing.T) {
	a, b := Snapshot{Stats: "1/2/3"}, Snapshot{Stats: "2/5/9"}
	if got := strings.Join(a.changed(b), ","); got != "stats" {
		t.Errorf("changed = %q, want stats", got)
	}
	if got := (Snapshot{}).changed(b); len(got) != 0 {
		t.Errorf("first scan finishing reported as %v", got)
	}
}

// TestEventsStreamStatsChange: a transcript appearing while a dashboard
// is connected reaches it as a "stats" change through the existing poll.
func TestEventsStreamStatsChange(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CWM_HOME", root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	tok := strings.Repeat("b", 64)
	srv, base, _ := startLive(t, Options{
		Version:       status.Version{Version: "test"},
		Layout:        storage.NewLayoutAt(root),
		EventsPoll:    30 * time.Millisecond,
		Heartbeat:     time.Second,
		StatsInterval: 20 * time.Millisecond,
	}, tok)
	t.Cleanup(func() { srv.statsEngine().Wait() })

	req, _ := http.NewRequest("GET", base+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	ch := make(chan sseEvent, 32)
	go readEvents(t, bufio.NewScanner(resp.Body), ch)
	next(t, ch, "hello", 2*time.Second)
	// Let the first (empty) scan land and be adopted, so the transcript
	// below is a change and not part of the first fingerprint.
	for deadline := time.Now().Add(5 * time.Second); srv.statsEngine().Fingerprint() == ""; {
		if time.Now().After(deadline) {
			t.Fatal("first stats scan did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // > 2 polls: the stream has adopted it

	writeFile(t, filepath.Join(root, "workspace", "projects", "alpha", "s.jsonl"), statsLines("u", "s", time.Now(), 5))
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.name != "state" {
				continue
			}
			changed, _ := ev.data["changed"].([]any)
			for _, c := range changed {
				if c == "stats" {
					// The first scan of the empty workspace may land first
					// ("0/0/0"); wait for the one that sees the transcript.
					snap := ev.data["snapshot"].(map[string]any)
					if s, _ := snap["stats"].(string); strings.HasPrefix(s, "2/") {
						return
					}
				}
			}
		case <-deadline:
			t.Fatal("no stats change event within 5s")
		}
	}
}
