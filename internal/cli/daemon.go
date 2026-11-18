package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// Daemon is cwmd's main (D35): no console, output to the agent log, errors
// in a message box. On Windows it runs the sibling cwm.exe with a hidden
// console rather than serving in-process: everything the agent starts
// (git, syncthing, claude --version) then inherits that hidden console
// instead of each flashing a window of its own.
func Daemon(args []string) int {
	if len(args) == 0 {
		args = []string{"agent", "--open"}
	}
	var out io.Writer = io.Discard
	logPath := "(no log)"
	if root, err := storage.HomeDir(); err == nil {
		if f, err := agent.OpenLog(root); err == nil {
			defer f.Close()
			out, logPath = f, agent.LogPath(root)
		}
	}
	fmt.Fprintf(out, "%s cwmd %v\n", time.Now().Format(time.RFC3339), args)
	code := ExitOK
	if exe := consoleSibling(); exe != "" {
		cmd := exec.Command(exe, args...)
		cmd.Stdout, cmd.Stderr = out, out
		cmd.SysProcAttr = agent.DetachAttr()
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			} else {
				fmt.Fprintf(out, "cwmd: run %s: %v\n", exe, err)
				code = ExitError
			}
		}
	} else {
		code = Run(args, out, out)
	}
	if code != ExitOK {
		alert("cwm", fmt.Sprintf("cwm could not complete `cwm %s` (exit %d).\n\nDetails: %s", strings.Join(args, " "), code, logPath))
	}
	return code
}

// consoleSibling is cwm.exe next to cwmd.exe on Windows ("" elsewhere, or
// when it is missing — then Daemon serves in-process).
func consoleSibling() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	self := selfExe()
	p := filepath.Join(filepath.Dir(self), "cwm.exe")
	if p == self {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// DoubleClicked reports that this console cwm was started from Explorer
// with no arguments (its console belongs to it alone, D35): instead of
// flashing the help text it should open the dashboard.
func DoubleClicked(args []string) bool {
	return len(args) == 0 && consoleOwnedBySelf()
}
