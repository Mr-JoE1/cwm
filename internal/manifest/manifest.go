// Package manifest reads and writes the repos.tsv repo manifest
// (name<TAB>url lines), staying wire-compatible with the legacy bash kit.
package manifest

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

// Manifest holds name → push-URL entries for one repos.tsv file. Load keeps
// the FIRST occurrence of a duplicated name, matching the bash kit's
// read-until-first-match lookup; Save writes entries deduped and sorted.
type Manifest struct {
	path    string
	entries map[string]string
}

// Load reads path. A missing file yields an empty manifest: hooks and the
// launcher both treat "no manifest yet" as "no repos recorded".
func Load(path string) (*Manifest, error) {
	m := &Manifest{path: path, entries: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, fmt.Errorf("manifest: read %s: %w", path, err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		name, url, ok := strings.Cut(line, "\t")
		if !ok || name == "" || url == "" {
			continue
		}
		if _, dup := m.entries[name]; !dup {
			m.entries[name] = url
		}
	}
	return m, nil
}

// Lookup returns the URL recorded for name.
func (m *Manifest) Lookup(name string) (url string, ok bool) {
	url, ok = m.entries[name]
	return url, ok
}

// Set records name → url, replacing any previous URL.
func (m *Manifest) Set(name, url string) {
	m.entries[name] = url
}

// Delete removes name and reports whether it existed.
func (m *Manifest) Delete(name string) bool {
	if _, ok := m.entries[name]; !ok {
		return false
	}
	delete(m.entries, name)
	return true
}

// Names returns all recorded repo names, sorted.
func (m *Manifest) Names() []string {
	names := make([]string, 0, len(m.entries))
	for n := range m.entries {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Len returns the number of entries.
func (m *Manifest) Len() int {
	return len(m.entries)
}

// Save atomically writes the manifest: one name<TAB>url line per entry,
// sorted by name, deduped, trailing newline (bash-kit format).
func (m *Manifest) Save() error {
	var b strings.Builder
	for _, name := range m.Names() {
		b.WriteString(name)
		b.WriteByte('\t')
		b.WriteString(m.entries[name])
		b.WriteByte('\n')
	}
	if err := storage.WriteFileAtomic(m.path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("manifest: save %s: %w", m.path, err)
	}
	return nil
}
