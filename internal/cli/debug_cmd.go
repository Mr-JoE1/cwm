// v0.6.1 (D52): `cwm debug [--copy]` — the same redacted bundle as the
// dashboard's Copy debug logs button, built by internal/debugbundle.
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/debugbundle"
	"github.com/Mr-JoE1/cwm/internal/service"
	"github.com/Mr-JoE1/cwm/internal/status"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
)

const debugUsage = "usage: cwm debug [--copy]"

// Seams: tests replace these so no clipboard tool, agent or syncthing is
// ever reached.
var (
	clipGOOS     = runtime.GOOS
	clipLookPath = exec.LookPath
	clipGetenv   = os.Getenv
	// clipRun runs name args with stdin and waits for it.
	clipRun = func(name string, args []string, stdin []byte) error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Stdin = bytes.NewReader(stdin)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return fmt.Errorf("%w: %s", err, msg)
			}
			return err
		}
		return nil
	}
	// debugAgentBundle fetches the running agent's own bundle.
	debugAgentBundle = fetchAgentBundle
	debugSync        = func() (syncengine.Client, error) { return connectSyncClient(false) }
)

func cmdDebug(e *Env, args []string) error {
	copyIt := false
	for _, a := range args {
		switch a {
		case "--copy":
			copyIt = true
		default:
			return fmt.Errorf("unexpected argument %q (%s)", a, debugUsage)
		}
	}
	text, err := buildDebug(e)
	if err != nil {
		return err
	}
	if !copyIt {
		_, err := io.WriteString(e.Stdout, text)
		return err
	}
	tool, err := copyToClipboard(clipGOOS, text)
	if err != nil {
		// Best effort: the report is still useful on stdout.
		_, _ = io.WriteString(e.Stdout, text)
		fmt.Fprintf(e.Stderr, "cwm debug: could not copy to the clipboard (%v) — the report is printed above; copy it from there\n", err)
		return nil
	}
	fmt.Fprintf(e.Stdout, "cwm debug: copied %d lines to the clipboard (via %s) — paste them into a GitHub issue: https://github.com/Mr-JoE1/cwm/issues/new?template=bug_report.yml\n",
		debugbundle.Lines(text), tool)
	return nil
}

// debugBase is what only the CLI side can build: `cwm service status`'s
// items, which need the service environment. The agent gets the same
// through gui.Options.DebugSources.
func debugBase() debugbundle.Sources {
	src := debugbundle.Sources{}
	if senv, err := serviceEnv(&Env{Stdout: io.Discard, Stderr: io.Discard}); err == nil {
		senv.DryRun = true // status only reads, but never let it mutate
		src.Service = func() []service.Item { return service.Status(senv) }
	}
	return src
}

func buildDebug(e *Env) (string, error) {
	layout, err := storage.NewLayout()
	if err != nil {
		return "", err
	}
	src := debugBase()
	src.From = "cwm debug"
	src.Version = versionInfo()
	src.Layout = layout
	src.Claude = func() (claudebin.Result, error) { return claudebin.Lookup(layout) }
	src.SyncClient = debugSync
	src.Doctor = func() []status.Check { return status.Doctor(layout.Root, false) }
	c, info, err := agent.Wait(context.Background(), layout.Root, 0)
	if err != nil {
		src.AgentNote = "no agent answered — start it with `cwm dashboard`"
		src.APIErrorsNote = "only the running agent keeps API errors (in memory); none is running"
		return debugbundle.Build(src), nil
	}
	src.Agent = &info
	// The agent's own view matters most for "works in my shell, fails in
	// the dashboard" reports: its PATH and claude lookup can differ.
	agentText, ferr := debugAgentBundle(c)
	if ferr != nil {
		src.APIErrorsNote = "the agent did not hand over its errors: " + ferr.Error()
		return debugbundle.Build(src), nil
	}
	errs := debugbundle.Extract(agentText, "recent API errors")
	if errs == "" {
		errs = "(the agent's report had no API error section — an older agent?)"
	}
	src.Extra = append(src.Extra,
		debugbundle.Section{Title: "recent API errors (from the agent)", Body: errs},
		debugbundle.Section{Title: "claude (as the agent sees it)", Body: debugbundle.Extract(agentText, "claude")})
	src.APIErrorsNote = "see \"recent API errors (from the agent)\" below"
	return debugbundle.Build(src), nil
}

// fetchAgentBundle GETs /api/debug from the agent in c (Bearer token,
// loopback, 10 s cap; the text is already redacted by the agent).
func fetchAgentBundle(c agent.Config) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host := net.JoinHostPort(agent.ProbeHost(c.Bind), strconv.Itoa(c.Port))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+"/api/debug", nil)
	if err != nil {
		return "", fmt.Errorf("debug: request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("debug: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("debug: agent answered %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("debug: read: %w", err)
	}
	return string(data), nil
}

// clipTool is one clipboard program and how to feed it.
type clipTool struct {
	name string
	args []string
	// utf16 = clip.exe: it reads the console code page unless the input
	// starts with a UTF-16LE BOM.
	utf16 bool
}

func clipTools(goos string) []clipTool {
	switch goos {
	case "windows":
		return []clipTool{{name: "clip.exe", utf16: true}}
	case "darwin":
		return []clipTool{{name: "pbcopy"}}
	}
	tools := []clipTool{}
	if clipGetenv("WAYLAND_DISPLAY") != "" {
		tools = append(tools, clipTool{name: "wl-copy"})
	}
	return append(tools,
		clipTool{name: "xclip", args: []string{"-selection", "clipboard"}},
		clipTool{name: "xsel", args: []string{"--clipboard", "--input"}})
}

// copyToClipboard tries each tool for goos in order and reports which
// one took the text.
func copyToClipboard(goos, text string) (string, error) {
	var errs []string
	for _, t := range clipTools(goos) {
		path, err := clipLookPath(t.name)
		if err != nil {
			errs = append(errs, t.name+": not installed")
			continue
		}
		data := []byte(text)
		if t.utf16 {
			data = utf16LE(text)
		}
		if err := clipRun(path, t.args, data); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", t.name, err))
			continue
		}
		return t.name, nil
	}
	if len(errs) == 0 {
		return "", errors.New("no clipboard tool for this OS")
	}
	return "", errors.New(strings.Join(errs, "; "))
}

// utf16LE encodes s as UTF-16LE with a BOM, CRLF line ends (Notepad and
// browser text boxes on Windows expect them).
func utf16LE(s string) []byte {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
	u := utf16.Encode([]rune(s))
	out := make([]byte, 0, 2+2*len(u))
	out = append(out, 0xFF, 0xFE)
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}
