// Package cli implements cwm argument parsing, global flags, subcommand
// dispatch and help text. Stage A wires version/help; [B]/[C] commands
// report "not implemented in this stage" until their stage lands.
package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/Mr-JoE1/cwm/internal/buildinfo"
	"github.com/Mr-JoE1/cwm/internal/gui/term"
	"github.com/Mr-JoE1/cwm/internal/status"
)

// holdRequested records term.HoldFlag for HoldOnError.
var holdRequested atomic.Bool

// Legacy -X targets, still honoured (older build scripts stamp them); the
// release stamps internal/buildinfo (D69). Read them through buildInfo().
var (
	Version = "dev"
	Commit  = "dev"
)

// buildInfo resolves the binary's identity: buildinfo's ldflags, these
// legacy overrides, then the VCS data of the Go build.
func buildInfo() buildinfo.Info { return buildinfo.Resolve(Version, Commit) }

// versionInfo is buildInfo in the shape the API and the debug bundle use.
func versionInfo() status.Version {
	in := buildInfo()
	return status.Version{Version: in.Version, Commit: in.Commit, OS: in.OS, Arch: in.Arch,
		Dev: in.Dev, Dirty: in.Dirty, BuildDate: in.BuildDate, Go: in.Go}
}

// Exit codes: usage errors get their own code so scripts can distinguish
// "user typo" from "command ran and failed".
const (
	ExitOK     = 0
	ExitError  = 1
	ExitUsage  = 2
	ExitNotImp = 3
)

// ReservedNames can never be repo names: they collide with subcommands or
// with names Claude Code owns (statusline, gui). A repo literally named
// `list` must be opened via `cwm open list`.
var ReservedNames = []string{
	"init", "import", "open", "list", "wip", "doctor",
	"account", "mcp", "hook", "version", "help", "statusline", "gui",
	"pair", "devices", "sessions", "agent", "service", "dashboard", "debug", "stats",
}

// IsReserved reports whether name is reserved.
func IsReserved(name string) bool {
	for _, r := range ReservedNames {
		if r == name {
			return true
		}
	}
	return false
}

// Env carries everything a command may touch, so commands stay testable.
type Env struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	// Account is the --account global flag value ("" = default account).
	Account string
	// DryRun is the --dry-run global flag: print the plan, change nothing.
	DryRun bool

	// helpWanted records that -h/--help appeared before the subcommand.
	helpWanted bool
}

type command struct {
	name  string
	args  string
	short string
	stage byte
	run   func(e *Env, args []string) error
}

var commands = []*command{
	{name: "version", short: "print version information"},
	{name: "help", args: "[command]", short: "help for cwm or a command"},
	{name: "init", args: "[--migrate] [--account <name>] [--yes]", short: "create the cwm workspace skeleton", stage: 'C'},
	{name: "import", args: "[--from <dir>] [--ccam] [--account <name>] [--yes]", short: "import an existing claude config dir or ccam profiles", stage: 'C'},
	{name: "open", args: "<repo> [new|last]", short: "open a repo (clone on demand) and launch claude", stage: 'B'},
	{name: "list", args: "[--json]", short: "list workspace projects with session counts", stage: 'B'},
	{name: "sessions", args: "[project] [--pick [N]] [--limit K] [--json]", short: "list and resume workspace sessions"},
	{name: "wip", args: "[status] | dirty [--json]", short: "run the WIP auto-push now; 'wip status' lists sync branches, 'wip dirty' lists repos with uncommitted work", stage: 'B'},
	{name: "doctor", args: "[--net] [--json]", short: "run workspace health checks (--net adds an SSH probe)", stage: 'B'},
	{name: "account", args: "add|login|list|use|remove", short: "manage claude accounts", stage: 'C'},
	{name: "mcp", args: "add|list|remove", short: "manage workspace mcp.json servers", stage: 'C'},
	{name: "hook", args: "session-start|session-end", short: "hook entry points (wired from settings.json)", stage: 'B'},
	{name: "pair", args: "[--start] [<device-id> [name]]", short: "pair this workspace with another device (syncthing)"},
	{name: "devices", args: "[--json]", short: "list paired devices and their sync health"},
	{name: "gui", args: "[--port N] [--no-open]", short: "run the dashboard agent in the foreground and open it (beta)"},
	{name: "agent", args: "[--bind ADDR] [--port N] [--open|--no-open] [--allow-host H]", short: "run the always-on dashboard agent (one per user; a second launch opens it)"},
	{name: "dashboard", args: "[--no-open] [--wait SECONDS]", short: "open the dashboard, starting the agent in the background if needed"},
	{name: "service", args: "install|uninstall|status|start|stop|rotate-token", short: "start the agent at login and open the dashboard (per user, no admin)"},
	{name: "stats", args: "[--range 7d|30d|90d|365d|all] [--tz ZONE] [--json]", short: "usage stats from the local session transcripts: sessions, messages, tokens, streaks, est. cost"},
	{name: "debug", args: "[--copy]", short: "print a redacted debug report for bug reports (--copy puts it on the clipboard)"},
}

