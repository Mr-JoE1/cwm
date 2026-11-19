package claudebin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ProbeTimeout bounds one `--version` probe. A var so tests can shorten
// the hang case.
var ProbeTimeout = 5 * time.Second

// probeMaxOutput is how much combined stdout+stderr a probe keeps.
const probeMaxOutput = 4 << 10

// ProbeOutputShown is how much of a probe's output reports carry.
const ProbeOutputShown = 200

// ProbeResult is what `<path> --version` said about itself.
type ProbeResult struct {
	Verified bool
	// Version is the semantic version on the Claude Code line
	// ("2.1.319", or with its pre-release/build suffix).
	Version string
	// Line is that whole line ("2.1.319 (Claude Code)").
	Line string
	// Output is the captured output (at most probeMaxOutput bytes).
	Output string
	// Reason says why it did not verify ("" when it did).
	Reason string
}

// Short is the one-line verdict reports show for a candidate.
func (p ProbeResult) Short() string {
	if p.Verified {
		return "verified: " + p.Line
	}
	return p.Reason
}

// semverRe matches a semantic version with an optional pre-release or
// build suffix: future builds may append one ("2.1.400-beta.1",
// "2.1.400+abc"), and the verifier must not care.
var semverRe = regexp.MustCompile(`\bv?(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z][0-9A-Za-z.+-]*)?)`)

// ParseVersionOutput finds the Claude Code version line in `claude
// --version` output. The real shape is `2.1.319 (Claude Code)`. Any line
// with both a semantic version and the word "claude" (any case) counts,
// so a changed suffix or added build info does not break verification,
// while an unrelated tool printing a bare version does not pass.
func ParseVersionOutput(out string) (version, line string, ok bool) {
	for l := range strings.SplitSeq(out, "\n") {
		l = strings.TrimSpace(l)
		if !strings.Contains(strings.ToLower(l), "claude") {
			continue
		}
		if m := semverRe.FindStringSubmatch(l); m != nil {
			return m[1], l, true
		}
	}
	return "", "", false
}

// capWriter keeps the first max bytes and silently drops the rest, so a
// chatty binary can neither grow memory nor block on a full pipe.
type capWriter struct {
	buf []byte
	max int
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.max - len(w.buf); room > 0 {
		if len(p) > room {
			w.buf = append(w.buf, p[:room]...)
		} else {
			w.buf = append(w.buf, p...)
		}
	}
	return len(p), nil
}

// probeCache remembers verdicts per (path, size, mtime) for the life of
// the process: the agent's state poll resolves every few seconds, and a
// file that did not change cannot change its answer.
var probeCache sync.Map

type probeKey struct {
	path  string
	size  int64
	mtime int64
}

// Probe runs `path --version` (ProbeTimeout, stdin from the null device,
// no console window) and verifies the answer: exit status 0 and a Claude
// Code version line (D54). Never panics; any failure is a Reason.
func Probe(path string) ProbeResult {
	var key probeKey
	if fi, err := os.Stat(path); err == nil {
		key = probeKey{path: path, size: fi.Size(), mtime: fi.ModTime().UnixNano()}
		if v, ok := probeCache.Load(key); ok {
			return v.(ProbeResult)
		}
	}
	res := runProbe(path)
	if key.path != "" {
		probeCache.Store(key, res)
	}
	return res
}

func runProbe(path string) ProbeResult {
	timeout := ProbeTimeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	out := &capWriter{max: probeMaxOutput}
	cmd.Stdin = nil // the null device: a probe must never wait for input
	cmd.Stdout, cmd.Stderr = out, out
	// A killed script's grandchild may still hold the pipe open; give
	// up on it after a second instead of hanging with it.
	cmd.WaitDelay = time.Second
	hideWindow(cmd)
	err := cmd.Run()
	res := ProbeResult{Output: string(out.buf)}
	var ee *exec.ExitError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.Reason = fmt.Sprintf("timed out after %s", timeout)
	case errors.As(err, &ee):
		res.Reason = fmt.Sprintf("exit status %d", ee.ExitCode())
	case err != nil:
		res.Reason = "could not run: " + err.Error()
	default:
		if v, line, ok := ParseVersionOutput(res.Output); ok {
			res.Verified, res.Version, res.Line = true, v, line
		} else if strings.TrimSpace(res.Output) == "" {
			res.Reason = "no output from --version"
		} else {
			res.Reason = "no Claude Code version line in its --version output"
		}
	}
	return res
}

// shorten cuts s to at most n runes, marking the cut.
func shorten(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
