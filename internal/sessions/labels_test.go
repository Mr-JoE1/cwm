package sessions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListPromptTidiesEllipsis(t *testing.T) {
	for in, want := range map[string]string{"one two three …": "one two three…", "a\t…": "a…", "plain": "plain", "x…": "x…"} {
		if got := ListPrompt(Session{FirstPrompt: in}); got != want {
			t.Errorf("ListPrompt(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ListPrompt(Session{Summary: "sum …"}); got != "sum…" {
		t.Errorf("summary = %q", got)
	}
}

func TestTranscriptOwner(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"user","message":{"content":"session-owner"}}` + "\n" +
		`{"type":"session-owner","accountUuid":"u-1"}` + "\n" +
		`{"type":"assistant"}` + "\n" +
		`{"type":"session-owner","accountUuid":"u-2"}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := TranscriptOwner(p); got != "u-2" {
		t.Fatalf("TranscriptOwner = %q", got)
	}
	if got := TranscriptOwner(p + ".missing"); got != "" {
		t.Fatalf("missing = %q", got)
	}
}