// Command implementations are wired in init to avoid an initialization cycle
// between the registry and the functions that consult it. Stages B and C add
// their commands here as they land.
func init() {
	for _, c := range commands {
		switch c.name {
		case "version":
			c.run = cmdVersion
		case "help":
			c.run = cmdHelp
		case "list":
			c.run = cmdList
		case "wip":
			c.run = cmdWip
		case "doctor":
			c.run = cmdDoctor
		case "hook":
			c.run = cmdHook
		case "open":
			c.run = cmdOpen
		case "init":
			c.run = initWithMigrate
		case "account":
			c.run = cmdAccount
		case "import":
			c.run = cmdImport
		case "mcp":
			c.run = cmdMcp
		case "pair":
			c.run = cmdPair
		case "devices":
			c.run = cmdDevices
		case "sessions":
			c.run = cmdSessions
		case "gui":
			c.run = cmdGui
		case "agent":
			c.run = cmdAgent
		case "dashboard":
			c.run = cmdDashboard
		case "service":
			c.run = cmdService
		case "debug":
			c.run = cmdDebug
		case "stats":
			c.run = cmdStats
		}
	}
}

func lookupCommand(name string) *command {
	for _, c := range commands {
		if c.name == name {
			return c
		}
	}
	return nil
}

// Run parses args and dispatches; it never calls os.Exit. The returned int is
// the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	e := &Env{Stdout: stdout, Stderr: stderr, Stdin: os.Stdin}
	return e.Run(args)
}

// Run executes args against env e.
func (e *Env) Run(args []string) int {
	cmdName, rest, err := e.parseGlobalFlags(normalizeFlags(args))
	if err != nil {
		return e.usageErr(err)
	}
	if e.helpWanted {
		return ExitOK
	}
	if cmdName == "" {
		e.printUsage()
		return ExitOK
	}
	cmd := lookupCommand(cmdName)
	if cmd == nil {
		// Direct launch form: `cwm <repo> [new|last] [claude args...]`.
		return e.runDirect(cmdName, rest)
	}
	if cmd.run == nil {
		fmt.Fprintf(e.Stderr, "cwm %s: not implemented in this stage (stage %s)\n", cmd.name, string(cmd.stage))
		return ExitNotImp
	}
	if err := cmd.run(e, rest); err != nil {
		var se silentExit
		if errors.As(err, &se) {
			return se.code
		}
		fmt.Fprintf(e.Stderr, "cwm %s: %v\n", cmd.name, err)
		return ExitError
	}
	return ExitOK
}

// parseGlobalFlags consumes flags that apply to every subcommand. It stops at
// the first non-flag argument (the subcommand) and returns it plus the rest.
func (e *Env) parseGlobalFlags(args []string) (string, []string, error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--help" || a == "-h":
			e.printUsage()
			e.helpWanted = true
			return "", nil, nil
		case a == "--version" || a == "-v":
			_ = cmdVersion(e, nil)
			e.helpWanted = true
			return "", nil, nil
		case a == "--account":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--account needs a value (try: --account <name>)")
			}
			i++
			e.Account = args[i]
		case strings.HasPrefix(a, "--account="):
			e.Account = strings.TrimPrefix(a, "--account=")
		case a == "--dry-run":
			e.DryRun = true
		case a == term.HoldFlag:
			// Set by the dashboard's terminal launches (D49): keep the
			// window open on failure. Process-wide, read by HoldOnError.
			holdRequested.Store(true)
		case strings.HasPrefix(a, "-"):
			return "", nil, fmt.Errorf("unknown global flag %q (see: cwm help)", a)
		default:
			return a, args[i+1:], nil
		}
	}
	return "", nil, nil
}

