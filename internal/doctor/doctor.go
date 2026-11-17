// Package doctor is the Go port of bin/doctor.sh: local workspace health
// checks with ok:/warn:/FAIL: prefixes and a "doctor: N ok, M warn, K fail"
// summary. Local-only by default (decision D9, docs/PLAN.md): the SSH probe
// runs only with --net, and default mode must never exec ssh. Paths are
// adapted to the v0.2 layout ($CWM_HOME/workspace instead of ~/.claude-me);
// launch-dir/account awareness arrives with stage C.
package doctor

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncthing"
)

// Status is a check outcome. The JSON values are the GUI/--json contract
// (D26); the text renderer maps them onto the bash kit's prefixes.
type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Check is one health-check outcome. Detail is the message exactly as the
// text report prints it after the prefix; Lines are indented follow-up
// lines (the WIP log tail) printed under it.
type Check struct {
	Name   string   `json:"name"`
	Status Status   `json:"status"`
	Detail string   `json:"detail"`
	Lines  []string `json:"lines,omitempty"`
}

// textPrefix aligns exactly like the bash kit's ok:/warn:/FAIL: printf widths.
func textPrefix(s Status) string {
	switch s {
	case StatusOK:
		return "ok:   "
	case StatusWarn:
		return "warn: "
	default:
		return "FAIL: "
	}
}

// checkList accumulates outcomes in report order.
type checkList []Check

func (c *checkList) add(name string, st Status, format string, args ...any) {
	*c = append(*c, Check{Name: name, Status: st, Detail: fmt.Sprintf(format, args...)})
}

func (c *checkList) ok(name, format string, args ...any)   { c.add(name, StatusOK, format, args...) }
func (c *checkList) warn(name, format string, args ...any) { c.add(name, StatusWarn, format, args...) }
func (c *checkList) fail(name, format string, args ...any) { c.add(name, StatusFail, format, args...) }

// quotedStringRe extracts every JSON string literal from raw settings text,
// standing in for the bash kit's grep-based scanning (no jq allowed).
var quotedStringRe = regexp.MustCompile(`"([^"\\]*(?:\\.[^"\\]*)*)"`)

// Run executes the checks against root ("" resolves $CWM_HOME / ~/.cwm)
// and writes the text report to w. It returns false when at least one FAIL
// was recorded — the caller then exits 1, like bin/doctor.sh.
func Run(w io.Writer, root string, netProbe bool) bool {
	return Render(w, Checks(root, netProbe))
}

// Counts tallies outcomes by status.
func Counts(list []Check) (nOK, nWarn, nFail int) {
	for _, c := range list {
		switch c.Status {
		case StatusOK:
			nOK++
		case StatusWarn:
			nWarn++
		default:
			nFail++
		}
	}
	return nOK, nWarn, nFail
}

// Render writes list in the bash kit's text format plus the summary line,
// and reports whether no check failed.
func Render(w io.Writer, list []Check) bool {
	for _, c := range list {
		fmt.Fprintf(w, "%s%s\n", textPrefix(c.Status), c.Detail)
		for _, line := range c.Lines {
			fmt.Fprintf(w, "      %s\n", line)
		}
	}
	nOK, nWarn, nFail := Counts(list)
	fmt.Fprintf(w, "doctor: %d ok, %d warn, %d fail\n", nOK, nWarn, nFail)
	return nFail == 0
}

