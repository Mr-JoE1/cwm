package cli

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/service"
)

// isolateHome points every home-ish variable at fresh temp dirs.
func isolateHome(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CWM_HOME", root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv(EnvServiceDryRun, "")
	return root
}

// startAgent runs `cwm agent args...` in the background until the test
// ends; it returns once the agent answers its probe.
func startAgent(t *testing.T, root string, args ...string) agent.Config {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	orig := guiSignalContext
	guiSignalContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	done := make(chan result, 1)
	go func() { done <- runCLI(t, append([]string{"agent"}, args...)...) }()
	c, _, err := agent.Wait(context.Background(), root, 5*time.Second)
	guiSignalContext = orig
	if err != nil {
		cancel()
		t.Fatalf("agent did not come up: %v (%+v)", err, <-done)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case r := <-done:
			if r.code != ExitOK || !strings.Contains(r.out, "cwm agent: stopped") {
				t.Errorf("agent exit: %d out=%q err=%q", r.code, r.out, r.err)
			}
		case <-time.After(5 * time.Second):
			t.Error("agent did not stop")
		}
	})
	return c
}

func TestAgentSingleInstanceAndPersistence(t *testing.T) {
	root := isolateHome(t)
	opened := stubGui(t)
	port := freePort(t)
	c := startAgent(t, root, "--port", strconv.Itoa(port), "--no-open")
	if c.Port != port || !agent.ValidToken(c.Token) || c.Bind != "127.0.0.1" || c.PID != os.Getpid() {
		t.Fatalf("server.toml = %+v", c)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(agent.ConfigPath(root)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("server.toml perms = %v (%v)", fi.Mode().Perm(), err)
		}
	}
	if len(*opened) != 0 {
		t.Errorf("--no-open opened %v", *opened)
	}

	// Second launch: exits 0, reuses, opens the browser at the running agent.
	got := runCLI(t, "agent")
	if got.code != ExitOK || !strings.Contains(got.out, "already running (pid ") || !strings.Contains(got.out, "127.0.0.1:"+strconv.Itoa(port)) {
		t.Fatalf("second launch: %d out=%q err=%q", got.code, got.out, got.err)
	}
	if strings.Contains(got.out, c.Token) {
		t.Error("agent output leaks the token")
	}
	if len(*opened) != 1 || (*opened)[0] != agent.URL("127.0.0.1", port, c.Token) {
		t.Errorf("reuse opened %v", *opened)
	}
	// `cwm gui` reuses it too and prints the sign-in URL.
	got = runCLI(t, "gui", "--no-open")
	if got.code != ExitOK || !strings.Contains(got.out, "cwm gui: already running") || !strings.Contains(got.out, "?t="+c.Token) {
		t.Errorf("gui reuse: %d %q %q", got.code, got.out, got.err)
	}

	// The persistent token authenticates the API.
	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/version", nil)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("persistent token rejected: %d", resp.StatusCode)
	}

	// dashboard finds it without spawning anything.
	*opened = nil
	got = runCLI(t, "dashboard", "--no-open")
	if got.code != ExitOK || !strings.Contains(got.out, "sign-in link: "+agent.URL("127.0.0.1", port, c.Token)) || len(*opened) != 0 {
		t.Errorf("dashboard --no-open: %d %q %q opened=%v", got.code, got.out, got.err, *opened)
	}
	got = runCLI(t, "dashboard")
	if got.code != ExitOK || len(*opened) != 1 {
		t.Errorf("dashboard: %d %q opened=%v", got.code, got.err, *opened)
	}
	got = runCLI(t, "service", "status")
	if got.code != ExitOK || !regexp.MustCompile(`agent\s+running\s+pid \d+`).MatchString(got.out) {
		t.Errorf("service status: %d %q %q", got.code, got.out, got.err)
	}
}

