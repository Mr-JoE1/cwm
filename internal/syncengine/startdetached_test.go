//go:build !windows

package syncengine

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStartDetachedMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty PATH: no syncthing anywhere
	err := StartDetached()
	if err == nil {
		t.Fatal("StartDetached must fail when syncthing is not on PATH")
	}
	want := "syncthing not on PATH — install it (apt install syncthing / brew install syncthing / winget install Syncthing.Syncthing)"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
}

func TestStartDetachedSpawnsStubWithoutWaiting(t *testing.T) {
	dir := t.TempDir()
	// The stub records its own PID, then keeps running for 30s — proving
	// StartDetached returned without waiting for it.
	marker := filepath.Join(dir, "spawned")
	stub := filepath.Join(dir, "syncthing")
	script := "#!/bin/sh\nprintf '%s\\n' $$ > " + marker + "\nexec sleep 30\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CWM_HOME", filepath.Join(dir, "home")) // log stays in the sandbox

	start := time.Now()
	if err := StartDetached(); err != nil {
		t.Fatalf("StartDetached: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("StartDetached waited %v — it must spawn without waiting", elapsed)
	}

	pidStr := waitForFile(t, marker, 5*time.Second)
	pid, err := strconv.Atoi(strings.TrimSpace(pidStr))
	if err != nil || pid <= 1 {
		t.Fatalf("stub pid = %q (%v), want a real pid", pidStr, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "home", "logs", "syncthing.log")); err != nil {
		t.Errorf("stdio log not created under $CWM_HOME/logs: %v", err)
	}

	// Clean up the detached child.
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Logf("kill stub pid %d: %v", pid, err)
	}
}

// waitForFile polls for path to appear and returns its content.
func waitForFile(t *testing.T, path string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if b, err := os.ReadFile(path); err == nil {
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
