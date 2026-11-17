// Stage V0.5-A: `cwm gui` — the embedded read-only dashboard (SPEC-V0.5,
// D23/D24). The server, auth and assets live in internal/gui; this file
// parses flags, wires the status data sources and owns the process
// lifecycle (print URL, open browser, Ctrl-C = graceful shutdown).
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/Mr-JoE1/cwm/internal/gui"
	"github.com/Mr-JoE1/cwm/internal/launcher"
	"github.com/Mr-JoE1/cwm/internal/status"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
)

// Seams for tests: the shutdown signal context and the browser opener.
var (
	guiSignalContext = func() (context.Context, context.CancelFunc) {
		return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	}
	guiOpenBrowser = gui.OpenBrowser
)

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

func cmdGui(e *Env, args []string) error {
	port, noOpen, err := parseGuiArgs(args)
	if err != nil {
		return err
	}
	layout, err := storage.NewLayout()
	if err != nil {
		return err
	}
	reposRoot, err := launcher.ReposRoot()
	if err != nil {
		return fmt.Errorf("repos root: %w", err)
	}
	token, err := gui.NewToken()
	if err != nil {
		return err
	}
	ln, err := gui.Listen(port)
	if err != nil {
		return err
	}
	// gui.Serve closes ln on shutdown; only the early-error path closes it here.
	srv, err := gui.New(gui.Options{
		Version:   status.Version{Version: Version, Commit: Commit, OS: runtime.GOOS, Arch: runtime.GOARCH},
		Layout:    layout,
		ReposRoot: reposRoot,
		SyncClient: func() (syncengine.Client, error) {
			return connectSyncClient(false) // the GUI never spawns syncthing
		},
	}, gui.Port(ln), token)
	if err != nil {
		_ = ln.Close()
		return err
	}

	url := srv.URL()
	fmt.Fprintf(e.Stdout, "cwm gui (beta): serving on %s\n", url)
	fmt.Fprintln(e.Stdout, "bound to 127.0.0.1 only; the token in the URL is this launch's key — press Ctrl-C to stop")
	if !noOpen {
		if err := guiOpenBrowser(url); err != nil {
			fmt.Fprintf(e.Stderr, "cwm gui: could not open a browser (%v) — open the URL above manually\n", err)
		}
	}
	ctx, stop := guiSignalContext()
	defer stop()
	if err := gui.Serve(ctx, ln, srv); err != nil {
		return err
	}
	fmt.Fprintln(e.Stdout, "cwm gui: stopped")
	return nil
}
