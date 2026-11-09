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
	sortSessions(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
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
			s.Summary = entry.Summary
			s.Messages = entry.MessageCount
			s.Branch = entry.GitBranch
			if entry.FirstPrompt != "" {
				s.FirstPrompt = entry.FirstPrompt
			}
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

func parseUserPrompt(line string) (string, bool) {
	var parsed struct {
		Type    string `json:"type"`
		Message *struct {
			Content any `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		return "", false
	}
	if parsed.Type != "user" || parsed.Message == nil {
		return "", false
	}
	content, ok := parsed.Message.Content.(string)
	if !ok {
		return "", false
	}
	prompt := collapseSpaces(content)
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
