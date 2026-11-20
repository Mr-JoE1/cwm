// Package stats derives usage statistics from the session transcripts in
// the workspace (v0.7.1, D48, D60-D64): sessions, messages, tokens by kind,
// per day, project, model and account, streaks and an API-list-price cost
// estimate. It only reads local files; nothing is ever sent anywhere.
//
// Scanning is incremental: per-file results are cached in
// <cwm home>/cache/stats.json keyed by path, size and mtime; a file that
// only grew is parsed from where the last scan stopped. Aggregation
// (range, time zone) runs on the in-memory Index, so changing either never
// rescans.
package stats

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Options tune a scan.
type Options struct {
	// CachePath is the stats cache file ("" = no persistent cache).
	CachePath string
	// Workers parallelises parsing (0 = GOMAXPROCS, capped at 8).
	Workers int
}

// ScanInfo describes one scan.
type ScanInfo struct {
	Files    int // transcripts seen
	Reused   int // unchanged: cached events reused
	Appended int // grew: parsed from the previous offset
	Parsed   int // new or rewritten: parsed from byte 0
	Dropped  int // in the cache but gone from disk
	Duration time.Duration
}

// HitRatio is reused / seen (1 for an empty workspace).
func (s ScanInfo) HitRatio() float64 {
	if s.Files == 0 {
		return 1
	}
	return float64(s.Reused) / float64(s.Files)
}

// Index is the result of a scan: every file's events plus the deduped
// view the aggregations run on. It is immutable once built, so readers
// can use it while the next scan builds its successor.
type Index struct {
	files   []*fileRec
	winners []ref
	info    ScanInfo
	built   time.Time
	fp      string
}

type ref struct {
	f *fileRec
	e *event
}

// Info reports the scan that built the index.
func (ix *Index) Info() ScanInfo { return ix.info }

// Built is when the scan finished.
func (ix *Index) Built() time.Time { return ix.built }

// Fingerprint changes whenever the all-time totals change: the counted
// messages, their tokens and the newest activity. The agent's event
// stream diffs it (D64).
func (ix *Index) Fingerprint() string { return ix.fp }

// ProjectsDir is where transcripts live inside a workspace.
func ProjectsDir(workspaceDir string) string { return filepath.Join(workspaceDir, "projects") }

func projectOf(rel string) string {
	if i := strings.IndexByte(rel, '/'); i > 0 {
		return rel[:i]
	}
	return rel
}

// Build scans workspaceDir from scratch, using (and refreshing) the disk
// cache.
func Build(workspaceDir string, opts Options) (*Index, error) {
	return Update(nil, workspaceDir, opts)
}

type candidate struct {
	rel   string
	abs   string
	size  int64
	mtime int64
}

// Update rescans workspaceDir. prev (may be nil) supplies the cached
// per-file results; without it the disk cache does. prev is not modified.
func Update(prev *Index, workspaceDir string, opts Options) (*Index, error) {
	start := time.Now()
	var cached map[string]*fileRec
	if prev != nil {
		cached = make(map[string]*fileRec, len(prev.files))
		for _, f := range prev.files {
			cached[f.Path] = f
		}
	} else {
		cached = loadCache(opts.CachePath)
	}
	cands, err := listTranscripts(ProjectsDir(workspaceDir))
	if err != nil {
		return nil, err
	}

	ix := &Index{}
	ix.info.Files = len(cands)
	recs := make([]*fileRec, len(cands))
	modes := make([]int, len(cands)) // 0 skipped, 1 reused, 2 appended, 3 parsed
	work := make(chan int)
	workers := opts.Workers
	if workers <= 0 {
		workers = min(runtime.GOMAXPROCS(0), 8)
	}
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				recs[i], modes[i] = scanFile(cands[i], cached[cands[i].rel])
			}
		}()
	}
	for i := range cands {
		work <- i
	}
	close(work)
	wg.Wait()

	seen := make(map[string]bool, len(cands))
	changed := false
	for i, r := range recs {
		switch modes[i] {
		case 1:
			ix.info.Reused++
		case 2:
			ix.info.Appended++
			changed = true
		case 3:
			ix.info.Parsed++
			changed = true
		}
		if r != nil {
			ix.files = append(ix.files, r)
			seen[r.Path] = true
		}
	}
	for p := range cached {
		if !seen[p] {
			ix.info.Dropped++
			changed = true
		}
	}
	sort.Slice(ix.files, func(i, j int) bool { return ix.files[i].Path < ix.files[j].Path })
	ix.dedupe()
	var saveErr error
	if changed || (prev == nil && opts.CachePath != "" && !fileExists(opts.CachePath)) {
		saveErr = saveCache(opts.CachePath, ix.files)
	}
	ix.info.Duration = time.Since(start)
	ix.built = time.Now()
	// A cache that cannot be written only costs the next process a full
	// scan; the numbers are right either way.
	_ = saveErr
	return ix, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// listTranscripts returns projects/*/*.jsonl, without agent-*.jsonl
