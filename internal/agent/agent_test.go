package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConfigRoundTripAndPerms(t *testing.T) {
	root := t.TempDir()
	c, err := Load(root)
	if err != nil || c.Token != "" {
		t.Fatalf("missing file: %+v %v", c, err)
	}
	c, err = EnsureToken(root)
	if err != nil || !ValidToken(c.Token) {
		t.Fatalf("EnsureToken = %+v, %v", c, err)
	}
	c.Port, c.Bind, c.PID = 47801, "127.0.0.1", 42
	c.Started = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	if err := Save(root, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(root)
	if err != nil || got != c {
		t.Fatalf("round trip: got %+v, want %+v (%v)", got, c, err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(ConfigPath(root))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("server.toml mode = %v, want 0600 (%v)", fi.Mode().Perm(), err)
		}
	}
	data, _ := os.ReadFile(ConfigPath(root))
	if !strings.Contains(string(data), "rotate it with `cwm service rotate-token`") {
		t.Errorf("server.toml lacks its header:\n%s", data)
	}
	// EnsureToken keeps an existing valid token.
	again, err := EnsureToken(root)
	if err != nil || again.Token != c.Token || again.Port != 47801 {
		t.Errorf("EnsureToken changed a valid config: %+v", again)
	}
}

func TestEnsureTokenRepairsAndRotateKeepsPort(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(ConfigPath(root), []byte("token = 'short'\nport = 47803\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := EnsureToken(root)
	if err != nil || !ValidToken(c.Token) || c.Port != 47803 {
		t.Fatalf("repair short token: %+v %v", c, err)
	}
	r, err := RotateToken(root)
	if err != nil || r.Token == c.Token || !ValidToken(r.Token) || r.Port != 47803 {
		t.Fatalf("rotate: %+v %v", r, err)
	}
	if err := os.WriteFile(ConfigPath(root), []byte("not = [toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Error("Load accepted malformed TOML")
	}
	if c, err := EnsureToken(root); err != nil || !ValidToken(c.Token) {
		t.Errorf("EnsureToken on malformed file: %+v %v", c, err)
	}
}

func TestValidToken(t *testing.T) {
	for tok, want := range map[string]bool{
		strings.Repeat("a", 64): true, strings.Repeat("A", 64): false,
		strings.Repeat("a", 63): false, strings.Repeat("g", 64): false, "": false,
	} {
		if ValidToken(tok) != want {
			t.Errorf("ValidToken(%q) = %v", tok, !want)
		}
	}
}

func TestLockIsExclusive(t *testing.T) {
	root := t.TempDir()
	l1, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(root); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Acquire: err = %v, want ErrLocked", err)
	}
	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l1.Release(); err != nil {
		t.Errorf("Release is not idempotent: %v", err)
	}
	l2, err := Acquire(root)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	l2.Release()
}

func TestCandidates(t *testing.T) {
	join := func(p []int) string {
		s := make([]string, len(p))
		for i, v := range p {
			s[i] = strconv.Itoa(v)
		}
		return strings.Join(s, ",")
	}
	if got := join(Candidates(9000, 47805)); got != "9000" {
		t.Errorf("explicit = %s", got)
	}
	if got := join(Candidates(0, 0)); got != "47800,47801,47802,47803,47804,47805,47806,47807,47808,47809" {
		t.Errorf("default = %s", got)
	}
	if got := join(Candidates(0, 47805)); got != "47805,47800,47801,47802,47803,47804,47806,47807,47808,47809" {
		t.Errorf("saved first = %s", got)
	}
	if got := join(Candidates(0, 51234)); !strings.HasPrefix(got, "51234,47800,") {
		t.Errorf("saved outside range = %s", got)
	}
}

func TestListenExplicitBusyFailsAndFallbackSkips(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	bp := busy.Addr().(*net.TCPAddr).Port
	if _, err := Listen("127.0.0.1", []int{bp}); err == nil || !strings.Contains(err.Error(), strconv.Itoa(bp)) {
		t.Errorf("explicit busy port: err = %v", err)
	}
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	fp := free.Addr().(*net.TCPAddr).Port
	free.Close()
	ln, err := Listen("127.0.0.1", []int{bp, fp})
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	defer ln.Close()
	if got := ln.Addr().(*net.TCPAddr).Port; got != fp {
		t.Errorf("fallback bound %d, want %d", got, fp)
	}
	if _, err := Listen("127.0.0.1", []int{0}); err == nil {
		t.Error("port 0 accepted")
	}
}

func TestBindHelpers(t *testing.T) {
	for b, want := range map[string]bool{"127.0.0.1": true, "::1": true, "localhost": true, "0.0.0.0": false, "192.168.1.5": false} {
		if IsLoopback(b) != want {
			t.Errorf("IsLoopback(%q) = %v", b, !want)
		}
	}
	for _, b := range []string{"example.com", "", "1.2.3", "0.0.0.0:80"} {
		if ValidBind(b) == nil {
			t.Errorf("ValidBind(%q) accepted", b)
		}
	}
	for b, want := range map[string]string{"": "127.0.0.1", "0.0.0.0": "127.0.0.1", "::": "127.0.0.1", "127.0.0.1": "127.0.0.1", "10.0.0.2": "10.0.0.2"} {
		if got := ProbeHost(b); got != want {
			t.Errorf("ProbeHost(%q) = %q, want %q", b, got, want)
		}
	}
	if got := URL("::1", 47800, "tok"); got != "http://[::1]:47800/?t=tok" {
		t.Errorf("URL v6 = %s", got)
	}
}

// fakeAgent serves /api/agent for token tok on a loopback port.
func fakeAgent(t *testing.T, tok string) (*httptest.Server, int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent" || r.Header.Get("Authorization") != "Bearer "+tok {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(Info{Agent: true, PID: 4242, Version: "test"})
	}))
	t.Cleanup(srv.Close)
	return srv, srv.Listener.Addr().(*net.TCPAddr).Port
}

