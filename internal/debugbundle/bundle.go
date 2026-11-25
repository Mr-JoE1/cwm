// Package debugbundle builds the plain-text "debug logs" report behind
// `cwm debug` and the dashboard's Copy debug logs button (D52): versions,
// agent and service state, the claude lookup, config keys, a workspace
// summary, syncthing, doctor, a log tail and recent API errors, all passed
// through the Redactor before anyone sees it. Both callers share Build;
// they differ only in the Sources they can fill (the agent knows itself
// and its API errors, the CLI probes the agent).
package debugbundle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/doctor"
	"github.com/Mr-JoE1/cwm/internal/service"
	"github.com/Mr-JoE1/cwm/internal/status"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
)

// LogLines is the total log-tail budget across every file in logs/.
const LogLines = 200

// perFileBytes bounds how much of one log is read: the tail is all we
// print, and a runaway log must not cost the agent memory.
const perFileBytes = 64 << 10

// APIError is one recorded non-2xx dashboard API answer.
type APIError struct {
	Time     time.Time
	Method   string
	Endpoint string
	Code     int
	Message  string
}

// Sources is everything Build reads. Nil funcs and zero values render as
// "unknown" / "not available" rather than failing: a debug report must
// come out even when half the machine is broken.
type Sources struct {
	Version status.Version
	// From is who built the report ("cwm debug" / "dashboard agent").
	From   string
	Layout *storage.Layout
	// Agent is the running agent (nil = none answered); AgentNote says why.
	Agent     *agent.Info
	AgentNote string
	// Service is `cwm service status`'s item list.
	Service func() []service.Item
	// Claude is the resolver (claudebin.Lookup: never writes).
	Claude func() (claudebin.Result, error)
	// SyncClient reaches syncthing (nil = not configured).
	SyncClient func() (syncengine.Client, error)
	// Doctor runs the local checks (never the SSH probe).
	Doctor func() []doctor.Check
	// APIErrors are the agent's recent API errors, oldest first. nil with
	// APIErrorsNote explains where they live (the CLI has none).
	APIErrors     []APIError
	APIErrorsNote string
	// Extra sections appended verbatim (still redacted), e.g. the agent's
	// own claude lookup as seen by `cwm debug`.
	Extra []Section

	GOOS    string
	OSInfo  func() string
	Getenv  func(string) string
	Now     func() time.Time
	Redact  Redactor
	ReadDir func(string) ([]os.DirEntry, error)
}

// Section is one titled block of the report.
type Section struct {
	Title string
	Body  string
}

// header marks a section; Extract finds sections by it.
func header(title string) string { return "== " + title + " ==" }

// Build renders and redacts the bundle.
func Build(src Sources) string {
	src.defaults()
	var b strings.Builder
	now := src.Now().UTC()
	fmt.Fprintf(&b, "cwm debug bundle — %s, built by %s\n", now.Format(time.RFC3339), src.From)
	b.WriteString("Redacted: tokens, secrets, e-mail addresses, private keys, your home folder and user name. Review before posting.\n")
	for _, s := range src.sections(now) {
		b.WriteString("\n" + header(s.Title) + "\n")
		body := strings.TrimRight(s.Body, "\n")
		if body == "" {
			body = "(nothing)"
		}
		b.WriteString(body + "\n")
	}
	return src.Redact.Redact(b.String())
}

func (src *Sources) defaults() {
	if src.GOOS == "" {
		src.GOOS = runtime.GOOS
	}
	if src.Getenv == nil {
		src.Getenv = os.Getenv
	}
	if src.Now == nil {
		src.Now = time.Now
	}
	if src.OSInfo == nil {
		src.OSInfo = OSInfo
	}
	if src.ReadDir == nil {
		src.ReadDir = os.ReadDir
	}
	if src.From == "" {
		src.From = "cwm"
	}
	if src.Redact == (Redactor{}) {
		src.Redact = DefaultRedactor()
	}
}

// DefaultRedactor redacts this process's home dir and login name.
func DefaultRedactor() Redactor {
	home, _ := os.UserHomeDir()
	name := os.Getenv("USER")
	if name == "" {
		name = os.Getenv("USERNAME")
	}
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
	}
	return Redactor{Home: home, User: name}
}

