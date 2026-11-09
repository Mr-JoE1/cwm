package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	idA1    = "aaaaaaaa-0000-0000-0000-000000000001"
	idA2    = "aaaaaaaa-0000-0000-0000-000000000002"
	idSide  = "aaaaaaaa-0000-0000-0000-000000000003"
	idGhost = "aaaaaaaa-0000-0000-0000-00000000dead"
	idB1    = "bbbbbbbb-0000-0000-0000-000000000001"
	idB2    = "bbbbbbbb-0000-0000-0000-000000000002"
	idB3    = "bbbbbbbb-0000-0000-0000-000000000003"
)

const (
	a1Content    = `{"type":"user","message":{"content":"raw should not win"}}` + "\n"
	a2Content    = `{"type":"user","message":{"content":"raw a2 should not win"}}` + "\n"
	sideContent  = `{"type":"user","message":{"content":"sidechain raw"}}` + "\n"
	agentContent = `{"type":"user","message":{"content":"agent raw"}}` + "\n"
	b1Content    = `{"type":"system","message":{"content":"sys"}}` + "\n" +
		`{"type":"user","message":{"content":"  fix the   login\nbug\ttoday  "}}` + "\n"
	b2Content = `{"type":"user","message":{"content":[{"type":"text","text":"tool result"}]}}` + "\n" +
		`{"type":"assistant","message":{"content":"hi"}}` + "\n" +
		`{"type":"user","message":{"content":"later   string line"}}` + "\n"
	b3Content = `{"type":"system","message":{"content":"x"}}` + "\n" +
		`{"type":"assistant","message":{"content":"y"}}` + "\n"
)

