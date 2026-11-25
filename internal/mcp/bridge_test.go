package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

// liveFixture is a .claude.json with account and machine keys the bridge
// must never touch (obviously fake values).
const liveFixture = `{
  "numStartups": 42,
  "userID": "fake-user-id-0000",
  "oauthAccount": {"emailAddress": "someone@example.invalid", "accountUuid": "00000000-fake"},
  "projects": {"/tmp/x": {"allowedTools": [], "note": "<keep & as is>"}},
  "mcpServers": %s
}
`

func writeLive(t *testing.T, path, servers string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(liveFixture, "%s", servers, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func liveServers(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	var servers map[string]any
	_ = json.Unmarshal(top["mcpServers"], &servers)
	var names []string
	for k := range servers {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func wsServers(t *testing.T, l *storage.Layout) []string {
	t.Helper()
	d, err := load(l)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for k := range d.servers {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func TestBridgeThreeWay(t *testing.T) {
	dir := t.TempDir()
	l := storage.NewLayoutAt(filepath.Join(dir, "cwm"))
	def := filepath.Join(dir, "home", ".claude.json")
	work := filepath.Join(dir, "cwm", "accounts", "work", ".claude.json")
	targets := []BridgeTarget{{Name: "default", Path: def}, {Name: "work", Path: work}, {Name: "gone", Path: filepath.Join(dir, "none", ".claude.json")}}

	if err := Add(l, "ws-only", "npx", []string{"ws"}); err != nil {
		t.Fatal(err)
	}
	writeLive(t, def, `{"user-added": {"command": "uvx", "args": ["u"]}}`)
	writeLive(t, work, `{}`)

	// First run: the user's own server reaches the workspace, the
	// workspace server reaches both files.
	rep, err := Bridge(l, targets)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.WorkspaceChanged || len(rep.Missing) != 1 || rep.Missing[0] != "gone" {
		t.Fatalf("report %+v", rep)
	}
	want := "user-added,ws-only"
	for _, p := range []string{def, work} {
		if got := strings.Join(liveServers(t, p), ","); got != want {
			t.Fatalf("%s servers = %s, want %s", p, got, want)
		}
	}
	if got := strings.Join(wsServers(t, l), ","); got != want {
		t.Fatalf("workspace = %s", got)
	}
	// Other keys are untouched, permissions kept, no HTML escaping.
	data, _ := os.ReadFile(def)
	var top map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	if top["numStartups"] != float64(42) || top["userID"] != "fake-user-id-0000" || top["oauthAccount"] == nil {
		t.Fatalf("other keys changed: %s", data)
	}
	if !strings.Contains(string(data), "<keep & as is>") {
		t.Fatalf("strings re-escaped: %s", data)
	}
	// Windows has no Unix permission bits (os.Stat reports 0666).
	if info, _ := os.Stat(def); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}

	// Idempotent: a second run writes nothing.
	before, _ := os.ReadFile(def)
	if rep, err := Bridge(l, targets); err != nil || rep.WorkspaceChanged || len(rep.Updated) != 0 {
		t.Fatalf("second run %+v %v", rep, err)
	}
	if after, _ := os.ReadFile(def); string(after) != string(before) {
		t.Fatal("second run rewrote .claude.json")
	}

	// `claude mcp remove` in the work account's file propagates
	// everywhere; a workspace removal does too.
	writeLive(t, work, `{"ws-only": {"command": "npx", "args": ["ws"]}}`)
	if _, err := Bridge(l, targets); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(liveServers(t, def), ","); got != "ws-only" {
		t.Fatalf("live deletion did not propagate: %s", got)
	}
	if err := Remove(l, "ws-only"); err != nil {
		t.Fatal(err)
	}
	if _, err := Bridge(l, targets); err != nil {
		t.Fatal(err)
	}
	if got := liveServers(t, work); len(got) != 0 {
		t.Fatalf("workspace deletion did not propagate: %v", got)
	}

	// Both sides change the same server: the workspace wins, reported.
	if err := Add(l, "both", "npx", []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Bridge(l, targets); err != nil {
		t.Fatal(err)
	}
	if err := Add(l, "both", "npx", []string{"ws-v2"}); err != nil {
		t.Fatal(err)
	}
	writeLive(t, def, `{"both": {"command": "npx", "args": ["live-v2"]}}`)
	rep, err = Bridge(l, targets)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Conflicts) != 1 || rep.Conflicts[0].Server != "both" {
		t.Fatalf("conflicts %+v", rep.Conflicts)
	}
	data, _ = os.ReadFile(def)
	if !strings.Contains(string(data), "ws-v2") || strings.Contains(string(data), "live-v2") {
		t.Fatalf("workspace did not win: %s", data)
	}
	if ok, off, err := InSync(l, targets); err != nil || !ok {
		t.Fatalf("InSync = %v %v %v", ok, off, err)
	}
	writeLive(t, work, `{}`)
	if ok, off, _ := InSync(l, targets); ok || len(off) != 1 || off[0] != "work" {
		t.Fatalf("InSync after drift = %v %v", ok, off)
	}
}

func TestBridgeRefusesBrokenLiveFile(t *testing.T) {
	dir := t.TempDir()
	l := storage.NewLayoutAt(filepath.Join(dir, "cwm"))
	p := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Add(l, "a", "npx", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Bridge(l, []BridgeTarget{{Name: "default", Path: p}}); err == nil {
		t.Fatal("a broken .claude.json must fail the bridge, not be overwritten")
	}
	if b, _ := os.ReadFile(p); string(b) != "{not json" {
		t.Fatalf("broken file rewritten: %q", b)
	}
}