func (src Sources) sections(now time.Time) []Section {
	out := []Section{
		{"version", src.versionSection()},
		{"agent", src.agentSection(now)},
		{"service", src.serviceSection()},
		{"claude", ClaudeSection(src.Claude, src.GOOS, src.Getenv, claudebin.ClearedNote(src.Layout))},
		{"config.toml", src.configSection()},
		{"workspace", src.workspaceSection()},
		{"syncthing", src.syncSection()},
		{"doctor", src.doctorSection()},
		{"logs", src.logsSection()},
		{"recent API errors", src.apiErrorsSection()},
	}
	return append(out, src.Extra...)
}

func (src Sources) versionSection() string {
	var b strings.Builder
	v := src.Version
	fmt.Fprintf(&b, "cwm:     %s (commit %s)\n", orUnknown(v.Version), orUnknown(v.Commit))
	fmt.Fprintf(&b, "os/arch: %s/%s\n", orUnknown(v.OS), orUnknown(v.Arch))
	fmt.Fprintf(&b, "go:      %s\n", runtime.Version())
	fmt.Fprintf(&b, "system:  %s\n", orUnknown(src.OSInfo()))
	for _, k := range []string{"SHELL", "TERM", "TERM_PROGRAM", "WT_SESSION", "LANG", "CWM_GLYPHS", "NO_COLOR", storage.EnvHome} {
		if val := src.Getenv(k); val != "" {
			if k == "WT_SESSION" {
				val = "set" // a GUID: presence is what matters
			}
			fmt.Fprintf(&b, "env %s=%s\n", k, val)
		}
	}
	return b.String()
}

func (src Sources) agentSection(now time.Time) string {
	if src.Agent == nil {
		return "not running" + noteSuffix(src.AgentNote)
	}
	a := src.Agent
	up := "unknown"
	if !a.Started.IsZero() {
		up = now.Sub(a.Started).Truncate(time.Second).String()
	}
	return fmt.Sprintf("pid:     %d\nport:    %d\nbind:    %s\nuptime:  %s (since %s)\nversion: %s\n",
		a.PID, a.Port, a.Bind, up, a.Started.UTC().Format(time.RFC3339), orUnknown(a.Version))
}

func (src Sources) serviceSection() string {
	if src.Service == nil {
		return "not available"
	}
	var b strings.Builder
	for _, it := range src.Service() {
		fmt.Fprintf(&b, "%-22s %-12s %s\n", it.Name, it.State, it.Detail)
	}
	return b.String()
}

// ClaudeSection renders the resolver result: where claude is, which step
// found it, whether it verified (D54) and what else was probed, whether a
// fresh shell would find it, plus the PATH entries the building process
// runs with (services often have a different PATH from the user's shell,
// D49). note is the last "cleared claude_path" line ("" = none).
func ClaudeSection(find func() (claudebin.Result, error), goos string, getenv func(string) string, note string) string {
	var b strings.Builder
	var cands []claudebin.Candidate
	if find == nil {
		b.WriteString("resolver: not available\n")
	} else if res, err := find(); err != nil {
		fmt.Fprintf(&b, "found:   no\nerror:   %v\n", err)
		var nf *claudebin.NotFoundError
		if errors.As(err, &nf) {
			cands = nf.Rejected
		}
	} else {
		src := string(res.Source)
		if l := res.Source.Label(); !strings.EqualFold(l, src) {
			src += " (" + l + ")"
		}
		fmt.Fprintf(&b, "found:   yes\npath:    %s\nsource:  %s\non PATH: %t\n", res.Path, src, res.OnPath)
		if res.Bundle != "" {
			fmt.Fprintf(&b, "bundle:  %s (looked up afresh each time, never pinned)\n", res.Bundle)
		}
		fmt.Fprintf(&b, "verified: yes, version %s\n", res.Version)
		if res.ProbeOutput != "" {
			fmt.Fprintf(&b, "probe:   %s\n", strings.ReplaceAll(res.ProbeOutput, "\n", " | "))
		}
		switch {
		case res.Source.Bundled():
			fmt.Fprintf(&b, "fix:     only a bundled copy; standalone CLI: %s\n", claudebin.InstallCommand(goos))
		case !res.OnPath:
			fmt.Fprintf(&b, "fix:     %s\n", claudebin.PathHint(goos, res.Dir()))
		}
		cands = res.Candidates
	}
	if len(cands) > 0 {
		b.WriteString("candidates probed:\n")
		for _, c := range cands {
			fmt.Fprintf(&b, "  %s [%s, on PATH: %t] %s\n", c.Path, c.Source, c.OnPath, c.Result)
		}
	}
	if note != "" {
		fmt.Fprintf(&b, "note:    %s\n", note)
	}
	if getenv != nil {
		for _, k := range []string{claudebin.EnvClaude, claudebin.EnvNoSearch} {
			if v := getenv(k); v != "" {
				fmt.Fprintf(&b, "env %s=%s\n", k, v)
			}
		}
		entries := claudebin.PathEntries(getenv("PATH"), goos)
		dups := claudebin.PathDuplicates(getenv("PATH"), goos)
		b.WriteString("PATH of this process:\n")
		for _, e := range entries {
			if e.Dup {
				b.WriteString("  " + e.Dir + "  (duplicate)\n")
			} else {
				b.WriteString("  " + e.Dir + "\n")
			}
		}
		if dups > 0 {
			fmt.Fprintf(&b, "PATH duplicates: %d (harmless; cwm does not change PATH)\n", dups)
		}
	}
	return b.String()
}

