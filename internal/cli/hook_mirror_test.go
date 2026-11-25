package cli

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestReadHookPayload(t *testing.T) {
	id, cwd := readHookPayload(strings.NewReader(`{"session_id":"s-1","cwd":"/x/api","hook_event_name":"SessionStart"}`))
	if id != "s-1" || cwd != "/x/api" {
		t.Errorf("payload = %q %q", id, cwd)
	}
	if id, _ := readHookPayload(strings.NewReader("not json")); id != "" {
		t.Error("garbage parsed")
	}
	if id, _ := readHookPayload(nil); id != "" {
		t.Error("nil stdin parsed")
	}
	old := hookStdinWait
	hookStdinWait = 50 * time.Millisecond
	defer func() { hookStdinWait = old }()
	r, w := newPipe(t)
	defer w.Close()
	start := time.Now()
	if id, _ := readHookPayload(r); id != "" || time.Since(start) > 2*time.Second {
		t.Error("an open stdin blocked the hook")
	}
}

func newPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, w
}
