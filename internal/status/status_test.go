package status

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncthing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// keys returns the JSON object keys of v, in sorted order.
func keys(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestProjects(t *testing.T) {
	root := t.TempDir()
	layout := storage.NewLayoutAt(root)

	_, err := Projects(layout)
	var nw *NoWorkspaceError
	if !errors.As(err, &nw) || !strings.Contains(err.Error(), "cwm init") {
		t.Fatalf("missing workspace: err = %v, want *NoWorkspaceError with init hint", err)
	}

	if err := os.MkdirAll(layout.WorkspaceDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	list, err := Projects(layout)
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("no projects dir: %v %v, want empty non-nil", list, err)
	}

	ws := layout.WorkspaceDir()
	writeFile(t, filepath.Join(ws, "projects", "alpha", "a.jsonl"), "{}")
	writeFile(t, filepath.Join(ws, "projects", "alpha", "b.jsonl"), "{}")
	writeFile(t, filepath.Join(ws, "projects", "empty", "keep.txt"), "x")
	when := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(ws, "projects", "alpha", "b.jsonl"), when, when); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(ws, "projects", "alpha", "a.jsonl"), when.Add(-time.Hour), when.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	list, err = Projects(layout)
	if err != nil || len(list) != 2 {
		t.Fatalf("Projects = %v, %v", list, err)
	}
	if list[0].Name != "alpha" || list[0].Sessions != 2 || !list[0].Modified.Equal(when) {
		t.Errorf("alpha = %+v", list[0])
	}
	if list[1].Name != "empty" || list[1].Sessions != 0 || !list[1].Modified.IsZero() {
		t.Errorf("empty = %+v", list[1])
	}
	if got := keys(t, list[0]); got != "modified,name,sessions" {
		t.Errorf("project JSON keys = %s", got)
	}
	if got := keys(t, list[1]); got != "name,sessions" {
		t.Errorf("never-modified project must omit modified: keys = %s", got)
	}
}

func TestSessions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "projects")
	if got := Sessions(dir, "", 0); got == nil || len(got) != 0 {
		t.Fatalf("absent projects dir: %v, want empty non-nil", got)
	}
	writeFile(t, filepath.Join(dir, "alpha", "s1.jsonl"),
		`{"type":"user","message":{"content":"fix   the\nbug"}}`+"\n")
	writeFile(t, filepath.Join(dir, "alpha", "agent-x.jsonl"), "{}\n")
	got := Sessions(dir, "", 0)
	if len(got) != 1 || got[0].ID != "s1" || got[0].FirstPrompt != "fix the bug" || got[0].Project != "alpha" {
		t.Fatalf("Sessions = %+v", got)
	}
	if _, err := time.Parse(time.RFC3339, got[0].Modified); err != nil {
		t.Errorf("modified %q is not RFC 3339: %v", got[0].Modified, err)
	}
	if k := keys(t, got[0]); k != "branch,copies,devices,firstPrompt,id,label,messages,modified,project,summary" { // v0.4 keys + additive v0.8 copies/devices + v0.9 label (projectPath only when known)
		t.Errorf("session JSON keys = %s (v0.4 --json contract + v0.8/v0.9 additive)", k)
	}
}

type fakeSync struct{ myID, peer string }

func (f fakeSync) Ping() error { return nil }
func (f fakeSync) Status() (*syncthing.SystemStatus, error) {
	return &syncthing.SystemStatus{MyID: f.myID}, nil
}
func (f fakeSync) GetConfig() (*syncthing.Config, error) {
	return &syncthing.Config{Devices: []syncthing.Device{{DeviceID: f.myID}, {DeviceID: f.peer, Name: "tower"}}}, nil
}
func (f fakeSync) PutConfig(*syncthing.Config) error { return nil }
func (f fakeSync) Connections() (*syncthing.Connections, error) {
	return &syncthing.Connections{Connections: map[string]syncthing.DeviceConnection{
		f.peer: {Connected: true, Address: "10.0.0.2:22000", InBytesTotal: 10, OutBytesTotal: 20},
	}}, nil
}
func (f fakeSync) Completion(string, string) (*syncthing.Completion, error) {
	return &syncthing.Completion{Completion: 100}, nil
}

