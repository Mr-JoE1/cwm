package stats

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// A single-pass reader for the few transcript fields stats needs.
// encoding/json validates a line completely before it decodes it, and the
// lines that matter carry large text payloads (tool output, replies);
// here strings are skipped with bytes.IndexByte and only the wanted
// values are materialised. It is still a strict structural parser: an
// unterminated string, an unbalanced object or trailing garbage fails the
// line, which is then skipped like any malformed line (D60).
type jreader struct {
	b []byte
	i int
}

func (r *jreader) ws() {
	for r.i < len(r.b) {
		switch r.b[r.i] {
		case ' ', '\t', '\n', '\r':
			r.i++
		default:
			return
		}
	}
}

func (r *jreader) peek() byte {
	r.ws()
	if r.i >= len(r.b) {
		return 0
	}
	return r.b[r.i]
}

// rawString consumes a string and returns its body (escapes undecoded)
// and whether it contained a backslash.
func (r *jreader) rawString() ([]byte, bool, bool) {
	if r.peek() != '"' {
		return nil, false, false
	}
	start := r.i + 1
	j := start
	esc := false
	for {
		k := bytes.IndexByte(r.b[j:], '"')
		if k < 0 {
			return nil, false, false
		}
		q := j + k
		// An odd run of backslashes escapes the quote.
		n := 0
		for p := q - 1; p >= start && r.b[p] == '\\'; p-- {
			n++
		}
		if n > 0 {
			esc = true
		}
		if n%2 == 0 {
			r.i = q + 1
			return r.b[start:q], esc, true
		}
		j = q + 1
	}
}

func (r *jreader) str() (string, bool) {
	start := r.i
	body, esc, ok := r.rawString()
	if !ok {
		return "", false
	}
	if !esc {
		return string(body), true
	}
	var s string
	if err := json.Unmarshal(r.b[start:r.i], &s); err != nil {
		return "", false
	}
	return s, true
}

func (r *jreader) number() (int64, bool) {
	start := r.i
	for r.i < len(r.b) {
		c := r.b[r.i]
		if (c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E' {
			r.i++
			continue
		}
		break
	}
	s := string(r.b[start:r.i])
	if s == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return int64(f), true
}

func (r *jreader) literal(word string) bool {
	if bytes.HasPrefix(r.b[r.i:], []byte(word)) {
		r.i += len(word)
		return true
	}
	return false
}

// skip consumes any value.
func (r *jreader) skip() bool {
	switch c := r.peek(); {
	case c == '"':
		_, _, ok := r.rawString()
		return ok
	case c == '{':
		return r.object(func([]byte) bool { return r.skip() })
	case c == '[':
		return r.array(func() bool { return r.skip() })
	case c == 't':
		return r.literal("true")
	case c == 'f':
		return r.literal("false")
	case c == 'n':
		return r.literal("null")
	case c == '-' || (c >= '0' && c <= '9'):
		_, ok := r.number()
		return ok
	}
	return false
}

// object walks an object; val must consume the value of each key.
func (r *jreader) object(val func(key []byte) bool) bool {
	if r.peek() != '{' {
		return false
	}
	r.i++
	if r.peek() == '}' {
		r.i++
		return true
	}
	for {
		key, _, ok := r.rawString()
		if !ok || r.peek() != ':' {
			return false
		}
		r.i++
		if !val(key) {
			return false
		}
		switch r.peek() {
		case ',':
			r.i++
		case '}':
			r.i++
			return true
		default:
			return false
		}
	}
}

func (r *jreader) array(elem func() bool) bool {
	if r.peek() != '[' {
		return false
	}
	r.i++
	if r.peek() == ']' {
		r.i++
		return true
	}
	for {
		if !elem() {
			return false
		}
		switch r.peek() {
		case ',':
			r.i++
		case ']':
			r.i++
			return true
		default:
			return false
		}
	}
}

