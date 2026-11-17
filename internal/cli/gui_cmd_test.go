package cli

import (
	"context"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// stubGui makes `cwm gui` return right after binding: the shutdown context
// is already cancelled and the browser opener only records its URL.
func stubGui(t *testing.T) *[]string {
	t.Helper()
	opened := &[]string{}
	origCtx, origOpen := guiSignalContext, guiOpenBrowser
	guiSignalContext = func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, cancel
	}
	guiOpenBrowser = func(url string) error {
		*opened = append(*opened, url)
		return nil
	}
	t.Cleanup(func() { guiSignalContext, guiOpenBrowser = origCtx, origOpen })
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

	// Two launches never share a token.
	got2 := runCLI(t, "gui", "--no-open", "--port", strconv.Itoa(freePort(t)))
	t1 := regexp.MustCompile(`t=([0-9a-f]{64})`).FindStringSubmatch(got.out)
	t2 := regexp.MustCompile(`t=([0-9a-f]{64})`).FindStringSubmatch(got2.out)
	if t1 == nil || t2 == nil || t1[1] == t2[1] {
		t.Errorf("tokens must differ per launch: %v vs %v", t1, t2)
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
