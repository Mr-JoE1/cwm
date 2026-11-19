package termout

import (
	"bytes"
	"testing"
)

func TestUnicodeDecision(t *testing.T) {
	cases := []struct {
		name string
		p    Probe
		want bool
	}{
		{"linux", Probe{GOOS: "linux"}, true},
		{"darwin", Probe{GOOS: "darwin"}, true},
		{"linux override ascii", Probe{GOOS: "linux", Override: "ascii"}, false},
		// The v0.6.0 report: PowerShell 5.1 piping cwm.exe, console CP 437.
		{"windows pipe cp437", Probe{GOOS: "windows", CodePage: 437}, false},
		{"windows pipe cp437 in WT", Probe{GOOS: "windows", CodePage: 437, WTSession: "x"}, false},
		{"windows console cp437 conhost", Probe{GOOS: "windows", CodePage: 437, IsConsole: true}, false},
		{"windows console in Windows Terminal", Probe{GOOS: "windows", CodePage: 437, IsConsole: true, WTSession: "x"}, true},
		{"windows pipe utf-8 (installer set OutputEncoding)", Probe{GOOS: "windows", CodePage: 65001}, true},
		{"windows console utf-8", Probe{GOOS: "windows", CodePage: 65001, IsConsole: true}, true},
		{"windows no console (agent log)", Probe{GOOS: "windows"}, false},
		{"windows override unicode", Probe{GOOS: "windows", CodePage: 437, Override: "Unicode"}, true},
		{"windows override utf-8", Probe{GOOS: "windows", Override: " utf-8 "}, true},
		{"windows override ascii beats utf-8", Probe{GOOS: "windows", CodePage: 65001, Override: "ascii"}, false},
		{"windows unknown override ignored", Probe{GOOS: "windows", CodePage: 65001, Override: "fancy"}, true},
	}
	for _, c := range cases {
		if got := Unicode(c.p); got != c.want {
			t.Errorf("%s: Unicode(%+v) = %v, want %v", c.name, c.p, got, c.want)
		}
	}
}

func TestASCIIRewritesGlyphs(t *testing.T) {
	var b bytes.Buffer
	w := ASCII(&b)
	in := "✓ cwm home initialized at C:\\Users\\Jürgen\\.cwm\n⚠ skip — bad → name… ✗ “x” ≥ 2\n"
	n, err := w.Write([]byte(in))
	if err != nil || n != len(in) {
		t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(in))
	}
	want := "ok cwm home initialized at C:\\Users\\Jürgen\\.cwm\n! skip - bad -> name... [x] \"x\" >= 2\n"
	if b.String() != want {
		t.Errorf("got  %q\nwant %q", b.String(), want)
	}
}

func TestASCIISplitRunes(t *testing.T) {
	var b bytes.Buffer
	w := ASCII(&b)
	in := []byte("a✓b—ü")
	for i := range in { // one byte per Write: every rune arrives in pieces
		if n, err := w.Write(in[i : i+1]); err != nil || n != 1 {
			t.Fatalf("byte %d: Write = %d, %v", i, n, err)
		}
	}
	if b.String() != "aokb-ü" {
		t.Errorf("got %q, want %q", b.String(), "aokb-ü")
	}
}

func TestASCIIPassesInvalidBytes(t *testing.T) {
	var b bytes.Buffer
	w := ASCII(&b)
	in := []byte{'x', 0xff, 0x80, 'y'}
	if _, err := w.Write(in); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.Bytes(), in) {
		t.Errorf("invalid bytes altered: %q", b.Bytes())
	}
}
