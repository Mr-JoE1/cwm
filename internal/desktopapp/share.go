package desktopapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

// Code-tab sharing (D124). The app lists Code-tab sessions per account:
// <root>/claude-code-sessions/<account-uuid>/<org-id>/local_<id>.json, a
// small index entry (title, folder, cliSessionId). The transcript itself
// is the ~/.claude/projects file every account shares, so a session made
// under one account is only missing from the other account's list.
//
// Share copies the missing entries into the signed-in account's list,
// ported from karthiknl0/claude-account-switcher's Sync-CodeSessions (MIT)
// with the same safety rules:
//   - add-only: an existing entry is never changed or replaced;
//   - only lists that already exist under the signed-in account's uuid are
//     filled (none = that account hasn't used the Code tab: do nothing);
//   - sources are the other accounts' lists in the same live profile;
//   - one entry per conversation (cliSessionId);
//   - a deleted_* marker naming the entry id or its cliSessionId keeps it
//     deleted;
//   - only local_*.json is copied, nothing else, each written atomically.

// ShareOptions narrow a Share run.
type ShareOptions struct {
	// DryRun counts what would be added and writes nothing.
	DryRun bool
	// Session limits the run to the entries of one CLI session id (the
	// desktop resume); "" = every entry.
	Session string
}

// ShareResult reports a Share run.
type ShareResult struct {
	// Added is the number of entries added (or, in a dry run, that would
	// be).
	Added int `json:"added"`
	// Lists is the number of the signed-in account's lists looked at.
	Lists int `json:"lists"`
	// Skipped says why nothing could be done ("" = it ran).
	Skipped string `json:"skipped,omitempty"`
}

// Why nothing was shared.
const (
	SkipNoAccount = "the Claude app has no signed-in account"
	SkipNoList    = "the app's account hasn't used the Code tab yet (no session list to add to)"
)

// maxEntry bounds an index entry (they are a few hundred bytes); anything
// larger is not one and is left alone.
const maxEntry = 1 << 20

// Share adds the other accounts' Code-tab entries in root to the lists of
// account uuid (Status.UUID).
func Share(root, uuid string, o ShareOptions) (ShareResult, error) {
	var res ShareResult
	if uuid == "" || strings.ContainsAny(uuid, `/\`) || uuid == "." || uuid == ".." {
		res.Skipped = SkipNoAccount
		return res, nil
	}
	base := filepath.Join(root, SessionsDir)
	dests := subdirs(filepath.Join(base, uuid))
	if len(dests) == 0 {
		res.Skipped = SkipNoList
		return res, nil
	}
	res.Lists = len(dests)
	var sources []string
	for _, acct := range subdirs(base) {
		if filepath.Base(acct) != uuid {
			sources = append(sources, subdirs(acct)...)
		}
	}
	var errs []error
	for _, dest := range dests {
		have := map[string]bool{}
		haveCli := map[string]bool{}
		for _, n := range entries(dest) {
			have[n] = true
			if c := cliID(filepath.Join(dest, n)); c != "" {
				haveCli[c] = true
			}
		}
		deleted := markers(dest)
		for _, src := range sources {
			gone := append(markers(src), deleted...)
			for _, n := range entries(src) {
				if have[n] {
					continue
				}
				p := filepath.Join(src, n)
				cli := cliID(p)
				if o.Session != "" && cli != o.Session {
					continue
				}
				if cli != "" && haveCli[cli] {
					continue
				}
				id := strings.TrimSuffix(strings.TrimPrefix(n, "local_"), ".json")
				if isDeleted(gone, id, cli) {
					continue
				}
				if ok, err := copyEntry(p, filepath.Join(dest, n), o.DryRun); err != nil || !ok {
					if err != nil {
						errs = append(errs, err)
					}
					continue
				}
				have[n] = true
				if cli != "" {
					haveCli[cli] = true
				}
				res.Added++
			}
		}
	}
	return res, errors.Join(errs...)
}

// markers are the names of the deleted_* files in dir.
func markers(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range ents {
		if strings.HasPrefix(f.Name(), "deleted_") {
			out = append(out, f.Name())
		}
	}
	return out
}

func isDeleted(markers []string, id, cli string) bool {
	for _, m := range markers {
		if id != "" && strings.Contains(m, id) || cli != "" && strings.Contains(m, cli) {
			return true
		}
	}
	return false
}

// entryMeta is the one field of an index entry the dedupe needs.
type entryMeta struct {
	CliSessionID string `json:"cliSessionId"`
}

func cliID(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var m entryMeta
	if json.NewDecoder(io.LimitReader(f, maxEntry)).Decode(&m) != nil {
		return ""
	}
	return strings.TrimSpace(m.CliSessionID)
}

// copyEntry writes src to dst through a temp file in dst's dir and a
// rename, never over an existing dst, keeping src's mtime. ok = it was
// (or, dry, would be) added.
func copyEntry(src, dst string, dry bool) (bool, error) {
	fi, err := os.Lstat(src)
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxEntry {
		return false, nil
	}
	if _, err := os.Lstat(dst); err == nil {
		return false, nil
	}
	if dry {
		return true, nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	if err := storage.WriteFileAtomic(dst, data, 0o644); err != nil {
		return false, err
	}
	_ = os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	return true, nil
}

// Sync is one sharing run on the live profile st (Detect): it fills the
// signed-in account's lists and returns the run and its summary line
// ("" when nothing was added and nothing went wrong).
func Sync(st Status, o ShareOptions) (ShareResult, string, error) {
	if st.State == StateNone {
		return ShareResult{Skipped: "the Claude app has no profile here"}, "", nil
	}
	res, err := Share(st.Root, st.UUID(), o)
	return res, res.Summary(st), err
}

// Summary is the log line of a run: "added N Code-tab entries for
// <account>", plus when the app is running that it lists them after a
// restart ("" = nothing added).
func (r ShareResult) Summary(st Status) string {
	if r.Added == 0 {
		return ""
	}
	line := fmt.Sprintf("added %d Code-tab entr%s for %s", r.Added, plural(r.Added), st.Who())
	if st.Running {
		line += " — the Claude app is running and lists them after it restarts"
	}
	return line
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
