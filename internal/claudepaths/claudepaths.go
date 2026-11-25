// Package claudepaths is the one root resolver of v0.8 (D77): it turns the
// workspace mode and an account into the paths Claude Code reads — the
// workspace root, the live .claude.json, the live credentials — and the
// CLAUDE_CONFIG_DIR value a launch must set. Nothing else in cwm derives
// these paths on its own.
//
// Claude Code's own rules (SPEC-V0.8 "Research notes"): with
// CLAUDE_CONFIG_DIR unset it reads ~/.claude for its config dir, keeps
// .claude.json in the home dir (NOT inside ~/.claude) and the credentials
// in ~/.claude/.credentials.json (macOS: the login Keychain). With
// CLAUDE_CONFIG_DIR set, both files live inside that dir.
package claudepaths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// EnvConfigDir is the variable Claude Code reads for its config dir.
const EnvConfigDir = "CLAUDE_CONFIG_DIR"

// File names Claude Code uses inside (or next to) its config dir.
const (
	ClaudeJSONName  = ".claude.json"
	CredentialsName = ".credentials.json"
)

// ClaudeDir is the standard config dir: ~/.claude (Windows:
// %USERPROFILE%\.claude, which os.UserHomeDir returns).
func ClaudeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("claudepaths: cannot determine home dir: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}

// HomeClaudeJSON is ~/.claude.json, the .claude.json Claude Code uses when
// CLAUDE_CONFIG_DIR is unset.
func HomeClaudeJSON() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("claudepaths: cannot determine home dir: %w", err)
	}
	return filepath.Join(home, ClaudeJSONName), nil
}

// Root is the workspace root of mode: ~/.claude (capture) or
// ~/.cwm/workspace (isolated). An unknown mode is capture, the default.
func Root(l *storage.Layout, mode string) (string, error) {
	if mode == config.ModeIsolated {
		return l.IsolatedWorkspaceDir(), nil
	}
	return ClaudeDir()
}

// Paths is where one account's claude reads its state.
type Paths struct {
	Mode    string `json:"mode"`
	Account string `json:"account,omitempty"`
	// Default is set when the account is the default one, which uses the
	// workspace root directly; any other account runs from its login-only
	// dir (D80).
	Default bool `json:"default"`
	// Root is the workspace root (projects/, history.jsonl, ...).
	Root string `json:"root"`
	// ConfigDir is the dir claude treats as its config dir.
	ConfigDir   string `json:"config_dir"`
	ClaudeJSON  string `json:"claude_json"`
	Credentials string `json:"credentials"`
	// Env is the CLAUDE_CONFIG_DIR value to set; "" means leave it unset
	// (capture mode, default account: plain claude must see the same).
	Env string `json:"env,omitempty"`
}

// LoginDir is an account's login-only dir and vault entry,
// ~/.cwm/accounts/<name>.
func LoginDir(l *storage.Layout, account string) string {
	return filepath.Join(l.AccountsDir(), account)
}

// Live resolves account's live paths under mode. account "" or equal to
// defaultAccount is the default account.
func Live(l *storage.Layout, mode, account, defaultAccount string) (Paths, error) {
	root, err := Root(l, mode)
	if err != nil {
		return Paths{}, err
	}
	if !config.ValidMode(mode) {
		mode = config.ModeCapture
	}
	if account == "" {
		account = defaultAccount
	}
	p := Paths{Mode: mode, Account: account, Root: root}
	switch {
	case account != "" && account != defaultAccount:
		dir := LoginDir(l, account)
		p.ConfigDir, p.Env = dir, dir
		p.ClaudeJSON = filepath.Join(dir, ClaudeJSONName)
		p.Credentials = filepath.Join(dir, CredentialsName)
	case mode == config.ModeCapture:
		p.Default = true
		p.ConfigDir = root
		hj, err := HomeClaudeJSON()
		if err != nil {
			return Paths{}, err
		}
		p.ClaudeJSON = hj
		p.Credentials = filepath.Join(root, CredentialsName)
	default:
		p.Default = true
		p.ConfigDir, p.Env = root, root
		p.ClaudeJSON = filepath.Join(root, ClaudeJSONName)
		p.Credentials = filepath.Join(root, CredentialsName)
	}
	return p, nil
}

// LiveFor is Live with the mode and default account taken from cfg.
func LiveFor(l *storage.Layout, cfg *config.Config, account string) (Paths, error) {
	return Live(l, cfg.EffectiveMode(), account, cfg.DefaultAccount)
}

// Resolve loads l's config.toml and points l at the configured workspace
// root. It returns the loaded config so callers need not read it twice.
func Resolve(l *storage.Layout) (*config.Config, error) {
	cfg, err := config.Load(l.ConfigPath())
	if err != nil {
		return nil, fmt.Errorf("claudepaths: %w", err)
	}
	if err := Apply(l, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Apply points l at cfg's workspace root (the agent calls it after a mode
// change).
func Apply(l *storage.Layout, cfg *config.Config) error {
	root, err := Root(l, cfg.EffectiveMode())
	if err != nil {
		return err
	}
	l.SetWorkspaceDir(root)
	return nil
}

// NewLayout is storage.NewLayout resolved through the configured mode:
// what every command uses.
func NewLayout() (*storage.Layout, error) {
	l, err := storage.NewLayout()
	if err != nil {
		return nil, err
	}
	if _, err := Resolve(l); err != nil {
		return nil, err
	}
	return l, nil
}

// LayoutAt is NewLayout for an explicit cwm home. A config that cannot be
// read leaves the layout on capture mode's root (doctor reports the
// broken config separately); the error is returned for callers that care.
func LayoutAt(root string) (*storage.Layout, error) {
	l := storage.NewLayoutAt(root)
	if _, err := Resolve(l); err != nil {
		if r, rerr := Root(l, config.ModeCapture); rerr == nil {
			l.SetWorkspaceDir(r)
		}
		return l, err
	}
	return l, nil
}

// Tilde renders p with the home dir as ~ (reports never print a user name).
func Tilde(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	rel, err := filepath.Rel(home, p)
	sep := string(filepath.Separator)
	switch {
	case err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+sep):
		return p
	case rel == ".":
		return "~"
	}
	return "~" + sep + rel
}

// NewLayoutLenient is NewLayout for diagnostics (doctor, debug bundles,
// log paths): a config.toml that cannot be read leaves the layout on
// capture mode's root instead of failing, because the tool reporting the
// broken config must still run.
func NewLayoutLenient() (*storage.Layout, error) {
	root, err := storage.HomeDir()
	if err != nil {
		return nil, err
	}
	l, _ := LayoutAt(root)
	return l, nil
}
