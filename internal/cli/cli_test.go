package cli

import (
	"bytes"
	"os"
	"path/filepath"
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
		{"open", "usage: cwm open <repo> [new|last]", false},
		{"init", "usage: cwm init [--migrate] [--account <name>]", false},
		{"doctor", "usage: cwm doctor [--net]", false},
		{"list", "usage: cwm list", false},
		{"wip", "usage: cwm wip [status]", false},
		{"hook", "usage: cwm hook session-start|session-end", false},
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

func TestRepoLaunchDirectForm(t *testing.T) {
	// Stage B2: the direct launch form dispatches to the launcher. In a
	// sandbox without a workspace every case fails with a helpful error.
	tests := []struct {
		name string
		arg  string
		want string
	}{
		{"plain repo name", "alpha", "cwm init"},
		{"reserved statusline", "statusline", "reserved"},
		{"reserved gui", "gui", "reserved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CWM_HOME", t.TempDir())
			got := runCLI(t, tt.arg)
			if got.code != ExitError {
				t.Fatalf("code = %d, want %d", got.code, ExitError)
			}
			if !strings.Contains(got.err, tt.want) {
				t.Errorf("stderr = %q, want it to contain %q", got.err, tt.want)
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
	reserved := []string{"init", "import", "open", "list", "wip", "doctor", "account", "mcp", "hook", "version", "help", "statusline", "gui", "pair", "devices", "sessions"}
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
	// list/wip/doctor/hook now execute for real: sandbox the home and cwd so
	// the sweep never touches the real ~/.cwm or the cwm repo itself.
	t.Setenv("CWM_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	for _, r := range ReservedNames {
		got := runCLI(t, r)
		switch {
		case got.code == ExitOK:
			if got.out == "" && got.err == "" {
				t.Errorf("cwm %s: no output at all", r)
			}
		case got.code == ExitNotImp:
			if !strings.Contains(got.err, "not implemented") {
				t.Errorf("cwm %s: stderr = %q, want a not-implemented note", r, got.err)
			}
		case got.code == ExitError:
			if got.out == "" && got.err == "" {
				t.Errorf("cwm %s: ran and failed with no output", r)
			}
		default:
			t.Errorf("cwm %s: code = %d, unexpected", r, got.code)
		}
	}
}

func TestListProjects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CWM_HOME", home)
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("workspace/projects/alpha/a.jsonl", "{}")
	write("workspace/projects/alpha/b.jsonl", "{}")
	write("workspace/projects/empty-dir/keep.txt", "not a session")

	got := runCLI(t, "list")
	if got.code != ExitOK {
		t.Fatalf("code = %d, want %d (stderr: %s)", got.code, ExitOK, got.err)
	}
	for _, want := range []string{"workspace projects (", "alpha", "2 session(s)", "last:", "empty-dir", "0 session(s)", "never"} {
		if !strings.Contains(got.out, want) {
			t.Errorf("list output missing %q:\n%s", want, got.out)
		}
	}
}

func TestListEmptyAndMissingWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CWM_HOME", home)

	// Workspace exists, no projects dir yet.
	if err := os.MkdirAll(filepath.Join(home, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := runCLI(t, "list")
	if got.code != ExitOK || !strings.Contains(got.out, "none yet") {
		t.Fatalf("empty list: code = %d out = %q err = %q", got.code, got.out, got.err)
	}

	// No workspace at all: fail with an init pointer, like the launcher.
	t.Setenv("CWM_HOME", filepath.Join(home, "absent"))
	got = runCLI(t, "list")
	if got.code != ExitError || !strings.Contains(got.err, "cwm init") {
		t.Fatalf("missing workspace: code = %d err = %q, want init hint", got.code, got.err)
	}
}

func TestWipCommands(t *testing.T) {
	t.Setenv("CWM_HOME", t.TempDir())
	t.Chdir(t.TempDir()) // not a git repo: session-end skips, status errors

	got := runCLI(t, "wip")
	if got.code != ExitOK {
		t.Fatalf("cwm wip outside a repo: code = %d, want %d (the hook never fails)", got.code, ExitOK)
	}
	if !strings.Contains(got.out, "skip:") {
		t.Errorf("cwm wip output = %q, want a skip summary", got.out)
	}

	got = runCLI(t, "wip", "status")
	if got.code != ExitError || !strings.Contains(got.err, "cannot list sync branches") {
		t.Fatalf("cwm wip status outside a repo: code = %d err = %q", got.code, got.err)
	}

	got = runCLI(t, "wip", "bogus")
	if got.code != ExitError || !strings.Contains(got.err, "usage: cwm wip [status]") {
		t.Fatalf("cwm wip bogus: code = %d err = %q", got.code, got.err)
	}
}

func TestHookCommand(t *testing.T) {
	t.Setenv("CWM_HOME", t.TempDir())
	t.Chdir(t.TempDir()) // not a git repo: hooks must still exit 0 silently

	for _, args := range [][]string{{"hook"}, {"hook", "bogus"}, {"hook", "session-start", "extra"}} {
		got := runCLI(t, args...)
		if got.code != ExitError {
			t.Fatalf("cwm %v: code = %d, want %d", args, got.code, ExitError)
		}
	}

	got := runCLI(t, "hook", "session-start")
	if got.code != ExitOK || got.out != "" || got.err != "" {
		t.Fatalf("hook session-start outside a repo: code = %d out = %q err = %q, want silent 0", got.code, got.out, got.err)
	}
	got = runCLI(t, "hook", "session-end")
	if got.code != ExitOK || got.out != "" || got.err != "" {
		t.Fatalf("hook session-end outside a repo: code = %d out = %q err = %q, want silent 0", got.code, got.out, got.err)
	}
}

func TestDoctorCommand(t *testing.T) {
	t.Setenv("CWM_HOME", t.TempDir()) // no workspace: doctor must FAIL

	got := runCLI(t, "doctor")
	if got.code != ExitError {
		t.Fatalf("cwm doctor without workspace: code = %d, want %d", got.code, ExitError)
	}
	for _, want := range []string{"FAIL:", "workspace dir missing", "doctor: "} {
		if !strings.Contains(got.out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got.out)
		}
	}
	if got.err != "" {
		t.Errorf("doctor prints its own report; stderr must stay empty, got %q", got.err)
	}

	got = runCLI(t, "doctor", "--bogus")
	if got.code != ExitError || !strings.Contains(got.err, "usage: cwm doctor [--net]") {
		t.Fatalf("cwm doctor --bogus: code = %d err = %q", got.code, got.err)
	}
}
