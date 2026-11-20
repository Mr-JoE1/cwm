package stats

import (
	"bufio"
	"bytes"
	"errors"
	"hash/fnv"
	"io"
	"os"
	"time"
)

// Event kinds.
const (
	kindUser      = 0
	kindAssistant = 1
)

// syntheticModel marks Claude Code's local placeholder replies (API
// errors, interrupted turns): not an API response, never counted (D60).
const syntheticModel = "<synthetic>"

// event is one counted user prompt or assistant message of a file.
type event struct {
	kind  uint8
	ts    int64  // unix milliseconds
	sess  int    // index into fileRec.Sessions
	model int    // index into fileRec.Models, -1 = none
	owner int    // index into fileRec.Owners, -1 = no session-owner yet
	key   uint64 // FNV-1a of message.id (assistant) or uuid (user); 0 = no dedupe
	in    int64
	out   int64
	cw    int64 // cache_creation_input_tokens
	cw1h  int64 // the 1-hour share of cw (cost only)
	cr    int64
}

// parser accumulates one file's events; it can resume at an offset with
// the state of the previous scan.
type parser struct {
	rec      *fileRec
	sessIdx  map[string]int
	modelIdx map[string]int
	ownerIdx map[string]int
	// asstAt maps an assistant key to its event index for the in-file
	// "largest output wins" update when a later line repeats the id.
	asstAt  map[uint64]int
	curOwn  int
	stemKey string
}

func newParser(rec *fileRec, stem string) *parser {
	p := &parser{
		rec:      rec,
		sessIdx:  make(map[string]int, len(rec.Sessions)+1),
		modelIdx: make(map[string]int, len(rec.Models)+1),
		ownerIdx: make(map[string]int, len(rec.Owners)+1),
		asstAt:   make(map[uint64]int),
		curOwn:   -1,
		stemKey:  stem,
	}
	for i, s := range rec.Sessions {
		p.sessIdx[s] = i
	}
	for i, m := range rec.Models {
		p.modelIdx[m] = i
	}
	for i, o := range rec.Owners {
		p.ownerIdx[o] = i
	}
	if rec.Owner != "" {
		p.curOwn = p.intern(p.ownerIdx, &rec.Owners, rec.Owner)
	}
	for i, ev := range rec.events {
		if ev.kind == kindAssistant && ev.key != 0 {
			p.asstAt[ev.key] = i
		}
	}
	return p
}

func (p *parser) intern(m map[string]int, list *[]string, v string) int {
	if i, ok := m[v]; ok {
		return i
	}
	*list = append(*list, v)
	m[v] = len(*list) - 1
	return len(*list) - 1
}

var (
	// The type values alone, not `"type":"…"`: a writer that puts a
	// space after the colon must not be silently skipped.
	markAssistant = []byte(`"assistant"`)
	markUser      = []byte(`"user"`)
	markOwner     = []byte(`"session-owner"`)
)

// feed handles one complete line. It never fails: anything that does not
// parse is skipped (D60).
func (p *parser) feed(b []byte) {
	// Cheap prefilter: most bytes of a transcript are attachments, API
	// request blobs and tool output, none of which stats needs. The
	// markers only occur unescaped as real JSON structure, so a false
	// positive merely costs a full parse that then decides properly.
	if !bytes.Contains(b, markAssistant) && !bytes.Contains(b, markUser) && !bytes.Contains(b, markOwner) {
		return
	}
	l, ok := parseEntry(b)
	if !ok {
		return
	}
	switch l.Type {
	case "session-owner":
		if l.AccountUUID != "" {
			p.curOwn = p.intern(p.ownerIdx, &p.rec.Owners, l.AccountUUID)
			p.rec.Owner = l.AccountUUID
		}
	case "user":
		p.user(&l)
	case "assistant":
		p.assistant(&l)
	}
}

func parseTS(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0, false
	}
	return t.UnixMilli(), true
}

func hashKey(s string) uint64 {
	if s == "" {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	k := h.Sum64()
	if k == 0 {
		k = 1 // 0 means "no key"
	}
	return k
}

func (p *parser) session(id string) int {
	if id == "" {
		id = p.stemKey
	}
	return p.intern(p.sessIdx, &p.rec.Sessions, id)
}

// isPrompt: a string content, or an array with at least one block that
// is not a tool_result. Meta entries are injected by Claude Code.
func isPrompt(l *entry) bool {
	return !l.IsMeta && l.HasMessage && (l.Content == contentString || l.Content == contentBlocks)
}

func (p *parser) user(l *entry) {
	if !isPrompt(l) {
		return
	}
	ts, ok := parseTS(l.Timestamp)
	if !ok {
		return
	}
	p.rec.events = append(p.rec.events, event{
		kind: kindUser, ts: ts, sess: p.session(l.SessionID), model: -1, owner: p.curOwn,
		key: hashKey(l.UUID),
	})
}

func (p *parser) assistant(m *entry) {
	if !m.HasMessage || m.MsgID == "" || m.Model == syntheticModel {
		return
	}
	ts, ok := parseTS(m.Timestamp)
	if !ok {
		return
	}
	ev := event{
		kind: kindAssistant, ts: ts, sess: p.session(m.SessionID), model: -1, owner: p.curOwn,
		key: hashKey(m.MsgID),
	}
	if m.Model != "" {
		ev.model = p.intern(p.modelIdx, &p.rec.Models, m.Model)
	}
	if m.HasUsage {
		ev.in, ev.out, ev.cw, ev.cr = m.In, m.Out, m.CW, m.CR
		ev.cw1h = min(max(m.CW1h, 0), ev.cw)
	}
	if i, seen := p.asstAt[ev.key]; seen {
		if better(ev, p.rec.events[i]) {
			p.rec.events[i] = ev
		}
		return
	}
	p.asstAt[ev.key] = len(p.rec.events)
	p.rec.events = append(p.rec.events, ev)
}

// better is the dedupe order for one message id: the largest
// output_tokens wins, ties go to the earliest timestamp, then to whatever
// was seen first (the caller keeps the incumbent on a full tie).
func better(a, b event) bool {
	if a.out != b.out {
		return a.out > b.out
	}
	return a.ts < b.ts
}

// errShrunk is internal: the file is shorter than the resume offset.
var errShrunk = errors.New("stats: file shrank")

// parseFrom reads complete lines of f starting at rec.Offset and advances
// rec.Offset past them. An unterminated last line is consumed only when
// it is a complete JSON value (a writer that never adds the newline);
// otherwise it is left for the next scan.
func (p *parser) parseFrom(f *os.File, size int64) error {
	if p.rec.Offset > size {
		return errShrunk
	}
	if _, err := f.Seek(p.rec.Offset, io.SeekStart); err != nil {
		return err
	}
	r := bufio.NewReaderSize(io.LimitReader(f, size-p.rec.Offset), 256<<10)
	off := p.rec.Offset
	var long []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			long = append(long, chunk...)
			continue
		}
		b := chunk
		if long != nil {
			long = append(long, chunk...)
			b = long
		}
		if err == nil {
			p.feed(b[:len(b)-1])
			off += int64(len(b))
			p.rec.Offset = off
			long = nil
			continue
		}
		if errors.Is(err, io.EOF) {
			if len(b) > 0 && validJSON(b) {
				p.feed(b)
				off += int64(len(b))
				p.rec.Offset = off
			}
			return nil
		}
		return err
	}
}