func writeFile(t *testing.T, dir, name, content string, mtime time.Time) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func requireIDs(t *testing.T, list []Session, want ...string) {
	t.Helper()
	got := make([]string, len(list))
	for i, s := range list {
		got[i] = s.ID
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

func buildWorkspace(t *testing.T) (string, time.Time) {
	t.Helper()
	projects := filepath.Join(t.TempDir(), "projects")
	alpha := filepath.Join(projects, "alpha")
	beta := filepath.Join(projects, "beta")
	for _, d := range []string{
		alpha,
		beta,
		filepath.Join(alpha, idSide, "subagents"),
		filepath.Join(alpha, "notes"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
	writeFile(t, alpha, idA1+".jsonl", a1Content, base.Add(3*time.Hour))
	writeFile(t, alpha, idA2+".jsonl", a2Content, base.Add(2*time.Hour))
	writeFile(t, alpha, idSide+".jsonl", sideContent, base.Add(4*time.Hour))
	writeFile(t, alpha, "agent-worker.jsonl", agentContent, base.Add(4*time.Hour))
	writeFile(t, alpha, "readme.txt", "not a transcript\n", base.Add(4*time.Hour))
	writeFile(t, beta, idB1+".jsonl", b1Content, base.Add(5*time.Hour))
	writeFile(t, beta, idB2+".jsonl", b2Content, base.Add(2*time.Hour))
	writeFile(t, beta, idB3+".jsonl", b3Content, base.Add(1*time.Hour))
	if err := os.WriteFile(filepath.Join(alpha, idSide, "subagents", "decoy.jsonl"), []byte(agentContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alpha, "notes", "nested.jsonl"), []byte(agentContent), 0o644); err != nil {
		t.Fatal(err)
	}
	index := fmt.Sprintf(`{
  "entries": [
    {"sessionId": %q, "firstPrompt": "indexed prompt one", "summary": "sum one", "messageCount": 12, "modified": "2026-11-01T00:00:00Z", "gitBranch": "main", "isSidechain": false},
    {"sessionId": %q, "firstPrompt": "indexed prompt two", "messageCount": 3, "modified": 1730000000},
    {"sessionId": %q, "firstPrompt": "side prompt", "messageCount": 2, "gitBranch": "side", "isSidechain": true},
    {"sessionId": %q, "firstPrompt": "ghost prompt"}
  ]
}`, idA1, idA2, idSide, idGhost)
	writeFile(t, alpha, "sessions-index.json", index, base)
	return projects, base
}

func TestListScansAndEnriches(t *testing.T) {
	projects, base := buildWorkspace(t)
	list, err := List(projects, "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	requireIDs(t, list, idB1, idA1, idA2, idB2, idB3)
	for _, s := range list {
		switch s.ID {
		case idSide, idGhost, "agent-worker", "decoy", "nested":
			t.Fatalf("excluded id %q was listed", s.ID)
		}
	}

	row := list[0]
	if row.Project != "beta" || row.Path != filepath.Join(projects, "beta", idB1+".jsonl") {
		t.Fatalf("row 0 = %+v", row)
	}
	if row.FirstPrompt != "fix the login bug today" {
		t.Fatalf("row 0 FirstPrompt = %q", row.FirstPrompt)
	}
	if row.Summary != "" || row.Messages != 0 || row.Branch != "" {
		t.Fatalf("row 0 should be unenriched: %+v", row)
	}
	if !row.Modified.Equal(base.Add(5 * time.Hour)) {
		t.Fatalf("row 0 Modified = %v", row.Modified)
	}
	if row.Size != int64(len(b1Content)) {
		t.Fatalf("row 0 Size = %d, want %d", row.Size, len(b1Content))
	}

	row = list[1]
	if row.FirstPrompt != "indexed prompt one" || row.Summary != "sum one" || row.Messages != 12 || row.Branch != "main" {
		t.Fatalf("row 1 = %+v", row)
	}
	if !row.Modified.Equal(base.Add(3 * time.Hour)) {
		t.Fatalf("row 1 Modified = %v, want file mtime (index modified is advisory)", row.Modified)
	}

	row = list[2]
	if row.FirstPrompt != "indexed prompt two" || row.Messages != 3 || row.Summary != "" || row.Branch != "" {
		t.Fatalf("row 2 = %+v", row)
	}

	row = list[3]
	if row.FirstPrompt != "later string line" {
		t.Fatalf("row 3 FirstPrompt = %q, want array-content line skipped", row.FirstPrompt)
	}

	row = list[4]
	if row.FirstPrompt != noPrompt {
		t.Fatalf("row 4 FirstPrompt = %q", row.FirstPrompt)
	}
}

func TestListProjectFilter(t *testing.T) {
	projects, _ := buildWorkspace(t)
	alpha, err := List(projects, "alpha", 0)
	if err != nil {
		t.Fatalf("List alpha: %v", err)
	}
	requireIDs(t, alpha, idA1, idA2)
	beta, err := List(projects, "beta", 0)
	if err != nil {
		t.Fatalf("List beta: %v", err)
	}
	requireIDs(t, beta, idB1, idB2, idB3)
	none, err := List(projects, "does-not-exist", 0)
	if err != nil {
		t.Fatalf("List missing project: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("missing project listed %d sessions", len(none))
	}
}

func TestListLimit(t *testing.T) {
	projects, _ := buildWorkspace(t)
	two, err := List(projects, "", 2)
	if err != nil {
		t.Fatalf("List limit 2: %v", err)
	}
	requireIDs(t, two, idB1, idA1)
	all, err := List(projects, "", 0)
	if err != nil || len(all) != 5 {
		t.Fatalf("List limit 0 = %d sessions, err %v", len(all), err)
	}
	over, err := List(projects, "", 99)
	if err != nil || len(over) != 5 {
		t.Fatalf("List limit 99 = %d sessions, err %v", len(over), err)
	}
}

func TestResolvePick(t *testing.T) {
	projects, _ := buildWorkspace(t)
	s, err := ResolvePick(projects, "", 1)
	if err != nil || s.ID != idB1 {
		t.Fatalf("pick 1 = %+v, err %v", s, err)
	}
	s, err = ResolvePick(projects, "", 5)
	if err != nil || s.ID != idB3 {
		t.Fatalf("pick 5 = %+v, err %v", s, err)
	}
	s, err = ResolvePick(projects, "alpha", 2)
	if err != nil || s.ID != idA2 {
		t.Fatalf("alpha pick 2 = %+v, err %v", s, err)
	}
	for _, n := range []int{-1, 0, 6} {
		want := fmt.Sprintf("no session row %d — run 'cwm sessions' to see rows", n)
		_, err := ResolvePick(projects, "", n)
		if err == nil || err.Error() != want {
			t.Fatalf("pick %d err = %v, want %q", n, err, want)
		}
	}
	_, err = ResolvePick(projects, "alpha", 3)
	if err == nil || err.Error() != "no session row 3 — run 'cwm sessions' to see rows" {
		t.Fatalf("alpha pick 3 err = %v", err)
	}
}

func TestMalformedIndexAndBoundedRead(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	gamma := filepath.Join(projects, "gamma")
	if err := os.MkdirAll(gamma, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	idSmall := "cccccccc-0000-0000-0000-000000000001"
	idBig := "cccccccc-0000-0000-0000-000000000002"
	writeFile(t, gamma, idSmall+".jsonl", `{"type":"user","message":{"content":"  gamma   one  "}}`+"\n", base.Add(1*time.Hour))
	var big strings.Builder
	for i := 0; i < 1500; i++ {
		big.WriteString(`{"type":"assistant","message":{"content":"junk"}}` + "\n")
	}
	big.WriteString(`{"type":"user","message":{"content":"past the 64KB limit"}}` + "\n")
	writeFile(t, gamma, idBig+".jsonl", big.String(), base.Add(2*time.Hour))
	writeFile(t, gamma, "sessions-index.json", `{"entries": [{"sessionId": "cccccccc-`, base)
	list, err := List(projects, "", 0)
	if err != nil {
		t.Fatalf("List with malformed index: %v", err)
	}
	requireIDs(t, list, idBig, idSmall)
	if list[0].FirstPrompt != noPrompt {
		t.Fatalf("big file FirstPrompt = %q, want %q (64KB bound)", list[0].FirstPrompt, noPrompt)
	}
	if list[1].FirstPrompt != "gamma one" {
		t.Fatalf("small file FirstPrompt = %q", list[1].FirstPrompt)
	}
}

func TestSortTiebreakByID(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	solo := filepath.Join(projects, "solo")
	if err := os.MkdirAll(solo, 0o755); err != nil {
		t.Fatal(err)
	}
	m := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	idHigh := "dddddddd-0000-0000-0000-000000000002"
	idLow := "dddddddd-0000-0000-0000-000000000001"
	writeFile(t, solo, idHigh+".jsonl", b3Content, m)
	writeFile(t, solo, idLow+".jsonl", b3Content, m)
	list, err := List(projects, "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	requireIDs(t, list, idLow, idHigh)
}

func TestIndexEmptyFirstPromptFallsBack(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	solo := filepath.Join(projects, "fallback")
	if err := os.MkdirAll(solo, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "eeeeeeee-0000-0000-0000-000000000001"
	m := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	writeFile(t, solo, id+".jsonl", `{"type":"user","message":{"content":"fallback   prompt"}}`+"\n", m)
	writeFile(t, solo, "sessions-index.json",
		fmt.Sprintf(`{"entries":[{"sessionId":%q,"messageCount":7}]}`, id), m)
	list, err := List(projects, "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	got := list[0]
	if got.FirstPrompt != "fallback prompt" || got.Messages != 7 || got.Summary != "" || got.Branch != "" {
		t.Fatalf("session = %+v", got)
	}
}

func TestListMissingProjectsDir(t *testing.T) {
	_, err := List(filepath.Join(t.TempDir(), "missing"), "", 0)
	if err == nil {
		t.Fatal("List on missing projects dir should error")
	}
}
