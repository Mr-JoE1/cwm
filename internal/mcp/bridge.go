package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

// The MCP bridge (D82): workspace/mcp.json is the synced source of truth
// and every live .claude.json (the default one and each login-only dir)
// carries a copy of its mcpServers, because Claude Code reads user-scope
// servers from there. A 3-way merge against the last-synced snapshot of
// each target makes additions and deletions flow both ways: a server
// added with `claude mcp add --scope user` reaches mcp.json, a server
// removed from mcp.json leaves every .claude.json. When both sides
// changed the same server differently the workspace wins and the
// conflict is reported. No other .claude.json key is ever touched.

// BridgeTarget is one live .claude.json the bridge keeps in sync.
type BridgeTarget struct {
	Name string // shown in reports ("default", an account name)
	Path string // the .claude.json
}

// BridgeConflict is a server both sides changed differently since the
// last sync; the workspace version was kept.
type BridgeConflict struct {
	Target string `json:"target"`
	Server string `json:"server"`
}

// BridgeReport says what one bridge run changed.
type BridgeReport struct {
	WorkspaceChanged bool             `json:"workspace_changed"`
	FromLive         []string         `json:"from_live,omitempty"` // "<target>: +name" / "-name"
	Updated          []string         `json:"updated,omitempty"`   // targets whose .claude.json was rewritten
	Missing          []string         `json:"missing,omitempty"`   // targets without a .claude.json (skipped)
	Conflicts        []BridgeConflict `json:"conflicts,omitempty"`
}

// snapshot is the bridge state file: per target path, the mcpServers it
// held right after the last sync (canonical JSON per server).
type snapshot struct {
	Version int                          `json:"version"`
	Targets map[string]map[string]string `json:"targets"`
}

// SnapshotPath is the bridge's local state (never synced).
func SnapshotPath(l *storage.Layout) string {
	return filepath.Join(l.StateDir(), "mcp-bridge.json")
}

// canon renders one server value canonically (sorted keys, compact) so
// equal servers compare equal however they were formatted.
func canon(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("mcp: encode server: %w", err)
	}
	return string(bytes.TrimSpace(buf.Bytes())), nil
}

func canonMap(servers map[string]any) (map[string]string, error) {
	out := make(map[string]string, len(servers))
	for k, v := range servers {
		c, err := canon(v)
		if err != nil {
			return nil, err
		}
		out[k] = c
	}
	return out, nil
}

func decodeValue(s string) (any, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("mcp: decode server: %w", err)
	}
	return v, nil
}

// liveFile is a decoded .claude.json: raw top-level values (kept byte for
// byte) plus its mcpServers in canonical form.
type liveFile struct {
	path    string
	mode    os.FileMode
	top     map[string]json.RawMessage
	servers map[string]string
}