// secretKeyRe marks config keys whose values are never printed, on top
// of the redactor (which runs over the whole text anyway).
var secretKeyRe = regexp.MustCompile(`(?i)token|secret|key|password|passwd|credential|cookie|email|uuid|organization`)

func (src Sources) configSection() string {
	if src.Layout == nil {
		return "not available"
	}
	path := src.Layout.ConfigPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return path + ": missing (run `cwm init`)"
	}
	if err != nil {
		return fmt.Sprintf("%s: unreadable: %v", path, err)
	}
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return fmt.Sprintf("%s: does not parse: %v", path, err)
	}
	var lines []string
	flatten("", raw, &lines)
	sort.Strings(lines)
	return path + "\n" + strings.Join(lines, "\n")
}

// flatten renders nested TOML as dotted key = value lines, printing a
// value only when its key is not secret-looking.
func flatten(prefix string, v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flatten(key, val, out)
		}
	case []map[string]any:
		for i, m := range t {
			flatten(fmt.Sprintf("%s[%d]", prefix, i), m, out)
		}
	case []any:
		for i, e := range t {
			flatten(fmt.Sprintf("%s[%d]", prefix, i), e, out)
		}
	default:
		leaf := prefix
		if i := strings.LastIndex(leaf, "."); i >= 0 {
			leaf = leaf[i+1:]
		}
		if secretKeyRe.MatchString(leaf) {
			*out = append(*out, prefix+" = <set, not shown>")
			return
		}
		*out = append(*out, fmt.Sprintf("%s = %v", prefix, t))
	}
}

func (src Sources) workspaceSection() string {
	if src.Layout == nil {
		return "not available"
	}
	var b strings.Builder
	l := src.Layout
	fmt.Fprintf(&b, "home:        %s\n", l.Root)
	fmt.Fprintf(&b, "initialized: %t\n", status.Initialized(l))
	if list, err := status.Projects(l); err != nil {
		fmt.Fprintf(&b, "projects:    %v\n", err)
	} else {
		total := 0
		names := make([]string, 0, len(list))
		for _, p := range list {
			total += p.Sessions
			names = append(names, fmt.Sprintf("%s (%d)", p.Name, p.Sessions))
		}
		fmt.Fprintf(&b, "projects:    %d, sessions: %d\n", len(list), total)
		if len(names) > 0 {
			fmt.Fprintf(&b, "             %s\n", strings.Join(capList(names, 30), ", "))
		}
	}
	// Accounts: names and counts only. Config data, never the account
	// dirs' files (.credentials.json is not even opened).
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		fmt.Fprintf(&b, "accounts:    %v\n", err)
		return b.String()
	}
	// v0.8 (D77): where claude reads from. Paths only; the vault and the
	// live credential files are never opened here.
	mode := cfg.EffectiveMode()
	if cfg.NeedsMigration() {
		mode += " (config predates v0.8: needs migration)"
	}
	fmt.Fprintf(&b, "mode:        %s\n", mode)
	if live, err := claudepaths.LiveFor(l, cfg, ""); err == nil {
		fmt.Fprintf(&b, "root:        %s\n", live.Root)
		fmt.Fprintf(&b, "claude.json: %s\n", live.ClaudeJSON)
		fmt.Fprintf(&b, "login file:  %s\n", live.Credentials)
	}
	names := make([]string, 0, len(cfg.Accounts))
	loggedIn := 0
	for _, a := range cfg.Accounts {
		tags := []string{}
		if a.Name == cfg.DefaultAccount {
			tags = append(tags, "default")
		}
		if a.Login != nil {
			loggedIn++
		}
		// The login state (D55): read-only here, transitions are the
		// agent's to persist.
		st, _ := accounts.DeriveLogin(l, a)
		tags = append(tags, "login: "+string(st.Status))
		if st.Error != "" {
			tags = append(tags, st.Error)
		}
		names = append(names, a.Name+" ("+strings.Join(tags, ", ")+")")
	}
	fmt.Fprintf(&b, "accounts:    %d (%d with a recorded login)\n", len(cfg.Accounts), loggedIn)
	if len(names) > 0 {
		fmt.Fprintf(&b, "             %s\n", strings.Join(names, ", "))
	}
	return b.String()
}

