// v0.6 (SPEC-V0.6 C, D34): `cwm service …` — per-user autostart of the
// dashboard agent and the open-browser-at-login opener. The plans (files
// from templates + commands) live in internal/service; this file resolves
// the environment, applies plans and adds the agent-level steps (start one
// now, stop a stray one, rotate the token).
package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/service"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// EnvServiceDryRun makes every `cwm service` mutation print its plan only
// (used by the installer test harness).
const EnvServiceDryRun = "CWM_SERVICE_DRYRUN"

// Seams for tests: the platform view and the process signaller. Tests
// replace serviceRunner so no real systemctl/schtasks/launchctl ever runs.
var (
	serviceGOOS                       = runtime.GOOS
	serviceRunner      service.Runner = service.ExecRunner{}
	serviceLookPath                   = exec.LookPath
	serviceStat                       = os.Stat
	serviceTerminate                  = terminatePID
	serviceOpenURLPoll                = 2 * time.Second
	serviceExe                        = selfExe
)

const serviceUsage = "usage: cwm service install [--no-open] | install --open-only --url URL | uninstall | status | start | stop | rotate-token"

func serviceDryRun(e *Env) bool {
	return e.DryRun || os.Getenv(EnvServiceDryRun) == "1"
}

func serviceEnv(e *Env) (service.Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return service.Env{}, fmt.Errorf("service: home dir: %w", err)
	}
	root, err := storage.HomeDir()
	if err != nil {
		return service.Env{}, err
	}
	user := os.Getenv("USERNAME")
	if d := os.Getenv("USERDOMAIN"); d != "" && user != "" {
		user = d + `\` + user
	}
	return service.Env{
		GOOS:       serviceGOOS,
		Home:       home,
		Root:       root,
		CwmHome:    os.Getenv(storage.EnvHome),
		Exe:        serviceExe(),
		Path:       os.Getenv("PATH"),
		UID:        os.Getuid(),
		UserID:     user,
		XDGConfig:  os.Getenv("XDG_CONFIG_HOME"),
		XDGRuntime: os.Getenv("XDG_RUNTIME_DIR"),
		LookPath:   serviceLookPath,
		Stat:       serviceStat,
		Runner:     serviceRunner,
		Out:        e.Stdout,
		DryRun:     serviceDryRun(e),
	}, nil
}

func cmdService(e *Env, args []string) error {
	if len(args) == 0 {
		return errors.New(serviceUsage)
	}
	sub, rest := args[0], args[1:]
	if sub != "install" && sub != "open-url" && len(rest) > 0 {
		return fmt.Errorf("unexpected argument %q (%s)", rest[0], serviceUsage)
	}
	senv, err := serviceEnv(e)
	if err != nil {
		return err
	}
	switch sub {
	case "install":
		return serviceInstall(e, senv, rest)
	case "uninstall":
		plan, err := service.BuildUninstall(senv)
		if err != nil {
			return err
		}
		if err := senv.Apply(plan); err != nil {
			return err
		}
		if !senv.DryRun {
			recordAutostart(e, false, nil)
			fmt.Fprintln(e.Stdout, "cwm service: autostart removed (a running agent keeps running until `cwm service stop` or logout)")
		}
		return nil
	case "status":
		return serviceStatus(e, senv)
	case "start":
		return serviceStart(e, senv)
	case "stop":
		return serviceStop(e, senv)
	case "rotate-token":
		return serviceRotate(e, senv)
	case "open-url":
		return serviceOpenURL(e, rest)
	}
	return fmt.Errorf("unknown service subcommand %q (%s)", sub, serviceUsage)
}

func serviceInstall(e *Env, senv service.Env, args []string) error {
	var o service.Options
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-open":
			o.NoOpen = true
			continue
		case "--open-only":
			o.OpenOnly = true
			continue
		}
		if v, ok, err := flagValue(args, &i, "--url"); ok {
			if err != nil {
				return err
			}
			o.URL = v
			continue
		}
		return fmt.Errorf("unexpected argument %q (%s)", args[i], serviceUsage)
	}
	if o.OpenOnly && o.NoOpen {
		return errors.New("--open-only and --no-open are mutually exclusive")
	}
	plan, err := service.BuildInstall(senv, o)
	if err != nil {
		return err
	}
	if senv.DryRun {
		fmt.Fprintf(e.Stdout, "cwm service install (dry run, %s): nothing is changed\n", senv.GOOS)
	}
	if err := senv.Apply(plan); err != nil {
		return err
	}
	if senv.DryRun {
		return nil
	}
	if o.OpenOnly {
		fmt.Fprintf(e.Stdout, "cwm service: %s opens at every login\n", o.URL)
		return nil
	}
	open := !o.NoOpen
	recordAutostart(e, true, &open)
	// Make sure an agent runs now, not only after the next login (systemd
	// and launchd already started it; Windows' task only fires at logon).
	c, info, err := agent.Wait(context.Background(), senv.Root, 3*time.Second)
	if err != nil {
		if err := agentSpawn(senv.Root); err != nil {
			return err
		}
		if c, info, err = agent.Wait(context.Background(), senv.Root, agentStartWait); err != nil {
			return fmt.Errorf("autostart installed, but the agent did not answer within %s — see %s: %w", agentStartWait, agent.LogPath(senv.Root), err)
		}
	}
	fmt.Fprintf(e.Stdout, "cwm service: agent running (pid %d) at %s\n", info.PID, agent.BaseURL(agent.ProbeHost(c.Bind), c.Port))
	if o.NoOpen {
		fmt.Fprintln(e.Stdout, "cwm service: the agent starts at every login; open the dashboard with `cwm dashboard`")
	} else {
		fmt.Fprintln(e.Stdout, "cwm service: the agent starts at every login and the dashboard opens in your browser")
	}
	return nil
}

func serviceStatus(e *Env, senv service.Env) error {
	for _, it := range service.Status(senv) {
		fmt.Fprintf(e.Stdout, "%-22s %-12s %s\n", it.Name, it.State, it.Detail)
	}
	c, info, err := agent.Wait(context.Background(), senv.Root, 0)
	if err != nil {
		fmt.Fprintf(e.Stdout, "%-22s %-12s %s\n", "agent", "not running", "start it with `cwm service start` or `cwm dashboard`")
		return nil
	}
	fmt.Fprintf(e.Stdout, "%-22s %-12s pid %d, %s, version %s\n", "agent", "running", info.PID,
		agent.BaseURL(agent.ProbeHost(c.Bind), c.Port), info.Version)
	return nil
}

func serviceStart(e *Env, senv service.Env) error {
	if plan, ok := service.BuildControl(senv, service.Start); ok {
		if err := senv.Apply(plan); err != nil {
			fmt.Fprintf(e.Stderr, "cwm service start: %v — starting the agent directly\n", err)
		} else if senv.DryRun {
			return nil
		}
	} else if senv.DryRun {
		fmt.Fprintf(e.Stdout, "would start `%s agent --no-open` in the background\n", senv.Exe)
		return nil
	}
	c, info, err := agent.Wait(context.Background(), senv.Root, 3*time.Second)
	if err != nil {
		if err := agentSpawn(senv.Root); err != nil {
			return err
		}
		if c, info, err = agent.Wait(context.Background(), senv.Root, agentStartWait); err != nil {
			return fmt.Errorf("the agent did not answer within %s — see %s: %w", agentStartWait, agent.LogPath(senv.Root), err)
		}
	}
	fmt.Fprintf(e.Stdout, "cwm service: agent running (pid %d) at %s\n", info.PID, agent.BaseURL(agent.ProbeHost(c.Bind), c.Port))
	return nil
}

func serviceStop(e *Env, senv service.Env) error {
	if plan, ok := service.BuildControl(senv, service.Stop); ok {
		if err := senv.Apply(plan); err != nil {
			fmt.Fprintf(e.Stderr, "cwm service stop: %v\n", err)
		}
	}
	if senv.DryRun {
		fmt.Fprintln(e.Stdout, "would ask a still-running agent (pid from /api/agent) to exit")
		return nil
	}
	_, info, err := agent.Wait(context.Background(), senv.Root, 0)
	if err != nil {
		fmt.Fprintln(e.Stdout, "cwm service: agent stopped")
		return nil
	}
	// The agent outlived the service manager (started by `cwm dashboard`
	// or by hand): ask exactly the PID it reports to exit.
	if err := serviceTerminate(info.PID); err != nil {
		return fmt.Errorf("stop agent pid %d: %w", info.PID, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, err := agent.Wait(context.Background(), senv.Root, 0); err != nil {
			fmt.Fprintf(e.Stdout, "cwm service: agent (pid %d) stopped\n", info.PID)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("agent pid %d still answers after 5s", info.PID)
}

// terminatePID asks pid to exit: SIGTERM on POSIX (graceful shutdown),
// TerminateProcess on Windows (no signals there).
func terminatePID(pid int) error {
	if pid <= 0 || pid == os.Getpid() {
		return fmt.Errorf("refusing to signal pid %d", pid)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return p.Kill()
	}
	return p.Signal(syscall.SIGTERM)
}

func serviceRotate(e *Env, senv service.Env) error {
	if senv.DryRun {
		fmt.Fprintf(e.Stdout, "would write a new token to %s\n", agent.ConfigPath(senv.Root))
		return nil
	}
	c, err := agent.RotateToken(senv.Root)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "cwm service: new dashboard token written to %s\n", agent.ConfigPath(senv.Root))
	if _, _, err := agent.Wait(context.Background(), senv.Root, 0); err == nil {
		fmt.Fprintln(e.Stdout, "the running agent switches to it within a few seconds; old links, bookmarks and cookies stop working")
	}
	if c.Port != 0 {
		fmt.Fprintf(e.Stdout, "new sign-in link: %s\n", agent.URL(agent.ProbeHost(c.Bind), c.Port, c.Token))
	}
	return nil
}

// serviceOpenURL is the --open-only opener's entry point (hidden from
// help): wait until URL answers HTTP (a container may still be starting),
// then open it — after the timeout it opens anyway, so the user at least
// sees the browser's error page.
func serviceOpenURL(e *Env, args []string) error {
	var raw string
	timeout := 120
	for i := 0; i < len(args); i++ {
		if v, ok, err := flagValue(args, &i, "--timeout"); ok {
			if err != nil {
				return err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 3600 {
				return fmt.Errorf("--timeout needs seconds 0-3600, got %q", v)
			}
			timeout = n
			continue
		}
		if raw != "" {
			return fmt.Errorf("unexpected argument %q (usage: cwm service open-url URL [--timeout SECONDS])", args[i])
		}
		raw = args[i]
	}
	if err := service.ValidateURL(raw); err != nil {
		return err
	}
	cl := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	for {
		resp, err := cl.Get(raw)
		if err == nil {
			resp.Body.Close()
			break
		}
		if !time.Now().Before(deadline) {
			fmt.Fprintf(e.Stderr, "cwm service open-url: %s did not answer within %ds — opening anyway\n", raw, timeout)
			break
		}
		time.Sleep(serviceOpenURLPoll)
	}
	return guiOpenBrowser(raw)
}

// recordAutostart keeps [features] truthful after `cwm service
// install|uninstall` (D93): autostart, and whether the dashboard opens at
// login. Only an existing, readable config.toml is updated; the service
// change itself already happened, so a failure is a note, not an error.
func recordAutostart(e *Env, on bool, open *bool) {
	l, err := storage.NewLayout()
	if err != nil {
		return
	}
	if _, err := os.Stat(l.ConfigPath()); err != nil {
		return
	}
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		return
	}
	if cfg.Features.Autostart == on && (open == nil || cfg.Features.OpenDashboardAtLogin == *open) {
		return
	}
	cfg.Features.Autostart = on
	if open != nil {
		cfg.Features.OpenDashboardAtLogin = *open
	}
	if err := config.Save(l.ConfigPath(), cfg); err != nil {
		fmt.Fprintf(e.Stderr, "cwm service: could not record features.autostart in config.toml: %v\n", err)
	}
}

