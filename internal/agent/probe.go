package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Info is the body of GET /api/agent: who is serving the dashboard.
type Info struct {
	Agent   bool      `json:"agent"`
	PID     int       `json:"pid"`
	Port    int       `json:"port"`
	Bind    string    `json:"bind"`
	Started time.Time `json:"started"`
	Version string    `json:"version"`
}

// ErrNotRunning means no agent answered at the address in server.toml.
var ErrNotRunning = errors.New("agent: no running cwm agent answered")

// ProbeHost is the address a local client uses to reach an agent bound to
// bind: loopback for loopback/wildcard binds, the bind IP otherwise.
func ProbeHost(bind string) string {
	ip := net.ParseIP(bind)
	if bind == "" || ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return "127.0.0.1"
	}
	return bind
}

// Probe asks the agent recorded in c whether it is alive and accepts c's
// token. It is a plain authenticated GET /api/agent with a short timeout.
func Probe(ctx context.Context, c Config) (Info, error) {
	if c.Port < 1 || c.Port > 65535 || !ValidToken(c.Token) {
		return Info{}, ErrNotRunning
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	host := net.JoinHostPort(ProbeHost(c.Bind), strconv.Itoa(c.Port))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+"/api/agent", nil)
	if err != nil {
		return Info{}, fmt.Errorf("agent: probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := cl.Do(req)
	if err != nil {
		return Info{}, fmt.Errorf("%w: %v", ErrNotRunning, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("%w: %s answered %d", ErrNotRunning, host, resp.StatusCode)
	}
	var info Info
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&info); err != nil || !info.Agent {
		return Info{}, fmt.Errorf("%w: %s is not a cwm agent", ErrNotRunning, host)
	}
	return info, nil
}

// Wait re-reads server.toml under root and probes until an agent answers or
// d elapses (d <= 0 = one attempt). It returns the config it succeeded with.
func Wait(ctx context.Context, root string, d time.Duration) (Config, Info, error) {
	deadline := time.Now().Add(d)
	for {
		c, err := Load(root)
		if err == nil {
			info, perr := Probe(ctx, c)
			if perr == nil {
				return c, info, nil
			}
			err = perr
		}
		if !time.Now().Before(deadline) {
			if !errors.Is(err, ErrNotRunning) {
				err = fmt.Errorf("%w: %v", ErrNotRunning, err)
			}
			return c, Info{}, err
		}
		select {
		case <-ctx.Done():
			return c, Info{}, fmt.Errorf("%w: %v", ErrNotRunning, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}
