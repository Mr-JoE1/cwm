package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

// cwm's hook entries in the synced settings.json (spec "settings.json and
// hooks"): the bare command, so the same file works on every OS with cwm
// on PATH.
var cwmHooks = []struct{ Event, Command string }{
	{"SessionStart", "cwm hook session-start"},
	{"SessionEnd", "cwm hook session-end"},
}

// cwmHookRe recognises any hook command cwm ever wrote: the bare form,
// or an absolute (possibly quoted) path to a cwm binary, as v0.7 launch
// dirs had.
var cwmHookRe = regexp.MustCompile(`^"?(?:[^"]*[/\\])?cwm(?:\.exe)?"?\s+hook\s+session-(?:start|end)\s*$`)

// IsCwmHook reports whether a hook command string is one of cwm's.
func IsCwmHook(cmd string) bool { return cwmHookRe.MatchString(cmd) }

// SettingsPath is the workspace settings.json.
func SettingsPath(l *storage.Layout) string { return l.WorkspaceItemPath("settings.json") }

// EnsureHooks merges cwm's hook entries into the workspace settings.json
// idempotently: with enabled, exactly one bare `cwm hook session-start`
// (SessionStart) and `cwm hook session-end` (SessionEnd) entry exist;
// without, none do. Only cwm's own entries — recognised by the command
// string — are added, rewritten or removed; every other key and hook is
// kept. A missing file is created only when there is something to add.
// It reports whether it wrote.
func EnsureHooks(l *storage.Layout, enabled bool) (bool, error) {
	p := SettingsPath(l)
	top := map[string]any{}
	perm := os.FileMode(0o644)
	data, err := os.ReadFile(p)
	switch {
	case err == nil:
		if info, err := os.Stat(p); err == nil {
			perm = info.Mode().Perm()
		}
		if len(bytes.TrimSpace(data)) > 0 {
			dec := json.NewDecoder(bytes.NewReader(data))
			dec.UseNumber()
			if err := dec.Decode(&top); err != nil {
				return false, fmt.Errorf("workspace: parse %s (fix or remove it; cwm will not overwrite it): %w", p, err)
			}
			if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
				return false, fmt.Errorf("workspace: parse %s: unexpected data after the top-level object", p)
			}
		}
	case os.IsNotExist(err):
		if !enabled {
			return false, nil
		}
	default:
		return false, fmt.Errorf("workspace: read %s: %w", p, err)
	}

	hooks, _ := top["hooks"].(map[string]any)
	if top["hooks"] != nil && hooks == nil {
		return false, fmt.Errorf("workspace: %s: \"hooks\" is not an object; cwm will not rewrite it", p)
	}
	if hooks == nil {
		hooks = map[string]any{}
	}
	changed := false
	kept := map[string]bool{}
	want := map[string]string{}
	for _, h := range cwmHooks {
		want[h.Event] = h.Command
	}
	for event, raw := range hooks {
		groups, ok := raw.([]any)
		if !ok {
			continue // not a shape cwm writes: leave it alone
		}
		var outGroups []any
		for _, g := range groups {
			gm, ok := g.(map[string]any)
			if !ok {
				outGroups = append(outGroups, g)
				continue
			}
			entries, ok := gm["hooks"].([]any)
			if !ok {
				outGroups = append(outGroups, g)
				continue
			}
			var outEntries []any
			removed := false
			for _, e := range entries {
				em, ok := e.(map[string]any)
				cmd, _ := em["command"].(string)
				if !ok || !IsCwmHook(cmd) {
					outEntries = append(outEntries, e)
					continue
				}
				if enabled && want[event] == cmd && !kept[event] {
					kept[event] = true
					outEntries = append(outEntries, e)
					continue
				}
				removed = true
			}
			if removed {
				changed = true
				if len(outEntries) == 0 {
					continue // the group held only cwm entries
				}
				gm["hooks"] = outEntries
			}
			outGroups = append(outGroups, gm)
		}
		if len(outGroups) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = outGroups
		}
	}
	if enabled {
		for _, h := range cwmHooks {
			if kept[h.Event] {
				continue
			}
			groups, _ := hooks[h.Event].([]any)
			hooks[h.Event] = append(groups, map[string]any{
				"hooks": []any{map[string]any{"type": "command", "command": h.Command}},
			})
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	if len(hooks) == 0 {
		delete(top, "hooks")
	} else {
		top["hooks"] = hooks
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(top); err != nil {
		return false, fmt.Errorf("workspace: encode %s: %w", p, err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false, fmt.Errorf("workspace: create %s: %w", filepath.Dir(p), err)
	}
	if err := storage.WriteFileAtomic(p, buf.Bytes(), perm); err != nil {
		return false, fmt.Errorf("workspace: write %s: %w", p, err)
	}
	return true, nil
}

// HooksWired reports whether the workspace settings.json carries both of
// cwm's hooks (the health check).
func HooksWired(l *storage.Layout) bool {
	data, err := os.ReadFile(SettingsPath(l))
	if err != nil {
		return false
	}
	var top struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(data, &top) != nil {
		return false
	}
	for _, h := range cwmHooks {
		found := false
		for _, g := range top.Hooks[h.Event] {
			for _, e := range g.Hooks {
				found = found || e.Command == h.Command
			}
		}
		if !found {
			return false
		}
	}
	return true
}