func TestAgentRotateTokenHotReload(t *testing.T) {
	root := isolateHome(t)
	stubGui(t)
	orig := agentWatchEvery
	agentWatchEvery = 20 * time.Millisecond
	t.Cleanup(func() { agentWatchEvery = orig })
	c := startAgent(t, root, "--port", strconv.Itoa(freePort(t)), "--no-open")
	got := runCLI(t, "service", "rotate-token")
	if got.code != ExitOK || !strings.Contains(got.out, "new sign-in link") {
		t.Fatalf("rotate: %d %q %q", got.code, got.out, got.err)
	}
	nc, _ := agent.Load(root)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := agent.Probe(context.Background(), nc); err == nil {
			if _, err := agent.Probe(context.Background(), c); err != nil {
				return // new token works, old one is refused
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Error("running agent did not switch to the rotated token")
}

func TestDashboardStartsAgentWhenNoneRuns(t *testing.T) {
	root := isolateHome(t)
	opened := stubGui(t)
	spawned := 0
	agentSpawn = func(r string) error {
		spawned++
		if r != root {
			t.Errorf("spawn root = %q", r)
		}
		// Stand in for the detached process: a real agent in-process.
		startAgent(t, root, "--port", strconv.Itoa(freePort(t)), "--no-open")
		return nil
	}
	agentStartWait = 5 * time.Second
	got := runCLI(t, "dashboard")
	if got.code != ExitOK || spawned != 1 || len(*opened) != 1 || !strings.Contains(got.out, "starting one in the background") {
		t.Fatalf("dashboard: %d spawned=%d opened=%v out=%q err=%q", got.code, spawned, *opened, got.out, got.err)
	}
}

func TestDashboardSpawnFailure(t *testing.T) {
	isolateHome(t)
	stubGui(t)
	got := runCLI(t, "dashboard", "--no-open")
	if got.code != ExitError || !strings.Contains(got.err, "spawning disabled in tests") {
		t.Errorf("dashboard spawn failure: %d %q", got.code, got.err)
	}
	got = runCLI(t, "--dry-run", "dashboard")
	if got.code != ExitOK || !strings.Contains(got.out, "would start") {
		t.Errorf("dashboard dry run: %d %q %q", got.code, got.out, got.err)
	}
	for _, args := range [][]string{{"dashboard", "--wait", "x"}, {"dashboard", "--wait", "-1"}, {"dashboard", "bogus"}} {
		if got := runCLI(t, args...); got.code != ExitError {
			t.Errorf("%v: code %d", args, got.code)
		}
	}
}

func TestAgentArgs(t *testing.T) {
	o, err := parseAgentArgs([]string{"--bind", "0.0.0.0", "--port=47811", "--open", "--allow-host", "nas.lan", "--allow-host=proxy:443"})
	if err != nil || o.bind != "0.0.0.0" || o.port != 47811 || !o.open || strings.Join(o.allowHosts, ",") != "nas.lan,proxy:443" {
		t.Fatalf("parse = %+v, %v", o, err)
	}
	for _, bad := range [][]string{
		{"--bind", "example.com"}, {"--bind"}, {"--port", "0"}, {"--open", "--no-open"},
		{"--allow-host", "a/b"}, {"--allow-host="}, {"extra"},
	} {
		if _, err := parseAgentArgs(bad); err == nil {
			t.Errorf("parseAgentArgs(%q) accepted", bad)
		}
	}
}

func TestAgentNonLoopbackBindWarns(t *testing.T) {
	root := isolateHome(t)
	stubGui(t) // pre-cancelled: binds, prints, stops
	got := runCLI(t, "agent", "--bind", "0.0.0.0", "--port", strconv.Itoa(freePort(t)))
	if got.code != ExitOK || !strings.Contains(got.err, "WARNING: bound to 0.0.0.0") {
		t.Fatalf("0.0.0.0 bind: %d %q %q", got.code, got.out, got.err)
	}
	c, _ := agent.Load(root)
	if c.Bind != "0.0.0.0" {
		t.Errorf("bind not recorded: %+v", c)
	}
	// The recorded bind is never reused: a plain launch is loopback again.
	got = runCLI(t, "agent", "--port", strconv.Itoa(freePort(t)))
	if c, _ := agent.Load(root); got.code != ExitOK || c.Bind != "127.0.0.1" || strings.Contains(got.err, "WARNING") {
		t.Errorf("default bind after a 0.0.0.0 run: %+v %q", c, got.err)
	}
}

func TestVersionFlagsAndSingleDash(t *testing.T) {
	for _, f := range []string{"--version", "-v", "-version"} {
		got := runCLI(t, f)
		if got.code != ExitOK || !strings.HasPrefix(got.out, "cwm ") {
			t.Errorf("%s: %d %q", f, got.code, got.out)
		}
	}
	cases := map[string]string{
		"-port 1":                   "-port 1",
		"gui -port 47800 -no-open":  "gui --port 47800 --no-open",
		"-account work gui -port=5": "--account work gui --port=5",
		"-dry-run list -json":       "--dry-run list --json",
		"open repo -x -continue":    "open repo -x -continue",
		"myrepo new -resume abc":    "myrepo new -resume abc",
		"mcp add f -- npx -y pkg":   "mcp add f -- npx -y pkg",
		"service install -no-open":  "service install --no-open",
		"sessions -h -1 -9z":        "sessions -h -1 -9z",
		"doctor -- -net":            "doctor -- -net",
		"-h":                        "-h",
	}
	for in, want := range cases {
		got := strings.Join(normalizeFlags(strings.Fields(in)), " ")
		if in == "-port 1" {
			want = "--port 1" // a global position: normalized, then rejected as unknown
		}
		if got != want {
			t.Errorf("normalizeFlags(%q) = %q, want %q", in, got, want)
		}
	}
	isolateHome(t)
	stubGui(t)
	if got := runCLI(t, "gui", "-no-open", "-port", strconv.Itoa(freePort(t))); got.code != ExitOK {
		t.Errorf("single-dash gui flags: %d %q", got.code, got.err)
	}
}

// fakeServiceRunner records commands for the cwm service tests.
type fakeServiceRunner struct{ calls []string }

func (f *fakeServiceRunner) Run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
	return "", nil
}

func stubService(t *testing.T, goos string) *fakeServiceRunner {
	t.Helper()
	r := &fakeServiceRunner{}
	og, or, ol, os_, oe := serviceGOOS, serviceRunner, serviceLookPath, serviceStat, serviceExe
	serviceGOOS, serviceRunner = goos, r
	if goos == "windows" && runtime.GOOS != "windows" {
		serviceExe = func() string { return `C:\Users\t\AppData\Local\cwm\bin\cwm.exe` }
	}
	t.Cleanup(func() { serviceGOOS, serviceRunner, serviceLookPath, serviceStat, serviceExe = og, or, ol, os_, oe })
	return r
}

func TestServiceCommandDryRun(t *testing.T) {
	isolateHome(t)
	stubGui(t)
	r := stubService(t, "windows")
	t.Setenv(EnvServiceDryRun, "1")
	got := runCLI(t, "service", "install")
	if got.code != ExitOK || len(r.calls) != 0 || !strings.Contains(got.out, "would write") ||
		!strings.Contains(got.out, "would run: schtasks.exe /Create /TN \"cwm agent\"") || !strings.Contains(got.out, "dry run, windows") {
		t.Fatalf("dry-run install: %d calls=%v out=%q err=%q", got.code, r.calls, got.out, got.err)
	}
	for _, sub := range []string{"uninstall", "start", "stop", "rotate-token"} {
		if got := runCLI(t, "service", sub); got.code != ExitOK || len(r.calls) != 0 {
			t.Errorf("dry-run %s: %d calls=%v %q", sub, got.code, r.calls, got.err)
		}
	}
	t.Setenv(EnvServiceDryRun, "")
	got = runCLI(t, "--dry-run", "service", "install", "--open-only", "--url", "http://127.0.0.1:47800")
	if got.code != ExitOK || len(r.calls) != 0 || !strings.Contains(got.out, "service open-url http://127.0.0.1:47800") {
		t.Errorf("--dry-run open-only: %d %q %q", got.code, got.out, got.err)
	}
}

func TestServiceInstallLinuxNoSystemdStartsAgent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX paths in a Linux plan; the Windows plan is covered in internal/service (D22)")
	}
	root := isolateHome(t)
	stubGui(t)
	r := stubService(t, "linux")
	serviceLookPath = func(string) (string, error) { return "", os.ErrNotExist }
	spawned := 0
	agentSpawn = func(string) error {
		spawned++
		startAgent(t, root, "--port", strconv.Itoa(freePort(t)), "--no-open")
		return nil
	}
	agentStartWait = 5 * time.Second
	got := runCLI(t, "service", "install")
	if got.code != ExitOK || spawned != 1 || len(r.calls) != 0 || !strings.Contains(got.out, "agent running (pid") {
		t.Fatalf("install: %d spawned=%d calls=%v out=%q err=%q", got.code, spawned, r.calls, got.out, got.err)
	}
	home, _ := os.UserHomeDir()
	desk := filepath.Join(home, ".config", "autostart", service.DesktopName)
	data, err := os.ReadFile(desk)
	if err != nil || !strings.Contains(string(data), "dashboard --wait 5") || !strings.Contains(string(data), "env CWM_HOME="+root) {
		t.Errorf("desktop entry: %v\n%s", err, data)
	}
	got = runCLI(t, "service", "status")
	if got.code != ExitOK || !strings.Contains(got.out, "login opener") || !strings.Contains(got.out, "installed") {
		t.Errorf("status: %q", got.out)
	}
	got = runCLI(t, "service", "uninstall")
	if _, err := os.Stat(desk); got.code != ExitOK || err == nil {
		t.Errorf("uninstall: %d %q (desktop still there: %v)", got.code, got.err, err == nil)
	}
}