func (r *jreader) intVal(dst *int64) bool {
	if r.peek() == 'n' {
		return r.literal("null")
	}
	c := r.peek()
	if c != '-' && (c < '0' || c > '9') {
		return r.skip()
	}
	n, ok := r.number()
	*dst = n
	return ok
}

func (r *jreader) strVal(dst *string) bool {
	if r.peek() != '"' {
		return r.skip()
	}
	s, ok := r.str()
	*dst = s
	return ok
}

// Content kinds of message.content.
const (
	contentNone   = iota // missing, null, or not a string / array
	contentString        // a plain prompt
	contentBlocks        // an array with at least one non-tool_result block
	contentTools         // an array of tool_result blocks only (or empty)
)

// entry is what stats reads from one line.
type entry struct {
	Type, Timestamp, SessionID, UUID, AccountUUID string
	IsMeta                                        bool
	HasMessage                                    bool
	MsgID, Model                                  string
	Content                                       int
	HasUsage                                      bool
	In, Out, CW, CR, CW1h                         int64
}

func (r *jreader) content(e *entry) bool {
	switch r.peek() {
	case '"':
		e.Content = contentString
		return r.skip()
	case '[':
		e.Content = contentTools
		return r.array(func() bool {
			if r.peek() != '{' {
				e.Content = contentBlocks
				return r.skip()
			}
			typ := ""
			ok := r.object(func(k []byte) bool {
				if string(k) == "type" {
					return r.strVal(&typ)
				}
				return r.skip()
			})
			if typ != "tool_result" {
				e.Content = contentBlocks
			}
			return ok
		})
	}
	e.Content = contentNone
	return r.skip()
}

func (r *jreader) usage(e *entry) bool {
	if r.peek() != '{' {
		return r.skip()
	}
	e.HasUsage = true
	return r.object(func(k []byte) bool {
		switch string(k) {
		case "input_tokens":
			return r.intVal(&e.In)
		case "output_tokens":
			return r.intVal(&e.Out)
		case "cache_creation_input_tokens":
			return r.intVal(&e.CW)
		case "cache_read_input_tokens":
			return r.intVal(&e.CR)
		case "cache_creation":
			if r.peek() != '{' {
				return r.skip()
			}
			return r.object(func(k []byte) bool {
				if string(k) == "ephemeral_1h_input_tokens" {
					return r.intVal(&e.CW1h)
				}
				return r.skip()
			})
		}
		return r.skip()
	})
}

func (r *jreader) message(e *entry) bool {
	if r.peek() != '{' {
		return r.skip()
	}
	e.HasMessage = true
	return r.object(func(k []byte) bool {
		switch string(k) {
		case "id":
			return r.strVal(&e.MsgID)
		case "model":
			return r.strVal(&e.Model)
		case "content":
			return r.content(e)
		case "usage":
			return r.usage(e)
		}
		return r.skip()
	})
}

// parseEntry reads one line; ok=false for anything that is not exactly
// one JSON object.
func parseEntry(b []byte) (entry, bool) {
	var e entry
	r := jreader{b: b}
	ok := r.object(func(k []byte) bool {
		switch string(k) {
		case "type":
			return r.strVal(&e.Type)
		case "timestamp":
			return r.strVal(&e.Timestamp)
		case "sessionId":
			return r.strVal(&e.SessionID)
		case "uuid":
			return r.strVal(&e.UUID)
		case "accountUuid":
			return r.strVal(&e.AccountUUID)
		case "isMeta":
			if r.peek() == 't' {
				e.IsMeta = r.literal("true")
				return e.IsMeta
			}
			e.IsMeta = false
			return r.skip()
		case "message":
			return r.message(&e)
		}
		return r.skip()
	})
	if !ok {
		return entry{}, false
	}
	r.ws()
	return e, r.i == len(r.b)
}

// validJSON reports whether b is one complete JSON value (the unterminated
// last line check).
func validJSON(b []byte) bool {
	r := jreader{b: b}
	if !r.skip() {
		return false
	}
	r.ws()
	return r.i == len(r.b)
}
