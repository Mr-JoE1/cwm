package stats

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// benchWorkspace generates sessions transcripts of about 200 lines each,
// shaped like Claude Code's: an owner line, prompts, assistant messages
// streamed over two lines that share message.id, tool results, and
// attachment / api-request noise. Payload text is filler. It returns the
// workspace dir and the total bytes written.
func benchWorkspace(tb testing.TB, sessions int) (string, int64) {
	tb.Helper()
	dir := tb.TempDir()
	r := rand.New(rand.NewPCG(7, 11))
	filler := strings.Repeat("lorem ipsum dolor sit amet ", 80)
	text := func(n int) string { return filler[:n] }
	models := []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}
	base := time.Date(2025, 12, 1, 8, 0, 0, 0, time.UTC)
	var total int64
	for s := range sessions {
		proj := fmt.Sprintf("project-%d", s%6)
		if err := os.MkdirAll(filepath.Join(dir, "projects", proj), 0o755); err != nil {
			tb.Fatal(err)
		}
		sid := fmt.Sprintf("00000000-0000-4000-8000-%012d", s)
		ts := base.Add(time.Duration(s) * 7 * time.Hour)
		var b strings.Builder
		fmt.Fprintf(&b, `{"type":"session-owner","accountUuid":"acct-%d","organizationUuid":"org","sessionId":%q}`+"\n", s%2, sid)
		lines := 1
		msg := 0
		for lines < 200 {
			ts = ts.Add(time.Duration(20+r.IntN(90)) * time.Second)
			fmt.Fprintf(&b, `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":%q},"uuid":"u-%d-%d","timestamp":%q,"sessionId":%q,"cwd":"/work/%s","version":"2.1.0"}`+"\n",
				text(60+r.IntN(240)), s, lines, ts.Format(time.RFC3339Nano), sid, proj)
			lines++
			for range 3 {
				msg++
				id := fmt.Sprintf("msg_%d_%d", s, msg)
				model := models[r.IntN(3)]
				usage := fmt.Sprintf(`{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d,"service_tier":"standard","cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0}}`,
					r.IntN(20), r.IntN(5000), r.IntN(40000), 10+r.IntN(900))
				for k := range 2 {
					ts = ts.Add(time.Second)
					fmt.Fprintf(&b, `{"parentUuid":"p","isSidechain":false,"type":"assistant","message":{"id":%q,"type":"message","role":"assistant","model":%q,"content":[{"type":"text","text":%q}],"stop_reason":null,"usage":%s},"requestId":"req_%d","uuid":"a-%d-%d-%d","timestamp":%q,"sessionId":%q}`+"\n",
						id, model, text(200+r.IntN(500)), usage, msg, s, msg, k, ts.Format(time.RFC3339Nano), sid)
					lines++
				}
				fmt.Fprintf(&b, `{"parentUuid":"p","type":"user","message":{"role":"user","content":[{"tool_use_id":"tu","type":"tool_result","content":%q}]},"uuid":"t-%d-%d","timestamp":%q,"sessionId":%q,"toolUseResult":{"stdout":%q}}`+"\n",
					text(300+r.IntN(1200)), s, msg, ts.Format(time.RFC3339Nano), sid, text(100))
				fmt.Fprintf(&b, `{"type":"attachment","attachment":{"type":"file","content":%q},"timestamp":%q,"sessionId":%q}`+"\n",
					text(200+r.IntN(800)), ts.Format(time.RFC3339Nano), sid)
				lines += 2
			}
		}
		p := filepath.Join(dir, "projects", proj, sid+".jsonl")
		if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
			tb.Fatal(err)
		}
		total += int64(b.Len())
	}
	return dir, total
}

// Targets (SPEC-V0.7.1 B): 1000 sessions × ~200 lines, full scan < 2 s,
// unchanged rescan < 200 ms. Run: go test -run XXX -bench . ./internal/stats/
func BenchmarkFullScan1000(b *testing.B) {
	dir, size := benchWorkspace(b, 1000)
	cache := filepath.Join(b.TempDir(), "stats.json")
	b.SetBytes(size)
	b.ResetTimer()
	for b.Loop() {
		os.Remove(cache)
		if _, err := Build(dir, Options{CachePath: cache}); err != nil {
			b.Fatal(err)
		}
	}
}

// A fresh process (the CLI): load stats.json, stat every file, reuse all.
func BenchmarkRescanUnchanged1000(b *testing.B) {
	dir, _ := benchWorkspace(b, 1000)
	cache := filepath.Join(b.TempDir(), "stats.json")
	if _, err := Build(dir, Options{CachePath: cache}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		ix, err := Build(dir, Options{CachePath: cache})
		if err != nil || ix.Info().Reused != 1000 {
			b.Fatal(err, ix.Info())
		}
	}
}

// The agent: rescan against the in-memory index.
func BenchmarkRescanInMemory1000(b *testing.B) {
	dir, _ := benchWorkspace(b, 1000)
	ix, err := Build(dir, Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := Update(ix, dir, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

// One /api/stats answer from a ready index.
func BenchmarkSnapshot365d(b *testing.B) {
	dir, _ := benchWorkspace(b, 1000)
	ix, err := Build(dir, Options{})
	if err != nil {
		b.Fatal(err)
	}
	q := Query{Range: "365d", Loc: time.UTC, Now: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}
	b.ResetTimer()
	for b.Loop() {
		if _, err := ix.Snapshot(q); err != nil {
			b.Fatal(err)
		}
	}
}

func TestBenchWorkspaceShape(t *testing.T) {
	dir, size := benchWorkspace(t, 3)
	ix, err := Build(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := ix.Snapshot(Query{Range: "all", Loc: time.UTC, Now: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)})
	// 16 rounds of 13 lines (a prompt, 3 messages over 2 lines, a tool
	// result and an attachment each): 208 lines per file.
	if s.Totals.Sessions != 3 || s.Totals.UserMessages != 3*16 || s.Totals.AssistantMessages != 3*48 || size < 3*100_000 {
		t.Errorf("shape: %+v, %d bytes", s.Totals, size)
	}
}
