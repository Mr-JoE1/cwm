package main

import (
	"os"

	cwm "github.com/Mr-JoE1/cwm/internal/cli"
)

func main() {
	code := cwm.Run(os.Args[1:], os.Stdout, os.Stderr)
	cwm.HoldOnError(code, os.Getenv, cwm.StdinIsTTY, os.Stdin, os.Stderr)
	os.Exit(code)
}
