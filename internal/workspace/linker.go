// Package workspace creates the cwm workspace skeleton (`cwm init`) and
// assembles per-account launch dirs (D11): every workspace item linked in,
// account credentials copied in (never linked), and settings.json
// generated with the cwm hooks wired.
package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// Linker abstracts the platform link/copy primitives so launch-dir
// assembly is unit-testable and the windows junction path stays
// executable on Linux (D14 link modes).
type Linker interface {
	// Symlink creates newname as a symbolic link to oldname (os.Symlink).
	Symlink(oldname, newname string) error
	// Junction creates link as a directory junction to target
	// (windows `cmd /c mklink /J`).
	Junction(target, link string) error
	// Copy copies a regular file from src to dst.
	Copy(src, dst string) error
}

// OSLinker is the real filesystem implementation.
type OSLinker struct{}

func (OSLinker) Symlink(oldname, newname string) error {
	return os.Symlink(oldname, newname)
}

func (OSLinker) Junction(target, link string) error {
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("workspace: mklink /J %s %s: %w: %s", link, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (OSLinker) Copy(src, dst string) error {
	return storage.CopyFile(src, dst)
}

// linkKind decides how a workspace item is placed into a launch dir:
// unix always symlinks; windows dirs become junctions; windows files try a
// symlink first and fall back to a copy when the configured link mode
// allows it (auto/copy; a strict symlink mode must not silently copy).
func linkKind(goos string, isDir bool, linkMode string) string {
	if goos != "windows" {
		return "symlink"
	}
	if isDir {
		return "junction"
	}
	if linkMode == config.LinkModeCopy {
		return "copy"
	}
	return "symlink-or-copy"
}

// sameLinkTarget compares a wanted link target with what os.Readlink
// returned. Windows junction targets may carry \??\ or \\?\ prefixes and
// compare case-insensitively.
func sameLinkTarget(want, got string) bool {
	for _, p := range []string{`\\?\`, `\??\`} {
		got = strings.TrimPrefix(got, p)
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(want), filepath.Clean(got))
	}
	return filepath.Clean(want) == filepath.Clean(got)
}

// itemState classifies what currently sits at a launch-dir path.
type itemState int

const (
	stateMissing     itemState = iota // nothing there yet
	stateCorrectLink                  // link pointing exactly where we want
	stateReplace                      // wrong-target link or unexpected real entry
)

func classify(dst, wantTarget string) (itemState, error) {
	fi, err := os.Lstat(dst)
	if err != nil {
		if os.IsNotExist(err) {
			return stateMissing, nil
		}
		return stateMissing, fmt.Errorf("workspace: stat %s: %w", dst, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		got, err := os.Readlink(dst)
		if err != nil {
			return stateReplace, fmt.Errorf("workspace: readlink %s: %w", dst, err)
		}
		if sameLinkTarget(wantTarget, got) {
			return stateCorrectLink, nil
		}
		return stateReplace, nil
	}
	// Junctions on windows may not surface as symlinks everywhere; a real
	// entry with the wrong shape is still replaceable, so treat anything
	// non-symlink as a replace candidate and let the backup capture it.
	return stateReplace, nil
}
