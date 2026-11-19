//go:build windows

package debugbundle

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// OSInfo is `ver`'s answer ("Microsoft Windows [Version 10.0.22631.4460]"),
// run without a console window and bounded so a broken cmd.exe cannot hang
// the report.
func OSInfo() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cmd.exe", "/c", "ver")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.Output()
	if err != nil {
		return "Windows"
	}
	return strings.TrimSpace(string(out))
}
