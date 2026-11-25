package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// FindClaude resolves the claude binary (D49); a seam for tests.
var FindClaude = claudebin.Find

// Add validates name, creates accounts/<name> (0700), runs
// `claude auth login` with CLAUDE_CONFIG_DIR pointing at that dir (stdio
// inherited, so the interactive login works), parses the resulting
// identity and registers the account in cfg. cfg is mutated in memory; the
// caller persists it. reserved reports cwm's own command names (nil skips
// that rule).
func Add(l *storage.Layout, cfg *config.Config, name string, stdout io.Writer) error {
	return AddChecked(l, cfg, name, nil, stdout)
}

// AddChecked is Add with the reserved-name rule of ValidateNewName.
func AddChecked(l *storage.Layout, cfg *config.Config, name string, reserved func(string) bool, stdout io.Writer) error {
	if err := ValidateNewName(name, cfg, reserved); err != nil {
		return fmt.Errorf("accounts: %w", err)
	}
	// Resolve claude first: a missing claude must not leave an empty
	// account dir behind.
	res, err := FindClaude(l)
	if err != nil {
		return fmt.Errorf("accounts: %w", err)
	}
	claudebin.Remember(cfg, res)
	claudebin.Hint(stdout, runtime.GOOS, res)

	dir := filepath.Join(l.AccountsDir(), name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("accounts: create %s: %w", dir, err)
	}
	if err := runLogin(res.Path, dir); err != nil {
		return err
	}
	// Register the account even when no identity could be read: the login
	// may have succeeded without persisting creds yet (spec: warn, keep).
	if err := cfg.AddAccount(name, dir); err != nil {
		return fmt.Errorf("accounts: %w", err)
	}
	return recordIdentity(cfg, name, dir, stdout, "added")
}

// Info is one row of `cwm account list`: config identity plus on-disk
// login freshness.
type Info struct {
	Name      string
	Email     string
	Plan      string
	Default   bool
	HasLogin  bool
	LastLogin time.Time // zero = no .credentials.json
	Dir       string
}

// Gather collects list rows for every configured account, in config order.
func Gather(cfg *config.Config, l *storage.Layout) []Info {
	infos := make([]Info, 0, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		info := Info{
			Name:    a.Name,
			Default: a.Name == cfg.DefaultAccount,
			Dir:     a.ExpandedDir(),
		}
		if a.Login != nil {
			info.Email = a.Login.Email
			info.Plan = a.Login.Plan
			info.HasLogin = a.Login.Email != ""
		}
		if fi, err := os.Stat(filepath.Join(info.Dir, ".credentials.json")); err == nil {
			info.LastLogin = fi.ModTime()
		}
		infos = append(infos, info)
	}
	return infos
}

// List prints the account table (or one JSON object per account when
// asJSON is set) to w.
func List(cfg *config.Config, l *storage.Layout, asJSON bool, w io.Writer) error {
	infos := Gather(cfg, l)
	if asJSON {
		return renderJSON(w, infos)
	}
	return renderTable(w, infos)
}

func renderTable(w io.Writer, infos []Info) error {
	tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tname\temail\tplan\tdefault*\tlast-login\t")
	for i, in := range infos {
		email, plan, last := "-", "-", "-"
		if in.Email != "" {
			email = in.Email
		}
		if in.Plan != "" {
			plan = in.Plan
		}
		def := "-"
		if in.Default {
			def = "*"
		}
		if !in.LastLogin.IsZero() {
			last = in.LastLogin.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t\n", i+1, in.Name, email, plan, def, last)
	}
	return tw.Flush()
}

// Row is the machine-readable shape of one account: identity fields and
// login freshness only (D12) — never the account dir or any credential.
// `cwm account list --json` and the GUI's /api/accounts both serialize it
// (D26).
type Row struct {
	Name      string `json:"name"`
	Email     string `json:"email"`
	Plan      string `json:"plan"`
	Default   bool   `json:"default"`
	LastLogin string `json:"last_login,omitempty"`
}

// Rows converts gathered infos to their machine-readable rows (non-nil).
func Rows(infos []Info) []Row {
	out := make([]Row, 0, len(infos))
	for _, in := range infos {
		row := Row{Name: in.Name, Email: in.Email, Plan: in.Plan, Default: in.Default}
		if !in.LastLogin.IsZero() {
			row.LastLogin = in.LastLogin.Format(time.RFC3339)
		}
		out = append(out, row)
	}
	return out
}

func renderJSON(w io.Writer, infos []Info) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(Rows(infos))
}

// Use points the default account at name. cfg is mutated in memory; the
// caller persists it.
func Use(cfg *config.Config, name string) error {
	if _, ok := cfg.Account(name); !ok {
		return fmt.Errorf("accounts: account %q not found", name)
	}
	return cfg.SetDefaultAccount(name)
}

