package stats

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

// cacheVersion guards the stats.json format; any other version is a full
// rescan (D61).
const cacheVersion = 2

// headBytes is how much of a file's start is hashed to tell an append
// from a rewrite.
const headBytes = 4096

// fileRec is one transcript's cached parse: the key (size, mtime), where
// parsing stopped, the head hash, the resume state and the events.
type fileRec struct {
	// Path is relative to <workspace>/projects, slash-separated.
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Mtime   int64  `json:"mtime_ns"`
	Offset  int64  `json:"offset"`
	HeadLen int    `json:"head_len"`
	Head    string `json:"head"`
	// Owner is the last session-owner uuid seen (resume state).
	Owner    string   `json:"owner,omitempty"`
	Sessions []string `json:"sessions,omitempty"`
	Models   []string `json:"models,omitempty"`
	Owners   []string `json:"owners,omitempty"`
	// Packed holds the events, varint-packed: the cache stays small and
	// loads in milliseconds (encoding/json writes []byte as base64).
	Packed []byte `json:"events,omitempty"`

	events  []event
	project string
}

type cacheFile struct {
	Version int        `json:"version"`
	Files   []*fileRec `json:"files"`
}

func pack(evs []event) []byte {
	buf := make([]byte, 0, len(evs)*24)
	var tmp [binary.MaxVarintLen64]byte
	put := func(v int64) {
		n := binary.PutVarint(tmp[:], v)
		buf = append(buf, tmp[:n]...)
	}
	for _, e := range evs {
		buf = append(buf, e.kind)
		put(e.ts)
		put(int64(e.sess))
		put(int64(e.model))
		put(int64(e.owner))
		buf = binary.LittleEndian.AppendUint64(buf, e.key)
		put(e.in)
		put(e.out)
		put(e.cw)
		put(e.cw1h)
		put(e.cr)
	}
	return buf
}

var errCorrupt = errors.New("stats: corrupt cache entry")

func unpack(rec *fileRec) error {
	b := rec.Packed
	var evs []event
	get := func() (int64, bool) {
		v, n := binary.Varint(b)
		if n <= 0 {
			return 0, false
		}
		b = b[n:]
		return v, true
	}
	for len(b) > 0 {
		var e event
		e.kind = b[0]
		b = b[1:]
		if e.kind != kindUser && e.kind != kindAssistant {
			return errCorrupt
		}
		vals := make([]int64, 4)
		for i := range vals {
			v, ok := get()
			if !ok {
				return errCorrupt
			}
			vals[i] = v
		}
		e.ts, e.sess, e.model, e.owner = vals[0], int(vals[1]), int(vals[2]), int(vals[3])
		if len(b) < 8 {
			return errCorrupt
		}
		e.key = binary.LittleEndian.Uint64(b)
		b = b[8:]
		for _, p := range []*int64{&e.in, &e.out, &e.cw, &e.cw1h, &e.cr} {
			v, ok := get()
			if !ok {
				return errCorrupt
			}
			*p = v
		}
		if e.sess < 0 || e.sess >= len(rec.Sessions) || e.model < -1 || e.model >= len(rec.Models) ||
			e.owner < -1 || e.owner >= len(rec.Owners) {
			return errCorrupt
		}
		evs = append(evs, e)
	}
	rec.events = evs
	return nil
}

// loadCache reads the cache; any problem (missing, corrupt, another
// version) yields an empty cache, never an error: the scan rebuilds it.
func loadCache(path string) map[string]*fileRec {
	out := map[string]*fileRec{}
	if path == "" {
		return out
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var cf cacheFile
	if err := json.Unmarshal(data, &cf); err != nil || cf.Version != cacheVersion {
		return out
	}
	for _, rec := range cf.Files {
		if rec == nil || rec.Path == "" {
			continue
		}
		if err := unpack(rec); err != nil {
			return map[string]*fileRec{}
		}
		rec.project = projectOf(rec.Path)
		out[rec.Path] = rec
	}
	return out
}

func saveCache(path string, recs []*fileRec) error {
	if path == "" {
		return nil
	}
	cf := cacheFile{Version: cacheVersion, Files: recs}
	for _, r := range recs {
		r.Packed = pack(r.events)
	}
	data, err := json.Marshal(cf)
	for _, r := range recs {
		r.Packed = nil
	}
	if err != nil {
		return fmt.Errorf("stats: encode cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("stats: create cache dir: %w", err)
	}
	if err := storage.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("stats: write cache: %w", err)
	}
	return nil
}

// headHash hashes the first n bytes of f.
func headHash(f *os.File, n int64) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, 0, n)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
