//go:build windows

package term

import (
	"syscall"
	"testing"
)

// The portable copy must match the real syscall.EscapeArg it stands in for.
func TestPortableEscapeArgMatchesSyscall(t *testing.T) {
	for _, s := range []string{"", "plain", "two words", `C:\dir\`, `C:\my dir\`, `say "hi"`, `back\"slash`, "tab\there", `a\\b c\\`} {
		if got, want := portableEscapeArg(s), syscall.EscapeArg(s); got != want {
			t.Errorf("%q: portable %s, syscall %s", s, got, want)
		}
	}
}
