package stats

import (
	"errors"
	"sync"
	"time"
)

// Scan is the one-shot form: scan workspaceDir (incrementally, through
// opts.CachePath) and aggregate for q.
func Scan(workspaceDir string, opts Options, q Query) (*Snapshot, error) {
	ix, err := Build(workspaceDir, opts)
	if err != nil {
		return nil, err
	}
	return ix.Snapshot(q)
}

var errNoIndex = errors.New("stats: no scan finished yet")

// MinInterval is the shortest time between two scans in the agent (D64).
const MinInterval = 60 * time.Second

// Engine keeps one Index in memory for a long-running process (the
// agent). Scans run in the background, one at a time, at most once per
// Interval. Readers never wait for a scan once an index exists: they get
// the current one and, when it is stale, a refresh starts behind them.
type Engine struct {
	workspace string
	opts      Options
	interval  time.Duration
	now       func() time.Time

	mu       sync.Mutex
	ix       *Index
	err      error
	lastScan time.Time // start of the last scan
	running  chan struct{}
}

// NewEngine prepares an engine; nothing is scanned until it is asked.
// interval <= 0 means MinInterval.
func NewEngine(workspaceDir string, opts Options, interval time.Duration) *Engine {
	if interval <= 0 {
		interval = MinInterval
	}
	return &Engine{workspace: workspaceDir, opts: opts, interval: interval, now: time.Now}
}

// kick starts a background scan unless one runs or the last one started
// less than the interval ago (also after a failed scan: a broken
// workspace is not rescanned on every poll). Callers hold e.mu. It
// returns the channel that closes when the running scan ends (nil = none
// running).
func (e *Engine) kick() chan struct{} {
	if e.running != nil {
		return e.running
	}
	if !e.lastScan.IsZero() && e.now().Sub(e.lastScan) < e.interval {
		return nil
	}
	done := make(chan struct{})
	e.running = done
	e.lastScan = e.now()
	prev := e.ix
	go func() {
		ix, err := Update(prev, e.workspace, e.opts)
		e.mu.Lock()
		if err == nil {
			e.ix = ix
		}
		e.err = err
		e.running = nil
		e.mu.Unlock()
		close(done)
	}()
	return done
}

// Index returns the current index. Only when there is none yet does it
// wait for the first scan; otherwise it returns at once and, if the index
// is older than the interval, refreshes it in the background.
func (e *Engine) Index() (*Index, error) {
	e.mu.Lock()
	if e.ix != nil {
		ix := e.ix
		e.kick()
		e.mu.Unlock()
		return ix, nil
	}
	done := e.kick()
	e.mu.Unlock()
	if done != nil {
		<-done
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ix == nil && e.err == nil {
		return nil, errNoIndex
	}
	return e.ix, e.err
}

// Fingerprint is the current index's fingerprint without ever waiting
// ("" until the first scan finished). It nudges a refresh when stale, so
// the event stream's periodic poll is what keeps the index fresh.
func (e *Engine) Fingerprint() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.kick()
	if e.ix == nil {
		return ""
	}
	return e.ix.Fingerprint()
}

// Wait blocks until no scan runs (tests and shutdown).
func (e *Engine) Wait() {
	e.mu.Lock()
	done := e.running
	e.mu.Unlock()
	if done != nil {
		<-done
	}
}
