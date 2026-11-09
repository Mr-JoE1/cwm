// Stage V0.4-2: `cwm wip dirty` — the "push before switching devices"
// view: repos under $CWM_REPOS_ROOT with uncommitted work. Repos that are
// not cloned on this machine are clean by definition here.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/launcher"
)

const gitProbeTimeout = 2 * time.Second

func (e *Env) runWipDirty(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: cwm wip dirty")
	}
	root, err := launcher.ReposRoot()
	if err != nil {
		return fmt.Errorf("repos root: %w", err)
	}
	rows, err := scanDirtyRepos(root)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(e.Stdout, "(no dirty repos — everything committed)")
		return nil
	}
	table := [][]string{{"repo", "changed", "last commit"}}
	for _, r := range rows {
		table = append(table, []string{
			r.name,
			fmt.Sprintf("%d changed file(s)", r.changed),
			r.subject,
		})
	}
	printAlignedTable(e.Stdout, table)
	return nil
}

type dirtyRepo struct {
	name    string
	changed int
	subject string
}

// scanDirtyRepos walks root: only real directories containing .git are
// probed, and a repo that cannot answer git status is skipped (never a
// false "dirty").
func scanDirtyRepos(root string) ([]dirtyRepo, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read repos root %s: %w", root, err)
	}
	var rows []dirtyRepo
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(root, ent.Name())
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			continue
		}
		status, err := gitProbe(dir, "status", "--porcelain")
		if err != nil {
			continue
		}
		changed := 0
		for _, line := range strings.Split(status, "\n") {
			if strings.TrimSpace(line) != "" {
				changed++
			}
		}
		if changed == 0 {
			continue
		}
		subject := "(no commits)"
		if out, err := gitProbe(dir, "log", "-1", "--format=%s"); err == nil {
			if s := strings.TrimSpace(out); s != "" {
				subject = s
			}
		}
		rows = append(rows, dirtyRepo{name: ent.Name(), changed: changed, subject: subject})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	return rows, nil
}

// gitProbe runs git in dir with a hard timeout so a wedged repo cannot
// stall the whole scan.
func gitProbe(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("git %s in %s timed out after %s: %w", args[0], dir, gitProbeTimeout, ctx.Err())
	}
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %w", args[0], dir, err)
	}
	return string(out), nil
}
