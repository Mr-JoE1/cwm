//go:build !windows

package claudeopen

import "os/exec"

func hideWindow(*exec.Cmd) {}
