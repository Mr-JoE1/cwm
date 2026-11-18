//go:build e2e

// v0.6 e2e: the real binary as an always-on agent in a temp home — single
// instance, SSE heartbeat, persistent token across restarts, service dry run.
package test

import (
	"bufio"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/agent"
)

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// startAgentProc runs `cwm agent` as a real process until stopped.
func startAgentProc(t *testing.T, e *env, port int) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(cwmBin, "agent", "--no-open", "--port", strconv.Itoa(port))
	cmd.Env = e.childEnv("USERPROFILE=" + e.home)
	log, err := os.Create(filepath.Join(e.home, "agent-"+strconv.Itoa(port)+".log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := agent.Load(e.root)
		if err == nil && c.Port == port && c.PID == cmd.Process.Pid {
			req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/healthz", nil)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
				return cmd
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	data, _ := os.ReadFile(log.Name())
	t.Fatalf("agent did not come up:\n%s", data)
	return nil
}

func stopAgentProc(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if runtime.GOOS == "windows" {
		_ = cmd.Process.Kill()
	} else {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("agent did not stop on SIGTERM")
	}
}

func bearerGet(t *testing.T, port int, tok, path string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestAgentLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process signals; Windows agent covered by unit tests (D22)")
	}
	e := newEnv(t)
	port := freeTCPPort(t)
	cmd := startAgentProc(t, e, port)
	c, err := agent.Load(e.root)
	if err != nil || !agent.ValidToken(c.Token) {
		t.Fatalf("server.toml: %+v %v", c, err)
	}

	// Second launch exits 0 and reuses the running agent.
	r := e.runWith(e.home, []string{"USERPROFILE=" + e.home}, "agent", "--no-open")
	if r.code != 0 || !strings.Contains(r.stdout, "already running (pid "+strconv.Itoa(cmd.Process.Pid)+")") {
		t.Fatalf("second launch: %d %q %q", r.code, r.stdout, r.errs)
	}
	r = e.run(e.home, "dashboard", "--no-open")
	if r.code != 0 || !strings.Contains(r.stdout, "sign-in link: http://127.0.0.1:"+strconv.Itoa(port)+"/?t="+c.Token) {
		t.Errorf("dashboard: %d %q %q", r.code, r.stdout, r.errs)
	}

	// /api/events streams hello + heartbeat.
	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(resp.Body)
	var seen []string
	for sc.Scan() && len(seen) < 2 {
		if strings.HasPrefix(sc.Text(), "event: ") {
			seen = append(seen, strings.TrimPrefix(sc.Text(), "event: "))
		}
	}
	resp.Body.Close()
	if strings.Join(seen, ",") != "hello,heartbeat" {
		t.Errorf("events = %v", seen)
	}

	// Persistent token survives a restart.
	stopAgentProc(t, cmd)
	port2 := freeTCPPort(t)
	startAgentProc(t, e, port2)
	if code := bearerGet(t, port2, c.Token, "/api/version"); code != 200 {
		t.Errorf("token after restart: %d", code)
	}
	// Rotation: the running agent drops the old token.
	r = e.run(e.home, "service", "rotate-token")
	if r.code != 0 {
		t.Fatalf("rotate: %q", r.errs)
	}
	deadline := time.Now().Add(6 * time.Second)
	for bearerGet(t, port2, c.Token, "/api/version") != 401 {
		if time.Now().After(deadline) {
			t.Fatal("old token still accepted after rotate-token")
		}
		time.Sleep(200 * time.Millisecond)
	}
	r = e.run(e.home, "service", "stop")
	if r.code != 0 || !strings.Contains(r.stdout, "stopped") {
		t.Errorf("service stop: %d %q %q", r.code, r.stdout, r.errs)
	}
}

func TestServiceInstallDryRunE2E(t *testing.T) {
	e := newEnv(t)
	r := e.runWith(e.home, []string{"CWM_SERVICE_DRYRUN=1", "XDG_CONFIG_HOME=", "USERPROFILE=" + e.home}, "service", "install")
	if r.code != 0 || !strings.Contains(r.stdout, "dry run") || !regexp.MustCompile(`would (write|run)`).MatchString(r.stdout) {
		t.Fatalf("dry run: %d %q %q", r.code, r.stdout, r.errs)
	}
	if _, err := os.Stat(filepath.Join(e.home, ".config")); err == nil {
		t.Error("dry run created ~/.config")
	}
	r = e.run(e.home, "--version")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "cwm ") {
		t.Errorf("--version: %q", r.stdout)
	}
}
