package stats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain isolates every home-dir variable: the package never reads
// them, and a regression that did must not reach the developer's files.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cwm-stats-test-")
	if err != nil {
		panic(err)
	}
	for _, k := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		os.Setenv(k, dir)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

var goldenNow = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

func goldenIndex(t *testing.T, cache string) *Index {
	t.Helper()
	ix, err := Build(filepath.Join("testdata", "golden"), Options{CachePath: cache})
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func snap(t *testing.T, ix *Index, q Query) *Snapshot {
	t.Helper()
	s, err := ix.Snapshot(q)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The golden numbers are counted by hand from testdata/golden (see the
// comments there and SPEC-V0.7.1 A):
//
//	u-1      user  11-17 10:00Z  alpha sess-1 acct-a
//	msg_1    asst  11-17 10:00Z  opus-5-5  in2 out10 cw100(1h) cr50   (3 lines; copy in beta loses: later)
//	u-2      user  11-17 23:50Z  alpha sess-1 (array with text)
//	msg_2    asst  11-18 00:10Z  sonnet-4-5-20250929 in1 out20 cr200
//	u-3      user  11-19 08:00Z  beta sess-2 (no owner)
//	msg_3    asst  11-19 08:00Z  claude-unknown-9 in10 out30
//	msg_4    asst  11-15 12:00Z  opus-5-5 in1 out1
//
// Not counted: tool_result-only, isMeta, <synthetic>, the malformed and
// the truncated line, the attachment, the u-1 copy, agent-*.jsonl and
// subagent files.
func TestGoldenUTC(t *testing.T) {
	ix := goldenIndex(t, "")
	s := snap(t, ix, Query{Range: "all", Loc: time.UTC, Now: goldenNow, Accounts: map[string]string{"acct-a": "work"}})

	wantTotals := Totals{
		Sessions: 2, Messages: 7, UserMessages: 3, AssistantMessages: 4,
		Tokens:     Tokens{Input: 14, Output: 61, CacheWrite: 100, CacheRead: 250, Total: 425},
		ActiveDays: 4, FirstActivity: "2026-11-15T12:00:00Z", LastActivity: "2026-11-19T08:00:05Z",
	}
	if !reflect.DeepEqual(s.Totals, wantTotals) {
		t.Errorf("totals\n got %+v\nwant %+v", s.Totals, wantTotals)
	}
	if want := (Streaks{Current: 3, Longest: 3, LongestStart: "2026-11-17", LongestEnd: "2026-11-19"}); s.Streaks != want {
		t.Errorf("streaks = %+v, want %+v", s.Streaks, want)
	}
	if s.From != "2026-11-15" || s.To != "2026-11-19" || len(s.Daily) != 5 {
		t.Fatalf("range %s..%s, %d days", s.From, s.To, len(s.Daily))
	}
	day := func(d string) Day {
		for _, x := range s.Daily {
			if x.Date == d {
				return x
			}
		}
		t.Fatalf("no day %s", d)
		return Day{}
	}
	// sess-1 spans midnight: on both days, once in the total.
	if d := day("2026-11-17"); d.Sessions != 1 || d.Messages != 3 || d.Tokens.Total != 162 {
		t.Errorf("11-17 = %+v", d)
	}
	if d := day("2026-11-18"); d.Sessions != 1 || d.Messages != 1 || d.Tokens.Total != 221 {
		t.Errorf("11-18 = %+v", d)
	}
	if d := day("2026-11-16"); d.Sessions != 0 || d.Messages != 0 {
		t.Errorf("11-16 = %+v", d)
	}
	if d := day("2026-11-19"); d.Sessions != 1 || d.Messages != 2 || d.Tokens.Total != 40 {
		t.Errorf("11-19 = %+v", d)
	}
	if d := day("2026-11-15"); d.Sessions != 1 || d.Messages != 1 || d.Tokens.Total != 2 {
		t.Errorf("11-15 = %+v", d)
	}

	names := func(gs []Group) []string {
		var out []string
		for _, g := range gs {
			out = append(out, g.Name+":"+itoa(g.Sessions)+"/"+itoa(int(g.Messages))+"/"+itoa(int(g.Tokens.Total)))
		}
		return out
	}
	if got, want := names(s.TopProjects), []string{"alpha:1/4/383", "beta:1/3/42"}; !reflect.DeepEqual(got, want) {
		t.Errorf("projects = %v, want %v", got, want)
	}
	if got, want := names(s.TopModels), []string{"claude-sonnet-4-5-20250929:1/1/221", "claude-opus-5-5:2/2/164", "claude-unknown-9:1/1/40"}; !reflect.DeepEqual(got, want) {
		t.Errorf("models = %v, want %v", got, want)
	}
	if got, want := names(s.Accounts), []string{"work:1/4/383", "unattributed:1/3/42"}; !reflect.DeepEqual(got, want) {
		t.Errorf("accounts = %v, want %v", got, want)
	}
	// opus-5-5: msg_1 (8+200+800 1h write+10)/1e6 + msg_4 24/1e6;
	// sonnet-4-5: (3+300+60)/1e6 → 0.001405 → 0.0014.
	if s.CostEstimate.USD != 0.0014 || s.CostEstimate.Note != CostNote || s.CostEstimate.PricesUpdated != PricesUpdated {
		t.Errorf("cost = %+v", s.CostEstimate)
	}
	if *s.TopModels[1].CostUSD != 0.001 { // 0.001042
		t.Errorf("opus cost = %v", *s.TopModels[1].CostUSD)
	}
	if !reflect.DeepEqual(s.Unpriced, []string{"claude-unknown-9"}) {
		t.Errorf("unpriced = %v", s.Unpriced)
	}
	if s.ScannedFiles != 2 || len(s.Calendar) != CalendarDays || s.Calendar[len(s.Calendar)-1].Date != "2026-11-19" {
		t.Errorf("scanned %d, calendar %d ending %s", s.ScannedFiles, len(s.Calendar), s.Calendar[len(s.Calendar)-1].Date)
	}
	if c := s.Calendar[len(s.Calendar)-3]; c.Date != "2026-11-17" || c.Tokens != 162 || c.Messages != 3 || c.Sessions != 1 {
		t.Errorf("calendar 11-17 = %+v", c)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// Los Angeles (UTC-8): msg_2 lands on 11-17 with the rest of sess-1, so
// the midnight crossing disappears and the run 17-18-19 breaks.
func TestGoldenTZOverride(t *testing.T) {
	ix := goldenIndex(t, "")
	la, err := ParseTZ("America/Los_Angeles")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	s := snap(t, ix, Query{Range: "all", Loc: la, Now: goldenNow})
	if s.Totals.ActiveDays != 3 || s.TZ != "America/Los_Angeles" {
		t.Errorf("active days = %d tz %s", s.Totals.ActiveDays, s.TZ)
	}
	if want := (Streaks{Current: 1, Longest: 1, LongestStart: "2026-11-19", LongestEnd: "2026-11-19"}); s.Streaks != want {
		t.Errorf("streaks = %+v, want %+v (latest run wins a tie)", s.Streaks, want)
	}
	for _, d := range s.Daily {
		if d.Date == "2026-11-17" && (d.Messages != 4 || d.Tokens.Total != 383) {
			t.Errorf("LA 11-17 = %+v", d)
		}
		if d.Date == "2026-11-18" && d.Messages != 0 {
			t.Errorf("LA 11-18 = %+v", d)
		}
	}
	if s.Totals.FirstActivity != "2026-11-15T04:00:00-08:00" {
		t.Errorf("first = %s", s.Totals.FirstActivity)
	}
	// Tokyo (UTC+9): u-2 and msg_2 both move to 11-18.
	tk, _ := ParseTZ("Asia/Tokyo")
	s = snap(t, ix, Query{Range: "all", Loc: tk, Now: goldenNow})
	for _, d := range s.Daily {
		if d.Date == "2026-11-18" && (d.Messages != 2 || d.Tokens.Total != 221) {
			t.Errorf("Tokyo 11-18 = %+v", d)
		}
	}
	if _, err := ParseTZ("Mars/Olympus"); err == nil {
		t.Error("bogus zone accepted")
	}
}

func TestStreakEdges(t *testing.T) {
	ix := goldenIndex(t, "")
	q := func(now time.Time) Streaks {
		return snap(t, ix, Query{Range: "7d", Loc: time.UTC, Now: now}).Streaks
	}
	// Today (11-20) not active yet: the run ending yesterday still counts.
	if s := q(time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)); s.Current != 3 {
		t.Errorf("yesterday rule: %+v", s)
	}
	// Two idle days: no current streak, the longest stays.
	if s := q(time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)); s.Current != 0 || s.Longest != 3 {
		t.Errorf("gap: %+v", s)
	}
	// Range and streaks are independent: 7d from 11-21 starts at 11-15.
	s := snap(t, ix, Query{Range: "7d", Loc: time.UTC, Now: time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)})
	if s.From != "2026-11-15" || s.Totals.Messages != 7 {
		t.Errorf("7d = %s %d", s.From, s.Totals.Messages)
	}
	s = snap(t, ix, Query{Range: "7d", Loc: time.UTC, Now: time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)})
	if s.From != "2026-11-16" || s.Totals.Messages != 6 || s.Totals.ActiveDays != 3 || s.Streaks.Longest != 3 {
		t.Errorf("7d later = %s %+v %+v", s.From, s.Totals, s.Streaks)
	}
	if _, err := ix.Snapshot(Query{Range: "2w"}); err == nil {
		t.Error("bad range accepted")
	}
	if r, _, _ := ParseRange("1y"); r != "365d" {
		t.Errorf("1y = %s", r)
	}
}

func TestEmptyWorkspace(t *testing.T) {
	ix, err := Build(t.TempDir(), Options{CachePath: filepath.Join(t.TempDir(), "c", "stats.json")})
	if err != nil {
		t.Fatal(err)
	}
	s := snap(t, ix, Query{Range: "all", Loc: time.UTC, Now: goldenNow})
	if s.Totals.Messages != 0 || s.Totals.ActiveDays != 0 || s.Streaks.Current != 0 || len(s.Daily) != 1 ||
		s.TopModels == nil || s.Unpriced == nil || s.CacheHit != 1 {
		t.Errorf("empty = %+v", s)
	}
	data, _ := json.Marshal(s)
	if !strings.Contains(string(data), `"top_models":[]`) {
		t.Errorf("empty lists must be [] not null: %s", data)
	}
}

func TestPriceFor(t *testing.T) {
	for model, want := range map[string]float64{
		"claude-opus-5-5":            4,
		"claude-sonnet-4-5-20250929": 3,
		"claude-opus-4-20250514":     15,
		"claude-opus-4-5-20251101":   5,
		"claude-sonnet-5-5[1m]":      2,
		"CLAUDE-HAIKU-5-5":           0.10,
	} {
		p, ok := PriceFor(model)
		if !ok || p.Input != want {
			t.Errorf("%s = %v %v, want input %v", model, p, ok, want)
		}
	}
	for _, model := range []string{"claude-opus-4-9", "gpt-x", "", "claude-opus-5-5-beta", "claude-opus"} {
		if _, ok := PriceFor(model); ok {
			t.Errorf("%s priced", model)
		}
	}
}

func TestFormatCompact(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1k", 1234: "1.2k", 1234567: "1.2M", 25_000_000: "25M", 123_456_789: "123M", 2e9: "2B"} {
		if got := FormatCompact(n); got != want {
			t.Errorf("%d = %s, want %s", n, got, want)
		}
	}
}