func readLive(path string) (*liveFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	lf := &liveFile{path: path, mode: info.Mode().Perm(), top: map[string]json.RawMessage{}, servers: map[string]string{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return lf, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&lf.top); err != nil {
		return nil, fmt.Errorf("mcp: parse %s: %w", path, err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("mcp: parse %s: unexpected data after the top-level object", path)
	}
	if raw, ok := lf.top[serversKey]; ok && string(bytes.TrimSpace(raw)) != "null" {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var servers map[string]any
		if err := d.Decode(&servers); err != nil {
			return nil, fmt.Errorf("mcp: %s: %s is not an object: %w", path, serversKey, err)
		}
		if lf.servers, err = canonMap(servers); err != nil {
			return nil, err
		}
	}
	return lf, nil
}

// writeServers replaces only the mcpServers key and writes the file
// atomically with its old permissions.
func (lf *liveFile) writeServers(servers map[string]string) error {
	obj := make(map[string]json.RawMessage, len(servers))
	for k, v := range servers {
		obj[k] = json.RawMessage(v)
	}
	raw, err := marshalNoEscape(obj, "")
	if err != nil {
		return err
	}
	lf.top[serversKey] = raw
	data, err := marshalNoEscape(lf.top, "  ")
	if err != nil {
		return err
	}
	if err := storage.WriteFileAtomic(lf.path, data, lf.mode); err != nil {
		return fmt.Errorf("mcp: write %s: %w", lf.path, err)
	}
	return nil
}

func marshalNoEscape(v any, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("mcp: encode: %w", err)
	}
	if indent == "" {
		return bytes.TrimSpace(buf.Bytes()), nil
	}
	return buf.Bytes(), nil
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// Bridge runs one 3-way sync between workspace/mcp.json and the targets.
// Pass 1 folds each target's changes since its snapshot into the
// workspace set (workspace wins conflicts); pass 2 writes the result
// into every target whose mcpServers differ and records the snapshots.
// A target without a .claude.json is skipped (Claude creates it on first
// start; the next run bridges it); an unreadable one fails the run
// before anything is written.
func Bridge(l *storage.Layout, targets []BridgeTarget) (BridgeReport, error) {
	var rep BridgeReport
	d, err := load(l)
	if err != nil {
		return rep, err
	}
	ws, err := canonMap(d.servers)
	if err != nil {
		return rep, err
	}
	snap, err := loadSnapshot(l)
	if err != nil {
		return rep, err
	}
	type tgt struct {
		BridgeTarget
		lf *liveFile
	}
	var live []tgt
	for _, t := range targets {
		lf, err := readLive(t.Path)
		if err != nil {
			if os.IsNotExist(err) {
				rep.Missing = append(rep.Missing, t.Name)
				continue
			}
			return rep, err
		}
		live = append(live, tgt{t, lf})
	}

	orig := make(map[string]string, len(ws))
	for k, v := range ws {
		orig[k] = v
	}
	for _, t := range live {
		base := snap.Targets[filepath.Clean(t.Path)]
		names := map[string]bool{}
		for k := range ws {
			names[k] = true
		}
		for k := range t.lf.servers {
			names[k] = true
		}
		for k := range base {
			names[k] = true
		}
		sorted := make([]string, 0, len(names))
		for k := range names {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, n := range sorted {
			w, wok := ws[n]
			v, lok := t.lf.servers[n]
			b, bok := base[n]
			switch {
			case wok == lok && w == v: // same on both sides
			case lok == bok && v == b: // only the workspace changed: it flows out in pass 2
			case wok == bok && w == b: // only the live file changed: take it
				if lok {
					ws[n] = v
					rep.FromLive = append(rep.FromLive, t.Name+": +"+n)
				} else {
					delete(ws, n)
					rep.FromLive = append(rep.FromLive, t.Name+": -"+n)
				}
			default: // both changed differently: the workspace wins
				rep.Conflicts = append(rep.Conflicts, BridgeConflict{Target: t.Name, Server: n})
			}
		}
	}

	if !equalMaps(orig, ws) {
		d.servers = map[string]any{}
		for k, v := range ws {
			val, err := decodeValue(v)
			if err != nil {
				return rep, err
			}
			d.servers[k] = val
		}
		if err := save(l, d); err != nil {
			return rep, err
		}
		rep.WorkspaceChanged = true
	}
	for _, t := range live {
		if !equalMaps(t.lf.servers, ws) {
			if err := t.lf.writeServers(ws); err != nil {
				return rep, err
			}
			rep.Updated = append(rep.Updated, t.Name)
		}
		cp := make(map[string]string, len(ws))
		for k, v := range ws {
			cp[k] = v
		}
		snap.Targets[filepath.Clean(t.Path)] = cp
	}
	if err := saveSnapshot(l, snap); err != nil {
		return rep, err
	}
	return rep, nil
}

// InSync reports whether every existing target already carries exactly
// the workspace servers (the health check; read-only).
func InSync(l *storage.Layout, targets []BridgeTarget) (bool, []string, error) {
	d, err := load(l)
	if err != nil {
		return false, nil, err
	}
	ws, err := canonMap(d.servers)
	if err != nil {
		return false, nil, err
	}
	var off []string
	for _, t := range targets {
		lf, err := readLive(t.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, nil, err
		}
		if !equalMaps(lf.servers, ws) {
			off = append(off, t.Name)
		}
	}
	return len(off) == 0, off, nil
}

func loadSnapshot(l *storage.Layout) (*snapshot, error) {
	s := &snapshot{Version: 1, Targets: map[string]map[string]string{}}
	data, err := os.ReadFile(SnapshotPath(l))
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("mcp: read bridge state: %w", err)
	}
	if err := json.Unmarshal(data, s); err != nil {
		// A damaged snapshot only loses deletion tracking for one run:
		// start over rather than block the bridge.
		return &snapshot{Version: 1, Targets: map[string]map[string]string{}}, nil
	}
	if s.Targets == nil {
		s.Targets = map[string]map[string]string{}
	}
	return s, nil
}

func saveSnapshot(l *storage.Layout, s *snapshot) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("mcp: encode bridge state: %w", err)
	}
	p := SnapshotPath(l)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("mcp: create %s: %w", filepath.Dir(p), err)
	}
	if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, append(data, '\n')) {
		return nil
	}
	if err := storage.WriteFileAtomic(p, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("mcp: write bridge state: %w", err)
	}
	return nil
}
