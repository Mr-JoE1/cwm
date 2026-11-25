// v0.7.1: `cwm stats` — local usage statistics derived from the session
// transcripts in the workspace (D48, D60-D64). Nothing leaves the machine.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/gui"
	"github.com/Mr-JoE1/cwm/internal/stats"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// statsNow is the clock of `cwm stats` (tests pin it).
var statsNow = time.Now

// statsAccountMap maps accountUuid → cwm account name (D62) through
// accounts.UUIDMap; an unreadable config attributes nothing.
func statsAccountMap(l *storage.Layout) map[string]string {
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		return map[string]string{}
	}
	return accounts.UUIDMap(cfg)
}

type statsArgs struct {
	rng, tz string
	asJSON  bool
}

func parseStatsArgs(args []string) (statsArgs, error) {
	var p statsArgs
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func(flag string) (string, error) {
			if v, ok := strings.CutPrefix(a, flag+"="); ok {
				return v, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value (see: cwm help stats)", flag)
			}
			i++
			return args[i], nil
		}
		var err error
		switch {
		case a == "--json":
			p.asJSON = true
		case a == "--range" || strings.HasPrefix(a, "--range="):
			p.rng, err = val("--range")
		case a == "--tz" || strings.HasPrefix(a, "--tz="):
			p.tz, err = val("--tz")
		default:
			return p, fmt.Errorf("unknown stats argument %q (usage: cwm stats [--range 7d|30d|90d|365d|all] [--tz ZONE] [--json])", a)
		}
		if err != nil {
			return p, err
		}
	}
	return p, nil
}

func cmdStats(e *Env, args []string) error {
	p, err := parseStatsArgs(args)
	if err != nil {
		return err
	}
	layout, err := claudepaths.NewLayout()
	if err != nil {
		return err
	}
	if cfg, err := config.Load(layout.ConfigPath()); err == nil && !cfg.Features.Stats {
		// features.stats off: no scan, a disabled state instead.
		if p.asJSON {
			return writeJSON(e.Stdout, gui.StatsOff)
		}
		fmt.Fprintln(e.Stdout, gui.StatsOff.Reason)
		return nil
	}
	q, err := gui.StatsQuery(p.rng, p.tz, statsAccountMap(layout))
	if err != nil {
		return err
	}
	q.Now = statsNow()
	snap, err := stats.Scan(layout.WorkspaceDir(), stats.Options{CachePath: layout.StatsCachePath()}, q)
	if err != nil {
		return err
	}
	if p.asJSON {
		// Byte-for-byte the GET /api/stats body (same encoder as writeAPI).
		data, err := json.Marshal(snap)
		if err != nil {
			return fmt.Errorf("encode stats: %w", err)
		}
		_, err = e.Stdout.Write(append(data, '\n'))
		return err
	}
	return renderStats(e.Stdout, snap)
}

// statsRangeLabel is the human name of a canonical range.
func statsRangeLabel(r string) string {
	switch r {
	case "all":
		return "all time"
	case "365d":
		return "last 365 days"
	}
	return "last " + strings.TrimSuffix(r, "d") + " days"
}

// renderStats is the default table. Only glyphs termout maps to ASCII
// (█ → #, · → -, — → -) so legacy Windows consoles stay clean (D50).
func renderStats(w io.Writer, s *stats.Snapshot) error {
	var b strings.Builder
	t := s.Totals
	fmt.Fprintf(&b, "cwm stats — %s (%s to %s, %s)\n", statsRangeLabel(s.Range), s.From, s.To, s.TZ)
	if t.Messages == 0 {
		b.WriteString("\nNo sessions yet — start one and your activity appears here.\n")
		_, err := io.WriteString(w, b.String())
		return err
	}
	b.WriteString("\n")
	kv := func(k, v string) { fmt.Fprintf(&b, "  %-16s %s\n", k, v) }
	kv("sessions", fmt.Sprint(t.Sessions))
	kv("messages", fmt.Sprintf("%d (%d prompts · %d replies)", t.Messages, t.UserMessages, t.AssistantMessages))
	kv("tokens", fmt.Sprintf("%s (in %s · out %s · cache write %s · cache read %s)",
		stats.FormatCompact(t.Tokens.Total), stats.FormatCompact(t.Tokens.Input), stats.FormatCompact(t.Tokens.Output),
		stats.FormatCompact(t.Tokens.CacheWrite), stats.FormatCompact(t.Tokens.CacheRead)))
	kv("active days", fmt.Sprint(t.ActiveDays))
	streak := fmt.Sprintf("%d current · %d longest", s.Streaks.Current, s.Streaks.Longest)
	if s.Streaks.Longest > 0 {
		streak += fmt.Sprintf(" (%s to %s)", s.Streaks.LongestStart, s.Streaks.LongestEnd)
	}
	kv("streak (days)", streak)
	kv("est. cost", fmt.Sprintf("$%.2f (%s)", s.CostEstimate.USD, s.CostEstimate.Note))

	group := func(title string, gs []stats.Group) {
		if len(gs) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s\n", title)
		width := 4
		for _, g := range gs {
			width = max(width, min(len(g.Name), 40))
		}
		for _, g := range gs {
			name := g.Name
			if len(name) > 40 {
				name = name[:39] + "…"
			}
			fmt.Fprintf(&b, "  %-*s %8s tokens %6d msgs %5d sessions\n", width, name,
				stats.FormatCompact(g.Tokens.Total), g.Messages, g.Sessions)
		}
	}
	group("top projects", s.TopProjects)
	group("top models", s.TopModels)
	group("accounts", s.Accounts)

	days := s.Daily
	if len(days) > 14 {
		days = days[len(days)-14:]
	}
	var peak int64
	for _, d := range days {
		peak = max(peak, d.Tokens.Total)
	}
	fmt.Fprintf(&b, "\nlast %d days (tokens)\n", len(days))
	const barWidth = 30
	for _, d := range days {
		n := 0
		if peak > 0 {
			n = int((d.Tokens.Total*barWidth + peak - 1) / peak)
		}
		bar := strings.Repeat("█", n)
		if n == 0 {
			bar = "·"
		}
		fmt.Fprintf(&b, "  %s  %-*s %s\n", d.Date, barWidth, bar, stats.FormatCompact(d.Tokens.Total))
	}
	if len(s.Unpriced) > 0 {
		fmt.Fprintf(&b, "\nunpriced models (counted as $0): %s\n", strings.Join(s.Unpriced, ", "))
	}
	fmt.Fprintf(&b, "\nprices as of %s · local data only, nothing is sent anywhere\n", s.CostEstimate.PricesUpdated)
	_, err := io.WriteString(w, b.String())
	return err
}
