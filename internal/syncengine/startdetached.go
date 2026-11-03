// StartDetached: the one-shot daemon spawn escape hatch (D17). cwm never
// supervises syncthing — `cwm pair --start` may launch ONE detached
// `syncthing serve --no-browser` process when the API is unreachable, and
// that is all.
package syncengine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

// StartDetached spawns `syncthing serve --no-browser` as a detached process
// (own session on unix, detached process group on windows) with stdio
// appended to $CWM_HOME/logs/syncthing.log. It returns right after the
// spawn — no waiting, no supervision.
func StartDetached() error {
	name := "syncthing"
	if runtime.GOOS == "windows" {
		name = "syncthing.exe"
	}
	bin, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("syncthing not on PATH — install it (apt install syncthing / brew install syncthing / winget install Syncthing.Syncthing)")
	}
	home, err := storage.HomeDir()
	if err != nil {
		return fmt.Errorf("syncengine: %w", err)
	}
	logDir := filepath.Join(home, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("syncengine: create %s: %w", logDir, err)
	}
	logPath := filepath.Join(logDir, "syncthing.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("syncengine: open %s: %w", logPath, err)
	}
	defer logFile.Close()

	cmd := exec.Command(bin, "serve", "--no-browser")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("syncengine: start %s: %w", bin, err)
	}
	// Detached: the child must outlive cwm, so release it instead of
	// waiting. Its stdio fds are inherited across the spawn.
	_ = cmd.Process.Release()
	return nil
}
