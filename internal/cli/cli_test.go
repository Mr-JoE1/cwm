package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

type result struct {
	code int
	out  string
	err  string
}

func runCLI(t *testing.T, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, &out, &errb)
	return result{code: code, out: out.String(), err: errb.String()}
}

func TestVersion(t *testing.T) {
	got := runCLI(t, "version")
	if got.code != ExitOK {
		t.Fatalf("code = %d, want %d (stderr: %s)", got.code, ExitOK, got.err)
	}
	want := regexp.MustCompile(`^cwm \S+ \(\w+/\w+, \S+\)\n$`)
	if !want.MatchString(got.out) {
		t.Errorf("version output = %q, want 'cwm <ver> (<goos>/<goarch>, <commit>)'", got.out)
	}
}

func TestNoArgsPrintsUsage(t *testing.T) {
	got := runCLI(t)
	if got.code != ExitOK {
		t.Fatalf("code = %d, want %d", got.code, ExitOK)
	}
	for _, sub := range []string{"usage:", "commands:", "version", "--account"} {
		if !strings.Contains(got.out, sub) {
			t.Errorf("usage output missing %q:\n%s", sub, got.out)
		}
	}
}

func TestHelpFlags(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			got := runCLI(t, flag)
			if got.code != ExitOK {
				t.Fatalf("code = %d, want %d", got.code, ExitOK)
			}
			if !strings.Contains(got.out, "usage:") {
				t.Errorf("output missing usage:\n%s", got.out)
			}
		})
	}
}

func TestHelpCommand(t *testing.T) {
	got := runCLI(t, "help")
	if got.code != ExitOK {
		t.Fatalf("code = %d, want %d", got.code, ExitOK)
	}
	if !strings.Contains(got.out, "commands:") {
		t.Errorf("help output missing command list:\n%s", got.out)
	}
}

func TestHelpTopics(t *testing.T) {
	tests := []struct {
		topic     string
		wantUsage string
		notImp    bool
	}{
		{"version", "usage: cwm version", false},
		{"help", "usage: cwm help [command]", false},
		{"open", "usage: cwm open <repo> [new|last]", true},
		{"init", "usage: cwm init [--migrate] [--account <name>]", true},
		{"doctor", "usage: cwm doctor [--net]", true},
	}
	for _, tt := range tests {
		t.Run(tt.topic, func(t *testing.T) {
			got := runCLI(t, "help", tt.topic)
			if got.code != ExitOK {
				t.Fatalf("code = %d, want %d (stderr: %s)", got.code, ExitOK, got.err)
			}
			if !strings.Contains(got.out, tt.wantUsage) {
				t.Errorf("output = %q, want it to contain %q", got.out, tt.wantUsage)
			}
			if tt.notImp && !strings.Contains(got.out, "not implemented in this stage") {
				t.Errorf("output = %q, want a not-implemented note", got.out)
			}
		})
	}
}

func TestHelpUnknownTopic(t *testing.T) {
	got := runCLI(t, "help", "bogus")
	if got.code != ExitError {
		t.Fatalf("code = %d, want %d", got.code, ExitError)
	}
	if !strings.Contains(got.err, "unknown help topic") {
		t.Errorf("stderr = %q, want 'unknown help topic'", got.err)
	}
}

func TestNotImplementedCommands(t *testing.T) {
	for _, c := range commands {
		if c.run != nil {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			got := runCLI(t, c.name)
			if got.code != ExitNotImp {
				t.Fatalf("code = %d, want %d", got.code, ExitNotImp)
			}
			if !strings.Contains(got.err, "not implemented in this stage") {
				t.Errorf("stderr = %q, want 'not implemented in this stage'", got.err)
			}
			if !strings.Contains(got.err, c.name) {
				t.Errorf("stderr = %q, want it to name the command", got.err)
			}
		})
	}
}

func TestRepoLaunchNotImplemented(t *testing.T) {
	tests := []struct {
		name string
		arg  string
	}{
		{"plain repo name", "alpha"},
		{"reserved statusline", "statusline"},
		{"reserved gui", "gui"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCLI(t, tt.arg)
			if got.code != ExitNotImp {
				t.Fatalf("code = %d, want %d", got.code, ExitNotImp)
			}
			if !strings.Contains(got.err, "not implemented in this stage") {
				t.Errorf("stderr = %q, want 'not implemented in this stage'", got.err)
			}
			if !strings.Contains(got.err, tt.arg) {
				t.Errorf("stderr = %q, want it to name %q", got.err, tt.arg)
			}
		})
	}
}

func TestGlobalFlagsBeforeCommand(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantAcc string
		wantDry bool
	}{
		{"account split", []string{"--account", "main", "version"}, "main", false},
		{"account joined", []string{"--account=work", "version"}, "work", false},
		{"dry-run", []string{"--dry-run", "version"}, "", true},
		{"both", []string{"--dry-run", "--account", "x", "version"}, "x", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			e := &Env{Stdout: &out, Stderr: &errb, Stdin: strings.NewReader("")}
			code := e.Run(tt.args)
			if code != ExitOK {
				t.Fatalf("code = %d, want %d (stderr: %s)", code, ExitOK, errb.String())
			}
			if e.Account != tt.wantAcc {
				t.Errorf("Account = %q, want %q", e.Account, tt.wantAcc)
			}
			if e.DryRun != tt.wantDry {
				t.Errorf("DryRun = %v, want %v", e.DryRun, tt.wantDry)
			}
		})
	}
}

func TestFlagErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"account without value", []string{"--account"}, "--account needs a value"},
		{"unknown flag", []string{"--bogus", "version"}, "unknown global flag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCLI(t, tt.args...)
			if got.code != ExitUsage {
				t.Fatalf("code = %d, want %d", got.code, ExitUsage)
			}
			if !strings.Contains(got.err, tt.want) {
				t.Errorf("stderr = %q, want it to contain %q", got.err, tt.want)
			}
		})
	}
}

func TestIsReserved(t *testing.T) {
	reserved := []string{"init", "import", "open", "list", "wip", "doctor", "account", "mcp", "hook", "version", "help", "statusline", "gui"}
	if len(ReservedNames) != len(reserved) {
		t.Fatalf("len(ReservedNames) = %d, want %d", len(ReservedNames), len(reserved))
	}
	for _, r := range reserved {
		if !IsReserved(r) {
			t.Errorf("IsReserved(%q) = false, want true", r)
		}
	}
	for _, free := range []string{"alpha", "", "versions", "hel", "my-repo"} {
		if IsReserved(free) {
			t.Errorf("IsReserved(%q) = true, want false", free)
		}
	}
}

func TestEveryReservedNameHasCLIHandling(t *testing.T) {
	for _, r := range ReservedNames {
		got := runCLI(t, r)
		switch {
		case got.code == ExitOK:
			if got.out == "" && got.err == "" {
				t.Errorf("cwm %s: no output at all", r)
			}
		case got.code == ExitNotImp || got.code == ExitError:
			if !strings.Contains(got.err, "not implemented") && !strings.Contains(got.err, "unknown") {
				t.Errorf("cwm %s: stderr = %q, want a not-implemented or error message", r, got.err)
			}
		default:
			t.Errorf("cwm %s: code = %d, unexpected", r, got.code)
		}
	}
}
