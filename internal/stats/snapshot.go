package stats

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Unattributed is the account of activity no cwm account can be matched
// to (D62).
const Unattributed = "unattributed"

// CalendarDays is the length of Snapshot.Calendar: 54 weeks, enough for a
// 53-week grid whichever day the week starts on.
const CalendarDays = 378

// topN caps the project and model lists.
const topN = 10

// Ranges accepted by ParseRange, in UI order.
var Ranges = []string{"7d", "30d", "90d", "365d", "all"}

// DefaultRange is used when none is given.
const DefaultRange = "30d"

// ParseRange validates a range ("1y" is an alias of "365d") and returns
// its canonical name and length in days (0 = all).
func ParseRange(s string) (string, int, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return DefaultRange, 30, nil
	case "7d":
		return "7d", 7, nil
	case "30d":
		return "30d", 30, nil
	case "90d":
		return "90d", 90, nil
	case "365d", "1y":
		return "365d", 365, nil
	case "all":
		return "all", 0, nil
	}
	return "", 0, fmt.Errorf("unknown range %q (want 7d, 30d, 90d, 365d or all)", s)
}

// ParseTZ resolves an IANA zone name ("" = the machine's zone).
func ParseTZ(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown time zone %q: %w", name, err)
	}
	return loc, nil
}

// Query selects what a Snapshot covers.
type Query struct {
	Range string         // see ParseRange
	Loc   *time.Location // nil = time.Local
	Now   time.Time      // zero = time.Now()
	// Accounts maps an accountUuid to the cwm account name.
	Accounts map[string]string
}

// Tokens are counts by kind.
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheWrite int64 `json:"cache_write"`
	CacheRead  int64 `json:"cache_read"`
	Total      int64 `json:"total"`
}

func (t *Tokens) add(e *event) {
	t.Input += e.in
	t.Output += e.out
	t.CacheWrite += e.cw
	t.CacheRead += e.cr
	t.Total += e.in + e.out + e.cw + e.cr
}

// Totals are the range's headline numbers.
type Totals struct {
	Sessions          int    `json:"sessions"`
	Messages          int64  `json:"messages"`
	UserMessages      int64  `json:"user_messages"`
	AssistantMessages int64  `json:"assistant_messages"`
	Tokens            Tokens `json:"tokens"`
	ActiveDays        int    `json:"active_days"`
	FirstActivity     string `json:"first_activity,omitempty"`
	LastActivity      string `json:"last_activity,omitempty"`
}

// Streaks are all-time runs of consecutive active days.
type Streaks struct {
	Current      int    `json:"current"`
	Longest      int    `json:"longest"`
	LongestStart string `json:"longest_start,omitempty"`
	LongestEnd   string `json:"longest_end,omitempty"`
}

// Day is one row of the daily series.
type Day struct {
	Date     string `json:"date"`
	Sessions int    `json:"sessions"`
	Messages int64  `json:"messages"`
	Tokens   Tokens `json:"tokens"`
}

// CalDay is one heatmap cell.
type CalDay struct {
	Date     string `json:"date"`
	Sessions int    `json:"sessions"`
	Messages int64  `json:"messages"`
	Tokens   int64  `json:"tokens"`
}

// Group is one project, model or account.
type Group struct {
	Name     string   `json:"name"`
	Sessions int      `json:"sessions"`
	Messages int64    `json:"messages"`
	Tokens   Tokens   `json:"tokens"`
	CostUSD  *float64 `json:"cost_usd,omitempty"`
}

// Cost is the API-list-price estimate.
type Cost struct {
	USD           float64 `json:"usd"`
	PricesUpdated string  `json:"prices_updated"`
	Note          string  `json:"note"`
}

