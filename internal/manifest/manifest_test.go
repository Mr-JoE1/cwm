package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingYieldsEmpty(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "repos.tsv"))
	if err != nil {
		t.Fatalf("Load(missing) error = %v", err)
	}
	if m.Len() != 0 {
		t.Errorf("Len() = %d, want 0", m.Len())
	}
	if _, ok := m.Lookup("alpha"); ok {
		t.Error("Lookup on empty manifest = ok, want !ok")
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.tsv")
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	m.Set("alpha", "git@github.com:me/alpha.git")
	m.Set("beta", "git@github.com:me/beta.git")
	if err := m.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", got.Len())
	}
	if url, ok := got.Lookup("alpha"); !ok || url != "git@github.com:me/alpha.git" {
		t.Errorf("Lookup(alpha) = %q, %v", url, ok)
	}
	if url, ok := got.Lookup("beta"); !ok || url != "git@github.com:me/beta.git" {
		t.Errorf("Lookup(beta) = %q, %v", url, ok)
	}
	if _, ok := got.Lookup("gamma"); ok {
		t.Error("Lookup(gamma) = ok, want !ok")
	}
}

func TestSaveFormatMatchesBashKit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.tsv")
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	m.Set("beta", "url-b")
	m.Set("alpha", "url-a")
	if err := m.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	want := "alpha\turl-a\nbeta\turl-b\n"
	if string(data) != want {
		t.Errorf("file = %q, want %q", data, want)
	}
}

func TestSaveSortedDedupedAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.tsv")
	if err := os.WriteFile(path, []byte("zeta\told\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	m.Set("alpha", "url-a")
	m.Set("zeta", "url-z")
	if err := m.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if want := "alpha\turl-a\nzeta\turl-z\n"; string(data) != want {
		t.Errorf("file = %q, want %q", data, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want 1 (atomic, no temp leftovers)", len(entries))
	}
}

func TestSetReplacesURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.tsv")
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	m.Set("alpha", "old")
	m.Set("alpha", "new")
	if url, _ := m.Lookup("alpha"); url != "new" {
		t.Errorf("Lookup(alpha) = %q, want replaced 'new'", url)
	}
	if m.Len() != 1 {
		t.Errorf("Len() = %d, want 1", m.Len())
	}
}

func TestDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.tsv")
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	m.Set("alpha", "url-a")
	if m.Delete("ghost") {
		t.Error("Delete(ghost) = true, want false")
	}
	if !m.Delete("alpha") {
		t.Error("Delete(alpha) = false, want true")
	}
	if m.Len() != 0 {
		t.Errorf("Len() = %d, want 0", m.Len())
	}
}

func TestNamesSorted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.tsv")
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, n := range []string{"delta", "bravo", "charlie", "alpha"} {
		m.Set(n, "u")
	}
	got := m.Names()
	want := []string{"alpha", "bravo", "charlie", "delta"}
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	}
}

func TestLoadBashKitFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.tsv")
	// Exactly what hooks/session-start.sh produces after append + sort -u.
	content := "alpha\tgit@github.com:me/alpha.git\nbeta\tgit@github.com:me/beta.git\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if m.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", m.Len())
	}
	if url, ok := m.Lookup("beta"); !ok || url != "git@github.com:me/beta.git" {
		t.Errorf("Lookup(beta) = %q, %v", url, ok)
	}
}

func TestLoadFirstWinsOnDuplicateName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.tsv")
	content := "alpha\tfirst\nalpha\tsecond\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if url, _ := m.Lookup("alpha"); url != "first" {
		t.Errorf("Lookup(alpha) = %q, want first entry to win (bash lookup order)", url)
	}
	if err := m.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if want := "alpha\tfirst\n"; string(data) != want {
		t.Errorf("after save file = %q, want deduped %q", data, want)
	}
}

func TestLoadTolerantOfJunk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.tsv")
	content := strings.Join([]string{
		"alpha\turl-a",
		"",
		"no-tab-line",
		"\tonly-url",
		"name-with-empty-url\t",
		"crlf-line\turl-c\r",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if m.Len() != 2 {
		t.Fatalf("Len() = %d, want 2 (alpha + crlf-line)", m.Len())
	}
	if url, ok := m.Lookup("alpha"); !ok || url != "url-a" {
		t.Errorf("Lookup(alpha) = %q, %v", url, ok)
	}
	if url, ok := m.Lookup("crlf-line"); !ok || url != "url-c" {
		t.Errorf("Lookup(crlf-line) = %q, %v", url, ok)
	}
}
