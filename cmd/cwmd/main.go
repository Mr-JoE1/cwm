// Command cwmd is the windowless cwm entry point (v0.6, D35). Release
// builds link it with -H=windowsgui, so starting it — a double-click, the
// logon task — never opens a console window. With no arguments it runs
// `cwm agent --open` (start the agent or reuse the running one, then open
// the dashboard); otherwise it runs the given cwm arguments. Output goes
// to <cwm home>/logs/agent.log; a failure is shown in a message box.
package main

import (
	"os"

	cwm "github.com/Mr-JoE1/cwm/internal/cli"
)

func main() {
	os.Exit(cwm.Daemon(os.Args[1:]))
}
