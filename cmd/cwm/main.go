package main

import (
	"os"

	cwm "github.com/Mr-JoE1/cwm/internal/cli"
)

func main() {
	args := os.Args[1:]
	if cwm.DoubleClicked(args) {
		// Started from Explorer (D35): open the dashboard (starting the
		// agent in the background) instead of flashing the help text; keep
		// the window up only when something failed.
		code := cwm.Run([]string{"dashboard"}, os.Stdout, os.Stderr)
		cwm.HoldOnError(code, func(string) string { return "1" }, func() bool { return true }, os.Stdin, os.Stderr)
		os.Exit(code)
	}
	code := cwm.Run(args, os.Stdout, os.Stderr)
	cwm.HoldOnError(code, os.Getenv, cwm.StdinIsTTY, os.Stdin, os.Stderr)
	os.Exit(code)
}
