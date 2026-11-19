// Package termout keeps cwm's status glyphs readable on Windows consoles
// that are not UTF-8 (D50).
//
// The v0.6.0 installer transcript showed "Γ£ô" where cwm printed "✓":
// PowerShell 5.1 piped cwm.exe's UTF-8 bytes and decoded them with the
// console's OEM code page (437). Go itself is fine when stdout is the
// console (it uses WriteConsoleW), but piped or redirected output is raw
// bytes that the reader decodes with whatever code page is active, and the
// legacy conhost's default font has no ✓/⚠/✗ even when they arrive intact.
//
// So on Windows, cwm decides per output handle: Unicode when the console
// output code page is 65001 (UTF-8), or when the handle is a console inside
// Windows Terminal (WT_SESSION; WriteConsoleW + font fallback). Otherwise
// the stream goes through ASCII(), which rewrites cwm's own typographic
// glyphs (✓ ⚠ ✗ — → … and friends) to ASCII and passes everything else
// through untouched. CWM_GLYPHS=ascii|unicode overrides the guess.
//
// Rejected: SetConsoleOutputCP(65001) at startup. The code page belongs to
// the console, not the process — it would stay switched in the user's
// shell after cwm exits, and it cannot help when cwm's output is a pipe
// read by a parent that already chose its decoder.
package termout

import (
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// EnvGlyphs overrides the glyph choice: "ascii" or "unicode".
const EnvGlyphs = "CWM_GLYPHS"

// cpUTF8 is the Windows code page identifier for UTF-8.
const cpUTF8 = 65001

// Probe is everything the glyph decision depends on.
type Probe struct {
	GOOS      string
	CodePage  uint32 // GetConsoleOutputCP(); 0 = no console attached
	IsConsole bool   // this handle is a console (not a pipe or file)
	WTSession string // $WT_SESSION (set inside Windows Terminal)
	Override  string // $CWM_GLYPHS
}

// Unicode reports whether a stream described by p may carry cwm's Unicode
// glyphs as-is.
func Unicode(p Probe) bool {
	switch strings.ToLower(strings.TrimSpace(p.Override)) {
	case "ascii":
		return false
	case "unicode", "utf8", "utf-8":
		return true
	}
	if p.GOOS != "windows" {
		return true
	}
	if p.CodePage == cpUTF8 {
		return true
	}
	return p.IsConsole && p.WTSession != ""
}

// Wrap returns f itself when it may carry Unicode glyphs, else an ASCII()
// writer around it.
func Wrap(f *os.File) io.Writer {
	if Unicode(probe(f)) {
		return f
	}
	return ASCII(f)
}

// replacements are the non-ASCII glyphs cwm's own messages use. Anything
// else (a user name, a path with accents) is left alone: rewriting data
// would be worse than a stray mojibake character.
var replacements = map[rune]string{
	'✓': "ok", '✔': "ok",
	'✗': "[x]", '✘': "[x]", '×': "x",
	'⚠': "!",
	'—': "-", '–': "-", '−': "-",
	'→': "->", '←': "<-", '⇒': "=>",
	'…': "...",
	'•': "*", '·': "-",
	'“': `"`, '”': `"`, '‘': "'", '’': "'",
	'≥': ">=", '≤': "<=",
	' ': " ",
}

// ASCII wraps w so cwm's glyphs come out as ASCII (see replacements). A
// multi-byte character split across two Write calls is reassembled.
func ASCII(w io.Writer) io.Writer { return &asciiWriter{w: w} }

type asciiWriter struct {
	w       io.Writer
	pending []byte // an incomplete UTF-8 sequence from the previous Write
}

func (a *asciiWriter) Write(p []byte) (int, error) {
	buf := p
	if len(a.pending) > 0 {
		buf = append(append([]byte(nil), a.pending...), p...)
		a.pending = a.pending[:0]
	}
	// Hold back a trailing partial rune (at most 3 bytes) for next time.
	if cut := partialTail(buf); cut < len(buf) {
		a.pending = append(a.pending, buf[cut:]...)
		buf = buf[:cut]
	}
	out := make([]byte, 0, len(buf))
	for i := 0; i < len(buf); {
		if buf[i] < utf8.RuneSelf {
			out = append(out, buf[i])
			i++
			continue
		}
		r, size := utf8.DecodeRune(buf[i:])
		if rep, ok := replacements[r]; ok {
			out = append(out, rep...)
		} else {
			out = append(out, buf[i:i+size]...)
		}
		i += size
	}
	if _, err := a.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// partialTail returns the index where an incomplete trailing UTF-8
// sequence starts (len(b) when the buffer ends on a rune boundary).
func partialTail(b []byte) int {
	for back := 1; back <= 3 && back <= len(b); back++ {
		c := b[len(b)-back]
		if c < utf8.RuneSelf {
			return len(b) // ASCII: boundary
		}
		if utf8.RuneStart(c) {
			if !utf8.FullRune(b[len(b)-back:]) {
				return len(b) - back
			}
			return len(b)
		}
	}
	return len(b)
}
