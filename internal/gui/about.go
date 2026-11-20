package gui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/buildinfo"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/manifest"
	"github.com/Mr-JoE1/cwm/internal/status"
)

// UpdateURL is the one network call the dashboard can cause (D69), and
// only on an explicit "check for updates" POST.
const UpdateURL = "https://api.github.com/repos/Mr-JoE1/cwm/releases/latest"

// updateTimeout bounds the update check end to end.
const updateTimeout = 5 * time.Second

// release is one row of the About timeline (`git log --graph` style).
type release struct {
	V     string `json:"v"`
	State string `json:"state,omitempty"` // next | current | ""
	Note  string `json:"note"`
}

// releases is newest first; "current" is filled in per request.
var releases = []release{
	{V: "0.8", State: "next", Note: "In-browser terminal, full CLI parity"},
	{V: "0.7.2", Note: "UI refresh: terminal vocabulary"},
	{V: "0.7.1", Note: "Stats"},
	{V: "0.7", Note: "Live dashboard, cyber theme"},
	{V: "0.6.2", Note: "Accounts page, sync setup card"},
	{V: "0.6.1", Note: "Windows field fixes, debug bundle"},
	{V: "0.6", Note: "One-line install, always-on agent"},
	{V: "0.5", Note: "Local dashboard"},
}

type aboutAgent struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Listen    string    `json:"listen"`
	UptimeS   int64     `json:"uptime_s"`
}

type aboutWorkspace struct {
	Path     string `json:"path"`
	Repos    int    `json:"repos"`
	Devices  *int   `json:"devices"` // null: Syncthing not reachable
	Accounts int    `json:"accounts"`
}

type aboutAuthor struct {
	Name   string `json:"name"`
	GitHub string `json:"github"`
	Repo   string `json:"repo"`
}

// updateInfo is the last manual update check (zero = never checked).
type updateInfo struct {
	Enabled   bool       `json:"enabled"`
	Latest    *string    `json:"latest"`
	Newer     bool       `json:"newer,omitempty"`
	URL       string     `json:"url,omitempty"`
	CheckedAt *time.Time `json:"checked_at"`
}

type aboutBody struct {
	Version   string         `json:"version"`
	Label     string         `json:"label"`
	Channel   string         `json:"channel"`
	Commit    string         `json:"commit"`
	Dirty     bool           `json:"dirty"`
	Dev       bool           `json:"dev"`
	BuiltAt   string         `json:"built_at"`
	Go        string         `json:"go"`
	OS        string         `json:"os"`
	Arch      string         `json:"arch"`
	Agent     aboutAgent     `json:"agent"`
	Workspace aboutWorkspace `json:"workspace"`
	Author    aboutAuthor    `json:"author"`
	License   string         `json:"license"`
	Releases  []release      `json:"releases"`
	Update    updateInfo     `json:"update"`
}

// updateState is the server's memory of the last manual check.
type updateState struct {
	mu   sync.Mutex
	last updateInfo
}

func (s *Server) buildInfo() buildinfo.Info {
	v := s.opts.Version
	in := buildinfo.Info{Version: strings.TrimPrefix(v.Version, "v"), Commit: v.Commit, BuildDate: v.BuildDate,
		Dirty: v.Dirty, Dev: v.Dev, Go: v.Go, OS: v.OS, Arch: v.Arch}
	if in.Version == "" || in.Version == "dev" {
		in.Version, in.Dev = "dev", true
	}
	if in.Commit == "" || in.Commit == "dev" {
		in.Commit = "unknown"
	}
	return in
}

