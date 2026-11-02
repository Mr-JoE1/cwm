package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

func setTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestRoundTrip(t *testing.T) {
	setTestHome(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	c := Default()
	if err := c.AddAccount("main", "~/.cwm/accounts/main"); err != nil {
		t.Fatalf("AddAccount() error = %v", err)
	}
	if err := c.AddAccount("work-2", "/srv/cwm/accounts/work-2"); err != nil {
		t.Fatalf("AddAccount() error = %v", err)
	}
	if err := Save(path, c); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
	if got.LinkMode != "auto" {
		t.Errorf("LinkMode = %q, want auto", got.LinkMode)
	}
	if got.DefaultAccount != "main" {
		t.Errorf("DefaultAccount = %q, want main", got.DefaultAccount)
	}
	if len(got.Accounts) != 2 {
		t.Fatalf("len(Accounts) = %d, want 2", len(got.Accounts))
	}
	if got.Accounts[0].Name != "main" || got.Accounts[0].Dir != "~/.cwm/accounts/main" {
		t.Errorf("Accounts[0] = %+v, want name=main dir=~/.cwm/accounts/main", got.Accounts[0])
	}
}

func TestSaveAtomicNoTempLeftovers(t *testing.T) {
	setTestHome(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := Save(path, Default()); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := Save(path, Default()); err != nil {
		t.Fatalf("Save() 2nd error = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want 1 (no temp leftovers)", len(entries))
	}
}

func TestLoadMissingFileYieldsDefault(t *testing.T) {
	setTestHome(t)
	c, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("Load(missing) error = %v, want nil", err)
	}
	if c.Version != CurrentVersion || c.LinkMode != LinkModeAuto || len(c.Accounts) != 0 {
		t.Errorf("Load(missing) = %+v, want defaults", c)
	}
}

func TestLoadSpecExample(t *testing.T) {
	setTestHome(t)
	data := `
version = 1
default_account = "main"
link_mode = "auto"

[[account]]
name = "main"
dir  = "~/.cwm/accounts/main"
`
	c, err := parse(data)
	if err != nil {
		t.Fatalf("parse(spec example) error = %v", err)
	}
	if c.DefaultAccount != "main" || len(c.Accounts) != 1 {
		t.Errorf("parse = %+v, want one account 'main'", c)
	}
	if c.Accounts[0].ExpandedDir() != filepath.Join(os.Getenv("HOME"), ".cwm", "accounts", "main") {
		t.Errorf("ExpandedDir() = %q, want ~ expanded", c.Accounts[0].ExpandedDir())
	}
}

func TestLoadToleratesUnknownKeys(t *testing.T) {
	setTestHome(t)
	data := `
version = 1
future_key = "whatever"

[[account]]
name = "main"
dir = "/a"
extra_sub = true
`
	if _, err := parse(data); err != nil {
		t.Errorf("parse(unknown keys) error = %v, want tolerated", err)
	}
}

func TestLoadRejectsHomeDir(t *testing.T) {
	setTestHome(t)
	data := `
version = 1
home_dir = "/elsewhere"
`
	_, err := parse(data)
	if err == nil {
		t.Fatal("parse(home_dir) = nil error, want rejection")
	}
	if !strings.Contains(err.Error(), "home_dir") || !strings.Contains(err.Error(), storage.EnvHome) {
		t.Errorf("error = %v, want it to mention home_dir and %s", err, storage.EnvHome)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	setTestHome(t)
	tests := []struct {
		name string
		data string
		want string
	}{
		{"bad version", "version = 2\n", "unsupported version"},
		{"bad link mode", "version = 1\nlink_mode = \"weird\"\n", "link_mode"},
		{"broken toml", "version = \n", "parse"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse(tt.data)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("parse() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestLoadFillsDefaults(t *testing.T) {
	setTestHome(t)
	c, err := parse("[[account]]\nname = \"a\"\ndir = \"/a\"\n")
	if err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	if c.Version != CurrentVersion {
		t.Errorf("Version = %d, want %d", c.Version, CurrentVersion)
	}
	if c.LinkMode != LinkModeAuto {
		t.Errorf("LinkMode = %q, want %q", c.LinkMode, LinkModeAuto)
	}
}

func TestValidAccountName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"plain", "main", true},
		{"digits and dashes", "work-2", true},
		{"underscores", "a_b_c", true},
		{"leading letter", "a1234567890123456789012345678901", true},
		{"uppercase", "Main", false},
		{"leading dash", "-main", false},
		{"leading underscore", "_main", false},
		{"dot", "ma.in", false},
		{"empty", "", false},
		{"all digits", "123", false},
		{"all digits long", "123456", false},
		{"too long", "a23456789012345678901234567890123", false},
		{"digits plus letter", "123a", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidAccountName(tt.in); got != tt.want {
				t.Errorf("ValidAccountName(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestAccountCRUD(t *testing.T) {
	setTestHome(t)
	c := Default()

	if err := c.AddAccount("main", "/d/main"); err != nil {
		t.Fatalf("AddAccount(main) error = %v", err)
	}
	if c.DefaultAccount != "main" {
		t.Errorf("DefaultAccount = %q, want first account 'main' to become default", c.DefaultAccount)
	}
	if err := c.AddAccount("main", "/d/dupe"); err == nil {
		t.Error("AddAccount(duplicate) = nil error, want error")
	}
	if err := c.AddAccount("BAD", "/d/bad"); err == nil {
		t.Error("AddAccount(invalid name) = nil error, want error")
	}
	if err := c.AddAccount("empty", ""); err == nil {
		t.Error("AddAccount(empty dir) = nil error, want error")
	}
	if err := c.AddAccount("second", "/d/second"); err != nil {
		t.Fatalf("AddAccount(second) error = %v", err)
	}
	if c.DefaultAccount != "main" {
		t.Errorf("DefaultAccount = %q, still main after second add", c.DefaultAccount)
	}

	if _, ok := c.Account("second"); !ok {
		t.Error("Account(second) not found")
	}
	if _, ok := c.Account("nope"); ok {
		t.Error("Account(nope) found, want not found")
	}

	if err := c.SetDefaultAccount("second"); err != nil {
		t.Fatalf("SetDefaultAccount(second) error = %v", err)
	}
	if c.DefaultAccount != "second" {
		t.Errorf("DefaultAccount = %q, want second", c.DefaultAccount)
	}
	if err := c.SetDefaultAccount("ghost"); err == nil {
		t.Error("SetDefaultAccount(ghost) = nil error, want error")
	}

	if err := c.RemoveAccount("second"); err != nil {
		t.Fatalf("RemoveAccount(second) error = %v", err)
	}
	if len(c.Accounts) != 1 {
		t.Errorf("len(Accounts) = %d, want 1", len(c.Accounts))
	}
	if err := c.RemoveAccount("second"); err == nil {
		t.Error("RemoveAccount(missing) = nil error, want error")
	}

	if err := c.RemoveAccount("main"); err != nil {
		t.Fatalf("RemoveAccount(main) error = %v", err)
	}
	if c.DefaultAccount != "" {
		t.Errorf("DefaultAccount = %q, want cleared when default removed", c.DefaultAccount)
	}
}

func TestAccountExpandedDir(t *testing.T) {
	home := setTestHome(t)
	tests := []struct {
		in   string
		want string
	}{
		{"~/.cwm/accounts/main", filepath.Join(home, ".cwm", "accounts", "main")},
		{"/abs/accounts/main", "/abs/accounts/main"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			a := Account{Name: "x", Dir: tt.in}
			if got := a.ExpandedDir(); got != tt.want {
				t.Errorf("ExpandedDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCRUDSurvivesSaveLoad(t *testing.T) {
	setTestHome(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	c := Default()
	if err := c.AddAccount("main", "/d/main"); err != nil {
		t.Fatalf("AddAccount() error = %v", err)
	}
	if err := Save(path, c); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := got.RemoveAccount("main"); err != nil {
		t.Fatalf("RemoveAccount() after reload error = %v", err)
	}
	if err := Save(path, got); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	final, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(final.Accounts) != 0 || final.DefaultAccount != "" {
		t.Errorf("after remove+save: %+v, want no accounts and no default", final)
	}
}
