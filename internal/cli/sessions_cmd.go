// Stage V0.4-2: `cwm sessions` — list every session in the synced
// workspace (any device, fresh clone or not) and resume one via the
// launcher passthrough (claude --resume <id>).
package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/launcher"
	"github.com/Mr-JoE1/cwm/internal/sessions"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

const (
	sessionsDefaultLimit = 20
	sessionsPromptMax    = 60
	sessionsPickTries    = 3
)

// sessionsArgs is the parsed form of
// `cwm sessions [project] [--pick [N]] [--limit K] [--json]`.
type sessionsArgs struct {
	project string
	pickSet bool // --pick appeared
	pickNum bool // --pick came with an explicit row number
	pickN   int
	limit   int
	asJSON  bool
}

func parseSessionsArgs(args []string) (sessionsArgs, error) {
	p := sessionsArgs{limit: sessionsDefaultLimit}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			p.asJSON = true
		case a == "--pick":
			p.pickSet = true
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					p.pickNum, p.pickN = true, n
					i++
				}
			}
		case strings.HasPrefix(a, "--pick="):
			p.pickSet = true
			n, err := strconv.Atoi(strings.TrimPrefix(a, "--pick="))
			if err != nil {
				return p, fmt.Errorf("--pick needs a row number (try: --pick 2)")
			}
			p.pickNum, p.pickN = true, n
		case a == "--limit":
			if i+1 >= len(args) {
				return p, fmt.Errorf("--limit needs a value (try: --limit 20)")
			}
			k, err := strconv.Atoi(args[i+1])
			if err != nil || k < 0 {
				return p, fmt.Errorf("--limit needs a non-negative number (try: --limit 20)")
			}
			p.limit = k
			i++
		case strings.HasPrefix(a, "--limit="):
			k, err := strconv.Atoi(strings.TrimPrefix(a, "--limit="))
			if err != nil || k < 0 {
				return p, fmt.Errorf("--limit needs a non-negative number (try: --limit 20)")
			}
			p.limit = k
		case strings.HasPrefix(a, "-"):
			return p, fmt.Errorf("unknown sessions flag %q (see: cwm help sessions)", a)
		default:
			if p.project != "" {
				return p, fmt.Errorf("unexpected sessions argument %q (only one project filter allowed)", a)
			}
			p.project = a
		}
	}
	return p, nil
}

func cmdSessions(e *Env, args []string) error {
	p, err := parseSessionsArgs(args)
	if err != nil {
		return err
	}
	layout, err := storage.NewLayout()
	if err != nil {
		return err
	}
	ws := layout.WorkspaceDir()
	if info, err := os.Stat(ws); err != nil || !info.IsDir() {
		return fmt.Errorf("workspace %s does not exist — run `cwm init` or pair Syncthing so the folder is restored ('cwm doctor' can diagnose)", ws)
	}
	projectsDir := filepath.Join(ws, "projects")

	switch {
	case p.pickNum:
		sess, err := sessions.ResolvePick(projectsDir, p.project, p.pickN)
		if err != nil {
			return err
		}
		return openSession(e, sess)
	case p.pickSet:
		if !stdinIsTerminal() {
			return fmt.Errorf("--pick needs a row number when stdin is not a terminal")
		}
		return e.pickSession(projectsDir, p)
	}

	list := listSessions(projectsDir, p.project, p.limit)
	if p.asJSON {
		return writeSessionsJSON(e.Stdout, list)
	}
	writeSessionsTable(e.Stdout, list, time.Now())
	return nil
}

// listSessions wraps sessions.List: a workspace without a projects dir
// yet simply has no sessions.
func listSessions(projectsDir, project string, limit int) []sessions.Session {
	if _, err := os.Stat(projectsDir); err != nil {
		return nil
	}
	list, err := sessions.List(projectsDir, project, limit)
	if err != nil {
		return nil
	}
	return list
}

// openSession resumes s through the single launch path (clone-on-demand,
// pinning and sync hints all apply).
func openSession(e *Env, s sessions.Session) error {
	return launcher.Open(s.Project, launcher.ModeNew, []string{"--resume", s.ID}, e.Account, e.Stdout, e.Stderr)
}

