package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// credFiles are the account items that are ALWAYS copied into a launch dir
// (never linked): they are private, rotate on re-auth, and a link would go
// stale or leak across accounts.
var credFiles = []string{".credentials.json", ".claude.json"}

// settingsEnv is the env block written into every generated settings.json
// (port of templates/settings.*.json.tmpl semantics).
var settingsEnv = map[string]string{
	"DISABLE_TELEMETRY":                        "1",
	"DISABLE_ERROR_REPORTING":                  "1",
	"DISABLE_BUG_COMMAND":                      "1",
	"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type hookMatcher struct {
	Hooks []hookCommand `json:"hooks"`
}

type settingsFile struct {
	Env   map[string]string        `json:"env"`
	Hooks map[string][]hookMatcher `json:"hooks"`
}

// EnsureLaunch assembles launch/<account> (idempotent): every workspace
// item linked in with the platform-appropriate kind, credentials freshly
// copied in, settings.json generated. Re-running with correct state
// changes nothing except refreshing the credential copies.
func EnsureLaunch(l *storage.Layout, cfg *config.Config, account string) error {
	return EnsureLaunchWithLinker(l, cfg, account, OSLinker{})
}

// EnsureLaunchWithLinker is EnsureLaunch with an injected Linker (tests,
// planners).
func EnsureLaunchWithLinker(l *storage.Layout, cfg *config.Config, account string, lk Linker) error {
	acct, ok := cfg.Account(account)
	if !ok {
		return fmt.Errorf("workspace: unknown account %q", account)
	}
	acctDir := acct.ExpandedDir()
	if info, err := os.Stat(acctDir); err != nil || !info.IsDir() {
		return fmt.Errorf("workspace: account dir %s is missing — run `cwm account add %s`", acctDir, account)
	}
	launch := filepath.Join(l.LaunchDir(), account)
	if err := os.MkdirAll(launch, 0o700); err != nil {
		return fmt.Errorf("workspace: create %s: %w", launch, err)
	}

	backup := ""
	backupOnce := func() (string, error) {
		if backup == "" {
			backup = l.BackupDirPath("launch-" + storage.SanitizedLabel(account))
			if err := os.MkdirAll(backup, 0o755); err != nil {
				return "", fmt.Errorf("workspace: create backup %s: %w", backup, err)
			}
		}
		return backup, nil
	}

	for _, item := range storage.WorkspaceItems() {
		src := l.WorkspaceItemPath(item)
		info, err := os.Stat(src)
		if err != nil {
			continue // item not present in the (possibly not yet synced) workspace
		}
		if err := placeItem(lk, cfg.LinkMode, launch, src, item, info.IsDir(), backupOnce); err != nil {
			return err
		}
	}

	for _, cred := range credFiles {
		src := filepath.Join(acctDir, cred)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(launch, cred)
		fi, err := os.Lstat(dst)
		switch {
		case err == nil && fi.Mode()&os.ModeSymlink != 0:
			// A link where a real copy must live: back the anomaly up,
			// then replace with the account's file.
			b, err := backupOnce()
			if err != nil {
				return err
			}
			if err := storage.BackupEntry(dst, b); err != nil {
				return err
			}
			if err := os.Remove(dst); err != nil {
				return fmt.Errorf("workspace: remove %s: %w", dst, err)
			}
		case err == nil:
			// Stale copy from a previous assembly: refresh below.
			if err := os.Remove(dst); err != nil {
				return fmt.Errorf("workspace: remove %s: %w", dst, err)
			}
		case !os.IsNotExist(err):
			return fmt.Errorf("workspace: stat %s: %w", dst, err)
		}
		if err := lk.Copy(src, dst); err != nil {
			return fmt.Errorf("workspace: copy %s: %w", src, err)
		}
	}

	if err := writeSettings(launch); err != nil {
		return err
	}
	return nil
}

// placeItem puts one workspace item into the launch dir: correct link →
// skip; missing → link; wrong-target link or unexpected real entry → back
// up, remove, re-place.
func placeItem(lk Linker, linkMode, launch, src, name string, isDir bool, backupOnce func() (string, error)) error {
	dst := filepath.Join(launch, name)
	state, err := classify(dst, src)
	if err != nil {
		return err
	}
	if state == stateCorrectLink {
		return nil
	}
	if state == stateReplace {
		b, err := backupOnce()
		if err != nil {
			return err
		}
		if err := storage.BackupEntry(dst, b); err != nil {
			return err
		}
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("workspace: remove %s: %w", dst, err)
		}
	}
	kind := linkKind(runtime.GOOS, isDir, linkMode)
	if err := place(lk, kind, src, dst); err != nil {
		return fmt.Errorf("workspace: link %s -> %s: %w", dst, src, err)
	}
	return nil
}

func place(lk Linker, kind, src, dst string) error {
	switch kind {
	case "junction":
		return lk.Junction(src, dst)
	case "copy":
		return lk.Copy(src, dst)
	case "symlink-or-copy":
		if err := lk.Symlink(src, dst); err != nil {
			if cerr := lk.Copy(src, dst); cerr != nil {
				return fmt.Errorf("symlink failed (%v); copy fallback failed: %w", err, cerr)
			}
		}
		return nil
	default: // symlink
		return lk.Symlink(src, dst)
	}
}

// writeSettings generates the launch dir's settings.json: telemetry env
// off, cwm hooks wired via the absolute path of the running binary.
func writeSettings(launch string) error {
	bin := selfBinaryPath()
	if strings.ContainsAny(bin, " \t") {
		bin = `"` + bin + `"`
	}
	s := settingsFile{
		Env: settingsEnv,
		Hooks: map[string][]hookMatcher{
			"SessionStart": {{Hooks: []hookCommand{{Type: "command", Command: bin + " hook session-start"}}}},
			"SessionEnd":   {{Hooks: []hookCommand{{Type: "command", Command: bin + " hook session-end"}}}},
		},
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("workspace: encode settings.json: %w", err)
	}
	data = append(data, '\n')
	if err := storage.WriteFileAtomic(filepath.Join(launch, "settings.json"), data, 0o644); err != nil {
		return fmt.Errorf("workspace: write settings.json: %w", err)
	}
	return nil
}

// selfBinaryPath returns the absolute path of the running cwm binary
// (best effort; "cwm" as a PATH fallback).
func selfBinaryPath() string {
	ex, err := os.Executable()
	if err != nil {
		return "cwm"
	}
	if abs, err := filepath.Abs(ex); err == nil {
		return abs
	}
	return ex
}
