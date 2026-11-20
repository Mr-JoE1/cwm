package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func seed(t *testing.T, l *storage.Layout, data []byte) {
	t.Helper()
	if err := os.MkdirAll(l.WorkspaceDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.WorkspaceItemPath("mcp.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// What is asserted (D66):
//   - a file already in cwm's normal form (golden) survives add+remove of
//     another entry byte for byte;
//   - any other valid file (key order, indent, \u escapes) is rewritten
//     to exactly that normal form on the first add, and is stable from
//     then on: unknown top-level keys and unknown server fields are all
//     kept, numbers keep their text (2.50, 20-digit ints), <>& are not
//     HTML-escaped. Lost by normalisation only: whitespace, key order,
//     \u escapes of printable characters and duplicate keys (last wins).
func TestGoldenExtraFieldsRoundTrip(t *testing.T) {
	golden := readFile(t, filepath.Join("testdata", "extra-fields.golden.json"))
	input := readFile(t, filepath.Join("testdata", "extra-fields.input.json"))

	t.Run("normal form is byte-stable", func(t *testing.T) {
		l := storage.NewLayoutAt(t.TempDir())
		seed(t, l, golden)
		if err := AddServer(l, "tmp", Server{Command: "echo", Args: []string{"hi"}, Env: map[string]string{"A": "b"}}); err != nil {
			t.Fatal(err)
		}
		mid := readFile(t, l.WorkspaceItemPath("mcp.json"))
		if !bytes.Contains(mid, []byte(`"tmp": {`)) {
			t.Fatalf("tmp not added:\n%s", mid)
		}
		if err := AddServer(l, "tmp2", Server{URL: "https://x.example/mcp"}); err != nil {
			t.Fatal(err)
		}
		for _, n := range []string{"tmp", "tmp2"} {
			if err := Remove(l, n); err != nil {
				t.Fatal(err)
			}
		}
		if got := readFile(t, l.WorkspaceItemPath("mcp.json")); !bytes.Equal(got, golden) {
			t.Fatalf("not byte-identical after add+remove:\n--- got\n%s\n--- want\n%s", got, golden)
		}
	})

	t.Run("first rewrite normalises", func(t *testing.T) {
		l := storage.NewLayoutAt(t.TempDir())
		seed(t, l, input)
		if err := Add(l, "tmp", "echo", nil); err != nil {
			t.Fatal(err)
		}
		if err := Remove(l, "tmp"); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, l.WorkspaceItemPath("mcp.json")); !bytes.Equal(got, golden) {
			t.Fatalf("normalised file differs from golden:\n--- got\n%s\n--- want\n%s", got, golden)
		}
	})

	t.Run("input and golden are the same JSON", func(t *testing.T) {
		var a, b any
		if err := json.Unmarshal(input, &a); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(golden, &b); err != nil {
			t.Fatal(err)
		}
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if !bytes.Equal(ja, jb) {
			t.Fatalf("fixtures disagree:\n%s\n%s", ja, jb)
		}
	})
}

func TestAddServerShapes(t *testing.T) {
	l := storage.NewLayoutAt(t.TempDir())
	if err := AddServer(l, "cmd", Server{Command: "npx", Args: []string{"-y", "srv"}, Env: map[string]string{"TOKEN": "a=b", "EMPTY": ""}}); err != nil {
		t.Fatal(err)
	}
	if err := AddServer(l, "web", Server{URL: "https://mcp.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer x"}}); err != nil {
		t.Fatal(err)
	}
	if err := AddServer(l, "events", Server{URL: "http://localhost:9000/sse", Type: TypeSSE}); err != nil {
		t.Fatal(err)
	}
	want := `{
  "mcpServers": {
    "cmd": {
      "args": [
        "-y",
        "srv"
      ],
      "command": "npx",
      "env": {
        "EMPTY": "",
        "TOKEN": "a=b"
      }
    },
    "events": {
      "type": "sse",
      "url": "http://localhost:9000/sse"
    },
    "web": {
      "headers": {
        "Authorization": "Bearer x"
      },
      "type": "http",
      "url": "https://mcp.example.com/mcp"
    }
  }
}
`
	if got := string(readFile(t, l.WorkspaceItemPath("mcp.json"))); got != want {
		t.Fatalf("mcp.json:\n%s\nwant:\n%s", got, want)
	}

	var out bytes.Buffer
	if err := List(l, &out); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"web", "events"} {
		found := false
		for _, l := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(l, line+" ") && strings.Contains(l, "://") {
				found = true
			}
		}
		if !found {
			t.Errorf("list has no url for %s:\n%s", line, out.String())
		}
	}
	if !strings.Contains(out.String(), "https://mcp.example.com/mcp") || !strings.Contains(out.String(), "sse") || !strings.Contains(out.String(), "stdio") {
		t.Errorf("list:\n%s", out.String())
	}
}

func TestAddServerValidation(t *testing.T) {
	l := storage.NewLayoutAt(t.TempDir())
	for _, tt := range []struct {
		name string
		s    Server
		want string
	}{
		{"both", Server{Command: "x", URL: "https://a.b"}, "not both"},
		{"neither", Server{}, "needs a command or a url"},
		{"bad scheme", Server{URL: "ftp://a.b"}, "invalid url"},
		{"no host", Server{URL: "https://"}, "invalid url"},
		{"bad type", Server{URL: "https://a.b", Type: "ws"}, "invalid type"},
		{"env on url", Server{URL: "https://a.b", Env: map[string]string{"A": "1"}}, "command servers"},
		{"headers on cmd", Server{Command: "x", Headers: map[string]string{"A": "1"}}, "url servers"},
	} {
		if err := AddServer(l, "s", tt.s); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
	if _, err := os.Stat(l.WorkspaceItemPath("mcp.json")); !os.IsNotExist(err) {
		t.Fatal("validation failures must not write mcp.json")
	}
}

// A file cwm cannot represent is refused, never overwritten.
func TestLoadRefusesNonObjects(t *testing.T) {
	for _, data := range []string{`[]`, `{"mcpServers": []}`, `{"mcpServers": {}} {}`, `{"mcpServers": {`} {
		l := storage.NewLayoutAt(t.TempDir())
		seed(t, l, []byte(data))
		if err := Add(l, "x", "echo", nil); err == nil {
			t.Errorf("%s: add succeeded", data)
		}
		if got := string(readFile(t, l.WorkspaceItemPath("mcp.json"))); got != data {
			t.Errorf("%s: file rewritten to %s", data, got)
		}
	}
}

// Removing the last server keeps a file that still has other keys.
func TestRemoveLastKeepsOtherTopLevelKeys(t *testing.T) {
	l := storage.NewLayoutAt(t.TempDir())
	seed(t, l, []byte(`{"keep": 1, "mcpServers": {"a": {"command": "x"}}}`))
	if err := Remove(l, "a"); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"keep\": 1,\n  \"mcpServers\": {}\n}\n"
	if got := string(readFile(t, l.WorkspaceItemPath("mcp.json"))); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
