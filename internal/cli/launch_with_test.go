package cli

import (
	"strings"
	"testing"
)

func TestWithFlag(t *testing.T) {
	cases := []struct {
		args    []string
		val     string
		next    int
		ok, bad bool
	}{
		{[]string{"--with", "vscode"}, "vscode", 1, true, false},
		{[]string{"--with=desktop"}, "desktop", 0, true, false},
		{[]string{"--with"}, "", 0, true, true},
		{[]string{"--with", "code"}, "", 1, true, true},
		{[]string{"--with=cli;x"}, "", 0, true, true},
		{[]string{"--dry-run"}, "", 0, false, false},
	}
	for _, c := range cases {
		val, next, ok, err := withFlag(c.args, 0)
		if val != c.val || next != c.next || ok != c.ok || (err != nil) != c.bad {
			t.Errorf("%v = %q %d %v %v", c.args, val, next, ok, err)
		}
		if err != nil && !strings.Contains(err.Error(), "auto, cli, vscode, desktop") {
			t.Errorf("%v error lacks the choices: %v", c.args, err)
		}
	}
}
