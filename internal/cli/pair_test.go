package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/syncengine"
	"github.com/Mr-JoE1/cwm/internal/syncthing"
)

var (
	testMyID   = strings.Repeat("A2B3C4D7EFGHJKLM", 3) + "NPQR"
	testPeerID = strings.Repeat("P7Q2R3S4T7UVWXYZ", 3) + "AB2C"
	testOther  = strings.Repeat("K7L2M3N4P7QRSTUV", 3) + "WX2Y"
)

// stubClient is the in-memory Client seam fake for CLI-level tests.
type stubClient struct {
	failPings  int // first N pings fail
	status     syncthing.SystemStatus
	cfg        *syncthing.Config
	conns      *syncthing.Connections
	completion map[string]*syncthing.Completion
	puts       int
}

func newSyncStub() *stubClient {
	return &stubClient{
		status:     syncthing.SystemStatus{MyID: testMyID},
		cfg:        &syncthing.Config{Devices: []syncthing.Device{{DeviceID: testMyID, Name: "self"}}},
		conns:      &syncthing.Connections{},
		completion: map[string]*syncthing.Completion{},
	}
}

func (s *stubClient) Ping() error {
	if s.failPings > 0 {
		s.failPings--
		return errors.New("stub: syncthing down")
	}
	return nil
}

func (s *stubClient) Status() (*syncthing.SystemStatus, error) {
	st := s.status
	return &st, nil
}

func (s *stubClient) GetConfig() (*syncthing.Config, error) { return s.cfg, nil }

func (s *stubClient) PutConfig(cfg *syncthing.Config) error {
	s.puts++
	s.cfg = cfg
	return nil
}

func (s *stubClient) Connections() (*syncthing.Connections, error) { return s.conns, nil }

func (s *stubClient) Completion(device, _ string) (*syncthing.Completion, error) {
	if c, ok := s.completion[device]; ok {
		return c, nil
	}
	// Real syncthing reports 100% for devices with nothing pending.
	return &syncthing.Completion{Completion: 100}, nil
}

// useSyncStub swaps the CLI seams for the stub and restores them after the
// test.
func useSyncStub(t *testing.T, st *stubClient) *bool {
	t.Helper()
	spawned := new(bool)
	origDisc, origNew, origStart := stDiscover, stNewClient, stStartDetached
	stDiscover = func() (*syncthing.Endpoint, error) {
		return &syncthing.Endpoint{BaseURL: "http://stub.local", APIKey: "stub"}, nil
	}
	stNewClient = func(*syncthing.Endpoint) syncengine.Client { return st }
	stStartDetached = func() error { *spawned = true; return nil }
	t.Cleanup(func() {
		stDiscover, stNewClient, stStartDetached = origDisc, origNew, origStart
	})
	return spawned
}

// syncWorkspaceHome sandboxes $CWM_HOME with an existing workspace dir.
func syncWorkspaceHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CWM_HOME", home)
	return home
}

func TestPairStatusOutput(t *testing.T) {
	home := syncWorkspaceHome(t)
	st := newSyncStub()
	useSyncStub(t, st)

	got := runCLI(t, "pair")
	if got.code != ExitOK {
		t.Fatalf("code = %d, want %d (stderr: %s)", got.code, ExitOK, got.err)
	}
	for _, want := range []string{
		"device id: " + testMyID,
		syncengine.FolderID,
		"staggered versioning 30d",
		"on the OTHER device run: cwm pair " + testMyID + " <a-name-for-this-device>",
		"both devices must run it",
	} {
		if !strings.Contains(got.out, want) {
			t.Errorf("pair output missing %q:\n%s", want, got.out)
		}
	}
	if !strings.Contains(got.out, filepath.Join(home, "workspace")) {
		t.Errorf("pair output should name the workspace path:\n%s", got.out)
	}
	if st.puts != 1 {
		t.Errorf("PUTs = %d, want 1 (folder created)", st.puts)
	}
}

func TestPairWorkspaceAndConnectionGuards(t *testing.T) {
	// No workspace: same guard error as the launcher.
	t.Setenv("CWM_HOME", t.TempDir())
	st := newSyncStub()
	useSyncStub(t, st)
	got := runCLI(t, "pair")
	if got.code != ExitError || !strings.Contains(got.err, "cwm init") {
		t.Fatalf("missing workspace: code = %d err = %q, want init hint", got.code, got.err)
	}

	// Workspace but unreachable API: error pointing at --start.
	syncWorkspaceHome(t)
	st.failPings = 1000
	got = runCLI(t, "pair")
	if got.code != ExitError {
		t.Fatalf("unreachable syncthing: code = %d, want %d", got.code, ExitError)
	}
	if !strings.Contains(got.err, "not reachable") || !strings.Contains(got.err, "--start") {
		t.Errorf("stderr = %q, want unreachable hint mentioning --start", got.err)
	}

	// Discovery failure propagates.
	origDisc := stDiscover
	stDiscover = func() (*syncthing.Endpoint, error) { return nil, errors.New("boom") }
	t.Cleanup(func() { stDiscover = origDisc })
	got = runCLI(t, "pair")
	if got.code != ExitError || !strings.Contains(got.err, "boom") {
		t.Fatalf("discover failure: code = %d err = %q", got.code, got.err)
	}
}

