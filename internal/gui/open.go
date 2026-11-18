package gui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// browserCommand returns the platform opener for url (D23: the browser is
// the GUI shell; no tray icon in v0.5).
func browserCommand(goos, url string) (string, []string) {
	switch goos {
	case "windows":
		// The empty "" is start's window-title argument; without it a
		// quoted URL would be taken as the title.
		return "cmd", []string{"/c", "start", "", url}
	case "darwin":
		return "open", []string{url}
	default:
		return "xdg-open", []string{url}
	}
}

// headless refuses to run xdg-open without a graphical session: there it
// would fall back to a text-mode browser and take over the terminal (or
// block a login script).
func headless(goos string, getenv func(string) string) error {
	if goos == "windows" || goos == "darwin" {
		return nil
	}
	if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		return errors.New("headless session (neither DISPLAY nor WAYLAND_DISPLAY is set)")
	}
	return nil
}

// OpenBrowser launches the default browser at url without waiting for it.
// A missing opener is an error the caller reports as a hint (headless
// boxes simply open the printed URL elsewhere).
func OpenBrowser(url string) error {
	if err := headless(runtime.GOOS, os.Getenv); err != nil {
		return err
	}
	name, args := browserCommand(runtime.GOOS, url)
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("no browser opener %q on PATH: %w", name, err)
	}
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	go func() { _ = cmd.Wait() }() // reap the opener; its exit status is irrelevant
	return nil
}
