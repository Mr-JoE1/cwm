package term

import (
	"errors"
	"strings"
	"testing"
)

// decodeAppleScript parses one AppleScript string literal (the inverse of
// AppleScriptString) and fails on anything else.
func decodeAppleScript(t *testing.T, lit string) string {
	t.Helper()
	if len(lit) < 2 || lit[0] != '"' || lit[len(lit)-1] != '"' {
		t.Fatalf("not a string literal: %q", lit)
	}
	var b strings.Builder
	body := lit[1 : len(lit)-1]
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch c {
		case '"':
			t.Fatalf("unescaped quote at %d in %q", i, lit)
		case '\\':
			i++
			if i >= len(body) || (body[i] != '\\' && body[i] != '"') {
				t.Fatalf("bad escape at %d in %q", i, lit)
			}
			b.WriteByte(body[i])
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// splitShell parses a POSIX shell line made of bare words and
// single-quoted segments (all DarwinCommand emits); any other shell
// syntax fails the test, since it would mean the shell could interpret it.
func splitShell(t *testing.T, line string) []string {
	t.Helper()
	var words []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == ' ':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case c == '\'':
			inWord = true
			j := strings.IndexByte(line[i+1:], '\'')
			if j < 0 {
				t.Fatalf("unterminated quote in %q", line)
			}
			cur.WriteString(line[i+1 : i+1+j])
			i += j + 1
		case c == '\\':
			// only the '\'' idiom: an escaped single quote between quotes
			if i+1 >= len(line) || line[i+1] != '\'' {
				t.Fatalf("stray backslash in %q", line)
			}
			cur.WriteByte('\'')
			inWord = true
			i++
		case strings.ContainsRune("\"$`;&|<>(){}*?[]~#!\t\n", rune(c)):
			t.Fatalf("unquoted shell metacharacter %q in %q", c, line)
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

func TestDarwinAppleScriptQuotingRoundTrip(t *testing.T) {
	cases := [][]string{
		{"/Applications/cwm/cwm", "open", "proj", "new", "--resume", "abc-123"},
		{"/Users/Jane Doe/bin/cwm", "account", "add", "work"},
		{"/tmp/it's", `back\slash`, `"dq"`, "$(rm -rf ~)", "`id`", "a;b|c&d", "", "%PATH%", "ünï"},
		{`/x/"; do shell script "id`, `\" & quit & "`},
	}
	for _, argv := range cases {
		spec, err := Resolve(fakeEnv("darwin", map[string]string{"osascript": "/usr/bin/osascript"}, nil), argv)
		if err != nil {
			t.Fatalf("%q: %v", argv, err)
		}
		if spec.Path != "/usr/bin/osascript" || len(spec.Args) != 4 || spec.Args[0] != "-e" || spec.Args[2] != "-e" ||
			spec.Args[3] != `tell application "Terminal" to activate` {
			t.Fatalf("spec = %+v", spec)
		}
		const prefix = `tell application "Terminal" to do script `
		if !strings.HasPrefix(spec.Args[1], prefix) {
			t.Fatalf("script = %q", spec.Args[1])
		}
		line := decodeAppleScript(t, strings.TrimPrefix(spec.Args[1], prefix))
		words := splitShell(t, line)
		want := append([]string{"env", HoldEnv + "=1"}, argv...)
		if strings.Join(words, "\x00") != strings.Join(want, "\x00") || len(words) != len(want) {
			t.Errorf("round trip:\n got %q\nwant %q\nline %q", words, want, line)
		}
	}
}

func TestDarwinRefusesControlCharacters(t *testing.T) {
	env := fakeEnv("darwin", map[string]string{"osascript": "/usr/bin/osascript"}, nil)
	if _, err := Resolve(env, []string{"cwm", "a\nb"}); err == nil || errors.Is(err, ErrNoTerminal) {
		t.Errorf("newline argument: err = %v, want a refusal", err)
	}
}

func TestAppleScriptString(t *testing.T) {
	if got := AppleScriptString(`a"b\c`); got != `"a\"b\\c"` {
		t.Errorf("AppleScriptString = %s", got)
	}
}
