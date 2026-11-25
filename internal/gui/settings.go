package gui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/migrate"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
)

// settingRow is one GET /api/settings entry: the `cwm config list`
// registry entry plus its current value and the Settings-tab group.
type settingRow struct {
	config.KeyInfo
	Value string `json:"value"`
	Group string `json:"group"`
	// NA marks a switch that has no effect in the current mode
	// (features.path_mirror in isolated mode).
	NA bool `json:"na,omitempty"`
}

// settingsView is GET /api/settings (D95). Paths carry `~`.
type settingsView struct {
	Mode           string            `json:"mode"`
	NeedsMigration bool              `json:"needs_migration"`
	Root           string            `json:"root"`
	Live           claudepaths.Paths `json:"live"`
	Keys           []settingRow      `json:"keys"`
	// Hash fingerprints config.toml: the page compares it with the one
	// the event stream reports.
	Hash string `json:"hash"`
}

func settingGroup(key string) string {
	if key == "mode" || key == "link_mode" {
		return map[string]string{"mode": "mode", "link_mode": "advanced"}[key]
	}
	g, _, _ := strings.Cut(key, ".")
	return g
}

func tildePaths(p claudepaths.Paths) claudepaths.Paths {
	p.Root = claudepaths.Tilde(p.Root)
	p.ConfigDir = claudepaths.Tilde(p.ConfigDir)
	p.ClaudeJSON = claudepaths.Tilde(p.ClaudeJSON)
	p.Credentials = claudepaths.Tilde(p.Credentials)
	if p.Env != "" {
		p.Env = claudepaths.Tilde(p.Env)
	}
	return p
}

// configHash is a short fingerprint of config.toml ("" = unreadable).
func (s *Server) configHash() string {
	b, err := os.ReadFile(s.opts.Layout.ConfigPath())
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func (s *Server) settingsView() (settingsView, error) {
	l, cfg, err := s.liveLayout()
	if err != nil {
		return settingsView{}, err
	}
	live, err := claudepaths.LiveFor(l, cfg, "")
	if err != nil {
		return settingsView{}, err
	}
	v := settingsView{Mode: cfg.EffectiveMode(), NeedsMigration: cfg.NeedsMigration(), Root: claudepaths.Tilde(live.Root), Live: tildePaths(live), Hash: s.configHash()}
	for _, k := range config.Keys() {
		val, err := cfg.Get(k.Name)
		if err != nil {
			return settingsView{}, err
		}
		row := settingRow{KeyInfo: k, Value: val, Group: settingGroup(k.Name)}
		row.NA = k.Name == "features.path_mirror" && cfg.EffectiveMode() == config.ModeIsolated
		v.Keys = append(v.Keys, row)
	}
	return v, nil
}

func (s *Server) apiSettings(*http.Request) (any, int, error) {
	v, err := s.settingsView()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return v, http.StatusOK, nil
}

// settingsPut is the PUT /api/settings body: one key at a time, like
// `cwm config set`. DryRun only describes what the change would do (the
// Settings tab shows it before autostart and private sync are applied).
type settingsPut struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	DryRun bool   `json:"dry_run"`
}

type settingsPutResult struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Saved bool   `json:"saved"`
	// Notes are the lines `cwm config set` would print (what changed, or
	// with dry_run, what would change).
	Notes []string `json:"notes"`
	Hash  string   `json:"hash,omitempty"`
}

