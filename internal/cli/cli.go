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
)

// Overridden at release time via -X (see .goreleaser.yaml, stage C).
var (
	Version = "dev"
	Commit  = "dev"
)

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
	"pair", "devices",
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
	{name: "list", short: "list workspace projects with session counts", stage: 'B'},
	{name: "wip", args: "[status]", short: "run the WIP auto-push now; 'wip status' lists sync branches", stage: 'B'},
	{name: "doctor", args: "[--net]", short: "run workspace health checks (--net adds an SSH probe)", stage: 'B'},
	{name: "account", args: "add|list|use|remove", short: "manage claude accounts", stage: 'C'},
	{name: "mcp", args: "add|list|remove", short: "manage workspace mcp.json servers", stage: 'C'},
	{name: "hook", args: "session-start|session-end", short: "hook entry points (wired from settings.json)", stage: 'B'},
	{name: "pair", args: "[--start] [<device-id> [name]]", short: "pair this workspace with another device (syncthing)"},
	{name: "devices", short: "list paired devices and their sync health"},
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
	cmdName, rest, err := e.parseGlobalFlags(args)
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
		case strings.HasPrefix(a, "-"):
			return "", nil, fmt.Errorf("unknown global flag %q (see: cwm help)", a)
		default:
			return a, args[i+1:], nil
		}
	}
	return "", nil, nil
}

func (e *Env) usageErr(err error) int {
	fmt.Fprintf(e.Stderr, "cwm: %v\n", err)
	fmt.Fprintln(e.Stderr, "usage: cwm <command> [flags] — try 'cwm help'")
	return ExitUsage
}

func cmdVersion(e *Env, _ []string) error {
	fmt.Fprintf(e.Stdout, "cwm %s (%s/%s, %s)\n", Version, runtime.GOOS, runtime.GOARCH, Commit)
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
	b.WriteString("  -h, --help                     this help\n\n")
	fmt.Fprintf(&b, "reserved names (a repo cannot use these):\n  %s\n", strings.Join(ReservedNames, " "))
	_, _ = e.Stdout.Write(b.Bytes())
}

func cmdArgs(c *command) string {
	if c.args == "" {
		return ""
	}
	return " " + c.args
}
