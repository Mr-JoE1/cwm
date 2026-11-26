package sessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/pathmirror"
)

const (
	promptReadLimit = 64 * 1024
	indexFileName   = "sessions-index.json"
	noPrompt        = "(no prompt)"
)

type Session struct {
	Project     string
	ID          string
	Path        string
	FirstPrompt string
	Summary     string
	Messages    int
	Branch      string
	Modified    time.Time
	Size        int64
	// Copies is how many projects/ folders hold this session id (path
	// mirroring, D91, keeps one per clone path); the list shows it once.
	Copies int
	// Devices are the hosts that recorded running it (session-repos/).
	Devices []string
	// Label and Cwd (v0.9 polish): the list name (repo basename) and the
	// full cwd for tooltips, "" when unknown.
	Label string
	Cwd   string
}

type indexEntry struct {
	SessionID    string `json:"sessionId"`
	FirstPrompt  string `json:"firstPrompt"`
	Summary      string `json:"summary"`
	MessageCount int    `json:"messageCount"`
	GitBranch    string `json:"gitBranch"`
	IsSidechain  bool   `json:"isSidechain"`
}

func List(wsProjectsDir string, project string, limit int) ([]Session, error) {
	projects, err := projectDirs(wsProjectsDir, project)
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, name := range projects {
		out = append(out, listProject(filepath.Join(wsProjectsDir, name), name)...)
	}
	out = dedupe(out)
	idx := pathmirror.LoadSessionIndex(filepath.Dir(wsProjectsDir))
	labels := ProjectLabels(wsProjectsDir)
	for i := range out {
		out[i].Devices = idx.Devices[out[i].ID]
		l, ok := labels[out[i].Project]
		if !ok {
			l = Label{Text: ProjectLabel(out[i].Project)}
		}
		out[i].Label, out[i].Cwd = l.Text, l.Path
	}
	sortSessions(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// dedupe keeps one row per session id — the copy with the most
// messages, then the newest — and counts the copies.
func dedupe(list []Session) []Session {
	at := map[string]int{}
	out := list[:0:0]
	for _, s := range list {
		i, seen := at[s.ID]
		if !seen {
			s.Copies = 1
			at[s.ID] = len(out)
			out = append(out, s)
			continue
		}
		cur := out[i]
		if s.Messages > cur.Messages || (s.Messages == cur.Messages && s.Modified.After(cur.Modified)) {
			s.Copies = cur.Copies
			out[i] = s
		}
		out[i].Copies++
	}
	return out
}

func ResolvePick(wsProjectsDir string, project string, n int) (Session, error) {
	list, err := List(wsProjectsDir, project, 0)
	if err != nil {
		return Session{}, err
	}
	if n < 1 || n > len(list) {
		return Session{}, fmt.Errorf("no session row %d — run 'cwm sessions' to see rows", n)
	}
	return list[n-1], nil
}

// Find looks up session id within project using the same discovery as
// List (D19: raw files are the truth; subagents and sidechains are never
// found). Callers validate project and id first; ok=false means unknown.
func Find(wsProjectsDir string, project string, id string) (Session, bool, error) {
	if project == "" || id == "" {
		return Session{}, false, nil
	}
	list, err := List(wsProjectsDir, project, 0)
	if err != nil {
		return Session{}, false, err
	}
	for _, s := range list {
		if s.ID == id {
			return s, true, nil
		}
	}
	return Session{}, false, nil
}

func projectDirs(wsProjectsDir string, project string) ([]string, error) {
	if project != "" {
		info, err := os.Stat(filepath.Join(wsProjectsDir, project))
		if err != nil || !info.IsDir() {
			return nil, nil
		}
		return []string{project}, nil
	}
	entries, err := os.ReadDir(wsProjectsDir)
	if err != nil {
		return nil, fmt.Errorf("read projects dir %s: %w", wsProjectsDir, err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

func listProject(dir string, project string) []Session {
	index := loadIndex(filepath.Join(dir, indexFileName))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Session
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") || strings.HasPrefix(name, "agent-") {
			continue
		}
		id := strings.TrimSuffix(name, ".jsonl")
		entry, ok := index[id]
		if ok && entry.IsSidechain {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		s := Session{
			Project:  project,
			ID:       id,
			Path:     filepath.Join(dir, name),
			Modified: info.ModTime(),
			Size:     info.Size(),
		}
		if ok {
			s.Messages = entry.MessageCount
			s.Branch = entry.GitBranch
			s.Summary = CleanPrompt(entry.Summary)
			s.FirstPrompt = CleanPrompt(entry.FirstPrompt)
		}
		if s.FirstPrompt == "" {
			s.FirstPrompt = extractFirstPrompt(s.Path)
		}
		out = append(out, s)
	}
	return out
}

func loadIndex(path string) map[string]indexEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var parsed struct {
		Entries []indexEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	index := make(map[string]indexEntry, len(parsed.Entries))
	for _, e := range parsed.Entries {
		index[e.SessionID] = e
	}
	return index
}

func extractFirstPrompt(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return noPrompt
	}
	defer f.Close()
	reader := bufio.NewReader(io.LimitReader(f, promptReadLimit))
	for {
		line, err := reader.ReadString('\n')
		if prompt, ok := parseUserPrompt(line); ok {
			return prompt
		}
		if err != nil {
			return noPrompt
		}
	}
}

// parseUserPrompt returns the human text of a user turn. Content is a
// string or (current Claude Code) a list of blocks; tool results, meta
// turns and bare command output carry no prompt (v0.9 B1).
func parseUserPrompt(line string) (string, bool) {
	var parsed struct {
		Type    string `json:"type"`
		IsMeta  bool   `json:"isMeta"`
		Message *struct {
			Content any `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		return "", false
	}
	if parsed.Type != "user" || parsed.Message == nil || parsed.IsMeta {
		return "", false
	}
	var text string
	switch c := parsed.Message.Content.(type) {
	case string:
		text = c
	case []any:
		var parts []string
		for _, b := range c {
			if m, ok := b.(map[string]any); ok && m["type"] == "text" {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		text = strings.Join(parts, " ")
	default:
		return "", false
	}
	prompt := CleanPrompt(text)
	if prompt == "" {
		return "", false
	}
	return prompt, true
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func sortSessions(list []Session) {
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if !a.Modified.Equal(b.Modified) {
			return a.Modified.After(b.Modified)
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		return a.ID < b.ID
	})
}