func lines(b *bytes.Buffer) []string {
	out := []string{}
	for _, l := range strings.Split(b.String(), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// apiSettingsPut is PUT /api/settings (CSRF): validate and save one key,
// then run the same effects as `cwm config set`. mode is refused here: a
// mode change moves data and goes through /api/migrate/preview + apply.
func (s *Server) apiSettingsPut(r *http.Request) (any, int, error) {
	var req settingsPut
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	if req.Key == "mode" {
		return nil, http.StatusConflict, errors.New("a mode change moves data: preview it first (POST /api/migrate/preview, then /api/migrate/apply), or run `cwm mode set`")
	}
	l, cfg, err := s.liveLayout()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if _, ok := config.LookupKey(req.Key); !ok {
		return nil, http.StatusNotFound, fmt.Errorf("unknown setting %q", req.Key)
	}
	if err := cfg.Set(req.Key, req.Value); err != nil {
		return nil, http.StatusBadRequest, err
	}
	got, _ := cfg.Get(req.Key)
	res := settingsPutResult{Key: req.Key, Value: got, Notes: []string{}}
	var out bytes.Buffer
	if req.DryRun {
		if s.opts.SettingEffects != nil {
			if err := s.opts.SettingEffects(cfg, req.Key, true, &out); err != nil {
				return nil, http.StatusInternalServerError, err
			}
		}
		res.Notes = lines(&out)
		return res, http.StatusOK, nil
	}
	if err := config.Save(l.ConfigPath(), cfg); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	res.Saved = true
	res.Hash = s.configHash()
	s.invalidateSnapshot()
	if s.opts.SettingEffects != nil {
		if err := s.opts.SettingEffects(cfg, req.Key, false, &out); err != nil {
			return nil, http.StatusInternalServerError, fmt.Errorf("%s is saved, but applying it failed: %w", req.Key, err)
		}
	}
	res.Notes = lines(&out)
	return res, http.StatusOK, nil
}

// invalidateSnapshot makes the next /api/events poll recompute, so a
// settings change reaches every open page within one poll.
func (s *Server) invalidateSnapshot() {
	s.snapMu.Lock()
	s.snapAt = time.Time{}
	s.snapMu.Unlock()
}

// migrationJob is the dashboard's one mode-change run: the engine writes
// its progress lines here and the page polls GET /api/migrate.
type migrationJob struct {
	mu       sync.Mutex
	running  bool
	to       string
	lines    []string
	partial  string
	record   *migrate.Record
	err      string
	started  time.Time
	finished time.Time
}

// Write collects progress output line by line (an io.Writer for
// migrate.Options.Progress).
func (j *migrationJob) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	text := j.partial + string(p)
	parts := strings.Split(text, "\n")
	j.partial = parts[len(parts)-1]
	for _, l := range parts[:len(parts)-1] {
		if l = strings.TrimRight(l, "\r"); strings.TrimSpace(l) != "" {
			j.lines = append(j.lines, claudepaths.Tilde(l))
		}
	}
	return len(p), nil
}

type migrationStatus struct {
	Mode           string          `json:"mode"`
	NeedsMigration bool            `json:"needs_migration"`
	Running        bool            `json:"running"`
	To             string          `json:"to,omitempty"`
	Lines          []string        `json:"lines"`
	Record         *migrate.Record `json:"record,omitempty"`
	Error          string          `json:"error,omitempty"`
	Started        string          `json:"started,omitempty"`
	Finished       string          `json:"finished,omitempty"`
}

func (s *Server) apiMigrateStatus(*http.Request) (any, int, error) {
	cfg, err := config.Load(s.opts.Layout.ConfigPath())
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	j := &s.migration
	j.mu.Lock()
	defer j.mu.Unlock()
	var rec *migrate.Record
	if j.record != nil {
		cp := *j.record
		cp.Root, cp.Backup = claudepaths.Tilde(cp.Root), claudepaths.Tilde(cp.Backup)
		rec = &cp
	}
	return migrationStatus{
		Mode: cfg.EffectiveMode(), NeedsMigration: cfg.NeedsMigration(),
		Running: j.running, To: j.to, Lines: append([]string{}, j.lines...), Record: rec, Error: j.err,
		Started: rfc3339(j.started), Finished: rfc3339(j.finished),
	}, http.StatusOK, nil
}

type migrateRequest struct {
	To string `json:"to"`
}

// previewActions caps the per-file list the modal shows; the counts
// always cover the whole plan.
const previewActions = 200

type migratePreview struct {
	*migrate.Plan
	Counts    map[string]int `json:"counts"`
	Truncated int            `json:"truncated,omitempty"`
}

func (s *Server) migrateOptions(to string) migrate.Options {
	var o migrate.Options
	if s.opts.Migrate != nil {
		o = *s.opts.Migrate
	}
	o.To = to
	if o.SyncFolder == nil && s.opts.SyncClient != nil {
		connect := s.opts.SyncClient
		o.SyncFolder = func(root string) error {
			cl, err := connect()
			if err != nil {
				return err
			}
			_, err = syncengine.EnsureFolder(cl, root)
			return err
		}
	}
	return o
}

func validTarget(to string) error {
	if to != "" && !config.ValidMode(to) {
		return fmt.Errorf("invalid mode %q (want capture or isolated)", to)
	}
	return nil
}

// apiMigratePreview is POST /api/migrate/preview: the dry-run plan of
// `cwm migrate --dry-run --to <to>`. Nothing changes.
func (s *Server) apiMigratePreview(r *http.Request) (any, int, error) {
	var req migrateRequest
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	if err := validTarget(req.To); err != nil {
		return nil, http.StatusBadRequest, err
	}
	l, cfg, err := s.liveLayout()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	plan, err := migrate.BuildPlan(l, cfg, s.migrateOptions(req.To))
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	pv := migratePreview{Plan: plan, Counts: map[string]int{}}
	// A copy with `~` paths: the modal shows locations, never values.
	cp := *plan
	cp.Root = claudepaths.Tilde(cp.Root)
	cp.Sources = tildeAll(cp.Sources)
	cp.BackupRoots = tildeAll(cp.BackupRoots)
	cp.Actions = nil
	for i, a := range plan.Actions {
		pv.Counts[a.Kind]++
		if i < previewActions {
			a.Src, a.Dst = claudepaths.Tilde(a.Src), claudepaths.Tilde(a.Dst)
			cp.Actions = append(cp.Actions, a)
		}
	}
	if cp.Actions == nil {
		cp.Actions = []migrate.Action{}
	}
	pv.Truncated = max(0, len(plan.Actions)-previewActions)
	pv.Plan = &cp
	return pv, http.StatusOK, nil
}

func tildeAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		out = append(out, claudepaths.Tilde(p))
	}
	return out
}

