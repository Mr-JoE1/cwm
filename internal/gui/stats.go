package gui

import (
	"net/http"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/sessions"
	"github.com/Mr-JoE1/cwm/internal/stats"
	"github.com/Mr-JoE1/cwm/internal/status"
)

// statsEngine is the server's one stats.Engine (D64), created on first
// use: a dashboard nobody opens never scans.
func (s *Server) statsEngine() *stats.Engine {
	s.statsOnce.Do(func() {
		s.stats = stats.NewEngine(s.opts.Layout.WorkspaceDir(),
			stats.Options{CachePath: s.opts.Layout.StatsCachePath()}, s.opts.StatsInterval)
	})
	return s.stats
}

// statsAccounts maps accountUuid → account name for attribution (D62).
func (s *Server) statsAccounts() map[string]string {
	if s.opts.StatsAccounts == nil {
		return nil
	}
	return s.opts.StatsAccounts()
}

// StatsQuery parses the shared ?range= / ?tz= (and --range / --tz) values.
func StatsQuery(rng, tz string, accounts map[string]string) (stats.Query, error) {
	r, _, err := stats.ParseRange(rng)
	if err != nil {
		return stats.Query{}, err
	}
	loc, err := stats.ParseTZ(tz)
	if err != nil {
		return stats.Query{}, err
	}
	return stats.Query{Range: r, Loc: loc, Accounts: accounts}, nil
}

// StatsDisabled is the GET /api/stats body (and `cwm stats --json`) while
// features.stats is off: nothing is scanned.
type StatsDisabled struct {
	Disabled bool   `json:"disabled"`
	Reason   string `json:"reason"`
}

// StatsOff is the disabled-state body.
var StatsOff = StatsDisabled{Disabled: true, Reason: "stats are off (features.stats); turn them on: cwm config set features.stats on"}

// statsEnabled reads features.stats on every call, so a switch flipped
// from the CLI or Settings applies without an agent restart; an
// unreadable config keeps the default (on).
func (s *Server) statsEnabled() bool {
	cfg, err := config.Load(s.opts.Layout.ConfigPath())
	return err != nil || cfg.Features.Stats
}

// apiStats is GET /api/stats (D64). The request path only aggregates the
// in-memory index; scanning happens in the engine's background refresh.
// Only the first request of a process without an index waits for a scan.
func (s *Server) apiStats(r *http.Request) (any, int, error) {
	if !s.statsEnabled() {
		return StatsOff, http.StatusOK, nil
	}
	q := r.URL.Query()
	query, err := StatsQuery(q.Get("range"), q.Get("tz"), s.statsAccounts())
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	ix, err := s.statsEngine().Index()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	snap, err := ix.Snapshot(query)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	LabelTopProjects(snap, status.ProjectsDir(s.opts.Layout))
	return snap, http.StatusOK, nil
}

// LabelTopProjects names the top projects the way session lists do (repo
// basename, full cwd in Path); `cwm stats` calls it too.
func LabelTopProjects(snap *stats.Snapshot, projectsDir string) {
	if snap == nil || len(snap.TopProjects) == 0 {
		return
	}
	labels := sessions.ProjectLabels(projectsDir)
	for i, g := range snap.TopProjects {
		l, ok := labels[g.Name]
		if !ok {
			l = sessions.Label{Text: sessions.ProjectLabel(g.Name)}
		}
		snap.TopProjects[i].Label, snap.TopProjects[i].Path = l.Text, l.Path
	}
}