func (src Sources) syncSection() string {
	if src.SyncClient == nil {
		return "not configured"
	}
	cl, err := src.SyncClient()
	if err != nil {
		return "not reachable: " + err.Error()
	}
	var b strings.Builder
	b.WriteString("reachable: yes\n")
	if list, err := status.Devices(cl); err != nil {
		fmt.Fprintf(&b, "devices:   %v\n", err)
	} else {
		on := 0
		for _, d := range list {
			if d.Connected {
				on++
			}
		}
		fmt.Fprintf(&b, "devices:   %d paired, %d connected\n", len(list), on)
	}
	return b.String()
}

func (src Sources) doctorSection() string {
	if src.Doctor == nil {
		return "not available"
	}
	var b bytes.Buffer
	doctor.Render(&b, src.Doctor())
	return b.String()
}

func (src Sources) logsSection() string {
	if src.Layout == nil {
		return "not available"
	}
	dir := src.Layout.LogsDir()
	ents, err := src.ReadDir(dir)
	if err != nil {
		return dir + ": " + shortErr(err)
	}
	type logFile struct {
		name string
		mod  time.Time
	}
	var files []logFile
	for _, e := range ents {
		if !e.Type().IsRegular() || strings.Contains(strings.ToLower(e.Name()), "credential") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, logFile{e.Name(), info.ModTime()})
	}
	if len(files) == 0 {
		return dir + ": no log files yet"
	}
	// Newest first: the file touched last is the most likely culprit.
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	share := max(LogLines/len(files), 20)
	left := LogLines
	var b strings.Builder
	for _, f := range files {
		if left <= 0 {
			fmt.Fprintf(&b, "--- %s (skipped: line budget used)\n", f.name)
			continue
		}
		lines, err := tailFile(filepath.Join(dir, f.name), min(share, left))
		if err != nil {
			fmt.Fprintf(&b, "--- %s: %s\n", f.name, shortErr(err))
			continue
		}
		left -= len(lines)
		fmt.Fprintf(&b, "--- %s (modified %s, last %d lines)\n", f.name, f.mod.UTC().Format(time.RFC3339), len(lines))
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

// tailFile returns the last n lines of path, reading at most perFileBytes.
func tailFile(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := info.Size() - perFileBytes
	if off < 0 {
		off = 0
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, perFileBytes))
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if off > 0 && len(lines) > 1 {
		lines = lines[1:] // the first line was cut mid-way
	}
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

func (src Sources) apiErrorsSection() string {
	if src.APIErrors == nil {
		if src.APIErrorsNote != "" {
			return src.APIErrorsNote
		}
		return "none recorded"
	}
	if len(src.APIErrors) == 0 {
		return "none since the agent started"
	}
	var b strings.Builder
	for _, e := range src.APIErrors {
		fmt.Fprintf(&b, "%s %s %s %d %s\n", e.Time.UTC().Format(time.RFC3339), e.Method, e.Endpoint, e.Code, oneLine(e.Message))
	}
	return b.String()
}

// Extract returns the body of section title from a built bundle ("" when
// absent): `cwm debug` borrows the running agent's view this way.
func Extract(bundle, title string) string {
	h := header(title) + "\n"
	i := strings.Index(bundle, h)
	if i < 0 {
		return ""
	}
	rest := bundle[i+len(h):]
	if j := strings.Index(rest, "\n== "); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimRight(rest, "\n")
}

// Lines counts the lines of a bundle (the dashboard's toast reports it).
func Lines(s string) int {
	return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1
}

func capList(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(append([]string(nil), s[:n]...), fmt.Sprintf("… %d more", len(s)-n))
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

func noteSuffix(n string) string {
	if n == "" {
		return ""
	}
	return " (" + n + ")"
}

func shortErr(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}
