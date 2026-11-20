package gui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/status"
)

// roundTrip is a fake transport: no test ever reaches the network.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func noNetwork(t *testing.T) *http.Client {
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected network call to %s", r.URL)
		return nil, errors.New("no network in tests")
	})}
}

func TestAPIAbout(t *testing.T) {
	f := newFixture(t, okSync)
	f.srv.opts.UpdateClient = noNetwork(t)
	home := filepath.Dir(f.root)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if rec := f.do(t, "GET", "/api/about", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/about without auth = %d, want 401", rec.Code)
	}
	var got map[string]any
	getJSON(t, f, "/api/about", http.StatusOK, &got)
	for _, k := range []string{"version", "channel", "commit", "dirty", "built_at", "go", "os", "arch", "agent", "workspace", "author", "license", "releases", "update"} {
		if _, ok := got[k]; !ok {
			t.Errorf("/api/about lacks %q", k)
		}
	}
	if got["version"] != "0.5.0-test" || got["channel"] != "beta" || got["license"] != "MIT" {
		t.Errorf("version/channel/license = %v %v %v", got["version"], got["channel"], got["license"])
	}
	author := got["author"].(map[string]any)
	if len(author) != 3 || author["name"] != "Maher" || author["github"] != "https://github.com/Mr-JoE1" || author["repo"] != "https://github.com/Mr-JoE1/cwm" {
		t.Errorf("author = %v (owner decision: name, github, repo only)", author)
	}
	ws := got["workspace"].(map[string]any)
	if p, _ := ws["path"].(string); !strings.HasPrefix(p, "~") || !strings.HasSuffix(filepath.ToSlash(p), "/workspace") {
		t.Errorf("workspace.path = %q, want ~/…/workspace", p)
	}
	if ws["devices"] == nil {
		t.Error("workspace.devices is null with a reachable (fake) Syncthing")
	}
	up := got["update"].(map[string]any)
	if up["enabled"] != false || up["latest"] != nil || up["checked_at"] != nil {
		t.Errorf("update = %v, want disabled and never checked", up)
	}
	ag := got["agent"].(map[string]any)
	if ag["listen"] != "127.0.0.1:47805" {
		t.Errorf("agent.listen = %v", ag["listen"])
	}
	cur := 0
	for _, r := range got["releases"].([]any) {
		if r.(map[string]any)["state"] == "current" {
			cur++
		}
	}
	if cur != 1 {
		t.Errorf("releases have %d current rows, want 1", cur)
	}
	if strings.Contains(strings.ToLower(f.do(t, "GET", "/api/about", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.token) }).Body.String()), "linkedin") {
		t.Error("/api/about mentions LinkedIn")
	}
}

func TestAboutDevBuildLabel(t *testing.T) {
	f := newFixture(t, okSync)
	f.srv.opts.Version = status.Version{Version: "dev", Commit: "a1b2c3d", OS: "linux", Arch: "amd64", Dev: true}
	var got struct {
		Label string `json:"label"`
		Dev   bool   `json:"dev"`
	}
	getJSON(t, f, "/api/about", http.StatusOK, &got)
	if got.Label != "dev build a1b2c3d" || !got.Dev {
		t.Errorf("label = %q dev = %v, want \"dev build a1b2c3d\"", got.Label, got.Dev)
	}
}

func TestCheckUpdate(t *testing.T) {
	f := newFixture(t, okSync)
	var asked *http.Request
	f.srv.opts.UpdateURL = "https://api.github.invalid/latest"
	f.srv.opts.UpdateClient = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		asked = r
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("update check without a deadline")
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(`{"tag_name":"v0.8.0","html_url":"https://github.com/Mr-JoE1/cwm/releases/tag/v0.8.0"}`))}, nil
	})}

	// GET is not allowed and a POST needs the CSRF header.
	if rec := f.do(t, "GET", "/api/about/check-update", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.token) }); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET check-update = %d, want 405", rec.Code)
	}
	if rec := f.post(t, "/api/about/check-update", "{}", func(r *http.Request) { r.Header.Del(CSRFHeader) }); rec.Code != http.StatusForbidden {
		t.Errorf("POST without CSRF = %d, want 403", rec.Code)
	}
	if asked != nil {
		t.Fatal("the update check ran without a valid POST")
	}
	rec := f.post(t, "/api/about/check-update", "{}", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("check-update = %d: %s", rec.Code, rec.Body.String())
	}
	var up updateInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil {
		t.Fatal(err)
	}
	if !up.Enabled || up.Latest == nil || *up.Latest != "0.8.0" || !up.Newer || up.CheckedAt == nil {
		t.Errorf("update = %+v", up)
	}
	if asked == nil || asked.URL.String() != "https://api.github.invalid/latest" || !strings.HasPrefix(asked.Header.Get("User-Agent"), "cwm/") {
		t.Errorf("request = %v", asked)
	}
	// The result is remembered by GET /api/about, which itself stays offline.
	f.srv.opts.UpdateClient = noNetwork(t)
	var about struct {
		Update updateInfo `json:"update"`
	}
	getJSON(t, f, "/api/about", http.StatusOK, &about)
	if !about.Update.Enabled || about.Update.Latest == nil || *about.Update.Latest != "0.8.0" {
		t.Errorf("about.update = %+v", about.Update)
	}
}

func TestCheckUpdateFailure(t *testing.T) {
	f := newFixture(t, okSync)
	f.srv.opts.UpdateClient = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: no route")
	})}
	rec := f.post(t, "/api/about/check-update", "{}", nil)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "could not check for updates") {
		t.Errorf("failed check = %d %s", rec.Code, rec.Body.String())
	}
	if updateTimeout != 5*time.Second {
		t.Errorf("update timeout = %v, want 5s (D69)", updateTimeout)
	}
}

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		latest, cur string
		want        bool
	}{{"0.8.0", "0.7.2", true}, {"0.7.2", "0.7.2", false}, {"0.7.1", "0.7.2", false}, {"1.0.0-rc1", "0.9.9", true}, {"0.8.0", "dev", false}, {"0.7.10", "0.7.9", true}} {
		if got := newerVersion(c.latest, c.cur); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v", c.latest, c.cur, got)
		}
	}
}

// TestHealthCopyCoversDoctor: every doctor check that can warn or fail has
// a plain title / why / fix entry in the dashboard (D72), so a new check id
// cannot ship with only the raw doctor text.
func TestHealthCopyCoversDoctor(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "doctor", "doctor.go"))
	if err != nil {
		t.Fatal(err)
	}
	js, err := webFS.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	ids := regexp.MustCompile(`\.(?:warn|fail)\("([a-z0-9-]+)"`).FindAllSubmatch(src, -1)
	if len(ids) < 5 {
		t.Fatalf("found only %d warn/fail checks in doctor.go", len(ids))
	}
	for _, m := range ids {
		id := string(m[1])
		if !regexp.MustCompile(`\n    "?` + regexp.QuoteMeta(id) + `"?: \[`).Match(js) {
			t.Errorf("doctor check %q has no plain copy in app.js HEALTH", id)
		}
	}
}
