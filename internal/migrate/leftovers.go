package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// leftoverQuiet is how long a backup dir without a manifest is left alone:
// it may be one a migration is writing right now.
const leftoverQuiet = 10 * time.Minute

// IncompleteBackups lists backups/migrate-* dirs that never finished
// (D101): no manifest (a run before v0.8.3 that failed while backing up)
// or a manifest still marked incomplete whose process is gone. Neither is
// ever a rollback target.
func IncompleteBackups(l *storage.Layout) []string {
	ents, err := os.ReadDir(l.BackupsDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "migrate-") {
			continue
		}
		dir := filepath.Join(l.BackupsDir(), e.Name())
		if incomplete(dir) {
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out
}

func incomplete(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if errors.Is(err, fs.ErrNotExist) {
		info, serr := os.Stat(dir)
		return serr == nil && time.Since(info.ModTime()) > leftoverQuiet
	}
	if err != nil {
		return false
	}
	var m Manifest
	if json.Unmarshal(data, &m) != nil {
		return false // not ours to judge: leave it
	}
	if !m.Incomplete {
		return false
	}
	// A run still writing it: same process, or a live one.
	return m.PID != os.Getpid() && (m.PID <= 0 || !accounts.ProcessAlive(m.PID))
}

// RemoveIncomplete deletes every incomplete backup (they hold nothing
// but partial copies of files that are still in place) and returns the
// dirs it removed.
func RemoveIncomplete(l *storage.Layout) ([]string, error) {
	var gone []string
	var errs []error
	for _, d := range IncompleteBackups(l) {
		if err := os.RemoveAll(d); err != nil {
			errs = append(errs, fmt.Errorf("migrate: remove incomplete backup %s: %w", d, err))
			continue
		}
		gone = append(gone, d)
	}
	return gone, errors.Join(errs...)
}
