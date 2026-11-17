//go:build windows

package term

import "syscall"

func init() {
	escapeArg = syscall.EscapeArg
}

// createNoWindow hides the short-lived cmd.exe that only runs `start`.
const createNoWindow = 0x08000000

// startDetached starts the terminal in a new process group (no Ctrl-C
// propagation from the `cwm gui` console). On the cmd.exe route the raw
// command line is passed verbatim: each argument was quoted individually
// by Resolve, and Go must not re-quote the already-quoted line.
func startDetached(spec Spec) error {
	cmd := command(spec)
	attr := &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	if spec.CmdLine != "" {
		attr.CmdLine = spec.CmdLine
		attr.CreationFlags |= createNoWindow
	}
	cmd.SysProcAttr = attr
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