// RemoveOptions tune Remove.
type RemoveOptions struct {
	// DeleteDir also deletes accounts/<name> and launch/<name> once the
	// backup succeeded.
	DeleteDir bool
	// Switch makes next the default before a default account is removed
	// (v0.8, D79: the live login must change with it, so it goes through
	// the vault swap). nil = only config.toml changes (v0.7 behaviour).
	Switch func(cfg *config.Config, next string) error
	// Store forgets the account's Keychain item with the dir (macOS);
	// nil = nothing beyond the dir.
	Store CredStore
}

// RemoveResult says what Remove did.
type RemoveResult struct {
	// Backup is the backup dir ("" when the account dir did not exist).
	Backup     string
	DeletedDir bool
	// KeptDir is the account dir DeleteDir left in place because it lies
	// outside the cwm home ("" = nothing was kept).
	KeptDir string
	// NewDefault is the account that became the default because the
	// removed one was it ("" = the default did not change).
	NewDefault string
}

// ErrLastAccount refuses removing the only account: every launch needs
// one, and the CLI and the dashboard both say so instead of leaving a
// workspace with nothing to run claude as.
var ErrLastAccount = errors.New("accounts: refusing to remove the last account — add another account first (cwm account add <name>)")

// Remove backs up the account dir (copy, the source stays until the backup
// succeeded) to backups/<ts>-account-<name>/ and drops the account from
// cfg. The last account is refused (ErrLastAccount). Removing the default
// makes the first remaining account the default. cfg is mutated in
// memory; the caller persists it.
func Remove(l *storage.Layout, cfg *config.Config, name string) error {
	_, err := RemoveWith(l, cfg, name, RemoveOptions{})
	return err
}

// RemoveWith is Remove with options and a report.
func RemoveWith(l *storage.Layout, cfg *config.Config, name string, opts RemoveOptions) (RemoveResult, error) {
	var out RemoveResult
	acct, ok := cfg.Account(name)
	if !ok {
		return out, fmt.Errorf("accounts: account %q not found", name)
	}
	if len(cfg.Accounts) == 1 {
		return out, ErrLastAccount
	}
	wasDefault := cfg.DefaultAccount == name
	if wasDefault && opts.Switch != nil {
		// Switch first: if the next account cannot take over the live
		// login, nothing has changed yet.
		next := ""
		for _, a := range cfg.Accounts {
			if a.Name != name {
				next = a.Name
				break
			}
		}
		if err := opts.Switch(cfg, next); err != nil {
			return out, fmt.Errorf("accounts: %s is the default and %s cannot take over: %w", name, next, err)
		}
		out.NewDefault = next
	}
	dir := acct.ExpandedDir()
	if _, err := os.Stat(dir); err == nil {
		backup := l.BackupDirPath("account-" + storage.SanitizedLabel(name))
		if err := storage.CopyTree(dir, backup); err != nil {
			return out, fmt.Errorf("accounts: back up %s: %w", dir, err)
		}
		out.Backup = backup
	}
	if err := cfg.RemoveAccount(name); err != nil {
		return out, err
	}
	if wasDefault && opts.Switch == nil && len(cfg.Accounts) > 0 {
		out.NewDefault = cfg.Accounts[0].Name
		if err := cfg.SetDefaultAccount(out.NewDefault); err != nil {
			return out, err
		}
	}
	if opts.DeleteDir {
		// Only cwm's own dirs: an account imported from elsewhere keeps
		// its original folder (the backup has a copy either way).
		if Deletable(l, dir) {
			if err := os.RemoveAll(dir); err != nil {
				return out, fmt.Errorf("accounts: delete %s: %w", dir, err)
			}
			out.DeletedDir = true
		} else {
			out.KeptDir = dir
		}
		if err := os.RemoveAll(filepath.Join(l.LaunchDir(), name)); err != nil {
			return out, fmt.Errorf("accounts: delete launch dir: %w", err)
		}
		if f, ok := opts.Store.(interface {
			Forget(p claudepaths.Paths) error
		}); ok {
			if p, err := VaultPaths(l, cfg.EffectiveMode(), name); err == nil {
				_ = f.Forget(p) // best effort: the dir and its backup are what count
			}
		}
	}
	ClearLoginState(l, name)
	return out, nil
}

// Deletable reports whether RemoveOptions.DeleteDir may delete the
// account dir dir: only cwm's own folders (accounts/<name>,
// launch/<name>) qualify, never a folder imported from elsewhere.
func Deletable(l *storage.Layout, dir string) bool {
	return within(l.AccountsDir(), dir) || within(l.LaunchDir(), dir)
}

// within reports whether p is strictly inside dir.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