// applyAutostart makes the per-user service match features.autostart and
// features.open_dashboard_at_login (`cwm config set`, Settings): install
// with or without the login opener, or uninstall. dry only prints the
// plan. It does not start an agent: the caller usually is one, and
// systemd/launchd start it themselves.
func applyAutostart(e *Env, cfg *config.Config, dry bool) error {
	senv, err := serviceEnv(e)
	if err != nil {
		return err
	}
	senv.DryRun = senv.DryRun || dry
	var plan service.Plan
	if cfg.Features.Autostart {
		plan, err = service.BuildInstall(senv, service.Options{NoOpen: !cfg.Features.OpenDashboardAtLogin})
	} else {
		plan, err = service.BuildUninstall(senv)
	}
	if err != nil {
		return err
	}
	if err := senv.Apply(plan); err != nil {
		return err
	}
	switch {
	case senv.DryRun:
	case !cfg.Features.Autostart:
		fmt.Fprintln(e.Stdout, "  autostart removed: the agent no longer starts at login (a running agent keeps running)")
	case cfg.Features.OpenDashboardAtLogin:
		fmt.Fprintln(e.Stdout, "  autostart installed: the agent starts at login and the dashboard opens in your browser")
	default:
		fmt.Fprintln(e.Stdout, "  autostart installed: the agent starts at login (no browser)")
	}
	return nil
}
