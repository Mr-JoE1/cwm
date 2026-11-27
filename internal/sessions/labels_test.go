package sessions

import "testing"

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
