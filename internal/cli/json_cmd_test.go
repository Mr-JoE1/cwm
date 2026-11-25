package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/syncthing"
)

func TestListJSON(t *testing.T) {
	home := t.TempDir()
	isolatedCWMHome(t, home)
	path := filepath.Join(home, "workspace", "projects", "alpha", "a.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runCLI(t, "list", "--json")
	if got.code != ExitOK {
		t.Fatalf("list --json: code = %d err = %q", got.code, got.err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(got.out), &rows); err != nil {
		t.Fatalf("list --json is not a JSON array: %v\n%s", err, got.out)
	}
	if len(rows) != 1 || rows[0]["name"] != "alpha" || rows[0]["sessions"] != float64(1) {
		t.Errorf("rows = %v", rows)
	}

	got = runCLI(t, "list", "--bogus")
	if got.code != ExitError || !strings.Contains(got.err, "usage: cwm list [--json]") {
		t.Errorf("list --bogus: code = %d err = %q", got.code, got.err)
	}
}

func TestDoctorJSONExitCode(t *testing.T) {
	isolatedCWMHome(t, t.TempDir()) // no workspace: doctor must FAIL
	t.Setenv("PATH", t.TempDir())   // never a real claude
	t.Setenv(syncthing.EnvURL, "http://127.0.0.1:1")
	t.Setenv(syncthing.EnvAPIKey, "test")
	got := runCLI(t, "doctor", "--json")
	if got.code != ExitError {
		t.Fatalf("doctor --json without workspace: code = %d, want %d", got.code, ExitError)
	}
	if got.err != "" {
		t.Errorf("stderr must stay empty, got %q", got.err)
	}
	var checks []map[string]any
	if err := json.Unmarshal([]byte(got.out), &checks); err != nil {
		t.Fatalf("doctor --json is not a JSON array: %v\n%s", err, got.out)
	}
	if len(checks) == 0 || checks[0]["name"] != "workspace" || checks[0]["status"] != "fail" {
		t.Errorf("checks[0] = %v", checks[0])
	}
	if strings.Contains(got.out, "doctor: ") {
		t.Error("--json output must not include the text summary line")
	}
	got = runCLI(t, "doctor", "--json", "--bogus")
	if got.code != ExitError || !strings.Contains(got.err, "usage: cwm doctor [--net] [--json]") {
		t.Errorf("doctor --json --bogus: code = %d err = %q", got.code, got.err)
	}
}

func TestWipDirtyJSON(t *testing.T) {
	t.Setenv("CWM_REPOS_ROOT", filepath.Join(t.TempDir(), "absent"))
	got := runCLI(t, "wip", "dirty", "--json")
	if got.code != ExitOK || strings.TrimSpace(got.out) != "[]" {
		t.Fatalf("wip dirty --json on an empty root: code = %d out = %q err = %q", got.code, got.out, got.err)
	}
	got = runCLI(t, "wip", "dirty", "extra")
	if got.code != ExitError || !strings.Contains(got.err, "usage: cwm wip dirty [--json]") {
		t.Errorf("wip dirty extra: code = %d err = %q", got.code, got.err)
	}
}

func TestDevicesJSON(t *testing.T) {
	syncWorkspaceHome(t)
	st := newSyncStub()
	useSyncStub(t, st)
	got := runCLI(t, "devices", "--json")
	if got.code != ExitOK || strings.TrimSpace(got.out) != "[]" {
		t.Fatalf("devices --json, no peers: code = %d out = %q err = %q", got.code, got.out, got.err)
	}
	st.cfg = &syncthing.Config{Devices: []syncthing.Device{
		{DeviceID: testMyID, Name: "self"},
		{DeviceID: testPeerID, Name: "laptop"},
	}}
	got = runCLI(t, "devices", "--json")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(got.out), &rows); err != nil {
		t.Fatalf("devices --json: %v\n%s", err, got.out)
	}
	if len(rows) != 1 || rows[0]["id"] != testPeerID || rows[0]["completion"] != float64(100) {
		t.Errorf("rows = %v", rows)
	}
}
