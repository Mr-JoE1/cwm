// v0.6 (SPEC-V0.6 A/B, D31-D33): `cwm agent` — the always-on dashboard
// server — and `cwm dashboard`, which opens the browser at it (starting it
// in the background when needed). `cwm gui` is the foreground agent with
// the browser opened (gui_cmd.go). The handlers are internal/gui; the
// persistent token, lock, probe and spawn are internal/agent.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/gui"
	"github.com/Mr-JoE1/cwm/internal/launcher"
	"github.com/Mr-JoE1/cwm/internal/status"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
)

// Seams for tests: starting a detached agent, how long a reusing launch /
// `cwm dashboard` waits for an agent to answer, and the token-file watch
// interval.
var (
	agentSpawn = func(root string) error {
		return agent.StartDetached(root, selfExe(), []string{"agent", "--no-open"})
	}
	agentReuseWait  = 5 * time.Second
	agentStartWait  = 10 * time.Second
	agentWatchEvery = 2 * time.Second
)

// selfExe is this binary's resolved path ("cwm" if unknown).
func selfExe() string {
	p, err := os.Executable()
	if err != nil {
		return "cwm"
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// agentOpts is a parsed `cwm agent` / `cwm gui` invocation.
type agentOpts struct {
	bind       string // "" = agent.DefaultBind; only ever set by --bind
	port       int    // 0 = saved port, then 47800-47809
	open       bool   // open the browser once serving
	noOpen     bool   // --no-open: a reusing launch does not open either
	allowHosts []string
	gui        bool // `cwm gui` wording (prints the tokenized URL)
}

const agentUsage = "usage: cwm agent [--bind ADDR] [--port N] [--open|--no-open] [--allow-host H]..."

func parsePortValue(v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("--port needs a number 1-65535 (try: --port %d)", agent.PortFirst)
	}
	return n, nil
}

// flagValue handles both `--name value` and `--name=value` at args[*i].
// ok=false means args[*i] is not this flag.
func flagValue(args []string, i *int, name string) (val string, ok bool, err error) {
	a := args[*i]
	if a == name {
		if *i+1 >= len(args) {
			return "", true, fmt.Errorf("%s needs a value", name)
		}
		*i++
		return args[*i], true, nil
	}
	if strings.HasPrefix(a, name+"=") {
		return strings.TrimPrefix(a, name+"="), true, nil
	}
	return "", false, nil
}

func parseAgentArgs(args []string) (agentOpts, error) {
	var o agentOpts
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--open":
			o.open = true
			continue
		case "--no-open":
			o.noOpen = true
			continue
		}
		if v, ok, err := flagValue(args, &i, "--port"); ok {
			if err != nil {
				return o, err
			}
			if o.port, err = parsePortValue(v); err != nil {
				return o, err
			}
			continue
		}
		if v, ok, err := flagValue(args, &i, "--bind"); ok {
			if err != nil {
				return o, err
			}
			if err := agent.ValidBind(v); err != nil {
				return o, err
			}
			o.bind = v
			continue
		}
		if v, ok, err := flagValue(args, &i, "--allow-host"); ok {
			if err != nil {
				return o, err
			}
			if v == "" || strings.ContainsAny(v, "/ \t\r\n") {
				return o, fmt.Errorf("--allow-host needs a host or host:port, got %q", v)
			}
			o.allowHosts = append(o.allowHosts, v)
			continue
		}
		return o, fmt.Errorf("unexpected argument %q (%s)", a, agentUsage)
	}
	if o.open && o.noOpen {
		return o, errors.New("--open and --no-open are mutually exclusive")
	}
	return o, nil
}

func cmdAgent(e *Env, args []string) error {
	o, err := parseAgentArgs(args)
	if err != nil {
		return err
	}
	return runAgent(e, o)
}

