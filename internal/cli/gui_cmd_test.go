package cli

import (
	"bytes"
	"context"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/gui/term"
	"github.com/Mr-JoE1/cwm/internal/launcher"
)

// stubGui makes `cwm gui` return right after binding: the shutdown context
// is already cancelled and the browser opener only records its URL.
func stubGui(t *testing.T) *[]string {
	t.Helper()
	opened := &[]string{}
	origCtx, origOpen := guiSignalContext, guiOpenBrowser
	origSpawn, origReuse, origStart := agentSpawn, agentReuseWait, agentStartWait
	// Never start a real background agent; never wait long for one.
	agentSpawn = func(string) error { return errors.New("spawning disabled in tests") }
	agentReuseWait, agentStartWait = 200*time.Millisecond, 200*time.Millisecond
	guiSignalContext = func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, cancel
	}
	guiOpenBrowser = func(url string) error {
		*opened = append(*opened, url)
		return nil
	}
	t.Cleanup(func() {
		guiSignalContext, guiOpenBrowser = origCtx, origOpen
		agentSpawn, agentReuseWait, agentStartWait = origSpawn, origReuse, origStart
	})
	return opened
}

// freePort asks the kernel for a free loopback port.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func TestGuiCommand(t *testing.T) {
	t.Setenv("CWM_HOME", t.TempDir())
	opened := stubGui(t)
	port := freePort(t)

	got := runCLI(t, "gui", "--port", strconv.Itoa(port))
	if got.code != ExitOK {
		t.Fatalf("cwm gui: code = %d out = %q err = %q", got.code, got.out, got.err)
	}
	urlRe := regexp.MustCompile(`http://127\.0\.0\.1:` + strconv.Itoa(port) + `/\?t=[0-9a-f]{64}`)
	url := urlRe.FindString(got.out)
	if url == "" {
		t.Fatalf("output lacks the tokenized URL:\n%s", got.out)
	}
	if len(*opened) != 1 || (*opened)[0] != url {
		t.Errorf("browser opened %v, want [%s]", *opened, url)
	}
	if !strings.Contains(got.out, "cwm gui: stopped") {
		t.Errorf("graceful shutdown not reported:\n%s", got.out)
	}

	*opened = nil
	got = runCLI(t, "gui", "--no-open", "--port="+strconv.Itoa(freePort(t)))
	if got.code != ExitOK || len(*opened) != 0 {
		t.Fatalf("--no-open: code = %d opened = %v err = %q", got.code, *opened, got.err)
	}

	// v0.6 (D31): the token is persistent — two launches share it, so a
	// bookmark survives restarts — until `cwm service rotate-token`.
	tokRe := regexp.MustCompile(`t=([0-9a-f]{64})`)
	got2 := runCLI(t, "gui", "--no-open", "--port", strconv.Itoa(freePort(t)))
	t1, t2 := tokRe.FindStringSubmatch(got.out), tokRe.FindStringSubmatch(got2.out)
	if t1 == nil || t2 == nil || t1[1] != t2[1] {
		t.Errorf("token must persist across launches: %v vs %v", t1, t2)
	}
	if r := runCLI(t, "service", "rotate-token"); r.code != ExitOK {
		t.Fatalf("rotate-token: %d %q", r.code, r.err)
	}
	got3 := runCLI(t, "gui", "--no-open", "--port", strconv.Itoa(freePort(t)))
	if t3 := tokRe.FindStringSubmatch(got3.out); t3 == nil || t3[1] == t1[1] {
		t.Errorf("rotate-token must change the token: %v vs %v", t1, t3)
	}
}

func TestGuiCommandErrors(t *testing.T) {
	t.Setenv("CWM_HOME", t.TempDir())
	stubGui(t)
	for _, args := range [][]string{
		{"gui", "--port"},
		{"gui", "--port", "0"},
		{"gui", "--port", "99999"},
		{"gui", "--port=abc"},
		{"gui", "--bind", "0.0.0.0"},
		{"gui", "extra"},
	} {
		got := runCLI(t, args...)
		if got.code != ExitError || got.err == "" {
			t.Errorf("cwm %v: code = %d err = %q, want an error", args, got.code, got.err)
		}
	}

	// An explicitly requested busy port fails loudly (no silent fallback).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	got := runCLI(t, "gui", "--no-open", "--port", busy)
	if got.code != ExitError || !strings.Contains(got.err, "127.0.0.1:"+busy) {
		t.Errorf("busy port: code = %d err = %q", got.code, got.err)
	}
}

// The GUI's resume command must be exactly the D20 passthrough that
// `cwm sessions --pick` uses: `cwm open <p> new --resume <id>` parses back
// to the same launcher mode and claude args as launcher.ResumeArgs.
func TestGuiResumeArgvIsTheD20Passthrough(t *testing.T) {
	argv := launcher.ResumeCLIArgs("alpha", "1234-abcd")
	if strings.Join(argv, " ") != "open alpha new --resume 1234-abcd" {
		t.Fatalf("ResumeCLIArgs = %q", argv)
	}
	if lookupCommand(argv[0]) == nil || argv[1] != "alpha" {
		t.Fatalf("argv does not dispatch to cmdOpen: %q", argv)
	}
	mode, extra := parseMode(argv[2:])
	wantMode, wantExtra := launcher.ResumeArgs("1234-abcd")
	if mode != wantMode || strings.Join(extra, "|") != strings.Join(wantExtra, "|") {
		t.Errorf("parsed (%s, %q), want (%s, %q)", mode, extra, wantMode, wantExtra)
	}
	if mode != launcher.ModeNew || strings.Join(extra, "|") != "--resume|1234-abcd" {
		t.Errorf("D20 passthrough changed: (%s, %q)", mode, extra)
	}
}

func TestHoldOnError(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == term.HoldEnv {
				return v
			}
			return ""
		}
	}
	tty := func() bool { return true }
	noTTY := func() bool { return false }
	cases := []struct {
		name   string
		code   int
		getenv func(string) string
		isTTY  func() bool
		held   bool
	}{
		{"failure in a GUI terminal", ExitError, env("1"), tty, true},
		{"success never holds", ExitOK, env("1"), tty, false},
		{"not opened by the GUI", ExitError, env(""), tty, false},
		{"not interactive", ExitError, env("1"), noTTY, false},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		HoldOnError(tc.code, tc.getenv, tc.isTTY, strings.NewReader("\n"), &out)
		if got := strings.Contains(out.String(), "press Enter"); got != tc.held {
			t.Errorf("%s: held = %v, want %v (%q)", tc.name, got, tc.held, out.String())
		}
	}
}

func TestGuiWiresLauncherSeam(t *testing.T) {
	if guiLauncher == nil {
		t.Fatal("cwm gui has no terminal launcher")
	}
	if _, ok := guiLauncher.(*term.System); !ok {
		t.Errorf("default launcher = %T, want *term.System", guiLauncher)
	}
	if guiCwmPath() == "" {
		t.Error("empty cwm path for terminal commands")
	}
}
