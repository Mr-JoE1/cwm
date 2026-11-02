package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

func TestMcpRoundTrip(t *testing.T) {
	l := storage.NewLayoutAt(t.TempDir())

	// List before anything is configured.
	var out bytes.Buffer
	if err := List(l, &out); err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "(no MCP servers configured)") {
		t.Fatalf("empty list = %q", got)
	}

	if err := Add(l, "ctx", "npx", []string{"-y", "ctx-server"}); err != nil {
		t.Fatalf("add ctx: %v", err)
	}
	if err := Add(l, "fs", "npx", []string{"-y", "fs-server"}); err != nil {
		t.Fatalf("add fs: %v", err)
	}

	// Adding an existing name replaces it; other servers survive.
	if err := Add(l, "ctx", "docker", []string{"run", "-i", "--rm", "ctx"}); err != nil {
		t.Fatalf("replace ctx: %v", err)
	}
	data, err := os.ReadFile(l.WorkspaceItemPath("mcp.json"))
	if err != nil {
		t.Fatalf("read mcp.json: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse mcp.json: %v", err)
	}
	var servers map[string]Server
	if err := json.Unmarshal(doc["mcpServers"], &servers); err != nil {
		t.Fatalf("parse mcpServers: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("want 2 servers, got %d (%v)", len(servers), servers)
	}
	if servers["ctx"].Command != "docker" || strings.Join(servers["ctx"].Args, " ") != "run -i --rm ctx" {
		t.Fatalf("ctx not replaced: %+v", servers["ctx"])
	}
	if servers["fs"].Command != "npx" {
		t.Fatalf("fs not preserved: %+v", servers["fs"])
	}

	// Table listing.
	out.Reset()
	if err := List(l, &out); err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"ctx", "docker", "fs", "npx"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("list output %q missing %q", out.String(), want)
		}
	}

	// Remove errors when absent.
	if err := Remove(l, "nope"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("remove absent = %v, want error", err)
	}

	// Remove one of two.
	if err := Remove(l, "fs"); err != nil {
		t.Fatalf("remove fs: %v", err)
	}
	out.Reset()
	if err := List(l, &out); err != nil {
		t.Fatalf("list after remove: %v", err)
	}
	if strings.Contains(out.String(), "fs") || !strings.Contains(out.String(), "ctx") {
		t.Fatalf("list after remove = %q", out.String())
	}

	// Removing the last server removes the file entirely.
	if err := Remove(l, "ctx"); err != nil {
		t.Fatalf("remove ctx: %v", err)
	}
	if _, err := os.Stat(l.WorkspaceItemPath("mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("mcp.json still exists after removing last server: %v", err)
	}
	out.Reset()
	if err := List(l, &out); err != nil {
		t.Fatalf("list after last remove: %v", err)
	}
	if !strings.Contains(out.String(), "(no MCP servers configured)") {
		t.Fatalf("list after last remove = %q", out.String())
	}
}

func TestMcpAddValidation(t *testing.T) {
	l := storage.NewLayoutAt(t.TempDir())
	if err := Add(l, "", "cmd", nil); err == nil {
		t.Fatal("empty name accepted")
	}
	if err := Add(l, "-x", "cmd", nil); err == nil {
		t.Fatal("flag-like name accepted")
	}
	if err := Add(l, "srv", "", nil); err == nil {
		t.Fatal("empty command accepted")
	}
	if _, err := os.Stat(l.WorkspaceItemPath("mcp.json")); !os.IsNotExist(err) {
		t.Fatal("validation failures must not write mcp.json")
	}
}