// pickSession prints the table, then prompts on stdin for a row number:
// empty line aborts, a bad answer re-prompts (bounded), a good one resumes.
func (e *Env) pickSession(projectsDir string, p sessionsArgs) error {
	list := listSessions(projectsDir, p.project, p.limit)
	writeSessionsTable(e.Stdout, list, time.Now())
	if len(list) == 0 {
		return nil
	}
	reader := bufio.NewReader(e.Stdin)
	for tries := 0; tries < sessionsPickTries; tries++ {
		fmt.Fprint(e.Stdout, "resume which? [N] ")
		line, _ := reader.ReadString('\n')
		answer := strings.TrimSpace(line)
		if answer == "" {
			fmt.Fprintln(e.Stdout, "aborted")
			return nil
		}
		n, err := strconv.Atoi(answer)
		if err != nil {
			fmt.Fprintln(e.Stdout, "not a row number — try again (or just Enter to abort)")
			continue
		}
		sess, err := sessions.ResolvePick(projectsDir, p.project, n)
		if err != nil {
			fmt.Fprintf(e.Stdout, "%v\n", err)
			continue
		}
		return openSession(e, sess)
	}
	return fmt.Errorf("no session picked after %d tries — run 'cwm sessions' to see rows", sessionsPickTries)
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func writeSessionsTable(w io.Writer, list []sessions.Session, now time.Time) {
	if len(list) == 0 {
		fmt.Fprintln(w, "(no sessions yet — open a repo via cwm open <name> to create one)")
		return
	}
	rows := [][]string{
		{"N", "project", "id", "first prompt", "msgs", "branch", "modified"},
	}
	for i, s := range list {
		rows = append(rows, []string{
			strconv.Itoa(i + 1),
			s.Project,
			sessionID8(s.ID),
			clipPrompt(s.FirstPrompt),
			strconv.Itoa(s.Messages),
			s.Branch,
			relativeTime(s.Modified, now),
		})
	}
	printAlignedTable(w, rows)
}

type sessionJSON struct {
	Project     string `json:"project"`
	ID          string `json:"id"`
	FirstPrompt string `json:"firstPrompt"`
	Summary     string `json:"summary"`
	Messages    int    `json:"messages"`
	Branch      string `json:"branch"`
	Modified    string `json:"modified"`
}

func writeSessionsJSON(w io.Writer, list []sessions.Session) error {
	for _, s := range list {
		obj := sessionJSON{
			Project:     s.Project,
			ID:          s.ID,
			FirstPrompt: s.FirstPrompt,
			Summary:     s.Summary,
			Messages:    s.Messages,
			Branch:      s.Branch,
			Modified:    s.Modified.Format(time.RFC3339),
		}
		data, err := json.Marshal(obj)
		if err != nil {
			return fmt.Errorf("encode session %s: %w", s.ID, err)
		}
		if _, err := fmt.Fprintln(w, string(data)); err != nil {
			return fmt.Errorf("write session %s: %w", s.ID, err)
		}
	}
	return nil
}

// printAlignedTable renders rows as a space-aligned table (two spaces
// between columns, no trailing padding).
func printAlignedTable(w io.Writer, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	cols := len(rows[0])
	widths := make([]int, cols)
	for _, r := range rows {
		for c, cell := range r {
			if c < cols && len(cell) > widths[c] {
				widths[c] = len(cell)
			}
		}
	}
	for _, r := range rows {
		var b strings.Builder
		for c, cell := range r {
			if c > 0 {
				b.WriteString("  ")
			}
			if c == cols-1 {
				b.WriteString(cell)
			} else {
				fmt.Fprintf(&b, "%-*s", widths[c], cell)
			}
		}
		fmt.Fprintln(w, b.String())
	}
}

func sessionID8(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// clipPrompt forces a single line and caps the length at sessionsPromptMax
// runes (an ellipsis marks the cut).
func clipPrompt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= sessionsPromptMax {
		return s
	}
	return string(r[:sessionsPromptMax-3]) + "..."
}

// relativeTime renders "3h ago" / "2d ago" style ages, falling back to a
// plain date once the session is older than a week.
func relativeTime(t, now time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d <= 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