// runAgent is the agent lifecycle shared by `cwm agent` and `cwm gui`:
// lock → (reuse a running agent | bind, persist port, serve until signal).
func runAgent(e *Env, o agentOpts) error {
	name := "cwm agent"
	if o.gui {
		name = "cwm gui"
	}
	layout, err := storage.NewLayout()
	if err != nil {
		return err
	}
	root := layout.Root
	lock, err := agent.Acquire(root)
	if errors.Is(err, agent.ErrLocked) {
		return reuseAgent(e, name, root, o)
	}
	if err != nil {
		return err
	}
	defer lock.Release()

	cfg, err := agent.EnsureToken(root)
	if err != nil {
		return err
	}
	reposRoot, err := launcher.ReposRoot()
	if err != nil {
		return fmt.Errorf("repos root: %w", err)
	}
	bind := agent.DefaultBind
	if o.bind != "" {
		bind = o.bind // D32: a non-loopback bind only ever comes from the flag
	}
	ln, err := agent.Listen(bind, agent.Candidates(o.port, cfg.Port))
	if err != nil {
		return err
	}
	// gui.Serve closes ln on shutdown; only the early-error paths close it here.
	port := gui.Port(ln)
	cfg.Port, cfg.Bind, cfg.PID, cfg.Started = port, bind, os.Getpid(), time.Now().UTC().Truncate(time.Second)
	if err := agent.Save(root, cfg); err != nil {
		_ = ln.Close()
		return err
	}
	srv, err := gui.New(gui.Options{
		Version:   status.Version{Version: Version, Commit: Commit, OS: runtime.GOOS, Arch: runtime.GOARCH},
		Layout:    layout,
		ReposRoot: reposRoot,
		SyncClient: func() (syncengine.Client, error) {
			return connectSyncClient(false) // the GUI never spawns syncthing
		},
		Launcher:         guiLauncher,
		DebugSources:     debugBase,
		CwmPath:          guiCwmPath(),
		Reserved:         IsReserved,
		Bind:             bind,
		AllowHosts:       o.allowHosts,
		PersistentCookie: true,
		StatsAccounts:    func() map[string]string { return statsAccountMap(layout) },
	}, port, cfg.Token)
	if err != nil {
		_ = ln.Close()
		return err
	}

	url := srv.URL()
	base := agent.BaseURL(agent.ProbeHost(bind), port)
	if o.gui {
		fmt.Fprintf(e.Stdout, "cwm gui (beta): serving on %s\n", url)
		fmt.Fprintln(e.Stdout, "bound to 127.0.0.1 only; the token in the URL is your dashboard key (persistent; rotate with `cwm service rotate-token`) — press Ctrl-C to stop")
	} else {
		// The token never goes to the log (journald / launchd / agent.log).
		fmt.Fprintf(e.Stdout, "%s %s: serving on %s (pid %d) — open it with `cwm dashboard`\n", name, Version, base, os.Getpid())
	}
	if !agent.IsLoopback(bind) {
		fmt.Fprintf(e.Stderr, "%s: WARNING: bound to %s — reachable from the network over plain HTTP; "+
			"only the token protects it. Put TLS (a reverse proxy) in front and pass --allow-host for its name.\n", name, bind)
	}
	if o.open {
		if err := guiOpenBrowser(url); err != nil {
			fmt.Fprintf(e.Stderr, "%s: could not open a browser (%v) — run `cwm dashboard --no-open` for the URL\n", name, err)
		}
	}
	ctx, stop := guiSignalContext()
	defer stop()
	go agent.Watch(ctx, root, agentWatchEvery, func(c agent.Config) {
		if c.Token != srv.Token() {
			if err := srv.SetToken(c.Token); err == nil {
				fmt.Fprintf(e.Stdout, "%s: dashboard token rotated — old links and cookies are now invalid\n", name)
			}
		}
	})
	if err := gui.Serve(ctx, ln, srv); err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "%s: stopped\n", name)
	return nil
}

// reuseAgent is the second-launch path: another agent holds the lock, so
// point the user (and the browser) at it and exit 0.
func reuseAgent(e *Env, name, root string, o agentOpts) error {
	c, info, err := agent.Wait(context.Background(), root, agentReuseWait)
	if err != nil {
		return fmt.Errorf("another cwm agent holds %s but does not answer (%v) — wait a moment, or stop it with `cwm service stop`",
			agent.LockPath(root), err)
	}
	url := agent.URL(agent.ProbeHost(c.Bind), c.Port, c.Token)
	shown := agent.BaseURL(agent.ProbeHost(c.Bind), c.Port)
	if o.gui {
		shown = url
	}
	fmt.Fprintf(e.Stdout, "%s: already running (pid %d) at %s\n", name, info.PID, shown)
	if !o.noOpen {
		if err := guiOpenBrowser(url); err != nil {
			fmt.Fprintf(e.Stderr, "%s: could not open a browser (%v) — run `cwm dashboard --no-open` for the URL\n", name, err)
		}
	}
	return nil
}

const dashboardUsage = "usage: cwm dashboard [--no-open] [--wait SECONDS]"

// cmdDashboard opens the browser at the running agent, starting one
// detached when none answers (SPEC-V0.6 B).
func cmdDashboard(e *Env, args []string) error {
	noOpen := false
	wait := 0
	for i := 0; i < len(args); i++ {
		if args[i] == "--no-open" {
			noOpen = true
			continue
		}
		if v, ok, err := flagValue(args, &i, "--wait"); ok {
			if err != nil {
				return err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 600 {
				return fmt.Errorf("--wait needs seconds 0-600, got %q", v)
			}
			wait = n
			continue
		}
		return fmt.Errorf("unexpected argument %q (%s)", args[i], dashboardUsage)
	}
	root, err := storage.HomeDir()
	if err != nil {
		return err
	}
	ctx := context.Background()
	c, info, err := agent.Wait(ctx, root, time.Duration(wait)*time.Second)
	if err != nil {
		if e.DryRun {
			fmt.Fprintf(e.Stdout, "would start `%s agent --no-open` in the background (log: %s)\n", selfExe(), agent.LogPath(root))
			return nil
		}
		fmt.Fprintln(e.Stdout, "cwm dashboard: no agent running — starting one in the background")
		if err := agentSpawn(root); err != nil {
			return err
		}
		c, info, err = agent.Wait(ctx, root, agentStartWait)
		if err != nil {
			return fmt.Errorf("the agent did not answer within %s — see %s: %w", agentStartWait, agent.LogPath(root), err)
		}
	}
	url := agent.URL(agent.ProbeHost(c.Bind), c.Port, c.Token)
	fmt.Fprintf(e.Stdout, "cwm dashboard: agent pid %d at %s\n", info.PID, agent.BaseURL(agent.ProbeHost(c.Bind), c.Port))
	if noOpen {
		fmt.Fprintf(e.Stdout, "sign-in link: %s\n", url)
		return nil
	}
	if err := guiOpenBrowser(url); err != nil {
		fmt.Fprintf(e.Stderr, "cwm dashboard: could not open a browser (%v) — open this link: %s\n", err, url)
	}
	return nil
}
