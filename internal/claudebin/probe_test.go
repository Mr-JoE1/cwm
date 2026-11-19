package claudebin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// isolate points every home-ish variable and PATH at empty temp dirs, so
// nothing a test runs can reach this machine's real claude or profile.
func isolate(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		t.Setenv(k, t.TempDir())
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv(EnvClaude, "")
}

// The real `claude --version` output on the machine this was built on was
// "2.1.319 (Claude Code)"; the verifier keys on its shape, not its digits.
func TestParseVersionOutput(t *testing.T) {
	cases := []struct {
		out, version string
		ok           bool
	}{
		{"2.1.319 (Claude Code)\n", "2.1.319", true},
		{"2.1.400-beta.3 (Claude Code)\n", "2.1.400-beta.3", true},
		{"2.1.400+build.7 (Claude Code; native)\r\n", "2.1.400+build.7", true},
		{"Claude Code v3.0.0\n", "3.0.0", true},
		{"warning: something\n4.23118.1 (Claude Code)\n", "4.23118.1", true},
		{"2.1.319\n", "", false},                   // a bare version: any tool prints that
		{"claude: command not found\n", "", false}, // the marker without a version
		{"node v22.3.0\nnpm 10.0.0\n", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		v, _, ok := ParseVersionOutput(c.out)
		if v != c.version || ok != c.ok {
			t.Errorf("ParseVersionOutput(%q) = %q, %v; want %q, %v", c.out, v, ok, c.version, c.ok)
		}
	}
}

func TestProbeFakeBinaries(t *testing.T) {
	posixOnly(t)
	isolate(t)
	old := ProbeTimeout
	ProbeTimeout = 400 * time.Millisecond
	t.Cleanup(func() { ProbeTimeout = old })
	cases := []struct {
		name     string
		script   string
		verified bool
		version  string
		reason   string
	}{
		{"good version", "echo '2.1.319 (Claude Code)'", true, "2.1.319", ""},
		{"build suffix", "echo '2.1.500-rc.1+abc (Claude Code)'", true, "2.1.500-rc.1+abc", ""},
		{"version on stderr", "echo '2.1.319 (Claude Code)' >&2", true, "2.1.319", ""},
		{"bad exit code", "echo '2.1.319 (Claude Code)'; exit 1", false, "", "exit status 1"},
		{"hangs", "exec /bin/sleep 30", false, "", "timed out after 400ms"},
		{"no version", "echo 'Claude Desktop'", false, "", "no Claude Code version line"},
		{"bare version", "echo 4.23118.1", false, "", "no Claude Code version line"},
		{"no output", "exit 0", false, "", "no output from --version"},
		{"reads stdin", "read x; echo \"2.1.319 (Claude Code) $x\"", true, "2.1.319", ""},
	}
	dir := t.TempDir()
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(dir, "claude"+string(rune('a'+i)))
			if err := os.WriteFile(p, []byte("#!/bin/sh\n"+c.script+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			got := Probe(p)
			if took := time.Since(start); took > 3*time.Second {
				t.Errorf("probe took %s; the timeout must bound it", took)
			}
			if got.Verified != c.verified || got.Version != c.version || !strings.Contains(got.Reason, c.reason) {
				t.Errorf("Probe = %+v; want verified=%v version=%q reason~%q", got, c.verified, c.version, c.reason)
			}
		})
	}
}

func TestProbeCapsOutputAndCaches(t *testing.T) {
	posixOnly(t)
	isolate(t)
	dir := t.TempDir()
	count := filepath.Join(dir, "runs")
	p := filepath.Join(dir, "claude")
	script := "#!/bin/sh\necho x >> '" + count + "'\necho '2.1.319 (Claude Code)'\n/usr/bin/head -c 20000 /dev/zero | /usr/bin/tr '\\0' 'y'\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	first := Probe(p)
	if !first.Verified || len(first.Output) > probeMaxOutput {
		t.Fatalf("probe = verified %v, %d bytes; want verified, <= %d", first.Verified, len(first.Output), probeMaxOutput)
	}
	Probe(p)
	if data, _ := os.ReadFile(count); strings.Count(string(data), "x") != 1 {
		t.Errorf("an unchanged file must be probed once, ran %d times", strings.Count(string(data), "x"))
	}
	// A changed file is probed again.
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho x >> '"+count+"'\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Probe(p); got.Verified || got.Reason != "exit status 3" {
		t.Errorf("after the change: %+v, want exit status 3", got)
	}
}

