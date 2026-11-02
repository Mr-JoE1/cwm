// Package config loads and saves the cwm config.toml (version, default
// account, link mode, account list) and implements account CRUD.
package config

import (
	"bytes"
	"fmt"
	"os"
	"regexp"

	"github.com/BurntSushi/toml"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

// CurrentVersion is the only config.toml schema version v0.2 understands.
const CurrentVersion = 1

// LinkModes recognized in config.toml.
const (
	LinkModeAuto     = "auto"
	LinkModeSymlink  = "symlink"
	LinkModeJunction = "junction"
	LinkModeCopy     = "copy"
)

var accountNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// Login is the identity read from an account's .claude.json after
// `claude auth login` (the oauthAccount object). Every field is optional.
type Login struct {
	Email            string `toml:"email,omitempty"`
	AccountUUID      string `toml:"account_uuid,omitempty"`
	OrganizationName string `toml:"organization_name,omitempty"`
	Plan             string `toml:"plan,omitempty"`
}

// Account is one [[account]] table: a named login with its private dir.
type Account struct {
	Name string `toml:"name"`
	Dir  string `toml:"dir"`
	// Login carries the identity extracted at `account add` time (nil until
	// a login produced one).
	Login *Login `toml:"login,omitempty"`
}

// ExpandedDir returns Dir with a leading ~ expanded to the user home.
func (a Account) ExpandedDir() string {
	return storage.ExpandTilde(a.Dir)
}

// Config mirrors config.toml. Unknown keys are tolerated on load.
type Config struct {
	Version        int       `toml:"version"`
	DefaultAccount string    `toml:"default_account"`
	LinkMode       string    `toml:"link_mode"`
	Accounts       []Account `toml:"account"`
}

// Default returns a fresh config with schema defaults filled in.
func Default() *Config {
	return &Config{Version: CurrentVersion, LinkMode: LinkModeAuto}
}

// Load reads path. A missing file yields Default(), not an error: several
// commands legitimately run before init (doctor, account add).
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	c, err := parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return c, nil
}

func parse(data string) (*Config, error) {
	var c Config
	meta, err := toml.Decode(data, &c)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if meta.IsDefined("home_dir") {
		return nil, fmt.Errorf("key home_dir is not allowed (set the %s env var instead)", storage.EnvHome)
	}
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if c.Version != CurrentVersion {
		return nil, fmt.Errorf("unsupported version %d (want %d)", c.Version, CurrentVersion)
	}
	if c.LinkMode == "" {
		c.LinkMode = LinkModeAuto
	}
	if !validLinkMode(c.LinkMode) {
		return nil, fmt.Errorf("invalid link_mode %q (want auto, symlink, junction or copy)", c.LinkMode)
	}
	return &c, nil
}

func validLinkMode(m string) bool {
	switch m {
	case LinkModeAuto, LinkModeSymlink, LinkModeJunction, LinkModeCopy:
		return true
	}
	return false
}

// Save atomically writes the config to path (temp file + fsync + rename).
func Save(path string, c *Config) error {
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if c.LinkMode == "" {
		c.LinkMode = LinkModeAuto
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	if err := storage.WriteFileAtomic(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("config: save %s: %w", path, err)
	}
	return nil
}

// ValidAccountName enforces [a-z0-9][a-z0-9_-]{0,31}, not all digits.
func ValidAccountName(name string) bool {
	if !accountNameRe.MatchString(name) {
		return false
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return true
		}
	}
	return false
}

// Account looks up an account by name.
func (c *Config) Account(name string) (Account, bool) {
	for _, a := range c.Accounts {
		if a.Name == name {
			return a, true
		}
	}
	return Account{}, false
}

// AddAccount appends a new account and makes it the default when it is the
// first one. dir may start with ~; it is stored verbatim.
func (c *Config) AddAccount(name, dir string) error {
	if !ValidAccountName(name) {
		return fmt.Errorf("config: invalid account name %q (want [a-z0-9][a-z0-9_-]{0,31}, not all digits)", name)
	}
	if _, exists := c.Account(name); exists {
		return fmt.Errorf("config: account %q already exists", name)
	}
	if dir == "" {
		return fmt.Errorf("config: account %q needs a dir", name)
	}
	c.Accounts = append(c.Accounts, Account{Name: name, Dir: dir})
	if c.DefaultAccount == "" {
		c.DefaultAccount = name
	}
	return nil
}

// RemoveAccount drops an account. Removing the default clears the default
// (the next AddAccount or an explicit SetDefaultAccount re-establishes one).
func (c *Config) RemoveAccount(name string) error {
	for i, a := range c.Accounts {
		if a.Name == name {
			c.Accounts = append(c.Accounts[:i], c.Accounts[i+1:]...)
			if c.DefaultAccount == name {
				c.DefaultAccount = ""
			}
			return nil
		}
	}
	return fmt.Errorf("config: account %q not found", name)
}

// SetDefaultAccount points the default at an existing account.
func (c *Config) SetDefaultAccount(name string) error {
	if _, ok := c.Account(name); !ok {
		return fmt.Errorf("config: account %q not found", name)
	}
	c.DefaultAccount = name
	return nil
}

// SetAccountLogin attaches login identity to an existing account.
func (c *Config) SetAccountLogin(name string, lg Login) error {
	for i := range c.Accounts {
		if c.Accounts[i].Name == name {
			l := lg
			c.Accounts[i].Login = &l
			return nil
		}
	}
	return fmt.Errorf("config: account %q not found", name)
}