// Checks runs the 12 checks against root ("" resolves $CWM_HOME / ~/.cwm)
// and returns them in report order — the single source for the text
// report, `cwm doctor --json` and the GUI (D26). netProbe adds the SSH
// probe (check 11); without it ssh is never invoked. Check 12 (syncthing
// reachability) is a local HTTP call and always runs (warn only: sync is
// optional per machine, D17).
func Checks(root string, netProbe bool) []Check {
	r := checkList{}
	if root == "" {
		layout, err := storage.NewLayout()
		if err != nil {
			r.fail("home", "cannot resolve cwm home: %v", err)
			return r
		}
		root = layout.Root
	}
	ws := filepath.Join(root, "workspace")

	// 1. workspace dir
	if info, err := os.Stat(ws); err == nil && info.IsDir() {
		r.ok("workspace", "workspace dir exists (%s)", ws)
	} else {
		r.fail("workspace", "workspace dir missing (%s) — run `cwm init` or pair Syncthing so the folder is restored", ws)
	}

	// 2 + 3. settings.json: present, hooks wired, wired hooks exist + are
	// executable. Key presence is checked by substring, exactly like the
	// bash kit's grep: those keys are what actually matter for wiring.
	settings := filepath.Join(ws, "settings.json")
	if data, err := os.ReadFile(settings); err != nil {
		r.warn("settings", "settings.json missing — hooks not wired (generated when a launch dir is assembled; run `cwm init`)")
	} else {
		raw := string(data)
		if strings.Contains(raw, `"hooks"`) && strings.Contains(raw, `"SessionStart"`) {
			r.ok("settings", `settings.json wires hooks ("hooks" + "SessionStart" keys present)`)
		} else {
			r.warn("settings", `settings.json lacks "hooks"/"SessionStart" keys — hooks not wired`)
		}
		wired := wiredHookCommands(raw)
		for _, bin := range wired {
			path, ok := resolveHookBin(bin)
			if !ok {
				r.fail("hook-wired", "hook wired but missing: %s — re-run the cwm bootstrap or `cwm init`", path)
				continue
			}
			if info, err := os.Stat(path); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
				r.warn("hook-wired", "hook not executable: %s (chmod +x)", path)
			}
		}
		if len(wired) > 0 {
			r.ok("hooks-executable", "wired hook scripts exist and are executable")
		}
	}

	// 4. hook scripts installed (bash-kit layout; the Go kit wires hooks via
	// settings.json instead, so absence is only a warning).
	if n := countFiles(filepath.Join(ws, "hooks")); n > 0 {
		r.ok("hook-scripts", "hook scripts present in %s (%d file(s))", filepath.Join(ws, "hooks"), n)
	} else {
		r.warn("hook-scripts", "no hook scripts in %s — the Go kit wires hooks via settings.json; the bash-kit fallback needs the bootstrap", filepath.Join(ws, "hooks"))
	}

	// 5. sync ignore policy
	if fileExists(filepath.Join(ws, ".stignore")) {
		r.ok("stignore", ".stignore present (sync ignore policy installed)")
	} else {
		r.warn("stignore", ".stignore missing — sync not configured (re-run the cwm bootstrap)")
	}

	// 6. claude presence + version (pinning needs >= 2.1.234)
	if _, ver, err := storage.ClaudeBin(); err != nil {
		r.warn("claude", "claude not on PATH — install Claude Code")
	} else if ver == "" {
		r.ok("claude", "claude on PATH (version not parseable)")
	} else if storage.VersionAtLeast(ver) {
		r.ok("claude", "claude %s on PATH (>= %s: project-name pinning supported)", ver, storage.MinVersion)
	} else {
		r.warn("claude", "claude %s < %s — project-name pinning unsupported; upgrade to >= %s", ver, storage.MinVersion, storage.MinVersion)
	}

	// 7. CLAUDE_CONFIG_DIR persisted in a shell profile
	if persistedConfigDir() {
		r.ok("config-dir", "CLAUDE_CONFIG_DIR persisted in ~/.bashrc or ~/.profile")
	} else {
		r.warn("config-dir", "CLAUDE_CONFIG_DIR not exported in ~/.bashrc or ~/.profile — run `cwm init` or the cwm bootstrap")
	}

	// 8. repos.tsv: readable, every non-empty line is name<TAB>url
	if data, err := os.ReadFile(filepath.Join(ws, "repos.tsv")); err != nil {
		r.warn("repos-tsv", "repos.tsv not readable — no repos recorded yet (open a repo via cwm to create it)")
	} else {
		total, bad := 0, 0
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSuffix(line, "\r")
			if line == "" {
				continue
			}
			total++
			parts := strings.Split(line, "\t")
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				bad++
			}
		}
		if bad > 0 {
			r.warn("repos-tsv", "repos.tsv has %d malformed line(s) (expected: name<TAB>url)", bad)
		} else {
			r.ok("repos-tsv", "repos.tsv readable, %d repo(s) recorded", total)
		}
	}

	// 9. session-folder hygiene: dash-prefixed dirs are OS path slugs from
	// unpinned `claude` launches; their sessions will not resume cross-OS.
	projects := filepath.Join(ws, "projects")
	if entries, err := os.ReadDir(projects); err != nil {
		r.ok("session-folders", "no session folders yet (%s absent — created on first launch)", projects)
	} else {
		var legacy []string
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "-") {
				legacy = append(legacy, e.Name())
			}
		}
		if len(legacy) > 0 {
			r.warn("session-folders", "unpinned session folders (resume via cwm to keep sessions cross-OS; these were created by plain claude): %s", strings.Join(legacy, " "))
		} else {
			r.ok("session-folders", "no unpinned (dash-prefixed) session folders")
		}
	}

	// 10. WIP log tail
	if data, err := os.ReadFile(filepath.Join(root, "logs", "wip.log")); err != nil {
		r.ok("wip-log", "no WIP log yet (no sync/<host> pushes recorded)")
	} else {
		r.ok("wip-log", "WIP log tail (last 3 lines of logs/wip.log):")
		r[len(r)-1].Lines = tailLines(string(data), 3)
	}

	// 11. --net only: SSH probe. Never runs in default mode (D9).
	if netProbe {
		out, err := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
			"-T", "git@github.com").CombinedOutput()
		text := string(out)
		if strings.Contains(text, "successfully authenticated") {
			r.ok("ssh", "ssh git@github.com: successfully authenticated")
		} else {
			reason := firstLine(text)
			if reason == "" && err != nil {
				reason = err.Error()
			}
			r.warn("ssh", "ssh git@github.com failed — check your SSH key (%s)", reason)
		}
	}

	// 12. syncthing API reachable (optional per machine: `cwm pair` sets
	// sync up — never FAIL, D17).
	if ep, err := syncthing.Discover(); err != nil {
		r.warn("syncthing", "syncthing not reachable (optional per machine — cwm pair sets it up): %v", err)
	} else if err := syncthing.NewClient(ep).Ping(); err != nil {
		r.warn("syncthing", "syncthing not reachable (optional per machine — cwm pair sets it up): %v", err)
	} else {
		r.ok("syncthing", "syncthing API reachable at %s", ep.BaseURL)
	}

	return r
}

