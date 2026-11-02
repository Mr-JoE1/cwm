package main

import (
	"os"

	cwm "github.com/Mr-JoE1/cwm/internal/cli"
)

func main() {
	os.Exit(cwm.Run(os.Args[1:], os.Stdout, os.Stderr))
}
