package agent

import (
	"errors"
	"fmt"
	"net"
	"strconv"
)

// Default agent ports (SPEC-V0.6 A.3): 47800 first, then 47801-47809.
const (
	PortFirst = 47800
	PortLast  = 47809
)

// DefaultBind is the only address used unless --bind says otherwise (D32).
const DefaultBind = "127.0.0.1"

// Candidates is the port order: an explicit port alone (no fallback);
// otherwise the previously bound port first, then PortFirst..PortLast.
func Candidates(explicit, saved int) []int {
	if explicit != 0 {
		return []int{explicit}
	}
	out := make([]int, 0, PortLast-PortFirst+2)
	if saved >= 1 && saved <= 65535 {
		out = append(out, saved)
	}
	for p := PortFirst; p <= PortLast; p++ {
		if p != saved {
			out = append(out, p)
		}
	}
	return out
}

// Listen binds the first free candidate port on bind.
func Listen(bind string, ports []int) (net.Listener, error) {
	if len(ports) == 0 {
		return nil, errors.New("agent: no candidate ports")
	}
	var lastErr error
	for _, p := range ports {
		if p < 1 || p > 65535 {
			return nil, fmt.Errorf("agent: invalid port %d (want 1-65535)", p)
		}
		addr := net.JoinHostPort(bind, strconv.Itoa(p))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		lastErr = err
		if len(ports) == 1 {
			return nil, fmt.Errorf("agent: listen on %s: %w", addr, err)
		}
	}
	return nil, fmt.Errorf("agent: no free port among %d candidates (%d-%d; pass --port N): %w",
		len(ports), PortFirst, PortLast, lastErr)
}

// IsLoopback reports whether bind is a loopback address (or "localhost").
func IsLoopback(bind string) bool {
	if bind == "localhost" {
		return true
	}
	ip := net.ParseIP(bind)
	return ip != nil && ip.IsLoopback()
}

// ValidBind accepts an IP literal or "localhost" (no hostnames: a name
// could resolve somewhere unexpected).
func ValidBind(bind string) error {
	if bind == "localhost" || net.ParseIP(bind) != nil {
		return nil
	}
	return fmt.Errorf("--bind needs an IP address such as 127.0.0.1 or 0.0.0.0, got %q", bind)
}