// passthroughCommands hand (some of) their arguments to other programs
// (claude, MCP servers), so their flags are never rewritten.
var passthroughCommands = map[string]bool{"open": true, "hook": true, "mcp": true}

// normalizeFlags rewrites single-dash long flags (-port, -no-open=x) to
// their double-dash form (D38) for global flags and for cwm's own
// subcommands. Untouched: single-letter flags (-h, -v), anything after
// "--", the direct-launch form `cwm <repo> ...` and passthrough commands,
// whose arguments belong to claude or other programs.
func normalizeFlags(args []string) []string {
	out := append([]string(nil), args...)
	i := 0
	for ; i < len(out); i++ {
		a := out[i]
		if a == "--" || !strings.HasPrefix(a, "-") {
			break
		}
		out[i] = longDash(a)
		if out[i] == "--account" {
			i++ // its value is never a flag
		}
	}
	if i >= len(out) || out[i] == "--" {
		return out
	}
	if lookupCommand(out[i]) == nil || passthroughCommands[out[i]] {
		return out
	}
	for j := i + 1; j < len(out); j++ {
		if out[j] == "--" {
			break
		}
		out[j] = longDash(out[j])
	}
	return out
}

// longDash maps "-name" / "-name=value" (name = a letter then letters,
// digits or dashes, at least two characters) to "--name..."; everything
// else is returned unchanged.
func longDash(a string) string {
	if len(a) < 3 || a[0] != '-' || a[1] == '-' {
		return a
	}
	name := a[1:]
	if k := strings.IndexByte(name, '='); k >= 0 {
		name = name[:k]
	}
	if len(name) < 2 || !(name[0] >= 'a' && name[0] <= 'z') {
		return a
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return a
		}
	}
	return "-" + a
}

func (e *Env) usageErr(err error) int {
	fmt.Fprintf(e.Stderr, "cwm: %v\n", err)
	fmt.Fprintln(e.Stderr, "usage: cwm <command> [flags] — try 'cwm help'")
	return ExitUsage
}

func cmdVersion(e *Env, _ []string) error {
	in := buildInfo()
	fmt.Fprintf(e.Stdout, "cwm %s (%s/%s, %s)\n", in.Version, runtime.GOOS, runtime.GOARCH, in.CommitLabel())
	return nil
}

func cmdHelp(e *Env, args []string) error {
	if len(args) == 0 {
		e.printUsage()
		return nil
	}
	topic := args[0]
	cmd := lookupCommand(topic)
	if cmd == nil {
		return fmt.Errorf("unknown help topic %q", topic)
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "usage: cwm %s", cmd.name)
	if cmd.args != "" {
		fmt.Fprintf(&b, " %s", cmd.args)
	}
	b.WriteString("\n\n  ")
	b.WriteString(cmd.short)
	b.WriteString(".\n")
	if cmd.run == nil {
		fmt.Fprintf(&b, "\n  (not implemented in this stage — arrives with stage %s)\n", string(cmd.stage))
	}
	_, err := e.Stdout.Write(b.Bytes())
	return err
}

func (e *Env) printUsage() {
	var b bytes.Buffer
	b.WriteString("cwm — Claude workspace manager\n\n")
	b.WriteString("usage:\n")
	b.WriteString("  cwm <command> [flags]\n")
	b.WriteString("  cwm <repo> [new|last]          open a repo (clone on demand)\n\n")
	b.WriteString("commands:\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "  %-34s %s", c.name+cmdArgs(c), c.short)
		if c.run == nil {
			fmt.Fprintf(&b, " [stage %s]", string(c.stage))
		}
		b.WriteString("\n")
	}
	b.WriteString("\nglobal flags:\n")
	b.WriteString("  --account <name>               use a specific account for this invocation\n")
	b.WriteString("  --dry-run                      mutating commands print their plan only\n")
	b.WriteString("  -v, --version                  print the version\n")
	b.WriteString("  -h, --help                     this help\n")
	b.WriteString("  (single-dash long flags such as -port 47800 are accepted too)\n\n")
	fmt.Fprintf(&b, "reserved names (a repo cannot use these):\n  %s\n", strings.Join(ReservedNames, " "))
	_, _ = e.Stdout.Write(b.Bytes())
}

func cmdArgs(c *command) string {
	if c.args == "" {
		return ""
	}
	return " " + c.args
}
