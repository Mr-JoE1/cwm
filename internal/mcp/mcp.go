// Package mcp manages workspace/mcp.json — the MCP server configuration
// the launcher hands to claude via --mcp-config (D13). The file is managed
// by cwm but never linked into launch dirs.
//
// The file is read as raw JSON (D66): cwm only adds or drops one entry of
// mcpServers, and every other top-level key and every field of every
// server (env, url, headers, type, whatever Claude Code grows next)
// round-trips. Writes are normalised: object keys sorted, 2-space indent,
// no HTML escaping, numbers kept verbatim, a trailing newline.
package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

const serversKey = "mcpServers"

// Server types cwm writes. A command server has no type field (Claude
// Code's default, stdio).
const (
	TypeHTTP = "http"
	TypeSSE  = "sse"
)

// Server is the part of one mcpServers entry cwm writes on `mcp add`.
// Either Command (a stdio server) or URL (a remote server) is set.
type Server struct {
	Command string
	Args    []string
	Env     map[string]string

	URL     string
	Type    string // TypeHTTP (default with URL) or TypeSSE
	Headers map[string]string
}

// doc is the decoded mcp.json: the top-level object, with mcpServers
// split out. Values are generic JSON (json.Number for numbers), so
// nothing cwm does not know about is lost.
type doc struct {
	top     map[string]any
	servers map[string]any
}

func filePath(l *storage.Layout) string {
	return l.WorkspaceItemPath("mcp.json")
}

// load reads mcp.json; a missing or empty file yields an empty document,
// not an error. A file that is not a JSON object, or whose mcpServers is
// not an object, is an error: rewriting it would destroy it.
func load(l *storage.Layout) (*doc, error) {
	p := filePath(l)
	d := &doc{top: map[string]any{}, servers: map[string]any{}}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return d, nil
		}
		return nil, fmt.Errorf("mcp: read %s: %w", p, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return d, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("mcp: parse %s: %w", p, err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("mcp: parse %s: unexpected data after the top-level object", p)
	}
	top, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mcp: %s: top-level JSON value is not an object", p)
	}
	d.top = top
	if raw, present := top[serversKey]; present && raw != nil {
		servers, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mcp: %s: %s is not an object", p, serversKey)
		}
		d.servers = servers
	}
	return d, nil
}

// encode renders v the way cwm writes mcp.json.
func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil { // Encode appends the newline
		return nil, fmt.Errorf("mcp: encode: %w", err)
	}
	return buf.Bytes(), nil
}

func save(l *storage.Layout, d *doc) error {
	d.top[serversKey] = d.servers
	data, err := encode(d.top)
	if err != nil {
		return err
	}
	p := filePath(l)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("mcp: create workspace dir: %w", err)
	}
	if err := storage.WriteFileAtomic(p, data, 0o644); err != nil {
		return fmt.Errorf("mcp: write %s: %w", p, err)
	}
	return nil
}

// Add registers name → {command, args} in workspace/mcp.json, preserving
// every other configured server. The write is atomic.
func Add(l *storage.Layout, name, command string, args []string) error {
	return AddServer(l, name, Server{Command: command, Args: args})
}

// AddServer registers name → s in workspace/mcp.json. An existing entry
// of that name is replaced; every other key of the file is kept. The
// write is atomic.
func AddServer(l *storage.Layout, name string, s Server) error {
	entry, err := s.entry(name)
	if err != nil {
		return err
	}
	d, err := load(l)
	if err != nil {
		return err
	}
	d.servers[name] = entry
	return save(l, d)
}

// entry validates s and renders it as a mcpServers value.
func (s Server) entry(name string) (map[string]any, error) {
	if name == "" || strings.HasPrefix(name, "-") {
		return nil, fmt.Errorf("mcp: invalid server name %q", name)
	}
	e := map[string]any{}
	switch {
	case s.URL != "" && s.Command != "":
		return nil, fmt.Errorf("mcp: server %q: give a command or a url, not both", name)
	case s.URL != "":
		if len(s.Args) > 0 || len(s.Env) > 0 {
			return nil, fmt.Errorf("mcp: server %q: args and env apply to command servers, not url servers", name)
		}
		u, err := url.Parse(s.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("mcp: server %q: invalid url %q (want http:// or https://)", name, s.URL)
		}
		typ := s.Type
		if typ == "" {
			typ = TypeHTTP
		}
		if typ != TypeHTTP && typ != TypeSSE {
			return nil, fmt.Errorf("mcp: server %q: invalid type %q (want http or sse)", name, s.Type)
		}
		e["type"] = typ
		e["url"] = s.URL
		if len(s.Headers) > 0 {
			e["headers"] = stringMap(s.Headers)
		}
	case s.Command != "":
		if s.Type != "" || len(s.Headers) > 0 {
			return nil, fmt.Errorf("mcp: server %q: type and headers apply to url servers, not command servers", name)
		}
		e["command"] = s.Command
		if len(s.Args) > 0 {
			args := make([]any, len(s.Args))
			for i, a := range s.Args {
				args[i] = a
			}
			e["args"] = args
		}
		if len(s.Env) > 0 {
			e["env"] = stringMap(s.Env)
		}
	default:
		return nil, fmt.Errorf("mcp: server %q needs a command or a url", name)
	}
	return e, nil
}

func stringMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// List prints the server table, or a placeholder line when no servers are
// configured. A url server shows its url where a command server shows
// its command.
func List(l *storage.Layout, w io.Writer) error {
	d, err := load(l)
	if err != nil {
		return err
	}
	if len(d.servers) == 0 {
		fmt.Fprintln(w, "(no MCP servers configured)")
		return nil
	}
	names := make([]string, 0, len(d.servers))
	for n := range d.servers {
		names = append(names, n)
	}
	sort.Strings(names)
	tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "name\ttype\tcommand / url\targs\t")
	for _, n := range names {
		typ, target, args := describe(d.servers[n])
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t\n", n, typ, target, args)
	}
	return tw.Flush()
}

// describe pulls the listable fields out of a raw entry; whatever does
// not have the expected shape is shown empty, never an error.
func describe(v any) (typ, target, args string) {
	m, _ := v.(map[string]any)
	str := func(k string) string { s, _ := m[k].(string); return s }
	typ, target = str("type"), str("command")
	if u := str("url"); u != "" && target == "" {
		target = u
		if typ == "" {
			typ = TypeHTTP
		}
	}
	if typ == "" && target != "" {
		typ = "stdio"
	}
	if list, ok := m["args"].([]any); ok {
		parts := make([]string, 0, len(list))
		for _, a := range list {
			if s, ok := a.(string); ok {
				parts = append(parts, s)
			} else {
				parts = append(parts, fmt.Sprint(a))
			}
		}
		args = strings.Join(parts, " ")
	}
	return typ, target, args
}

// Remove drops the named server and keeps everything else. Removing the
// last server removes workspace/mcp.json itself, so the launcher stops
// passing --mcp-config, unless the file holds other top-level keys: then
// it is kept with an empty mcpServers.
func Remove(l *storage.Layout, name string) error {
	d, err := load(l)
	if err != nil {
		return err
	}
	if _, ok := d.servers[name]; !ok {
		return fmt.Errorf("mcp: server %q not configured", name)
	}
	delete(d.servers, name)
	_, hasServers := d.top[serversKey]
	if len(d.servers) == 0 && len(d.top) == btoi(hasServers) {
		if err := os.Remove(filePath(l)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("mcp: remove %s: %w", filePath(l), err)
		}
		return nil
	}
	return save(l, d)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
