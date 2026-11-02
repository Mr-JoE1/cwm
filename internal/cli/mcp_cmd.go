package cli

import (
	"fmt"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/mcp"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// cmdMcp implements `cwm mcp add|list|remove`.
func cmdMcp(e *Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cwm mcp add|list|remove (see: cwm help mcp)")
	}
	l, err := storage.NewLayout()
	if err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	switch args[0] {
	case "add":
		return cmdMcpAdd(e, l, args[1:])
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("mcp list takes no arguments")
		}
		return mcp.List(l, e.Stdout)
	case "remove":
		if len(args) != 2 || strings.HasPrefix(args[1], "-") {
			return fmt.Errorf("usage: cwm mcp remove <name>")
		}
		if e.DryRun {
			fmt.Fprintf(e.Stdout, "would remove mcp server %s\n", args[1])
			return nil
		}
		if err := mcp.Remove(l, args[1]); err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "✓ removed mcp server %s\n", args[1])
		return nil
	case "-h", "--help":
		fmt.Fprintf(e.Stdout, "usage: cwm mcp add|list|remove\n\n"+
			"  add <name> -- <command> [args...]   register a server in workspace/mcp.json\n"+
			"  list                                show configured servers\n"+
			"  remove <name>                       drop a server\n")
		return nil
	default:
		return fmt.Errorf("unknown mcp subcommand %q (want add, list or remove)", args[0])
	}
}

func cmdMcpAdd(e *Env, l *storage.Layout, args []string) error {
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep <= 0 || sep+1 >= len(args) {
		return fmt.Errorf("usage: cwm mcp add <name> -- <command> [args...]")
	}
	name := args[0]
	if sep > 1 {
		return fmt.Errorf("unexpected argument %q (only <name> may precede --)", args[1])
	}
	tail := args[sep+1:]
	if e.DryRun {
		fmt.Fprintf(e.Stdout, "would add mcp server %s (%s %s)\n", name, tail[0], strings.Join(tail[1:], " "))
		return nil
	}
	if err := mcp.Add(l, name, tail[0], tail[1:]); err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "✓ added mcp server %s\n", name)
	return nil
}