// apiMigrateApply is POST /api/migrate/apply: start the migration (or
// mode change) in the background; GET /api/migrate follows it. One run
// at a time.
func (s *Server) apiMigrateApply(r *http.Request) (any, int, error) {
	var req migrateRequest
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	if err := validTarget(req.To); err != nil {
		return nil, http.StatusBadRequest, err
	}
	j := &s.migration
	j.mu.Lock()
	if j.running {
		j.mu.Unlock()
		return nil, http.StatusConflict, errors.New("a migration is already running")
	}
	if h, why := s.opts.Pauser.Paused(); h != "" {
		j.mu.Unlock()
		return nil, http.StatusConflict, fmt.Errorf("the agent is paused for %s (%s) — wait for it to finish", why, h)
	}
	j.running, j.to, j.lines, j.partial, j.record, j.err = true, req.To, []string{}, "", nil, ""
	j.started, j.finished = time.Now(), time.Time{}
	j.mu.Unlock()
	go s.runMigration(req.To)
	return map[string]any{"started": true, "to": req.To}, http.StatusAccepted, nil
}

func (s *Server) runMigration(to string) {
	// The same lock as every other action: no account switch or setting
	// write while files move.
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	j := &s.migration
	rec, err := func() (*migrate.Record, error) {
		// D101: the maintenance tick runs in this process too; hold it
		// while files move, so the mirror, bridge and .stignore writer
		// never see a half-migrated tree.
		if err := s.opts.Pauser.Pause(dashboardHolder, "the dashboard's migration", 0); err != nil {
			return nil, err
		}
		defer s.opts.Pauser.Resume(dashboardHolder)
		l, cfg, err := s.liveLayout()
		if err != nil {
			return nil, err
		}
		o := s.migrateOptions(to)
		o.Progress = j
		plan, err := migrate.BuildPlan(l, cfg, o)
		if err != nil {
			return nil, err
		}
		if plan.Kind == migrate.KindNone {
			fmt.Fprintf(j, "nothing to migrate: mode is already %s\n", plan.ToMode)
			return nil, nil
		}
		return migrate.Apply(l, cfg, plan, o)
	}()
	_, _, _ = s.liveLayout() // re-point at the (new) root
	s.invalidateSnapshot()
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.partial != "" {
		j.lines = append(j.lines, claudepaths.Tilde(j.partial))
		j.partial = ""
	}
	j.running, j.record, j.finished = false, rec, time.Now()
	if err != nil {
		j.err = claudepaths.Tilde(err.Error())
	}
}

// dashboardHolder is the pause holder of a dashboard migration.
const dashboardHolder = "dashboard"

// apiAgentPause is POST /api/agent/pause {holder, why, seconds} (D101):
// `cwm migrate` and `cwm migrate rollback` hold the maintenance tick for
// a lease (renewed every 30 s) while they move files. 409 while another
// holder (the dashboard's own migration) has it.
func (s *Server) apiAgentPause(r *http.Request) (any, int, error) {
	var req agent.PauseRequest
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	if req.Holder == "" || req.Holder == dashboardHolder || len(req.Holder) > 64 {
		return nil, http.StatusBadRequest, errors.New("pause needs a holder id")
	}
	secs := req.Seconds
	if secs <= 0 || secs > 600 {
		secs = int(agent.PauseLease / time.Second)
	}
	why := req.Why
	if why == "" || len(why) > 80 {
		why = "a migration"
	}
	s.migration.mu.Lock()
	running := s.migration.running
	s.migration.mu.Unlock()
	if running {
		return nil, http.StatusConflict, errors.New("the dashboard is running a migration — wait for it to finish")
	}
	if err := s.opts.Pauser.Pause(req.Holder, why, time.Duration(secs)*time.Second); err != nil {
		return nil, http.StatusConflict, err
	}
	return map[string]any{"paused": true, "seconds": secs}, http.StatusOK, nil
}

// apiAgentResume is POST /api/agent/resume {holder}.
func (s *Server) apiAgentResume(r *http.Request) (any, int, error) {
	var req agent.PauseRequest
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	if req.Holder == dashboardHolder {
		return nil, http.StatusBadRequest, errors.New("the dashboard's own pause ends with its migration")
	}
	s.opts.Pauser.Resume(req.Holder)
	return map[string]any{"paused": false}, http.StatusOK, nil
}
