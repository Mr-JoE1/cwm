package accounts

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// Add validates name, creates accounts/<name> (0700), runs
// `claude auth login` with CLAUDE_CONFIG_DIR pointing at that dir (stdio
// inherited, so the interactive login works), parses the resulting
// identity and registers the account in cfg. cfg is mutated in memory; the
// caller persists it.
func Add(l *storage.Layout, cfg *config.Config, name string, stdout io.Writer) error {
	if !config.ValidAccountName(name) {
		return fmt.Errorf("accounts: invalid account name %q (want [a-z0-9][a-z0-9_-]{0,31}, not all digits)", name)
	}
	if _, exists := cfg.Account(name); exists {
		return fmt.Errorf("accounts: account %q already exists", name)
	}
	dir := filepath.Join(l.AccountsDir(), name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("accounts: create %s: %w", dir, err)
	}

	bin, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("accounts: claude not found on PATH — install Claude Code first: %w", err)
	}
	login := exec.Command(bin, "auth", "login")
	login.Env = append(os.Environ(), ClaudeConfigDirEnv+"="+dir)
	login.Stdin, login.Stdout, login.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := login.Run(); err != nil {
		return fmt.Errorf("accounts: claude auth login failed: %w", err)
	}

	// Register the account even when no identity could be read: the login
	// may have succeeded without persisting creds yet (spec: warn, keep).
	id, idErr := ReadIdentity(filepath.Join(dir, ".claude.json"))
	if err := cfg.AddAccount(name, dir); err != nil {
		return fmt.Errorf("accounts: %w", err)
	}
	if idErr != nil || id.Email == "" {
		fmt.Fprintf(stdout, "⚠ %s added (no login identity found — re-run the login if this looks wrong)\n", name)
		return nil
	}
	if err := cfg.SetAccountLogin(name, config.Login{
		Email:            id.Email,
		AccountUUID:      id.AccountUUID,
		OrganizationName: id.OrganizationName,
		Plan:             id.Plan,
	}); err != nil {
		return fmt.Errorf("accounts: %w", err)
	}
	fmt.Fprintf(stdout, "✓ %s (%s) added\n", name, id.Email)
	return nil
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

// jsonAccount is the --json shape of one account row.
type jsonAccount struct {
	Name      string `json:"name"`
	Email     string `json:"email"`
	Plan      string `json:"plan"`
	Default   bool   `json:"default"`
	LastLogin string `json:"last_login,omitempty"`
}

func renderJSON(w io.Writer, infos []Info) error {
	out := make([]jsonAccount, 0, len(infos))
	for _, in := range infos {
		row := jsonAccount{Name: in.Name, Email: in.Email, Plan: in.Plan, Default: in.Default}
		if !in.LastLogin.IsZero() {
			row.LastLogin = in.LastLogin.Format(time.RFC3339)
		}
		out = append(out, row)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// Use points the default account at name. cfg is mutated in memory; the
// caller persists it.
func Use(cfg *config.Config, name string) error {
	if _, ok := cfg.Account(name); !ok {
		return fmt.Errorf("accounts: account %q not found", name)
	}
	return cfg.SetDefaultAccount(name)
}

// Remove backs up the account dir (copy, the source stays until the backup
// succeeded) to backups/<ts>-account-<name>/ and drops the account from
// cfg. cfg is mutated in memory; the caller persists it.
func Remove(l *storage.Layout, cfg *config.Config, name string) error {
	acct, ok := cfg.Account(name)
	if !ok {
		return fmt.Errorf("accounts: account %q not found", name)
	}
	dir := acct.ExpandedDir()
	if _, err := os.Stat(dir); err == nil {
		backup := l.BackupDirPath("account-" + storage.SanitizedLabel(name))
		if err := storage.CopyTree(dir, backup); err != nil {
			return fmt.Errorf("accounts: back up %s: %w", dir, err)
		}
	}
	return cfg.RemoveAccount(name)
}
