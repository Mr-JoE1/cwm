package gui

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/workspace"
)

// conflictsResult is GET /api/conflicts.
type conflictsResult struct {
	Conflicts []workspace.Conflict `json:"conflicts"`
}

// conflictResolveRequest is POST /api/conflicts/resolve. Choice is
// "current", "merge" or the name of one conflict copy.
type conflictResolveRequest struct {
	Path   string `json:"path"`
	Choice string `json:"choice"`
}

type conflictResolveResult struct {
	Path   string `json:"path"`
	Choice string `json:"choice"`
	Backup string `json:"backup"`
}

// liveLayout is the server's layout with the config read now, re-pointed
// at the configured mode's root: a `cwm migrate` or `cwm mode set` run
// from the CLI while the agent is up must not leave it on the old root.
func (s *Server) liveLayout() (*storage.Layout, *config.Config, error) {
	cfg, err := config.Load(s.opts.Layout.ConfigPath())
	if err != nil {
		return s.opts.Layout, nil, err
	}
	if err := claudepaths.Apply(s.opts.Layout, cfg); err != nil {
		return s.opts.Layout, cfg, err
	}
	return s.opts.Layout, cfg, nil
}

// apiConflicts is GET /api/conflicts: the same list as `cwm conflicts`.
func (s *Server) apiConflicts(*http.Request) (any, int, error) {
	l, cfg, err := s.liveLayout()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	cs, err := workspace.FindConflicts(l.WorkspaceDir(), cfg)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if cs == nil {
		cs = []workspace.Conflict{}
	}
	return conflictsResult{Conflicts: cs}, http.StatusOK, nil
}

// apiConflictsResolve is POST /api/conflicts/resolve: the same
// workspace.ResolveConflict as `cwm conflicts resolve`.
func (s *Server) apiConflictsResolve(r *http.Request) (any, int, error) {
	var req conflictResolveRequest
	if code, err := decodeBody(r, &req); err != nil {
		return nil, code, err
	}
	if req.Path == "" || req.Choice == "" || strings.Contains(req.Path, "..") || strings.Contains(req.Choice, "..") {
		return nil, http.StatusBadRequest, errors.New("path and choice are required (choice: current, merge or a copy name)")
	}
	l, cfg, err := s.liveLayout()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	bdir, err := workspace.ResolveConflict(l, cfg, req.Path, req.Choice, time.Now())
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("resolve %s: %w", req.Path, err)
	}
	return conflictResolveResult{Path: req.Path, Choice: req.Choice, Backup: claudepaths.Tilde(bdir)}, http.StatusOK, nil
}