type downSync struct{ fakeSync }

func (downSync) Status() (*syncthing.SystemStatus, error) { return nil, errors.New("boom") }

func TestDevices(t *testing.T) {
	f := fakeSync{myID: "SELF", peer: "PEER"}
	got, err := Devices(f)
	if err != nil || len(got) != 1 {
		t.Fatalf("Devices = %v, %v", got, err)
	}
	want := Device{ID: "PEER", Name: "tower", Connected: true, Address: "10.0.0.2:22000", InBytes: 10, OutBytes: 20, Completion: 100}
	if got[0] != want {
		t.Errorf("device = %+v, want %+v", got[0], want)
	}
	if k := keys(t, got[0]); k != "address,completion,connected,id,inBytes,name,outBytes" {
		t.Errorf("device JSON keys = %s", k)
	}
	if _, err := Devices(downSync{f}); err == nil {
		t.Error("Devices with a failing client returned no error")
	}
}

func TestDirty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	if got, err := Dirty(filepath.Join(root, "absent")); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("absent root: %v %v, want empty non-nil", got, err)
	}

	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for _, name := range []string{"dirty", "clean", "fresh"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		git(dir, "init", "-q")
		if name != "fresh" {
			writeFile(t, filepath.Join(dir, "README.md"), "x\n")
			git(dir, "add", ".")
			git(dir, "commit", "-q", "-m", "seed commit")
		}
	}
	writeFile(t, filepath.Join(root, "dirty", "a.txt"), "a\n")
	writeFile(t, filepath.Join(root, "dirty", "README.md"), "changed\n")
	writeFile(t, filepath.Join(root, "fresh", "new.txt"), "n\n")
	if err := os.MkdirAll(filepath.Join(root, "notgit"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := Dirty(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []DirtyRepo{
		{Name: "dirty", Changed: 2, LastCommit: "seed commit"},
		{Name: "fresh", Changed: 1, LastCommit: "(no commits)"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Dirty = %+v, want %+v", got, want)
	}
	if k := keys(t, got[0]); k != "changed,lastCommit,name" {
		t.Errorf("dirty JSON keys = %s", k)
	}
}

func TestDoctorShape(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CWM_HOME", root)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv(syncthing.EnvURL, "http://127.0.0.1:1")
	t.Setenv(syncthing.EnvAPIKey, "test")
	writeFile(t, filepath.Join(root, "logs", "wip.log"), "one\ntwo\n")

	checks := Doctor(root, false)
	if len(checks) == 0 {
		t.Fatal("no checks")
	}
	byName := map[string]Check{}
	for _, c := range checks {
		if c.Name == "" || c.Detail == "" {
			t.Errorf("check missing name/detail: %+v", c)
		}
		switch c.Status {
		case "ok", "warn", "fail":
		default:
			t.Errorf("check %s: status %q", c.Name, c.Status)
		}
		byName[c.Name] = c
	}
	if byName["workspace"].Status != "fail" {
		t.Errorf("missing workspace must fail: %+v", byName["workspace"])
	}
	if wl := byName["wip-log"]; len(wl.Lines) != 2 || wl.Lines[1] != "two" {
		t.Errorf("wip-log lines = %+v", wl)
	}
	if _, ok := byName["ssh"]; ok {
		t.Error("ssh probe ran without net=true (D9)")
	}
	if k := keys(t, byName["wip-log"]); k != "detail,lines,name,status" {
		t.Errorf("check JSON keys = %s", k)
	}
	if k := keys(t, byName["workspace"]); k != "detail,name,status" {
		t.Errorf("check without lines must omit them: keys = %s", k)
	}
}
