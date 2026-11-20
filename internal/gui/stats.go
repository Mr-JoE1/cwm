package gui

import (
	"net/http"

	"github.com/Mr-JoE1/cwm/internal/stats"
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

// apiStats is GET /api/stats (D64). The request path only aggregates the
// in-memory index; scanning happens in the engine's background refresh.
// Only the first request of a process without an index waits for a scan.
func (s *Server) apiStats(r *http.Request) (any, int, error) {
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
	return snap, http.StatusOK, nil
}