// Snapshot is the body of GET /api/stats and `cwm stats --json`.
type Snapshot struct {
	Range        string   `json:"range"`
	TZ           string   `json:"tz"`
	From         string   `json:"from"`
	To           string   `json:"to"`
	Totals       Totals   `json:"totals"`
	Streaks      Streaks  `json:"streaks"`
	Daily        []Day    `json:"daily"`
	Calendar     []CalDay `json:"calendar"`
	TopProjects  []Group  `json:"top_projects"`
	TopModels    []Group  `json:"top_models"`
	Accounts     []Group  `json:"accounts"`
	CostEstimate Cost     `json:"cost_estimate"`
	Unpriced     []string `json:"unpriced"`
	ScannedFiles int      `json:"scanned_files"`
	ScanMS       float64  `json:"scan_ms"`
	CacheHit     float64  `json:"cache_hit_ratio"`
}

type agg struct {
	sessions map[string]struct{}
	messages int64
	tokens   Tokens
	cost     float64
}

func newAgg() *agg { return &agg{sessions: map[string]struct{}{}} }

func (a *agg) add(sess string, e *event) {
	a.sessions[sess] = struct{}{}
	a.messages++
	if e.kind == kindAssistant {
		a.tokens.add(e)
	}
}

// civil day number of t's local date (days since 1970-01-01).
func dayNum(t time.Time) int {
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

func dayString(n int) string {
	return time.Unix(int64(n)*86400, 0).UTC().Format("2006-01-02")
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

// Snapshot aggregates the index for q. It never touches the disk.
func (ix *Index) Snapshot(q Query) (*Snapshot, error) {
	rng, days, err := ParseRange(q.Range)
	if err != nil {
		return nil, err
	}
	loc := q.Loc
	if loc == nil {
		loc = time.Local
	}
	now := q.Now
	if now.IsZero() {
		now = time.Now()
	}
	today := dayNum(now.In(loc))

	type ev struct {
		r   ref
		day int
	}
	evs := make([]ev, 0, len(ix.winners))
	first := math.MaxInt
	for _, r := range ix.winners {
		d := dayNum(time.UnixMilli(r.e.ts).In(loc))
		evs = append(evs, ev{r, d})
		first = min(first, d)
	}
	from := today - days + 1
	if days == 0 {
		from = min(first, today)
	}

	byDay := map[int]*agg{}
	total := newAgg()
	var users, assts int64
	var firstTS, lastTS int64 = math.MaxInt64, math.MinInt64
	projects, models, accounts := map[string]*agg{}, map[string]*agg{}, map[string]*agg{}
	unpriced := map[string]bool{}
	priceCache := map[string]*Price{}
	var cost float64
	group := func(m map[string]*agg, k string) *agg {
		a := m[k]
		if a == nil {
			a = newAgg()
			m[k] = a
		}
		return a
	}
	for _, x := range evs {
		f, e := x.r.f, x.r.e
		sess := f.Sessions[e.sess]
		da := byDay[x.day]
		if da == nil {
			da = newAgg()
			byDay[x.day] = da
		}
		da.add(sess, e)
		if x.day < from || x.day > today {
			continue
		}
		total.add(sess, e)
		firstTS, lastTS = min(firstTS, e.ts), max(lastTS, e.ts)
		group(projects, f.project).add(sess, e)
		acct := Unattributed
		if e.owner >= 0 {
			if name, ok := q.Accounts[f.Owners[e.owner]]; ok && name != "" {
				acct = name
			}
		}
		group(accounts, acct).add(sess, e)
		if e.kind != kindAssistant {
			users++
			continue
		}
		assts++
		model := "unknown"
		if e.model >= 0 {
			model = f.Models[e.model]
		}
		ma := group(models, model)
		ma.add(sess, e)
		p, seen := priceCache[model]
		if !seen {
			if pr, ok := PriceFor(model); ok {
				p = &pr
			}
			priceCache[model] = p
		}
		if p == nil {
			unpriced[model] = true
			continue
		}
		c := p.cost(e.in, e.out, e.cw, e.cw1h, e.cr)
		ma.cost += c
		cost += c
	}

	s := &Snapshot{
		Range: rng, TZ: loc.String(), From: dayString(from), To: dayString(today),
		Daily: []Day{}, Calendar: make([]CalDay, 0, CalendarDays),
		TopProjects: groups(projects, topN, false), TopModels: groups(models, topN, true),
		Accounts: groups(accounts, 0, false), Unpriced: []string{},
		CostEstimate: Cost{USD: round(cost, 4), PricesUpdated: PricesUpdated, Note: CostNote},
		ScannedFiles: ix.info.Files, ScanMS: round(float64(ix.info.Duration.Microseconds())/1000, 2),
		CacheHit: round(ix.info.HitRatio(), 4),
	}
	s.Totals = Totals{
		Sessions: len(total.sessions), Messages: total.messages, UserMessages: users,
		AssistantMessages: assts, Tokens: total.tokens,
	}
	if total.messages > 0 {
		s.Totals.FirstActivity = time.UnixMilli(firstTS).In(loc).Format(time.RFC3339)
		s.Totals.LastActivity = time.UnixMilli(lastTS).In(loc).Format(time.RFC3339)
	}
	for d := from; d <= today; d++ {
		row := Day{Date: dayString(d)}
		if a := byDay[d]; a != nil {
			row.Sessions, row.Messages, row.Tokens = len(a.sessions), a.messages, a.tokens
			s.Totals.ActiveDays++
		}
		s.Daily = append(s.Daily, row)
	}
	for d := today - CalendarDays + 1; d <= today; d++ {
		c := CalDay{Date: dayString(d)}
		if a := byDay[d]; a != nil {
			c.Sessions, c.Messages, c.Tokens = len(a.sessions), a.messages, a.tokens.Total
		}
		s.Calendar = append(s.Calendar, c)
	}
	s.Streaks = streaks(byDay, today)
	for m := range unpriced {
		s.Unpriced = append(s.Unpriced, m)
	}
	sort.Strings(s.Unpriced)
	return s, nil
}

func groups(m map[string]*agg, limit int, withCost bool) []Group {
	out := make([]Group, 0, len(m))
	for name, a := range m {
		g := Group{Name: name, Sessions: len(a.sessions), Messages: a.messages, Tokens: a.tokens}
		if withCost {
			c := round(a.cost, 4)
			g.CostUSD = &c
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Tokens.Total != b.Tokens.Total {
			return a.Tokens.Total > b.Tokens.Total
		}
		if a.Messages != b.Messages {
			return a.Messages > b.Messages
		}
		return a.Name < b.Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// streaks: the current run ends today, or yesterday while today has no
// activity yet; the longest run is the latest one on a tie.
func streaks(byDay map[int]*agg, today int) Streaks {
	var st Streaks
	if len(byDay) == 0 {
		return st
	}
	days := make([]int, 0, len(byDay))
	for d := range byDay {
		if d <= today {
			days = append(days, d)
		}
	}
	sort.Ints(days)
	runStart := 0
	for i := range days {
		if i == 0 || days[i] != days[i-1]+1 {
			runStart = days[i]
		}
		if n := days[i] - runStart + 1; n >= st.Longest {
			st.Longest, st.LongestStart, st.LongestEnd = n, dayString(runStart), dayString(days[i])
		}
	}
	end := today
	if _, ok := byDay[today]; !ok {
		end = today - 1
	}
	for d := end; ; d-- {
		if _, ok := byDay[d]; !ok {
			break
		}
		st.Current++
	}
	return st
}

// FormatCompact renders 1234567 as "1.2M" (the dashboard's KPI style).
func FormatCompact(n int64) string {
	abs := n
	if abs < 0 {
		abs = -abs
	}
	units := []struct {
		v int64
		s string
	}{{1e12, "T"}, {1e9, "B"}, {1e6, "M"}, {1e3, "k"}}
	for _, u := range units {
		if abs >= u.v {
			v := float64(n) / float64(u.v)
			str := strconv.FormatFloat(v, 'f', 1, 64)
			if v >= 100 || strings.HasSuffix(str, ".0") {
				str = strconv.FormatFloat(v, 'f', 0, 64)
			}
			return str + u.s
		}
	}
	return strconv.FormatInt(n, 10)
}
