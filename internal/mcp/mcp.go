// Package mcp manages workspace/mcp.json — the MCP server configuration
// the launcher hands to claude via --mcp-config (D13). The file is managed
// by cwm but never linked into launch dirs.
package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

// Server is one mcpServers entry.
type Server struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// File is the mcp.json document shape.
type File struct {
	Servers map[string]Server `json:"mcpServers"`
}

func filePath(l *storage.Layout) string {
	return l.WorkspaceItemPath("mcp.json")
}

// load reads mcp.json; a missing or empty file yields an empty config, not
// an error.
func load(l *storage.Layout) (*File, error) {
	data, err := os.ReadFile(filePath(l))
	if err != nil {
		if os.IsNotExist(err) {
			return &File{}, nil
		}
		return nil, fmt.Errorf("mcp: read %s: %w", filePath(l), err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return &File{}, nil
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("mcp: parse %s: %w", filePath(l), err)
	}
	if f.Servers == nil {
		f.Servers = map[string]Server{}
	}
	return &f, nil
}

func save(l *storage.Layout, f *File) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("mcp: encode: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(filePath(l)), 0o755); err != nil {
		return fmt.Errorf("mcp: create workspace dir: %w", err)
	}
	if err := storage.WriteFileAtomic(filePath(l), data, 0o644); err != nil {
		return fmt.Errorf("mcp: write %s: %w", filePath(l), err)
	}
	return nil
}

// Add registers name → {command, args} in workspace/mcp.json, preserving
// every other configured server. The write is atomic.
func Add(l *storage.Layout, name, command string, args []string) error {
	if name == "" || strings.HasPrefix(name, "-") {
		return fmt.Errorf("mcp: invalid server name %q", name)
	}
	if command == "" {
		return fmt.Errorf("mcp: server %q needs a command", name)
	}
	f, err := load(l)
	if err != nil {
		return err
	}
	if f.Servers == nil {
		f.Servers = map[string]Server{}
	}
	f.Servers[name] = Server{Command: command, Args: args}
	return save(l, f)
}

// List prints the server table, or a placeholder line when no servers are
// configured.
func List(l *storage.Layout, w io.Writer) error {
	f, err := load(l)
	if err != nil {
		return err
	}
	if len(f.Servers) == 0 {
		fmt.Fprintln(w, "(no MCP servers configured)")
		return nil
	}
	names := make([]string, 0, len(f.Servers))
	for n := range f.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "name\tcommand\targs\t")
	for _, n := range names {
		s := f.Servers[n]
		fmt.Fprintf(tw, "%s\t%s\t%s\t\n", n, s.Command, strings.Join(s.Args, " "))
	}
	return tw.Flush()
}

// Remove drops the named server. Removing the last server removes
// workspace/mcp.json itself so the launcher stops passing --mcp-config.
func Remove(l *storage.Layout, name string) error {
	f, err := load(l)
	if err != nil {
		return err
	}
	if _, ok := f.Servers[name]; !ok {
		return fmt.Errorf("mcp: server %q not configured", name)
	}
	delete(f.Servers, name)
	if len(f.Servers) == 0 {
		if err := os.Remove(filePath(l)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("mcp: remove %s: %w", filePath(l), err)
		}
		return nil
	}
	return save(l, f)
}