func TestProbeAndWait(t *testing.T) {
	tok := strings.Repeat("b", 64)
	_, port := fakeAgent(t, tok)
	info, err := Probe(context.Background(), Config{Token: tok, Port: port})
	if err != nil || info.PID != 4242 {
		t.Fatalf("Probe = %+v, %v", info, err)
	}
	if _, err := Probe(context.Background(), Config{Token: strings.Repeat("c", 64), Port: port}); !errors.Is(err, ErrNotRunning) {
		t.Errorf("wrong token: err = %v", err)
	}
	if _, err := Probe(context.Background(), Config{Token: tok}); !errors.Is(err, ErrNotRunning) {
		t.Errorf("no port: err = %v", err)
	}

	root := t.TempDir()
	if _, _, err := Wait(context.Background(), root, 0); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Wait without server.toml: %v", err)
	}
	// The config appears while Wait is polling.
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = Save(root, Config{Token: tok, Port: port})
	}()
	c, info, err := Wait(context.Background(), root, 3*time.Second)
	if err != nil || c.Port != port || info.PID != 4242 {
		t.Errorf("Wait = %+v %+v %v", c, info, err)
	}
}

func TestWatchReportsTokenChanges(t *testing.T) {
	root := t.TempDir()
	c, err := EnsureToken(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var seen []string
	done := make(chan struct{})
	go func() {
		Watch(ctx, root, 20*time.Millisecond, func(n Config) {
			mu.Lock()
			seen = append(seen, n.Token)
			mu.Unlock()
		})
		close(done)
	}()
	time.Sleep(60 * time.Millisecond)
	r, err := RotateToken(root)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(seen)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 || seen[len(seen)-1] != r.Token || r.Token == c.Token {
		t.Errorf("Watch saw %v, want the rotated token", seen)
	}
}

func TestStartDetachedMissingBinary(t *testing.T) {
	root := t.TempDir()
	if err := StartDetached(root, "/definitely/not/here/cwm", []string{"agent"}); err == nil {
		t.Error("StartDetached of a missing binary succeeded")
	}
	if _, err := os.Stat(LogPath(root)); err != nil {
		t.Errorf("log not created: %v", err)
	}
}
