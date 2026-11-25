package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/mcp"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

const mcpAddUsage = "usage: cwm mcp add <name> [--env K=V]... -- <command> [args...]\n" +
	"       cwm mcp add <name> --url <url> [--type http|sse] [--header 'K: V']..."

// cmdMcp implements `cwm mcp add|list|remove`.
func cmdMcp(e *Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cwm mcp add|list|remove (see: cwm help mcp)")
	}
	l, err := claudepaths.NewLayout()
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
			"  add <name> [--env K=V]... -- <command> [args...]\n"+
			"                                      register a command (stdio) server in the workspace mcp.json\n"+
			"  add <name> --url <url> [--type http|sse] [--header 'K: V']...\n"+
			"                                      register a remote server (type defaults to http)\n"+
			"  list                                show configured servers\n"+
			"  remove <name>                       drop a server\n\n"+
			"  Other keys and fields in mcp.json are kept as they are. cwm merges the\n"+
			"  servers into every account's .claude.json on each agent tick and launch.\n")
		return nil
	default:
		return fmt.Errorf("unknown mcp subcommand %q (want add, list or remove)", args[0])
	}
}

// parseMcpAdd turns `add` arguments into a name and a server. Flags sit
// between <name> and "--"; everything after "--" is the command line,
// passed through untouched.
func parseMcpAdd(args []string) (string, mcp.Server, error) {
	var s mcp.Server
	head, tail := args, []string(nil)
	for i, a := range args {
		if a == "--" {
			head, tail = args[:i], args[i+1:]
			break
		}
	}
	hasSep := len(head) < len(args)
	var name string
	for i := 0; i < len(head); i++ {
		a := head[i]
		flag, val, inline := strings.Cut(a, "=")
		value := func() (string, error) {
			if inline {
				return val, nil
			}
			if i+1 >= len(head) {
				return "", fmt.Errorf("%s needs a value\n%s", flag, mcpAddUsage)
			}
			i++
			return head[i], nil
		}
		switch flag {
		case "--env":
			v, err := value()
			if err != nil {
				return "", s, err
			}
			k, ev, ok := strings.Cut(v, "=")
			if !ok || k == "" || strings.ContainsAny(k, " \t") {
				return "", s, fmt.Errorf("bad --env %q (want KEY=VALUE)", v)
			}
			if s.Env == nil {
				s.Env = map[string]string{}
			}
			s.Env[k] = ev
		case "--header":
			v, err := value()
			if err != nil {
				return "", s, err
			}
			k, hv, ok := strings.Cut(v, ":")
			k = strings.TrimSpace(k)
			if !ok || k == "" || strings.ContainsAny(k, " \t") {
				return "", s, fmt.Errorf("bad --header %q (want 'Name: value')", v)
			}
			if s.Headers == nil {
				s.Headers = map[string]string{}
			}
			s.Headers[k] = strings.TrimSpace(hv)
		case "--url":
			v, err := value()
			if err != nil {
				return "", s, err
			}
			s.URL = v
		case "--type":
			v, err := value()
			if err != nil {
				return "", s, err
			}
			if v != mcp.TypeHTTP && v != mcp.TypeSSE {
				return "", s, fmt.Errorf("bad --type %q (want http or sse)", v)
			}
			s.Type = v
		default:
			if strings.HasPrefix(a, "-") {
				return "", s, fmt.Errorf("unknown mcp add flag %q\n%s", a, mcpAddUsage)
			}
			if name != "" {
				return "", s, fmt.Errorf("unexpected argument %q (only <name> and flags may precede --)\n%s", a, mcpAddUsage)
			}
			name = a
		}
	}
	if name == "" {
		return "", s, fmt.Errorf("%s", mcpAddUsage)
	}
	switch {
	case s.URL != "" && hasSep:
		return "", s, fmt.Errorf("give --url or -- <command>, not both\n%s", mcpAddUsage)
	case s.URL == "" && (!hasSep || len(tail) == 0):
		return "", s, fmt.Errorf("%s", mcpAddUsage)
	case s.URL == "" && (s.Type != "" || len(s.Headers) > 0):
		return "", s, fmt.Errorf("--type and --header need --url\n%s", mcpAddUsage)
	case s.URL != "" && len(s.Env) > 0:
		return "", s, fmt.Errorf("--env applies to command servers, not --url servers\n%s", mcpAddUsage)
	}
	if s.URL == "" {
		s.Command, s.Args = tail[0], tail[1:]
	}
	return name, s, nil
}

func cmdMcpAdd(e *Env, l *storage.Layout, args []string) error {
	name, s, err := parseMcpAdd(args)
	if err != nil {
		return err
	}
	if e.DryRun {
		if s.URL != "" {
			typ := s.Type
			if typ == "" {
				typ = mcp.TypeHTTP
			}
			fmt.Fprintf(e.Stdout, "would add mcp server %s (%s %s%s)\n", name, typ, s.URL, keysNote("headers", s.Headers))
		} else {
			fmt.Fprintf(e.Stdout, "would add mcp server %s (%s %s%s)\n", name, s.Command, strings.Join(s.Args, " "), keysNote("env", s.Env))
		}
		return nil
	}
	if err := mcp.AddServer(l, name, s); err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "✓ added mcp server %s\n", name)
	return nil
}

// keysNote names the keys of m (never the values: env and headers often
// hold tokens).
func keysNote(label string, m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("; %s: %s", label, strings.Join(keys, ", "))
}
