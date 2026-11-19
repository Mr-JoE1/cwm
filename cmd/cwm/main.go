package main

import (
	"os"

	cwm "github.com/Mr-JoE1/cwm/internal/cli"
	"github.com/Mr-JoE1/cwm/internal/termout"
)

func main() {
	args := os.Args[1:]
	// ASCII glyphs on Windows consoles/pipes that are not UTF-8 (D50).
	stdout, stderr := termout.Wrap(os.Stdout), termout.Wrap(os.Stderr)
	if cwm.DoubleClicked(args) {
		// Started from Explorer (D35): open the dashboard (starting the
		// agent in the background) instead of flashing the help text; keep
		// the window up only when something failed.
		code := cwm.Run([]string{"dashboard"}, stdout, stderr)
		cwm.HoldOnError(code, func(string) string { return "1" }, func() bool { return true }, os.Stdin, stderr)
		os.Exit(code)
	}
	code := cwm.Run(args, stdout, stderr)
	cwm.HoldOnError(code, os.Getenv, cwm.StdinIsTTY, os.Stdin, stderr)
	os.Exit(code)
}
