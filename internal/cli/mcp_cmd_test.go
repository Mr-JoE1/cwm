package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

func mcpEnv(t *testing.T) (*Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	isolatedCWMHome(t, t.TempDir())
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA"} {
		t.Setenv(k, home)
	}
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	return &Env{Stdout: out, Stderr: errOut}, out, errOut
}

func readMcp(t *testing.T) map[string]map[string]any {
	t.Helper()
	l, err := storage.NewLayout()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(l.WorkspaceItemPath("mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Servers
}

func TestCliMcpAddEnv(t *testing.T) {
	e, out, errOut := mcpEnv(t)
	if code := e.Run([]string{"mcp", "add", "ctx", "--env", "TOKEN=a=b", "--env=DEBUG=", "--", "npx", "-y", "--env", "x"}); code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errOut.String())
	}
	s := readMcp(t)["ctx"]
	env, _ := s["env"].(map[string]any)
	if s["command"] != "npx" || env["TOKEN"] != "a=b" || env["DEBUG"] != "" || len(env) != 2 {
		t.Fatalf("server = %v", s)
	}
	// Flags after -- belong to the server's command line.
	if args, _ := s["args"].([]any); len(args) != 3 || args[1] != "--env" {
		t.Fatalf("args = %v", s["args"])
	}
	if _, ok := s["type"]; ok {
		t.Errorf("command server got a type: %v", s)
	}
	out.Reset()
	if code := e.Run([]string{"mcp", "list"}); code != ExitOK {
		t.Fatal(errOut.String())
	}
	if !strings.Contains(out.String(), "npx") || strings.Contains(out.String(), "a=b") {
		t.Errorf("list = %q", out.String())
	}
}

func TestCliMcpAddURL(t *testing.T) {
	e, out, errOut := mcpEnv(t)
	if code := e.Run([]string{"mcp", "add", "web", "--url", "https://mcp.example.com/mcp", "--header", "Authorization: Bearer tok", "--header=X-Team:core"}); code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errOut.String())
	}
	if code := e.Run([]string{"mcp", "add", "events", "--url=http://localhost:9/sse", "--type", "sse"}); code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errOut.String())
	}
	servers := readMcp(t)
	web := servers["web"]
	h, _ := web["headers"].(map[string]any)
	if web["type"] != "http" || web["url"] != "https://mcp.example.com/mcp" || h["Authorization"] != "Bearer tok" || h["X-Team"] != "core" {
		t.Fatalf("web = %v", web)
	}
	if _, ok := web["command"]; ok {
		t.Errorf("url server got a command: %v", web)
	}
	if ev := servers["events"]; ev["type"] != "sse" || ev["url"] != "http://localhost:9/sse" {
		t.Fatalf("events = %v", ev)
	}
	out.Reset()
	if code := e.Run([]string{"mcp", "list"}); code != ExitOK {
		t.Fatal(errOut.String())
	}
	for _, want := range []string{"https://mcp.example.com/mcp", "http://localhost:9/sse", "sse", "http"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list = %q, missing %q", out.String(), want)
		}
	}
	if strings.Contains(out.String(), "Bearer") {
		t.Errorf("list leaks header values: %q", out.String())
	}

	// Dry run names header keys, never values.
	out.Reset()
	dry := &Env{Stdout: out, Stderr: errOut, DryRun: true}
	if code := dry.Run([]string{"mcp", "add", "x", "--url", "https://a.example", "--header", "Authorization: Bearer zzz"}); code != ExitOK {
		t.Fatal(errOut.String())
	}
	if !strings.Contains(out.String(), "Authorization") || strings.Contains(out.String(), "zzz") {
		t.Errorf("dry run = %q", out.String())
	}
}

func TestCliMcpAddBadInput(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"ctx", "--env", "NOEQUALS", "--", "npx"}, "bad --env"},
		{[]string{"ctx", "--env", "=v", "--", "npx"}, "bad --env"},
		{[]string{"ctx", "--env", "A B=v", "--", "npx"}, "bad --env"},
		{[]string{"ctx", "--", "npx", "--env"}, ""}, // ok: after --
		{[]string{"ctx", "--env"}, "--env needs a value"},
		{[]string{"web", "--url", "https://a.example", "--header", "NoColon"}, "bad --header"},
		{[]string{"web", "--url", "https://a.example", "--type", "ws"}, "bad --type"},
		{[]string{"web", "--url", "ftp://a.example"}, "invalid url"},
		{[]string{"web", "--url", "https://a.example", "--", "npx"}, "not both"},
		{[]string{"web", "--url", "https://a.example", "--env", "A=1"}, "--env applies to command servers"},
		{[]string{"web", "--header", "A: b", "--", "npx"}, "need --url"},
		{[]string{"web", "--bogus", "--", "npx"}, "unknown mcp add flag"},
		{[]string{"a", "b", "--", "npx"}, "unexpected argument"},
		{[]string{"--", "npx"}, "usage: cwm mcp add"},
		{[]string{"ctx"}, "usage: cwm mcp add"},
	} {
		e, _, errOut := mcpEnv(t)
		code := e.Run(append([]string{"mcp", "add"}, tt.args...))
		if tt.want == "" {
			if code != ExitOK {
				t.Errorf("%v: code = %d, stderr %s", tt.args, code, errOut.String())
			}
			continue
		}
		if code != ExitError || !strings.Contains(errOut.String(), tt.want) {
			t.Errorf("%v: code = %d, stderr = %q, want %q", tt.args, code, errOut.String(), tt.want)
		}
		l, _ := storage.NewLayout()
		if _, err := os.Stat(l.WorkspaceItemPath("mcp.json")); !os.IsNotExist(err) {
			t.Errorf("%v: a rejected add wrote mcp.json", tt.args)
		}
	}
}