func TestPairPeerFlow(t *testing.T) {
	syncWorkspaceHome(t)
	st := newSyncStub()
	useSyncStub(t, st)

	got := runCLI(t, "pair", testPeerID, "laptop")
	if got.code != ExitOK {
		t.Fatalf("pair <id> <name>: code = %d (stderr: %s)", got.code, got.err)
	}
	for _, want := range []string{"added device " + testPeerID[:7], "laptop", "shared folder " + syncengine.FolderID} {
		if !strings.Contains(got.out, want) {
			t.Errorf("pair output missing %q:\n%s", want, got.out)
		}
	}

	got = runCLI(t, "pair", testPeerID, "laptop")
	if got.code != ExitOK || !strings.Contains(got.out, "already paired — nothing to do") {
		t.Fatalf("second pair: code = %d out = %q, want nothing-to-do", got.code, got.out)
	}

	// Invalid device IDs are rejected before any config write.
	puts := st.puts
	for _, bad := range []string{"bogus", "also bad", strings.Repeat("x", 52)} {
		got = runCLI(t, "pair", bad)
		if got.code != ExitError || !strings.Contains(got.err, "invalid device ID") {
			t.Errorf("pair %q: code = %d err = %q, want invalid device ID error", bad, got.code, got.err)
		}
	}
	if st.puts != puts {
		t.Errorf("rejected IDs must not PUT (puts = %d, want %d)", st.puts, puts)
	}
}

func TestPairFlagParsing(t *testing.T) {
	syncWorkspaceHome(t)
	st := newSyncStub()
	spawned := useSyncStub(t, st)

	// Unreachable + --start: spawns detached, waits, proceeds.
	st.failPings = 1
	got := runCLI(t, "pair", "--start")
	if got.code != ExitOK {
		t.Fatalf("pair --start: code = %d (stderr: %s)", got.code, got.err)
	}
	if !*spawned {
		t.Error("pair --start must spawn a detached syncthing when the API is down")
	}
	if !strings.Contains(got.out, "device id: "+testMyID) {
		t.Errorf("pair --start output missing device id:\n%s", got.out)
	}

	// --start also accepted between id and name.
	st.failPings = 0
	got = runCLI(t, "pair", testPeerID, "--start", "laptop")
	if got.code != ExitOK || !strings.Contains(got.out, "added device") {
		t.Fatalf("pair <id> --start <name>: code = %d out = %q err = %q", got.code, got.out, got.err)
	}

	// Too many args and unknown flags are usage errors.
	for _, args := range [][]string{
		{"pair", "a", "b", "c"},
		{"pair", "--bogus"},
	} {
		got = runCLI(t, args...)
		if got.code != ExitError || !strings.Contains(got.err, "usage: cwm pair") {
			t.Errorf("cwm %v: code = %d err = %q, want usage error", args, got.code, got.err)
		}
	}
}

func TestPairHelpTopics(t *testing.T) {
	got := runCLI(t, "help", "pair")
	if got.code != ExitOK || !strings.Contains(got.out, "usage: cwm pair [--start] [<device-id> [name]]") {
		t.Fatalf("help pair: code = %d out = %q", got.code, got.out)
	}
	got = runCLI(t, "help", "devices")
	if got.code != ExitOK || !strings.Contains(got.out, "usage: cwm devices") {
		t.Fatalf("help devices: code = %d out = %q", got.code, got.out)
	}
}

func TestDevicesCommand(t *testing.T) {
	syncWorkspaceHome(t)
	st := newSyncStub()
	useSyncStub(t, st)

	// No peers yet.
	got := runCLI(t, "devices")
	if got.code != ExitOK || !strings.Contains(got.out, "(no paired devices yet — run cwm pair on both devices)") {
		t.Fatalf("empty devices: code = %d out = %q err = %q", got.code, got.out, got.err)
	}

	// Two peers, one connected mid-sync.
	st.cfg = &syncthing.Config{Devices: []syncthing.Device{
		{DeviceID: testMyID, Name: "self"},
		{DeviceID: testPeerID, Name: "laptop"},
		{DeviceID: testOther},
	}}
	st.conns = &syncthing.Connections{Connections: map[string]syncthing.DeviceConnection{
		testPeerID: {Connected: true, Address: "192.168.1.9:22000", InBytesTotal: 2048, OutBytesTotal: 1 << 20},
	}}
	st.completion[testPeerID] = &syncthing.Completion{Completion: 87.5}

	got = runCLI(t, "devices")
	if got.code != ExitOK {
		t.Fatalf("devices: code = %d (stderr: %s)", got.code, got.err)
	}
	for _, want := range []string{
		testPeerID[:7],
		testOther[:7],
		"laptop",
		"192.168.1.9:22000",
		"87.5%",
		"100.0%",
		"COMPLETION",
	} {
		if !strings.Contains(got.out, want) {
			t.Errorf("devices output missing %q:\n%s", want, got.out)
		}
	}
	if strings.Contains(got.out, testMyID[:7]) {
		t.Errorf("devices output must not list the local device:\n%s", got.out)
	}

	// devices takes no arguments and never spawns a daemon.
	got = runCLI(t, "devices", "--start")
	if got.code != ExitError || !strings.Contains(got.err, "usage: cwm devices") {
		t.Fatalf("devices --start: code = %d err = %q, want usage error", got.code, got.err)
	}
}
