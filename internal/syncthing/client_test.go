package syncthing

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const testAPIKey = "test-api-key"

// fakeSyncthing is an in-memory syncthing REST fake served by httptest.
type fakeSyncthing struct {
	mu sync.Mutex

	configJSON string

	pingFailures    int
	pings           int
	restartRequired bool
	restarts        int
	completionQuery url.Values
}

const initialFakeConfig = `{
  "version": 37,
  "options": {"rescanIntervalS": 10, "urAccepted": -1},
  "devices": [
    {"deviceID": "DEV-LOCAL-1234", "name": "local", "addresses": ["dynamic"], "compression": "metadata"},
    {"deviceID": "DEV-PEER-7777", "name": "peer", "addresses": ["tcp://10.0.0.2:22000"]}
  ],
  "folders": [
    {
      "id": "cwm-workspace",
      "label": "cwm workspace",
      "path": "/home/u/.cwm/workspace",
      "type": "sendreceive",
      "devices": [{"deviceID": "DEV-LOCAL-1234"}],
      "versioning": {"type": "staggered", "params": {"cleanInterval": "3600", "maxAge": "2592000"}},
      "paused": false,
      "rescanIntervalS": 3600
    }
  ]
}`

// Handler builds the fake API; every request must carry X-API-Key.
func (f *fakeSyncthing) Handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/rest/system/ping", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.pings++
		if f.pingFailures > 0 {
			f.pingFailures--
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"internal"}`)
			return
		}
		fmt.Fprint(w, `{"ping":"pong"}`)
	})

	mux.HandleFunc("/rest/system/status", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"myID":"DEV-LOCAL-1234","uptime":10}`)
	})

	mux.HandleFunc("/rest/config", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, f.configJSON)
		case http.MethodPut:
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				t.Errorf("fake: read config body: %v", err)
				http.Error(w, "read error", http.StatusBadRequest)
				return
			}
			var probe map[string]json.RawMessage
			if err := json.Unmarshal(body, &probe); err != nil {
				t.Errorf("fake: PUT body is not a JSON object: %v", err)
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			f.configJSON = string(body)
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/rest/config/restart-required", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		fmt.Fprintf(w, `{"requiresRestart":%t}`, f.restartRequired)
	})

	mux.HandleFunc("/rest/system/restart", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.restarts++
		fmt.Fprint(w, `{"ok":"restarting"}`)
	})

	mux.HandleFunc("/rest/system/connections", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
  "connections": {
    "DEV-LOCAL-1234": {"connected":true,"address":"127.0.0.1:22000","at":"2026-01-02T03:04:05Z","inBytesTotal":11,"outBytesTotal":22},
    "DEV-PEER-7777": {"connected":false,"address":"","at":"1970-01-01T00:00:00Z","inBytesTotal":0,"outBytesTotal":0}
  },
  "total": {"connected":true,"address":"","at":"2026-01-02T03:04:05Z","inBytesTotal":33,"outBytesTotal":44}
}`)
	})

	mux.HandleFunc("/rest/db/completion", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.completionQuery = r.URL.Query()
		fmt.Fprintf(w, `{"completion":87.5,"deviceID":%q,"folder":%q,"globalBytes":2048,"inSyncBytes":1792}`,
			r.URL.Query().Get("device"), r.URL.Query().Get("folder"))
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key := r.Header.Get("X-API-Key"); key != testAPIKey {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func newFake(t *testing.T) (*Client, *fakeSyncthing) {
	t.Helper()
	f := &fakeSyncthing{configJSON: initialFakeConfig}
	srv := httptest.NewServer(f.Handler(t))
	t.Cleanup(srv.Close)
	return NewClient(&Endpoint{BaseURL: srv.URL, APIKey: testAPIKey}), f
}

func TestClientPing(t *testing.T) {
	c, f := newFake(t)
	if err := c.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pings != 1 {
		t.Errorf("pings handled = %d, want 1", f.pings)
	}
}

func TestClientStatusMyID(t *testing.T) {
	c, _ := newFake(t)
	st, err := c.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.MyID != "DEV-LOCAL-1234" {
		t.Errorf("MyID = %q, want DEV-LOCAL-1234", st.MyID)
	}
}

func TestClientConfigRoundTrip(t *testing.T) {
	c, f := newFake(t)

	cfg, err := c.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if len(cfg.Devices) != 2 {
		t.Fatalf("devices = %d, want 2", len(cfg.Devices))
	}
	if cfg.Devices[0].DeviceID != "DEV-LOCAL-1234" || cfg.Devices[0].Name != "local" {
		t.Errorf("devices[0] = %+v", cfg.Devices[0])
	}
	if len(cfg.Devices[0].Addresses) != 1 || cfg.Devices[0].Addresses[0] != "dynamic" {
		t.Errorf("devices[0].Addresses = %v", cfg.Devices[0].Addresses)
	}
	if len(cfg.Folders) != 1 {
		t.Fatalf("folders = %d, want 1", len(cfg.Folders))
	}
	fo := cfg.Folders[0]
	if fo.ID != "cwm-workspace" || fo.Label != "cwm workspace" || fo.Path != "/home/u/.cwm/workspace" || fo.Type != "sendreceive" {
		t.Errorf("folder = %+v", fo)
	}
	if len(fo.Devices) != 1 || fo.Devices[0].DeviceID != "DEV-LOCAL-1234" {
		t.Errorf("folder.Devices = %+v", fo.Devices)
	}
	if fo.Versioning.Type != "staggered" || fo.Versioning.Params["maxAge"] != "2592000" || fo.Versioning.Params["cleanInterval"] != "3600" {
		t.Errorf("folder.Versioning = %+v", fo.Versioning)
	}
	if fo.IgnoreDelete != nil {
		t.Errorf("folder.IgnoreDelete = %v, want nil", *fo.IgnoreDelete)
	}
	if fo.Paused == nil || *fo.Paused {
		t.Errorf("folder.Paused = %v, want pointer to false", fo.Paused)
	}

	cfg.Devices = append(cfg.Devices, Device{DeviceID: "DEV-NEW", Name: "laptop"})
	cfg.Folders[0].Devices = append(cfg.Folders[0].Devices, FolderDevice{DeviceID: "DEV-NEW"})
	if err := c.PutConfig(cfg); err != nil {
		t.Fatalf("PutConfig: %v", err)
	}

	f.mu.Lock()
	stored := f.configJSON
	f.mu.Unlock()
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stored), &probe); err != nil {
		t.Fatalf("stored config is not JSON: %v", err)
	}
	if _, ok := probe["options"]; !ok {
		t.Errorf("stored config lost unknown field options: %s", stored)
	}
	if _, ok := probe["version"]; !ok {
		t.Errorf("stored config lost unknown field version: %s", stored)
	}

	cfg2, err := c.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig after PutConfig: %v", err)
	}
	if len(cfg2.Devices) != 3 || cfg2.Devices[2].DeviceID != "DEV-NEW" || cfg2.Devices[2].Name != "laptop" {
		t.Errorf("re-GET devices = %+v", cfg2.Devices)
	}
	if len(cfg2.Folders[0].Devices) != 2 {
		t.Errorf("re-GET folder devices = %+v", cfg2.Folders[0].Devices)
	}
}

func TestClientConnections(t *testing.T) {
	c, _ := newFake(t)
	conns, err := c.Connections()
	if err != nil {
		t.Fatalf("Connections: %v", err)
	}
	if len(conns.Connections) != 2 {
		t.Fatalf("connections = %d entries, want 2", len(conns.Connections))
	}
	local := conns.Connections["DEV-LOCAL-1234"]
	if !local.Connected || local.Address != "127.0.0.1:22000" || local.InBytesTotal != 11 || local.OutBytesTotal != 22 {
		t.Errorf("local connection = %+v", local)
	}
	if peer := conns.Connections["DEV-PEER-7777"]; peer.Connected {
		t.Errorf("peer connection = %+v, want disconnected", peer)
	}
	if !conns.Total.Connected || conns.Total.InBytesTotal != 33 || conns.Total.OutBytesTotal != 44 {
		t.Errorf("total = %+v", conns.Total)
	}
}

func TestClientCompletion(t *testing.T) {
	c, f := newFake(t)
	comp, err := c.Completion("DEV-PEER-7777", "cwm-workspace")
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}
	if comp.Completion != 87.5 {
		t.Errorf("Completion = %v, want 87.5", comp.Completion)
	}
	if comp.GlobalBytes != 2048 {
		t.Errorf("GlobalBytes = %v, want 2048", comp.GlobalBytes)
	}
	f.mu.Lock()
	q := f.completionQuery
	f.mu.Unlock()
	if q.Get("device") != "DEV-PEER-7777" || q.Get("folder") != "cwm-workspace" {
		t.Errorf("completion query = %v, want device+folder params", q)
	}
}

func TestClientRestart(t *testing.T) {
	c, f := newFake(t)
	rr, err := c.RestartRequired()
	if err != nil {
		t.Fatalf("RestartRequired: %v", err)
	}
	if rr {
		t.Errorf("RestartRequired = true, want false")
	}
	f.mu.Lock()
	f.restartRequired = true
	f.mu.Unlock()
	if rr, err = c.RestartRequired(); err != nil || !rr {
		t.Fatalf("RestartRequired after change = %v, %v; want true, nil", rr, err)
	}
	if err := c.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.restarts != 1 {
		t.Errorf("restarts = %d, want 1", f.restarts)
	}
}

func TestClientWaitReady(t *testing.T) {
	c, f := newFake(t)
	f.mu.Lock()
	f.pingFailures = 2
	f.mu.Unlock()
	if err := c.WaitReady(5, time.Millisecond); err != nil {
		t.Fatalf("WaitReady(5): %v", err)
	}

	f.mu.Lock()
	f.pingFailures = 3
	f.mu.Unlock()
	err := c.WaitReady(2, time.Millisecond)
	if err == nil {
		t.Fatal("WaitReady(2) with 3 ping failures: want error")
	}
	if !strings.Contains(err.Error(), "not ready") || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v, want wrapped not-ready/500 error", err)
	}
}

func TestClientHTTPError(t *testing.T) {
	c, f := newFake(t)
	f.mu.Lock()
	f.pingFailures = 1
	f.mu.Unlock()
	err := c.Ping()
	if err == nil {
		t.Fatal("Ping against 500: want error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v, want HTTP 500 mention", err)
	}
}

func TestClientRejectsWrongAPIKey(t *testing.T) {
	f := &fakeSyncthing{configJSON: initialFakeConfig}
	srv := httptest.NewServer(f.Handler(t))
	t.Cleanup(srv.Close)
	c := NewClient(&Endpoint{BaseURL: srv.URL, APIKey: "wrong-key"})
	if err := c.Ping(); err == nil {
		t.Fatal("Ping with wrong API key: want error")
	}
}

func TestClientPutConfigNil(t *testing.T) {
	c, _ := newFake(t)
	if err := c.PutConfig(nil); err == nil {
		t.Fatal("PutConfig(nil): want error")
	}
}
