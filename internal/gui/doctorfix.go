package gui

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/doctor"
	"github.com/Mr-JoE1/cwm/internal/status"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/upkeep"
)

// v0.9 (D110): POST /api/doctor/fix runs what `cwm doctor --fix` does for
// the named checks, from the dashboard. Only doctor.FixActions names are
// accepted; ?dry_run=1 returns the plan and changes nothing.

// maxFixChecks bounds a request (there are only a handful of names).
const maxFixChecks = 16

type doctorFixRequest struct {
	Checks []string `json:"checks"`
}

// fixResult is one check's outcome. Status: "planned" (dry run), "ok"
// (the check passes now), "failed" (it still does not; Detail says
// why) or "skipped" (the fix pass cannot run for it here).
type fixResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type doctorFixResponse struct {
	DryRun  bool           `json:"dry_run"`
	Results []fixResult    `json:"results"`
	Checks  []status.Check `json:"checks,omitempty"`
}

// fixNames validates the requested names: every one must be on the
// allow-list (400 otherwise); duplicates collapse, order is kept.
func fixNames(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, errors.New(`name at least one check: {"checks":["stignore"]}`)
	}
	if len(in) > maxFixChecks {
		return nil, fmt.Errorf("too many checks (%d, limit %d)", len(in), maxFixChecks)
	}
	var out, bad []string
	for _, n := range in {
		if _, ok := doctor.FixActions[n]; !ok {
			bad = append(bad, fmt.Sprintf("%q", n))
			continue
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	if len(bad) > 0 {
		allowed := make([]string, 0, len(doctor.FixActions))
		for k := range doctor.FixActions {
			allowed = append(allowed, k)
		}
		sort.Strings(allowed)
		return nil, fmt.Errorf("not fixable from the dashboard: %s (allowed: %s)", strings.Join(bad, ", "), strings.Join(allowed, ", "))
	}
	return out, nil
}

// fixSkip says why the fix pass cannot repair name on this config ("" =
// it can): a pre-v0.8 config waits for the reviewed migration (only the
// vault save is safe in either layout, D102), and with features.hooks off
// the pass leaves settings.json alone (D89).
func fixSkip(l *storage.Layout, cfg *config.Config, name string) string {
	if name == "vault" {
		if cfg.DefaultAccount == "" {
			return "no default account — pick one: cwm account use <name>"
		}
		return ""
	}
	if why := upkeep.Ready(l, cfg); why != "" {
		if cfg.NeedsMigration() {
			return "review the migration first: cwm migrate --dry-run"
		}
		return why
	}
	if (name == "cwm-hooks" || name == "settings") && !cfg.Features.Hooks {
		return "features.hooks is off, so the fix pass leaves settings.json alone; to remove cwm's entries: cwm config set features.hooks off"
	}
	return ""
}

// apiDoctorFix is POST /api/doctor/fix[?dry_run=1] with {"checks":[…]}.
func (s *Server) apiDoctorFix(r *http.Request) (any, int, error) {
	var req doctorFixRequest
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	names, err := fixNames(req.Checks)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	dry := r.URL.Query().Get("dry_run") == "1"
	l, cfg, err := s.liveLayout()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	resp := doctorFixResponse{DryRun: dry, Results: make([]fixResult, 0, len(names))}
	var run []string
	for _, n := range names {
		if why := fixSkip(l, cfg, n); why != "" {
			resp.Results = append(resp.Results, fixResult{Name: n, Status: "skipped", Detail: why})
			continue
		}
		run = append(run, n)
		if dry {
			resp.Results = append(resp.Results, fixResult{Name: n, Status: "planned", Detail: "would " + doctor.FixActions[n]})
		}
	}
	if dry {
		return resp, http.StatusOK, nil
	}
	if h, why := s.opts.Pauser.Paused(); h != "" {
		return nil, http.StatusConflict, fmt.Errorf("the agent is paused for %s (%s) — wait for it to finish", why, h)
	}
	errs := map[string]string{}
	if len(run) > 0 {
		// The agent's own tick takes the same lock: never two passes at once.
		ran := s.opts.Pauser.RunTick(func() { errs = s.runFix(l, cfg, run) })
		if !ran {
			return nil, http.StatusConflict, errors.New("the agent paused its maintenance (a migration started) — try again when it finishes")
		}
	}
	checks := status.Doctor(l.Root, false)
	for _, n := range run {
		res := fixResult{Name: n, Status: "ok", Detail: "fixed"}
		if c := findCheck(checks, n); c != nil && c.Status != doctor.StatusOK {
			res.Status, res.Detail = "failed", c.Detail
		}
		if e := errs[n]; e != "" {
			res.Status, res.Detail = "failed", e
		}
		resp.Results = append(resp.Results, res)
	}
	resp.Checks = checks
	s.doctorEvent()
	return resp, http.StatusOK, nil
}

// runFix is the `cwm doctor --fix` pass for the allowed names: one
// maintenance tick when any upkeep check is named, then the vault step.
// It returns an error text per check name.
func (s *Server) runFix(l *storage.Layout, cfg *config.Config, names []string) map[string]string {
	errs := map[string]string{}
	vault := slices.Contains(names, "vault")
	if len(names) > 1 || !vault {
		rep := upkeep.Tick(l, cfg, time.Now())
		for _, e := range rep.Errors {
			step, _, _ := strings.Cut(e, ":")
			for _, n := range names {
				if fixStep(n, step) {
					errs[n] = e
				}
			}
		}
	}
	if vault {
		store := s.switcher().Store
		if _, err := accounts.SaveLive(l, cfg, store); err != nil {
			errs["vault"] = "vault: " + err.Error()
		} else if upkeep.Ready(l, cfg) == "" {
			// A pre-v0.8 layout only gets the save before its migration (D102).
			if _, err := accounts.InstallDefault(l, cfg, cfg.DefaultAccount, store, false, time.Now()); err != nil {
				errs["vault"] = "live login: " + err.Error()
			}
		}
	}
	return errs
}

// fixStep maps an upkeep.Report error step to the check it breaks.
func fixStep(name, step string) bool {
	switch name {
	case "stignore":
		return step == "stignore"
	case "mcp-bridge":
		return step == "mcp bridge"
	case "cwm-hooks", "settings":
		return step == "hooks"
	}
	return false
}

func findCheck(list []status.Check, name string) *status.Check {
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}

// doctorEvent reaches every open page as a "doctor" change on the next
// /api/events poll, so Health and Home refetch the checks.
func (s *Server) doctorEvent() {
	s.doctorGen.Add(1)
	s.invalidateSnapshot()
}