func TestServiceStopTerminatesStrayAgent(t *testing.T) {
	root := isolateHome(t)
	stubGui(t)
	stubService(t, "linux")
	serviceLookPath = func(string) (string, error) { return "", os.ErrNotExist }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	orig := guiSignalContext
	guiSignalContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	done := make(chan result, 1)
	go func() { done <- runCLI(t, "agent", "--port", strconv.Itoa(freePort(t))) }()
	if _, _, err := agent.Wait(context.Background(), root, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	guiSignalContext = orig
	var killed int
	origTerm := serviceTerminate
	serviceTerminate = func(pid int) error { killed = pid; cancel(); return nil } // "SIGTERM" = cancel the in-process agent
	t.Cleanup(func() { serviceTerminate = origTerm })
	got := runCLI(t, "service", "stop")
	if got.code != ExitOK || killed != os.Getpid() {
		t.Errorf("stop: %d killed=%d %q %q", got.code, killed, got.out, got.err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("agent did not stop")
	}
}

func TestServiceArgErrors(t *testing.T) {
	isolateHome(t)
	stubGui(t)
	stubService(t, "linux")
	for _, args := range [][]string{
		{"service"}, {"service", "bogus"}, {"service", "status", "x"},
		{"service", "install", "--open-only"}, {"service", "install", "--url", "http://x"},
		{"service", "install", "--open-only", "--no-open", "--url", "http://x"},
		{"service", "install", "--open-only", "--url", "file:///etc/passwd"},
		{"service", "open-url", "javascript:alert(1)"},
	} {
		if got := runCLI(t, args...); got.code != ExitError || got.err == "" {
			t.Errorf("%v: code %d err %q", args, got.code, got.err)
		}
	}
}

func TestServiceOpenURLWaitsThenOpens(t *testing.T) {
	isolateHome(t)
	opened := stubGui(t)
	serviceOpenURLPoll = 10 * time.Millisecond
	t.Cleanup(func() { serviceOpenURLPoll = 2 * time.Second })
	u := "http://127.0.0.1:" + strconv.Itoa(freePort(t)) // nothing listens
	got := runCLI(t, "service", "open-url", u, "--timeout", "0")
	if got.code != ExitOK || len(*opened) != 1 || (*opened)[0] != u || !strings.Contains(got.err, "opening anyway") {
		t.Errorf("open-url: %d %v %q", got.code, *opened, got.err)
	}
}
