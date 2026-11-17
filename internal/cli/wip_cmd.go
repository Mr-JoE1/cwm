// Stage V0.4-2: `cwm wip dirty` — the "push before switching devices"
// view: repos under $CWM_REPOS_ROOT with uncommitted work. Repos that are
// not cloned on this machine are clean by definition here (D21). The scan
// itself lives in internal/status (D26).
package cli

import (
	"fmt"

	"github.com/Mr-JoE1/cwm/internal/launcher"
	"github.com/Mr-JoE1/cwm/internal/status"
)

func (e *Env) runWipDirty(args []string) error {
	asJSON, err := onlyJSONFlag(args, "usage: cwm wip dirty [--json]")
	if err != nil {
		return err
	}
	root, err := launcher.ReposRoot()
	if err != nil {
		return fmt.Errorf("repos root: %w", err)
	}
	rows, err := status.Dirty(root)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(e.Stdout, rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(e.Stdout, "(no dirty repos — everything committed)")
		return nil
	}
	table := [][]string{{"repo", "changed", "last commit"}}
	for _, r := range rows {
		table = append(table, []string{
			r.Name,
			fmt.Sprintf("%d changed file(s)", r.Changed),
			r.LastCommit,
		})
	}
	printAlignedTable(e.Stdout, table)
	return nil
}