// wiredHookCommands returns the executable each wired hook resolves to:
// legacy bash-kit strings ("<path>/hooks/<x>.sh", the whole string is the
// path) and Go-kit commands ("<cwm binary> hook session-start|session-end",
// first field is the binary), deduped and sorted.
func wiredHookCommands(raw string) []string {
	var cmds []string
	seen := map[string]bool{}
	for _, m := range quotedStringRe.FindAllStringSubmatch(raw, -1) {
		s := m[1]
		wired := strings.Contains(s, "/hooks/") && strings.HasSuffix(s, ".sh")
		wired = wired || strings.Contains(s, "hook session-start") || strings.Contains(s, "hook session-end")
		if wired && !seen[s] {
			seen[s] = true
			if strings.HasSuffix(s, ".sh") && strings.Contains(s, "/hooks/") {
				cmds = append(cmds, storage.ExpandTilde(s))
			} else if fields := strings.Fields(s); len(fields) > 0 {
				cmds = append(cmds, storage.ExpandTilde(fields[0]))
			}
		}
	}
	sort.Strings(cmds)
	return cmds
}

// resolveHookBin resolves a hook binary to an existing path: explicit paths
// are stat'd, bare names go through PATH lookup.
func resolveHookBin(bin string) (string, bool) {
	if filepath.IsAbs(bin) || strings.HasPrefix(bin, "./") ||
		strings.HasPrefix(bin, "../") || strings.Contains(bin, string(filepath.Separator)) {
		if _, err := os.Stat(bin); err != nil {
			return bin, false
		}
		return bin, true
	}
	if path, err := exec.LookPath(bin); err == nil {
		return path, true
	}
	return bin, false
}

func persistedConfigDir() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, name := range []string{".bashrc", ".profile"} {
		data, err := os.ReadFile(filepath.Join(home, name))
		if err == nil && strings.Contains(string(data), "CLAUDE_CONFIG_DIR") {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func countFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			n++
		}
	}
	return n
}

func tailLines(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