// (sidechains, as `cwm sessions` does). A missing projects dir is an
// empty workspace.
func listTranscripts(dir string) ([]candidate, error) {
	projects, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stats: read %s: %w", dir, err)
	}
	var out []candidate
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(dir, p.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".jsonl") || strings.HasPrefix(name, "agent-") {
				continue
			}
			info, err := e.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			out = append(out, candidate{
				rel:   path.Join(p.Name(), name),
				abs:   filepath.Join(dir, p.Name(), name),
				size:  info.Size(),
				mtime: info.ModTime().UnixNano(),
			})
		}
	}
	return out, nil
}

// scanFile returns the file's record and how it was obtained
// (1 reused, 2 appended, 3 parsed; 0 + nil = unreadable, skipped).
func scanFile(c candidate, old *fileRec) (*fileRec, int) {
	if old != nil && old.Size == c.size && old.Mtime == c.mtime {
		return old, 1
	}
	f, err := os.Open(c.abs)
	if err != nil {
		return nil, 0
	}
	defer f.Close()
	stem := projectOf(c.rel) + "/" + strings.TrimSuffix(path.Base(c.rel), ".jsonl")

	if old != nil && c.size > old.Size && old.Offset <= c.size && old.HeadLen > 0 {
		if h, err := headHash(f, int64(old.HeadLen)); err == nil && h == old.Head {
			rec := cloneRec(old)
			p := newParser(rec, stem)
			if err := p.parseFrom(f, c.size); err == nil {
				if err := finish(rec, f, c); err == nil {
					return rec, 2
				}
			}
		}
	}
	rec := &fileRec{Path: c.rel, project: projectOf(c.rel)}
	p := newParser(rec, stem)
	if err := p.parseFrom(f, c.size); err != nil {
		return nil, 0
	}
	if err := finish(rec, f, c); err != nil {
		return nil, 0
	}
	return rec, 3
}

func finish(rec *fileRec, f *os.File, c candidate) error {
	rec.Size, rec.Mtime = c.size, c.mtime
	n := min(int64(headBytes), c.size)
	if int64(rec.HeadLen) != n {
		h, err := headHash(f, n)
		if err != nil {
			return err
		}
		rec.Head, rec.HeadLen = h, int(n)
	}
	return nil
}

func cloneRec(r *fileRec) *fileRec {
	c := *r
	c.Sessions = slices.Clone(r.Sessions)
	c.Models = slices.Clone(r.Models)
	c.Owners = slices.Clone(r.Owners)
	c.events = slices.Clone(r.events)
	c.Packed = nil
	return &c
}

// dedupe builds the winners: each assistant message id once (largest
// output, then earliest, then first in path order), each user uuid once
// (earliest), user prompts without a uuid always (D60).
func (ix *Index) dedupe() {
	asst := map[uint64]ref{}
	users := map[uint64]ref{}
	var plain []ref
	for _, f := range ix.files {
		for i := range f.events {
			e := &f.events[i]
			r := ref{f: f, e: e}
			switch {
			case e.key == 0:
				plain = append(plain, r)
			case e.kind == kindAssistant:
				if cur, ok := asst[e.key]; !ok || better(*e, *cur.e) {
					asst[e.key] = r
				}
			default:
				if cur, ok := users[e.key]; !ok || e.ts < cur.e.ts {
					users[e.key] = r
				}
			}
		}
	}
	w := make([]ref, 0, len(plain)+len(asst)+len(users))
	w = append(w, plain...)
	for _, r := range asst {
		w = append(w, r)
	}
	for _, r := range users {
		w = append(w, r)
	}
	ix.winners = w
	var msgs, toks, last int64
	for _, r := range w {
		msgs++
		toks += r.e.in + r.e.out + r.e.cw + r.e.cr
		last = max(last, r.e.ts)
	}
	ix.fp = fmt.Sprintf("%d/%d/%d", msgs, toks, last)
}
