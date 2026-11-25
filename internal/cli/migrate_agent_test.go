package cli

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// fakeAgent answers /api/agent and, unless old, the pause endpoints.
func fakeAgent(t *testing.T, root string, old bool) *[]string {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch {
		case r.URL.Path == "/api/agent":
			_, _ = w.Write([]byte(`{"agent":true,"pid":1}`))
		case !old && (r.URL.Path == "/api/agent/pause" || r.URL.Path == "/api/agent/resume"):
			if r.Header.Get("X-CWM-CSRF") == "" || r.Header.Get("Content-Type") != "application/json" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte(`{"paused":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	tok, err := agent.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	c := agent.Config{Token: tok, Bind: "127.0.0.1"}
	for _, ch := range port {
		c.Port = c.Port*10 + int(ch-'0')
	}
	if err := agent.Save(root, c); err != nil {
		t.Fatal(err)
	}
	return &calls
}

func TestHoldAgent(t *testing.T) {
	root := t.TempDir()
	l := storage.NewLayoutAt(root)
	var out bytes.Buffer
	e := &Env{Stdout: &out, Stderr: &out}

	// No agent: the migration holds the agent's lock itself.
	release, err := holdAgent(e, l, "cwm migrate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Acquire(root); !errors.Is(err, agent.ErrLocked) {
		t.Fatalf("an agent could start during the migration: %v", err)
	}
	release()

	// An agent holds the lock but does not answer: refuse.
	lock, err := agent.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := holdAgent(e, l, "cwm migrate"); err == nil || !strings.Contains(err.Error(), "stop it first: cwm service stop") || !strings.Contains(err.Error(), "cwm service start") {
		t.Fatalf("silent agent: %v", err)
	}

	// An agent older than v0.8.3 (no pause endpoint): refuse.
	fakeAgent(t, root, true)
	if _, err := holdAgent(e, l, "cwm migrate"); err == nil || !strings.Contains(err.Error(), "stop it first") {
		t.Fatalf("old agent: %v", err)
	}

	// A current agent: paused, then resumed on release.
	calls := fakeAgent(t, root, false)
	release, err = holdAgent(e, l, "cwm migrate")
	if err != nil {
		t.Fatal(err)
	}
	release()
	release() // idempotent
	got := strings.Join(*calls, ",")
	if got != "GET /api/agent,POST /api/agent/pause,POST /api/agent/resume" {
		t.Fatalf("calls = %s", got)
	}
	if !strings.Contains(out.String(), "maintenance paused") {
		t.Fatalf("out = %s", out.String())
	}
}
