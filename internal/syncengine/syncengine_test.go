package syncengine

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/syncthing"
)

// Valid 52-char base32 device IDs standing in for real syncthing IDs.
var (
	fakeMyID   = strings.Repeat("A2B3C4D7EFGHJKLM", 3) + "NPQR"
	fakePeerID = strings.Repeat("P7Q2R3S4T7UVWXYZ", 3) + "AB2C"
	fakeOther  = strings.Repeat("K7L2M3N4P7QRSTUV", 3) + "WX2Y"
)

// fakeSyncthing is an httptest-backed stand-in: a mutable in-memory config
// plus status/connections/completion endpoints, fronted by a real
// syncthing.Client so the engine is exercised over actual REST plumbing.
type fakeSyncthing struct {
	srv        *httptest.Server
	mu         sync.Mutex
	cfg        syncthing.Config
	conns      syncthing.Connections
	completion map[string]syncthing.Completion
	puts       int
}

func newFakeSyncthing(t *testing.T) *fakeSyncthing {
	t.Helper()
	f := &fakeSyncthing{
		cfg:        syncthing.Config{Devices: []syncthing.Device{{DeviceID: fakeMyID, Name: "self"}}},
		completion: map[string]syncthing.Completion{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/system/ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ping":"pong"}`)
	})
	mux.HandleFunc("/rest/system/status", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"myID":%q}`, fakeMyID)
	})
	mux.HandleFunc("/rest/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "test-key" {
			http.Error(w, "bad api key", http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(&f.cfg)
		case http.MethodPut:
			var cfg syncthing.Config
			if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			f.cfg = cfg
			f.puts++
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/rest/system/connections", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(&f.conns)
	})
	mux.HandleFunc("/rest/db/completion", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("folder"); got != FolderID {
			http.Error(w, "unknown folder", http.StatusNotFound)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		c, ok := f.completion[r.URL.Query().Get("device")]
		if !ok {
			c = syncthing.Completion{Completion: 100}
		}
		_ = json.NewEncoder(w).Encode(&c)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSyncthing) client() Client {
	return syncthing.NewClient(&syncthing.Endpoint{BaseURL: f.srv.URL, APIKey: "test-key"})
}

func (f *fakeSyncthing) setConfig(cfg syncthing.Config) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg = cfg
}

func (f *fakeSyncthing) setConnections(c syncthing.Connections) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.conns = c
}

func (f *fakeSyncthing) setCompletion(device string, pct float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completion[device] = syncthing.Completion{Completion: pct}
}

func (f *fakeSyncthing) config() syncthing.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfg
}

func (f *fakeSyncthing) putCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.puts
}

func TestMyID(t *testing.T) {
	id, err := MyID(newFakeSyncthing(t).client())
	if err != nil {
		t.Fatalf("MyID: %v", err)
	}
	if id != fakeMyID {
		t.Errorf("MyID = %q, want %q", id, fakeMyID)
	}
}

func TestEnsureFolderCreatesAndIsIdempotent(t *testing.T) {
	f := newFakeSyncthing(t)

	changed, err := EnsureFolder(f.client(), "/tmp/ws")
	if err != nil {
		t.Fatalf("EnsureFolder: %v", err)
	}
	if !changed {
		t.Fatal("first EnsureFolder must change the config")
	}

	cfg := f.config()
	if len(cfg.Folders) != 1 {
		t.Fatalf("folders = %d, want 1", len(cfg.Folders))
	}
	folder := cfg.Folders[0]
	if folder.ID != FolderID || folder.Path != "/tmp/ws" || folder.Type != "sendreceive" {
		t.Errorf("folder = %+v", folder)
	}
	if folder.Versioning.Type != "staggered" ||
		folder.Versioning.Params["cleanInterval"] != "3600" ||
		folder.Versioning.Params["maxAge"] != "2592000" {
		t.Errorf("versioning = %+v, want staggered 3600/2592000", folder.Versioning)
	}
	if folder.IgnoreDelete != nil && *folder.IgnoreDelete {
		t.Error("ignoreDelete must be false/absent")
	}
	if !folderHasDevice(&folder, fakeMyID) {
		t.Error("new folder must be shared with the local device")
	}

	puts := f.putCount()
	changed, err = EnsureFolder(f.client(), "/tmp/ws")
	if err != nil {
		t.Fatalf("second EnsureFolder: %v", err)
	}
	if changed {
		t.Error("second EnsureFolder must be a no-op")
	}
	if f.putCount() != puts {
		t.Error("idempotent run must not PUT")
	}
}

func TestEnsureFolderFixesPathVersioningIgnoreDelete(t *testing.T) {
	ig := true
	f := newFakeSyncthing(t)
	f.setConfig(syncthing.Config{
		Devices: []syncthing.Device{{DeviceID: fakeMyID}},
		Folders: []syncthing.Folder{{
			ID:           FolderID,
			Label:        "old",
			Path:         "/wrong/path",
			Type:         "sendreceive",
			Devices:      []syncthing.FolderDevice{{DeviceID: fakeMyID}},
			Versioning:   syncthing.Versioning{Type: "simple", Params: map[string]string{"keep": "5"}},
			IgnoreDelete: &ig,
		}},
	})

	changed, err := EnsureFolder(f.client(), "/tmp/ws")
	if err != nil {
		t.Fatalf("EnsureFolder: %v", err)
	}
	if !changed {
		t.Fatal("wrong path/versioning must be fixed")
	}

	folder := f.config().Folders[0]
	if folder.Path != "/tmp/ws" {
		t.Errorf("path = %q, want /tmp/ws", folder.Path)
	}
	if folder.Versioning.Type != "staggered" ||
		folder.Versioning.Params["cleanInterval"] != "3600" ||
		folder.Versioning.Params["maxAge"] != "2592000" {
		t.Errorf("versioning = %+v, want staggered 3600/2592000", folder.Versioning)
	}
	if folder.IgnoreDelete != nil && *folder.IgnoreDelete {
		t.Error("ignoreDelete must be false/absent after the fix")
	}

	puts := f.putCount()
	changed, err = EnsureFolder(f.client(), "/tmp/ws")
	if err != nil {
		t.Fatalf("second EnsureFolder: %v", err)
	}
	if changed || f.putCount() != puts {
		t.Error("second run must be a no-op without a PUT")
	}
}

func TestPairAddsBothDevicesAndIsIdempotent(t *testing.T) {
	f := newFakeSyncthing(t)

	changed, err := Pair(f.client(), fakePeerID, "laptop", "/tmp/ws")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if !changed {
		t.Fatal("first Pair must change the config")
	}
	if got := f.putCount(); got != 1 {
		t.Errorf("PUTs = %d, want exactly 1", got)
	}

	cfg := f.config()
	peer := findDevice(&cfg, fakePeerID)
	if peer == nil {
		t.Fatal("peer not in device list")
	}
	if peer.Name != "laptop" {
		t.Errorf("peer name = %q, want laptop", peer.Name)
	}
	folder := findFolder(&cfg)
	if folder == nil {
		t.Fatal("cwm-workspace folder missing")
	}
	if !folderHasDevice(folder, fakeMyID) || !folderHasDevice(folder, fakePeerID) {
		t.Errorf("folder devices = %+v, want local AND peer", folder.Devices)
	}
	if folder.Path != "/tmp/ws" || folder.Versioning.Type != "staggered" {
		t.Errorf("folder = %+v", folder)
	}

	changed, err = Pair(f.client(), fakePeerID, "laptop", "/tmp/ws")
	if err != nil {
		t.Fatalf("second Pair: %v", err)
	}
	if changed {
		t.Error("second Pair must be a no-op")
	}
	if f.putCount() != 1 {
		t.Errorf("PUTs = %d after idempotent run, want 1", f.putCount())
	}
}

func TestPairNamesExistingDeviceAndSharesFolder(t *testing.T) {
	f := newFakeSyncthing(t)
	f.setConfig(syncthing.Config{
		Devices: []syncthing.Device{{DeviceID: fakeMyID}, {DeviceID: fakePeerID}},
		Folders: []syncthing.Folder{{
			ID:      FolderID,
			Label:   folderLabel,
			Path:    "/tmp/ws",
			Type:    "sendreceive",
			Devices: []syncthing.FolderDevice{{DeviceID: fakeMyID}},
			Versioning: syncthing.Versioning{Type: "staggered", Params: map[string]string{
				"cleanInterval": "3600", "maxAge": "2592000",
			}},
		}},
	})

	changed, err := Pair(f.client(), fakePeerID, "desk", "/tmp/ws")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if !changed {
		t.Fatal("unnamed device + unshared folder must trigger a change")
	}
	cfg := f.config()
	if d := findDevice(&cfg, fakePeerID); d == nil || d.Name != "desk" {
		t.Errorf("peer = %+v, want name desk", d)
	}
	if folder := findFolder(&cfg); folder == nil || !folderHasDevice(folder, fakePeerID) {
		t.Error("folder must be shared with the peer after Pair")
	}
	if got := f.putCount(); got != 1 {
		t.Errorf("PUTs = %d, want 1", got)
	}
}

func TestPairRejectsInvalidDeviceIDs(t *testing.T) {
	f := newFakeSyncthing(t)
	bad := []string{
		"",
		"short",
		strings.Repeat("A", 51),
		strings.Repeat("A", 61),
		strings.ToLower(strings.Repeat("A", 52)),
		"1" + strings.Repeat("A", 51),
		"8" + strings.Repeat("Z", 51),
		"0" + strings.Repeat("B", 51),
		"9" + strings.Repeat("C", 51),
		strings.Repeat("A", 45) + "-" + strings.Repeat("B", 7), // dashed form
	}
	for _, id := range bad {
		if _, err := Pair(f.client(), id, "x", "/tmp/ws"); err == nil {
			t.Errorf("Pair(%q) accepted an invalid device ID", id)
		}
	}
	if _, err := Pair(f.client(), fakeMyID, "me", "/tmp/ws"); err == nil {
		t.Error("pairing with this device's own ID must be rejected")
	}
	if got := f.putCount(); got != 0 {
		t.Errorf("rejected IDs must never PUT, got %d PUT(s)", got)
	}

	// Surrounding whitespace is trimmed, not rejected.
	if _, err := Pair(f.client(), "  "+strings.Repeat("A", 52)+"  ", "x", "/tmp/ws"); err != nil {
		t.Errorf("Pair must trim surrounding whitespace: %v", err)
	}
}

func TestDevicesReport(t *testing.T) {
	f := newFakeSyncthing(t)
	f.setConfig(syncthing.Config{Devices: []syncthing.Device{
		{DeviceID: fakeMyID, Name: "self"},
		{DeviceID: fakePeerID, Name: "laptop"},
		{DeviceID: fakeOther},
	}})
	f.setConnections(syncthing.Connections{Connections: map[string]syncthing.DeviceConnection{
		fakePeerID: {Connected: true, Address: "192.168.1.9:22000", InBytesTotal: 2048, OutBytesTotal: 1 << 20},
	}})
	f.setCompletion(fakePeerID, 87.5)

	rows, err := DevicesReport(f.client())
	if err != nil {
		t.Fatalf("DevicesReport: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (local device excluded)", len(rows))
	}
	got := rows[0]
	if got.ID != fakePeerID || got.Name != "laptop" || !got.Connected ||
		got.Address != "192.168.1.9:22000" || got.InBytes != 2048 || got.OutBytes != 1<<20 ||
		got.Completion != 87.5 {
		t.Errorf("connected row = %+v", got)
	}
	other := rows[1]
	if other.ID != fakeOther || other.Connected || other.Address != "" || other.Completion != 100 {
		t.Errorf("offline row = %+v", other)
	}
}
