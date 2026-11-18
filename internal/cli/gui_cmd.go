// Stages V0.5-A/B: `cwm gui` — the embedded localhost dashboard and setup
// wizard (SPEC-V0.5, D23/D24/D27). Since v0.6 it is the foreground agent
// with the browser opened (agent_cmd.go owns the lifecycle); this file
// keeps its flag parsing, the shared seams and the terminal-window helpers.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/Mr-JoE1/cwm/internal/gui"
	"github.com/Mr-JoE1/cwm/internal/gui/term"
)

// Seams for tests: the shutdown signal context and the browser opener.
var (
	guiSignalContext = func() (context.Context, context.CancelFunc) {
		return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	}
	guiOpenBrowser = gui.OpenBrowser
	// guiLauncher opens the terminal windows behind wizard/resume (D27).
	guiLauncher term.Launcher = term.NewSystem()
)

// guiCwmPath is argv[0] for terminal commands: this very binary, so the
// window runs the same cwm (and CWM_HOME) as the dashboard.
func guiCwmPath() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "cwm"
}

const guiUsage = "usage: cwm gui [--port N] [--no-open]"

func parseGuiArgs(args []string) (port int, noOpen bool, err error) {
	parsePort := func(v string) (int, error) {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return 0, fmt.Errorf("--port needs a number 1-65535 (try: --port %d)", gui.DefaultPortFirst)
		}
		return n, nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--no-open":
			noOpen = true
		case a == "--port":
			if i+1 >= len(args) {
				return 0, false, fmt.Errorf("--port needs a value (try: --port %d)", gui.DefaultPortFirst)
			}
			i++
			if port, err = parsePort(args[i]); err != nil {
				return 0, false, err
			}
		case strings.HasPrefix(a, "--port="):
			if port, err = parsePort(strings.TrimPrefix(a, "--port=")); err != nil {
				return 0, false, err
			}
		default:
			return 0, false, fmt.Errorf("unexpected argument %q (%s)", a, guiUsage)
		}
	}
	return port, noOpen, nil
}

// cmdGui is `cwm agent --open` in the foreground with v0.5 wording (D31):
// same lock, persistent token and port logic, so a second `cwm gui` just
// opens the running dashboard. It never accepts --bind (loopback only).
func cmdGui(e *Env, args []string) error {
	port, noOpen, err := parseGuiArgs(args)
	if err != nil {
		return err
	}
	return runAgent(e, agentOpts{port: port, open: !noOpen, noOpen: noOpen, gui: true})
}

// HoldOnError keeps a terminal window that `cwm gui` opened (D27) on
// screen after a failed command: the window would otherwise close with
// the error unread. Only when the launcher asked for it (term.HoldEnv)
// and stdin is an interactive terminal.
func HoldOnError(code int, getenv func(string) string, isTTY func() bool, stdin io.Reader, stderr io.Writer) {
	if code == ExitOK || getenv(term.HoldEnv) != "1" || !isTTY() {
		return
	}
	fmt.Fprintln(stderr, "\ncwm: the command above failed — press Enter to close this window")
	_, _ = bufio.NewReader(stdin).ReadString('\n')
}

// StdinIsTTY reports whether stdin is an interactive terminal.
func StdinIsTTY() bool { return stdinIsTTY() }
