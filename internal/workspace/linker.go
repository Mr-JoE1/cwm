// Package workspace creates the cwm workspace skeleton (`cwm init`) and
// keeps the login-only dirs of parallel accounts (D80): their own
// credentials and .claude.json, plus a link to every shared item of the
// one workspace root.
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
	// Hardlink creates newname as a hard link to oldname (os.Link): the
	// Windows fallback for a file when symlinks are not permitted.
	Hardlink(oldname, newname string) error
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

func (OSLinker) Hardlink(oldname, newname string) error {
	return os.Link(oldname, newname)
}

// linkKind decides how a shared item is placed into a login-only dir
// (SPEC-V0.8 Windows notes): unix always symlinks; windows dirs become
// junctions (no admin rights); windows files try a symlink first and fall
// back to a HARDLINK, never a copy — a copy would split the data. A
// strict symlink mode never falls back; the legacy copy mode means
// hardlink now.
func linkKind(goos string, isDir bool, linkMode string) string {
	if goos != "windows" {
		return "symlink"
	}
	if isDir {
		return "junction"
	}
	switch linkMode {
	case config.LinkModeSymlink:
		return "symlink"
	case config.LinkModeCopy:
		return "hardlink"
	}
	return "symlink-or-hardlink"
}

func place(lk Linker, kind, src, dst string) error {
	switch kind {
	case "junction":
		return lk.Junction(src, dst)
	case "hardlink":
		return lk.Hardlink(src, dst)
	case "symlink-or-hardlink":
		if err := lk.Symlink(src, dst); err != nil {
			if herr := lk.Hardlink(src, dst); herr != nil {
				return fmt.Errorf("symlink failed (%v); hardlink fallback failed: %w", err, herr)
			}
		}
		return nil
	default: // symlink
		return lk.Symlink(src, dst)
	}
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
