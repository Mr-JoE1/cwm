//go:build !windows

package claudebin

import "os/exec"

func hideWindow(*exec.Cmd) {}