// apiAbout is GET /api/about: everything is local; nothing here touches
// the network (D69).
func (s *Server) apiAbout(*http.Request) (any, int, error) {
	in := s.buildInfo()
	bind := s.opts.Bind
	if bind == "" {
		bind = agent.DefaultBind
	}
	ws := aboutWorkspace{Path: tildePath(s.opts.Layout.WorkspaceDir())}
	if m, err := manifest.Load(s.opts.Layout.ManifestPath()); err == nil {
		ws.Repos = m.Len()
	}
	if cfg, err := config.Load(s.opts.Layout.ConfigPath()); err == nil {
		ws.Accounts = len(cfg.Accounts)
	}
	if s.opts.SyncClient != nil {
		if cl, err := s.opts.SyncClient(); err == nil {
			if list, err := status.Devices(cl); err == nil {
				n := len(list)
				ws.Devices = &n
			}
		}
	}
	rel := make([]release, len(releases))
	copy(rel, releases)
	cur := -1
	for i, r := range rel {
		if r.V == in.Version {
			cur = i
		}
	}
	if cur < 0 { // a dev build is this tree: the newest shipped-or-shipping row
		for i, r := range rel {
			if r.State != "next" {
				cur = i
				break
			}
		}
	}
	if cur >= 0 {
		rel[cur].State = "current"
	}
	s.update.mu.Lock()
	up := s.update.last
	s.update.mu.Unlock()
	return aboutBody{
		Version: in.Version, Label: in.Label(), Channel: buildinfo.Channel, Commit: in.Commit, Dirty: in.Dirty, Dev: in.Dev,
		BuiltAt: in.BuildDate, Go: in.Go, OS: in.OS, Arch: in.Arch,
		Agent: aboutAgent{PID: os.Getpid(), StartedAt: s.started, Listen: hostPort(bind, s.port),
			UptimeS: int64(time.Since(s.started) / time.Second)},
		Workspace: ws,
		Author:    aboutAuthor{Name: buildinfo.AuthorName, GitHub: buildinfo.AuthorGitHub, Repo: buildinfo.RepoURL},
		License:   buildinfo.License(),
		Releases:  rel,
		Update:    up,
	}, http.StatusOK, nil
}

// apiCheckUpdate is POST /api/about/check-update: the agent (never the
// browser) asks GitHub for the latest release, 5 s at most.
func (s *Server) apiCheckUpdate(r *http.Request) (any, int, error) {
	ctx, cancel := context.WithTimeout(r.Context(), updateTimeout)
	defer cancel()
	latest, url, err := s.fetchLatest(ctx)
	if err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("could not check for updates: %w", err)
	}
	now := time.Now().UTC()
	up := updateInfo{Enabled: true, Latest: &latest, URL: url, CheckedAt: &now,
		Newer: newerVersion(latest, s.buildInfo().Version)}
	s.update.mu.Lock()
	s.update.last = up
	s.update.mu.Unlock()
	return up, http.StatusOK, nil
}

func (s *Server) fetchLatest(ctx context.Context) (version, url string, err error) {
	target := s.opts.UpdateURL
	if target == "" {
		target = UpdateURL
	}
	client := s.opts.UpdateClient
	if client == nil {
		client = &http.Client{Timeout: updateTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "cwm/"+s.buildInfo().Version)
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("GitHub is not reachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", "", fmt.Errorf("read the GitHub answer: %w", err)
	}
	if body.TagName == "" {
		return "", "", errors.New("the GitHub answer has no release tag")
	}
	if !strings.HasPrefix(body.HTMLURL, buildinfo.RepoURL+"/") {
		body.HTMLURL = buildinfo.RepoURL + "/releases"
	}
	return strings.TrimPrefix(body.TagName, "v"), body.HTMLURL, nil
}

// newerVersion compares dotted numeric versions; pre-release suffixes and
// dev builds never count as newer or older ("dev" is never behind).
func newerVersion(latest, current string) bool {
	if current == "dev" || current == "" {
		return false
	}
	a, b := versionParts(latest), versionParts(current)
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func versionParts(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}

// tildePath replaces the home folder with "~" (owner decision: paths in
// the dashboard never show the user's home).
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rel, err := filepath.Rel(home, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return "~" + string(filepath.Separator) + rel
	}
	return p
}

func hostPort(host string, port int) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + strconv.Itoa(port)
	}
	return host + ":" + strconv.Itoa(port)
}
