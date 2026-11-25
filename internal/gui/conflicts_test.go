package gui

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestConflictsEndpoints(t *testing.T) {
	f, _ := actionFixture(t, okSync)
	ws := filepath.Join(f.root, "workspace")
	// The endpoints resolve the root from the config on every request.
	if _, err := os.Stat(filepath.Join(f.root, "config.toml")); err != nil {
		writeFile(t, filepath.Join(f.root, "config.toml"), "version = 1\nmode = \"isolated\"\n")
	}
	writeFile(t, filepath.Join(ws, "CLAUDE.md"), "here\n")
	copyName := "CLAUDE.sync-conflict-20261124-101010-ABCDEF2.md"
	writeFile(t, filepath.Join(ws, copyName), "there\n")

	rec := f.do(t, "GET", "/api/conflicts", func(r *http.Request) { r.AddCookie(f.cookie()) })
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Conflicts []struct {
			Path      string `json:"path"`
			Mergeable bool   `json:"mergeable"`
			Copies    []struct {
				Name string `json:"name"`
			} `json:"copies"`
		} `json:"conflicts"`
	}
	decode(t, rec, &list)
	if len(list.Conflicts) != 1 || list.Conflicts[0].Path != "CLAUDE.md" || !list.Conflicts[0].Mergeable || list.Conflicts[0].Copies[0].Name != copyName {
		t.Fatalf("conflicts = %+v", list)
	}
	// CSRF is required.
	if rec := f.post(t, "/api/conflicts/resolve", `{"path":"CLAUDE.md","choice":"merge"}`, func(r *http.Request) { r.Header.Del(CSRFHeader) }); rec.Code != http.StatusForbidden {
		t.Fatalf("no CSRF = %d", rec.Code)
	}
	for _, bad := range []string{`{"path":"CLAUDE.md"}`, `{"path":"../x","choice":"merge"}`, `{"path":"CLAUDE.md","choice":"nope"}`} {
		if rec := f.post(t, "/api/conflicts/resolve", bad, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", bad, rec.Code)
		}
	}
	rec = f.post(t, "/api/conflicts/resolve", `{"path":"CLAUDE.md","choice":"merge"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve = %d %s", rec.Code, rec.Body.String())
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "CLAUDE.md")); string(b) != "here\nthere\n" {
		t.Fatalf("CLAUDE.md = %q", b)
	}
	if _, err := os.Stat(filepath.Join(ws, copyName)); !os.IsNotExist(err) {
		t.Fatal("conflict copy left in the workspace")
	}
}
