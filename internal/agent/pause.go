package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Pauser holds the agent's maintenance tick (path mirror, MCP bridge,
// conflict watcher, .stignore) while a migration or rollback moves files
// (D101). A pause is a lease: a holder in another process renews it, and
// a holder that dies without resuming loses it when the lease runs out,
// so a crashed `cwm migrate` never leaves the agent paused for good.
type Pauser struct {
	mu     sync.Mutex
	holder string
	why    string
	until  time.Time // zero = until Resume (an in-process holder)
	tick   sync.Mutex
	now    func() time.Time
}

// ErrPaused means another holder has the agent paused.
var ErrPaused = errors.New("agent: paused by another migration")

func (p *Pauser) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// active reports the current holder; p.mu must be held.
func (p *Pauser) active() bool {
	if p.holder == "" {
		return false
	}
	if !p.until.IsZero() && !p.clock().Before(p.until) {
		p.holder, p.why, p.until = "", "", time.Time{}
		return false
	}
	return true
}

// Pause takes (or renews) the lease for holder; ttl <= 0 holds it until
// Resume. It returns once no tick is running, so the caller owns the
// files from then on. ErrPaused (wrapped, naming the holder) when someone
// else holds it.
func (p *Pauser) Pause(holder, why string, ttl time.Duration) error {
	if holder == "" {
		return errors.New("agent: pause needs a holder")
	}
	p.mu.Lock()
	if p.active() && p.holder != holder {
		h, w := p.holder, p.why
		p.mu.Unlock()
		return fmt.Errorf("%w: %s (%s)", ErrPaused, w, h)
	}
	p.holder, p.why = holder, why
	p.until = time.Time{}
	if ttl > 0 {
		p.until = p.clock().Add(ttl)
	}
	p.mu.Unlock()
	// A barrier: a tick that started before the lease finishes first.
	p.tick.Lock()
	p.tick.Unlock()
	return nil
}

// Resume drops holder's lease (a no-op for anyone else).
func (p *Pauser) Resume(holder string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.holder == holder {
		p.holder, p.why, p.until = "", "", time.Time{}
	}
}

// Paused names the current holder and why ("" = not paused).
func (p *Pauser) Paused() (holder, why string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active() {
		return "", ""
	}
	return p.holder, p.why
}

// RunTick runs f unless the agent is paused; false = skipped.
func (p *Pauser) RunTick(f func()) bool {
	if h, _ := p.Paused(); h != "" {
		return false
	}
	p.tick.Lock()
	defer p.tick.Unlock()
	if h, _ := p.Paused(); h != "" {
		return false
	}
	f()
	return true
}

// PauseRequest is the body of POST /api/agent/pause and /api/agent/resume.
type PauseRequest struct {
	Holder  string `json:"holder"`
	Why     string `json:"why,omitempty"`
	Seconds int    `json:"seconds,omitempty"`
}

// PauseLease is how long a remote pause lasts without a renewal, and
// PauseRenew how often a remote holder renews it.
const (
	PauseLease = 2 * time.Minute
	PauseRenew = 30 * time.Second
)

// ErrNoPauseAPI means the agent answered but has no pause endpoint (an
// agent older than v0.8.3).
var ErrNoPauseAPI = errors.New("agent: this agent cannot pause (older than v0.8.3)")

// RemotePause asks the agent recorded in c to pause for holder, and
// returns a release func that stops renewing and resumes it. ErrPaused
// when another migration holds it; ErrNoPauseAPI for an older agent.
func RemotePause(ctx context.Context, c Config, holder, why string) (release func(), err error) {
	post := func(path string, seconds int) error {
		body, _ := json.Marshal(PauseRequest{Holder: holder, Why: why, Seconds: seconds})
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		host := net.JoinHostPort(ProbeHost(c.Bind), strconv.Itoa(c.Port))
		req, err := http.NewRequestWithContext(rctx, http.MethodPost, "http://"+host+path, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("agent: pause request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("X-CWM-CSRF", c.Token) // the token itself passes the CSRF check
		req.Header.Set("Content-Type", "application/json")
		cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := cl.Do(req)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNotRunning, err)
		}
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		switch resp.StatusCode {
		case http.StatusOK:
			return nil
		case http.StatusNotFound:
			return ErrNoPauseAPI
		case http.StatusConflict:
			var e struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(msg, &e)
			return fmt.Errorf("%w: %s", ErrPaused, e.Error)
		}
		return fmt.Errorf("agent: %s answered %d: %s", path, resp.StatusCode, bytes.TrimSpace(msg))
	}
	secs := int(PauseLease / time.Second)
	if err := post("/api/agent/pause", secs); err != nil {
		return nil, err
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(PauseRenew)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = post("/api/agent/pause", secs) // a missed renewal only shortens the lease
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
			_ = post("/api/agent/resume", 0)
		})
	}, nil
}