// TestSavedPathGoesStale: a claude_path that used to be Claude Code and
// now is not (overwritten, replaced by another tool) is cleared from
// config.toml, a note is kept, and the search goes on to PATH.
func TestSavedPathGoesStale(t *testing.T) {
	posixOnly(t)
	isolate(t)
	w := newWorld(t)
	good := w.fileWith(t, "pathdir/claude", true, "#!/bin/sh\necho '2.1.400 (Claude Code)'\n")
	pinned := w.fileWith(t, "pinned/claude", true, "#!/bin/sh\necho '2.1.300 (Claude Code)'\n")
	w.vars["PATH"] = filepath.Dir(good)
	env := w.env("linux")
	env.Probe = Probe // the real probe on real fake binaries
	layout := storage.NewLayoutAt(filepath.Join(w.root, "cwmhome"))
	if err := os.MkdirAll(layout.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.ClaudePath = pinned
	if err := config.Save(layout.ConfigPath(), cfg); err != nil {
		t.Fatal(err)
	}
	res, err := find(layout, env, true, false)
	if err != nil || res.Path != pinned || res.Source != SourceConfig || res.Version != "2.1.300" {
		t.Fatalf("pinned and good: %+v, %v", res, err)
	}
	// The pinned file stops being Claude Code.
	if err := os.WriteFile(pinned, []byte("#!/bin/sh\necho 'some other tool 1.2.3'\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err = find(layout, env, false, false) // even the read-only Lookup drops it
	if err != nil || res.Path != good || res.Source != SourcePath {
		t.Fatalf("after going stale: %+v, %v; want %s from PATH", res, err, good)
	}
	if res.Cleared == nil || res.Cleared.Path != pinned || res.Cleared.Reason != "exit status 2" {
		t.Errorf("Cleared = %+v", res.Cleared)
	}
	if got, _ := config.Load(layout.ConfigPath()); got.ClaudePath != "" {
		t.Errorf("claude_path = %q, want cleared", got.ClaudePath)
	}
	note := ClearedNote(layout)
	if !strings.Contains(note, "cleared claude_path "+pinned+": exit status 2") {
		t.Errorf("note = %q", note)
	}
	if len(res.Candidates) != 2 || res.Candidates[0].Verified || !res.Candidates[1].Verified {
		t.Errorf("candidates = %+v; want the rejected pin, then the PATH hit", res.Candidates)
	}
	// Remember must not put the stale pin back into a config that the
	// caller loaded before find() ran.
	stale := config.Default()
	stale.ClaudePath = pinned
	Remember(stale, res)
	if stale.ClaudePath != "" {
		t.Errorf("Remember kept the cleared pin: %q", stale.ClaudePath)
	}
}

// The v0.6.1 tester's binary lives in a folder no list of "official"
// locations needs to bless: the probe alone decides.
func TestAnthropicClaudeFolderIsTrustedByBehaviourOnly(t *testing.T) {
	w := newWorld(t)
	local := filepath.Join(w.root, "Users", "u", "AppData", "Local")
	w.vars["LOCALAPPDATA"], w.vars["USERPROFILE"] = local, filepath.Join(w.root, "Users", "u")
	w.vars["PATH"] = filepath.Join(w.root, "Windows", "System32")
	exe := w.fileWith(t, "Users/u/AppData/Local/AnthropicClaude/claude.exe", false, "4.23118.1 (Claude Code)\n")
	res, err := Resolve(w.env("windows"), "")
	if err != nil || res.Path != exe || res.Version != "4.23118.1" || res.OnPath {
		t.Fatalf("got %+v, %v; want %s verified, off PATH", res, err, exe)
	}
	// Same place, different program: not taken, and reported.
	w.fileWith(t, "Users/u/AppData/Local/AnthropicClaude/claude.exe", false, "Claude desktop app\n")
	_, err = Resolve(w.env("windows"), "")
	var nf *NotFoundError
	if !errors.As(err, &nf) || len(nf.Rejected) != 1 || nf.Rejected[0].Path != exe {
		t.Fatalf("err = %v; want not found with %s rejected", err, exe)
	}
	if !strings.Contains(err.Error(), "1 candidate(s) found but not Claude Code") {
		t.Errorf("message = %s", err)
	}
}

func TestPathWalkSkipsUnverifiedAndSurvey(t *testing.T) {
	w := newWorld(t)
	bad := w.fileWith(t, "first/claude.cmd", false, "@echo off\necho hello\n")
	good := w.fileWith(t, "second/claude.exe", false, "2.1.400 (Claude Code)\n")
	other := w.fileWith(t, "third/claude.exe", false, "2.1.300 (Claude Code)\n")
	w.vars["PATH"] = strings.Join([]string{filepath.Dir(bad), filepath.Dir(good), filepath.Dir(other)}, ";")
	w.vars["PATHEXT"] = ".COM;.EXE;.BAT;.CMD;.VBS"
	env := w.env("windows")
	res, err := Resolve(env, "")
	if err != nil || res.Path != good || res.Source != SourcePath || !res.OnPath {
		t.Fatalf("got %+v, %v; want %s (the first verified PATH entry)", res, err, good)
	}
	if len(res.Candidates) != 2 {
		t.Errorf("Resolve stops at the winner; candidates = %+v", res.Candidates)
	}
	res, err = Survey(env, "")
	if err != nil || res.Path != good || len(res.Candidates) != 3 || !res.Candidates[2].Verified || res.Candidates[0].Verified {
		t.Fatalf("Survey = %+v, %v; want all three, the good one winning", res, err)
	}
}

func TestEnvClaudeMustVerify(t *testing.T) {
	w := newWorld(t)
	p := w.fileWith(t, "bin/claude.exe", false, "not it\n")
	w.vars[EnvClaude] = p
	_, err := Resolve(w.env("windows"), "")
	if err == nil || !strings.Contains(err.Error(), "is not Claude Code") || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v; want an explicit-setting error", err)
	}
	w.fileWith(t, "bin/claude.exe", false, "2.1.400 (Claude Code)\n")
	res, err := Resolve(w.env("windows"), "")
	if err != nil || res.Source != SourceEnv || res.Version != "2.1.400" {
		t.Fatalf("got %+v, %v", res, err)
	}
}
