package sessions

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Label is how lists name a project (v0.9 polish): the repo basename when
// the folder's transcripts recorded a cwd, with a short parent prefix only
// when two projects share that basename. Path is the full cwd for
// tooltips ("" when the cwd is unknown or belongs to another clone path).
type Label struct {
	Text string
	Path string
}

// cwdEntry caches one project folder's cwd, keyed on the folder mtime: a
// new transcript changes it, so a folder without a cwd is re-read only
// when something was added.
type cwdEntry struct {
	mod time.Time
	cwd string
}

var cwdCache sync.Map // folder path → cwdEntry

// cwdFiles bounds the transcripts read per folder when looking for a cwd.
const cwdFiles = 3

// projectCwd returns the cwd Claude Code recorded in a project folder's
// transcripts (every turn carries one), or "".
func projectCwd(dir string) string {
	info, err := os.Stat(dir)
	if err != nil {
		return ""
	}
	if v, ok := cwdCache.Load(dir); ok && v.(cwdEntry).mod.Equal(info.ModTime()) {
		return v.(cwdEntry).cwd
	}
	cwd := ""
	if entries, err := os.ReadDir(dir); err == nil {
		read := 0
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".jsonl") || strings.HasPrefix(name, "agent-") {
				continue
			}
			if cwd = transcriptCwd(filepath.Join(dir, name)); cwd != "" {
				break
			}
			if read++; read >= cwdFiles {
				break
			}
		}
	}
	cwdCache.Store(dir, cwdEntry{mod: info.ModTime(), cwd: cwd})
	return cwd
}

func transcriptCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	reader := bufio.NewReader(io.LimitReader(f, promptReadLimit))
	for {
		line, err := reader.ReadString('\n')
		if strings.Contains(line, `"cwd"`) {
			var parsed struct {
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal([]byte(line), &parsed) == nil && parsed.Cwd != "" {
				return parsed.Cwd
			}
		}
		if err != nil {
			return ""
		}
	}
}

// slugOf is Claude Code's folder name for a cwd: every character that is
// not an ASCII letter or digit becomes "-".
func slugOf(p string) string {
	b := []byte(p)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

// pathParts splits a cwd from any OS ("/home/u/x", `C:\Users\u\x`).
func pathParts(p string) []string {
	var out []string
	for _, s := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

type labelInfo struct {
	Label
	parent  string
	fromCwd bool
}

func labelOf(slug, cwd string) labelInfo {
	parts := pathParts(cwd)
	if len(parts) > 0 {
		base := parts[len(parts)-1]
		same := strings.EqualFold(slugOf(cwd), slug)
		// a session mirrored from another clone path (D91) still names the
		// same repo: trust the basename when the folder ends with it.
		if same || strings.HasSuffix(strings.ToLower(slug), "-"+strings.ToLower(slugOf(base))) || strings.EqualFold(slug, slugOf(base)) {
			li := labelInfo{Label: Label{Text: base}, fromCwd: true}
			if same {
				li.Path = cwd
			}
			if len(parts) > 1 {
				li.parent = parts[len(parts)-2]
			}
			return li
		}
	}
	return labelInfo{Label: Label{Text: ProjectLabel(slug)}}
}

// Labels names every project in names. Basenames are kept unless two
// projects share one; those get their parent folder as a prefix
// ("work/api", "oss/api"), or the shortened folder name when the parent
// is unknown.
func Labels(names []string, cwdOf func(string) string) map[string]Label {
	infos := make(map[string]labelInfo, len(names))
	count := map[string]int{}
	for _, n := range names {
		li := labelOf(n, cwdOf(n))
		infos[n] = li
		count[strings.ToLower(li.Text)]++
	}
	out := make(map[string]Label, len(names))
	for n, li := range infos {
		if count[strings.ToLower(li.Text)] > 1 && li.fromCwd {
			if li.parent != "" {
				li.Text = li.parent + "/" + li.Text
			} else {
				li.Text = ProjectLabel(n)
			}
		}
		out[n] = li.Label
	}
	return out
}

// ProjectLabels names every project folder under wsProjectsDir.
func ProjectLabels(wsProjectsDir string) map[string]Label {
	names, err := projectDirs(wsProjectsDir, "")
	if err != nil {
		return map[string]Label{}
	}
	return Labels(names, func(n string) string { return projectCwd(filepath.Join(wsProjectsDir, n)) })
}
