package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLogHandOff (D123): each hand-off appends the command, folder, exit
// code and the note that claude's own output stays in its window.
func TestLogHandOff(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs", HandOffLogName)
	at := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	LogHandOff(p, "/bin/claude", []string{"--desktop", "--resume", "abc"}, nil, at)
	LogHandOff(p, "/bin/claude", []string{"--desktop", "--resume", "abc"}, errors.New("exec: not found"), at)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("log = %q", b)
	}
	if !strings.HasPrefix(lines[0], "2026-11-27T10:00:00Z /bin/claude --desktop --resume abc (in ") || !strings.Contains(lines[0], ") exit 0 — "+HandOffNote) {
		t.Errorf("success line = %q", lines[0])
	}
	if !strings.Contains(lines[1], "exit -1 (exec: not found) — ") {
		t.Errorf("failure line = %q", lines[1])
	}
	// Past the cap the log starts over and keeps the previous one.
	if err := os.WriteFile(p, make([]byte, handOffLogMax+1), 0o600); err != nil {
		t.Fatal(err)
	}
	LogHandOff(p, "c", nil, nil, at)
	if fi, _ := os.Stat(p); fi.Size() > 300 {
		t.Errorf("not rotated: %d bytes", fi.Size())
	}
	if _, err := os.Stat(p + ".1"); err != nil {
		t.Error(err)
	}
}
