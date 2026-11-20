package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mr-JoE1/cwm/internal/gui"
	"github.com/Mr-JoE1/cwm/internal/status"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/termout"
)

func statsSandbox(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CWM_HOME", home)
	for _, k := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		t.Setenv(k, t.TempDir())
	}
	return home
}

func writeStatsTranscript(t *testing.T, home, rel string, ts time.Time, out int) {
	t.Helper()
	sess := strings.TrimSuffix(filepath.Base(rel), ".jsonl")
	at := ts.UTC().Format(time.RFC3339Nano)
	body := fmt.Sprintf(`{"type":"user","uuid":"u-%s","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"hi"}}`+"\n", sess, sess, at) +
		fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"id":"m-%s","model":"claude-opus-4-5","usage":{"input_tokens":3,"output_tokens":%d,"cache_creation_input_tokens":20,"cache_read_input_tokens":300}}}`+"\n", sess, at, sess, out) +
		fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"id":"m-%s","model":"claude-opus-4-5","usage":{"input_tokens":3,"output_tokens":1,"cache_creation_input_tokens":20,"cache_read_input_tokens":300}}}`+"\n", sess, at, sess)
	p := filepath.Join(home, "workspace", "projects", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scanFree drops the three fields that describe the scan, not the data.
func scanFree(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	for _, k := range []string{"scan_ms", "cache_hit_ratio", "scanned_files"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %s", k)
		}
		delete(m, k)
	}
	return m
}

func TestStatsJSONEqualsAPIBody(t *testing.T) {
	home := statsSandbox(t)
	now := time.Now()
	statsNow = func() time.Time { return now }
	t.Cleanup(func() { statsNow = time.Now })
	writeStatsTranscript(t, home, "alpha/s1.jsonl", now.Add(-2*time.Hour), 50)
	writeStatsTranscript(t, home, "beta/s2.jsonl", now.Add(-50*time.Hour), 70)

	got := runCLI(t, "stats", "--range", "30d", "--tz", "UTC", "--json")
	if got.code != ExitOK {
		t.Fatalf("stats --json: %d %s", got.code, got.err)
	}
	cli := scanFree(t, []byte(got.out))
	if tot := cli["totals"].(map[string]any); tot["messages"] != 4.0 || tot["sessions"] != 2.0 {
		t.Errorf("totals = %v", tot)
	}

	tok := strings.Repeat("c", 64)
	srv, err := gui.New(gui.Options{Version: status.Version{Version: "t"}, Layout: storage.NewLayoutAt(home)}, 47801, tok)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/stats?range=30d&tz=UTC", nil)
	req.Host = "127.0.0.1:47801"
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/stats = %d %s", rec.Code, rec.Body)
	}
	api := scanFree(t, rec.Body.Bytes())
	a, _ := json.Marshal(api)
	c, _ := json.Marshal(cli)
	if string(a) != string(c) {
		t.Errorf("cwm stats --json differs from /api/stats:\ncli %s\napi %s", c, a)
	}
	// The second run reads the cache the first one wrote.
	got = runCLI(t, "stats", "--json")
	var again map[string]any
	if err := json.Unmarshal([]byte(got.out), &again); err != nil || again["cache_hit_ratio"] != 1.0 {
		t.Errorf("second run cache_hit_ratio = %v (%v)", again["cache_hit_ratio"], err)
	}
}

func TestStatsTableASCII(t *testing.T) {
	home := statsSandbox(t)
	now := time.Now()
	writeStatsTranscript(t, home, "alpha/s1.jsonl", now.Add(-time.Hour), 50)
	got := runCLI(t, "stats", "--range", "7d")
	if got.code != ExitOK {
		t.Fatalf("stats: %d %s", got.code, got.err)
	}
	for _, want := range []string{"sessions", "messages", "tokens", "streak", "est. cost", "flat-rate", "top projects", "alpha", "claude-opus-4-5", "unattributed", "█"} {
		if !strings.Contains(got.out, want) {
			t.Errorf("table lacks %q:\n%s", want, got.out)
		}
	}
	var b strings.Builder
	w := termout.ASCII(&b)
	if _, err := w.Write([]byte(got.out)); err != nil {
		t.Fatal(err)
	}
	for i, r := range b.String() {
		if r > 0x7e || (r < 0x20 && r != '\n') {
			t.Fatalf("non-ASCII %q at %d in the ASCII rendering:\n%s", r, i, b.String())
		}
	}
	if !strings.Contains(b.String(), "###") {
		t.Errorf("bars did not become #:\n%s", b.String())
	}
}

func TestStatsEmptyAndBadFlags(t *testing.T) {
	statsSandbox(t)
	got := runCLI(t, "stats")
	if got.code != ExitOK || !strings.Contains(got.out, "No sessions yet") {
		t.Errorf("empty stats: %d %q %q", got.code, got.out, got.err)
	}
	for _, args := range [][]string{{"stats", "--range", "2w"}, {"stats", "--tz", "Nowhere/Land"}, {"stats", "--bogus"}, {"stats", "--range"}} {
		if got := runCLI(t, args...); got.code != ExitError || got.err == "" {
			t.Errorf("%v: code %d err %q", args, got.code, got.err)
		}
	}
}

// TestStatsAttribution: an owner uuid recorded for a cwm account (here
// only in its .claude.json, camelCase as Claude Code writes it) names the
// account; another uuid stays unattributed and never appears raw.
func TestStatsAttribution(t *testing.T) {
	home := statsSandbox(t)
	acctDir := filepath.Join(home, "accounts", "work")
	if err := os.MkdirAll(acctDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acctDir, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"uuid-work","emailAddress":"w@example.invalid"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := "version = 1\ndefault_account = \"work\"\n\n[[account]]\nname = \"work\"\ndir = " + strconv.Quote(acctDir) + "\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, owner := range []string{"uuid-work", "uuid-other"} {
		rel := fmt.Sprintf("p/s%d.jsonl", i)
		writeStatsTranscript(t, home, rel, now.Add(-time.Hour), 10)
		p := filepath.Join(home, "workspace", "projects", filepath.FromSlash(rel))
		body, _ := os.ReadFile(p)
		if err := os.WriteFile(p, append([]byte(`{"type":"session-owner","accountUuid":"`+owner+`"}`+"\n"), body...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := runCLI(t, "stats", "--json")
	if got.code != ExitOK {
		t.Fatalf("stats: %d %s", got.code, got.err)
	}
	if strings.Contains(got.out, "uuid-") {
		t.Error("raw uuid in cwm stats --json")
	}
	var s struct {
		Accounts []struct {
			Name     string `json:"name"`
			Messages int    `json:"messages"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(got.out), &s); err != nil {
		t.Fatal(err)
	}
	names := map[string]int{}
	for _, a := range s.Accounts {
		names[a.Name] = a.Messages
	}
	if names["work"] != 2 || names["unattributed"] != 2 || len(names) != 2 {
		t.Errorf("accounts = %v", names)
	}
}
