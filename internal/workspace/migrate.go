package workspace

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// MigrateOptions carries the plan/confirm knobs shared by the migrate and
// import flows. Stdin feeds the interactive confirm prompt; a nil Stdin
// means confirmation is impossible and --yes is required.
type MigrateOptions struct {
	Stdout io.Writer
	Stdin  io.Reader
	DryRun bool
	Yes    bool
}

func (o MigrateOptions) withDefaults() MigrateOptions {
	if o.Stdout == nil {
		o.Stdout = io.Discard
	}
	return o
}

// accountLoginFiles are the per-account login files: never shared, never synced.
var accountLoginFiles = []string{".credentials.json", ".claude.json"}

// copyAction is one planned file copy. Conflicts never copy: the existing
// destination wins and the source stays untouched.
type copyAction struct {
	src string
	dst string
}

// accountAction is one planned account import: the credential copies plus
// the config registration.
type accountAction struct {
	name       string
	srcDir     string
	creds      []copyAction
	register   bool // account not in cfg yet → add it
	setDefault bool // point the default account at it
	email      string
}

// importPlan is a fully computed migration plan: printing, confirming and
// executing it never re-walk the sources.
type importPlan struct {
	label        string // error/plan prefix: "migrate", "import", "import ccam"
	source       string
	receiptLabel string // backups/<ts>-<receiptLabel>/RECEIPT.md
	copies       []copyAction
	conflicts    []copyAction
	accounts     []accountAction
	assemble     []string
	// planned tracks destinations already covered by a planned copy, so a
	// second source for the same destination loses (first-content-wins)
	// even before anything is executed.
	planned map[string]string
}

// MigrateFromBashKit imports the legacy bash-kit workspace ~/.claude-me:
// every canonical workspace item present in the source is COPIED into
// workspace/ (dirs merge file-by-file, existing content kept, conflicts
// logged), the source .credentials.json/.claude.json become account "main"
// (registered as the default account), and the launch dir is assembled.
// The source itself is never modified. dryRun prints the plan only.
func MigrateFromBashKit(l *storage.Layout, cfg *config.Config, o MigrateOptions) error {
	o = o.withDefaults()
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("migrate: resolve home dir: %w", err)
	}
	src := filepath.Join(home, ".claude-me")
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("source %s not found — nothing to migrate (the bash-kit --migrate flow arrived in v0.2-C2; run `cwm init` for a fresh workspace)", src)
	}
	p := &importPlan{label: "migrate", source: src, receiptLabel: "migrate"}
	planItems(src, l, p)
	planAccount(cfg, l, src, "main", true, p)
	return runPlan(l, cfg, p, o)
}

// ImportDir imports any claude config dir (default ~/.claude) under the
// same content rules as MigrateFromBashKit, registering the login files as
// the named account (default "imported"). The source is never modified.
func ImportDir(l *storage.Layout, cfg *config.Config, srcDir, accountName string, o MigrateOptions) error {
	o = o.withDefaults()
	if srcDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("import: resolve home dir: %w", err)
		}
		srcDir = filepath.Join(home, ".claude")
	}
	if accountName == "" {
		accountName = "imported"
	}
	if !config.ValidAccountName(accountName) {
		return fmt.Errorf("import: invalid account name %q (want [a-z0-9][a-z0-9_-]{0,31}, not all digits)", accountName)
	}
	if _, err := os.Stat(srcDir); err != nil {
		return fmt.Errorf("import: source dir %s not found", srcDir)
	}
	p := &importPlan{label: "import", source: srcDir, receiptLabel: "import"}
	planItems(srcDir, l, p)
	planAccount(cfg, l, srcDir, accountName, false, p)
	return runPlan(l, cfg, p, o)
}

// ImportCcam imports a ccam installation: every profile dir under
// ~/.local/share/ccam/profiles/ holding login files becomes an account
// named after the dir (identity extracted), and every recognized workspace
// item under ~/.local/share/ccam/groups/<g>/ is copied into workspace/
// (first-content-wins across groups, conflicts logged). The ccam dirs are
// never modified.
func ImportCcam(l *storage.Layout, cfg *config.Config, o MigrateOptions) error {
	o = o.withDefaults()
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("import ccam: resolve home dir: %w", err)
	}
	root := filepath.Join(home, ".local", "share", "ccam")
	profiles := filepath.Join(root, "profiles")

	p := &importPlan{label: "import ccam", source: root, receiptLabel: "import-ccam"}
	ents, err := os.ReadDir(profiles)
	if err != nil {
		return fmt.Errorf("import ccam: no ccam profiles found at ~/.local/share/ccam")
	}
	for _, ent := range ents {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(profiles, ent.Name())
		if !hasCredFiles(dir) {
			continue
		}
		name := strings.ToLower(ent.Name())
		if !config.ValidAccountName(name) {
			fmt.Fprintf(o.Stdout, "⚠ skip ccam profile %q: %q is not a valid account name\n", ent.Name(), name)
			continue
		}
		planAccount(cfg, l, dir, name, false, p)
	}
	groups := filepath.Join(root, "groups")
	if gents, err := os.ReadDir(groups); err == nil {
		for _, g := range gents {
			if g.IsDir() {
				planItems(filepath.Join(groups, g.Name()), l, p)
			}
		}
	}
	if len(p.accounts) == 0 && len(p.copies) == 0 {
		return fmt.Errorf("import ccam: no ccam profiles found at ~/.local/share/ccam")
	}
	return runPlan(l, cfg, p, o)
}

// planItems records the copies/conflicts for every canonical workspace
// item present under src.
func planItems(src string, l *storage.Layout, p *importPlan) {
	for _, item := range storage.WorkspaceItems() {
		srcItem := filepath.Join(src, item)
		fi, err := os.Lstat(srcItem)
		if err != nil {
			continue // item not present in the source
		}
		dstItem := l.WorkspaceItemPath(item)
		if isWorkspaceDirName(item) {
			if !fi.IsDir() {
				p.conflicts = append(p.conflicts, copyAction{src: srcItem, dst: dstItem})
				continue
			}
			_ = filepath.WalkDir(srcItem, func(path string, d fs.DirEntry, err error) error {
				if err != nil || !d.Type().IsRegular() {
					return nil
				}
				rel, relErr := filepath.Rel(srcItem, path)
				if relErr != nil {
					return nil
				}
				planFileCopy(path, filepath.Join(dstItem, rel), p)
				return nil
			})
		} else {
			if !fi.Mode().IsRegular() {
				p.conflicts = append(p.conflicts, copyAction{src: srcItem, dst: dstItem})
				continue
			}
			planFileCopy(srcItem, dstItem, p)
		}
	}
}

// planFileCopy merges one source file into the workspace: destinations
// already claimed by an earlier planned copy win (first-content-wins),
// missing or empty (placeholder) destinations are filled, identical
// content is skipped, differing content is a conflict that keeps the
// destination as is.
func planFileCopy(src, dst string, p *importPlan) {
	if p.planned == nil {
		p.planned = map[string]string{}
	}
	if _, claimed := p.planned[dst]; claimed {
		p.conflicts = append(p.conflicts, copyAction{src: src, dst: dst})
		return
	}
	fi, err := os.Lstat(dst)
	if err != nil {
		p.planned[dst] = src
		p.copies = append(p.copies, copyAction{src: src, dst: dst})
		return
	}
	if fi.IsDir() {
		p.conflicts = append(p.conflicts, copyAction{src: src, dst: dst})
		return
	}
	if fi.Size() == 0 {
		p.planned[dst] = src
		p.copies = append(p.copies, copyAction{src: src, dst: dst})
		return
	}
	if same, err := filesIdentical(src, dst); err == nil && same {
		return
	}
	p.conflicts = append(p.conflicts, copyAction{src: src, dst: dst})
}

// planAccount records the credential copies and config registration for
// one account sourced from srcDir. A source without any login file yields
// no account at all.
func planAccount(cfg *config.Config, l *storage.Layout, srcDir, name string, makeDefault bool, p *importPlan) {
	act := accountAction{name: name, srcDir: srcDir}
	for _, f := range accountLoginFiles {
		src := filepath.Join(srcDir, f)
		if _, err := os.Stat(src); err == nil {
			act.creds = append(act.creds, copyAction{src: src, dst: filepath.Join(l.AccountsDir(), name, f)})
		}
	}
	if len(act.creds) == 0 {
		return
	}
	_, exists := cfg.Account(name)
	act.register = !exists
	act.setDefault = makeDefault && !exists
	if _, err := os.Stat(filepath.Join(srcDir, ".claude.json")); err == nil {
		if id, err := accounts.ReadIdentity(filepath.Join(srcDir, ".claude.json")); err == nil {
			act.email = id.Email
		}
	}
	p.accounts = append(p.accounts, act)
	p.assemble = append(p.assemble, name)
}

// runPlan prints the plan, honors dry-run, asks for confirmation (or takes
// --yes) and executes.
func runPlan(l *storage.Layout, cfg *config.Config, p *importPlan, o MigrateOptions) error {
	printPlan(p, o)
	if o.DryRun {
		return nil
	}
	proceed, err := confirmPlan(o)
	if err != nil {
		return fmt.Errorf("%s: %w", p.label, err)
	}
	if !proceed {
		fmt.Fprintln(o.Stdout, "aborted — nothing changed")
		return nil
	}
	return executePlan(l, cfg, p, o)
}

func printPlan(p *importPlan, o MigrateOptions) {
	w := o.Stdout
	if o.DryRun {
		fmt.Fprintf(w, "plan for %s (dry run — nothing will be changed):\n", p.label)
	} else {
		fmt.Fprintf(w, "plan for %s:\n", p.label)
	}
	fmt.Fprintf(w, "  source: %s\n", p.source)
	for _, c := range p.copies {
		fmt.Fprintf(w, "  copy    %s -> %s\n", c.src, c.dst)
	}
	for _, c := range p.conflicts {
		fmt.Fprintf(w, "  keep    %s (source %s differs — existing content kept)\n", c.dst, c.src)
	}
	for _, a := range p.accounts {
		desc := a.name
		if a.email != "" {
			desc += " (" + a.email + ")"
		}
		if a.setDefault {
			desc += " [default]"
		}
		if a.register {
			fmt.Fprintf(w, "  add     account %s\n", desc)
		} else {
			fmt.Fprintf(w, "  refresh account %s\n", desc)
		}
	}
	for _, n := range p.assemble {
		fmt.Fprintf(w, "  link    launch/%s\n", n)
	}
	fmt.Fprintf(w, "  receipt backups/<ts>-%s/RECEIPT.md\n", p.receiptLabel)
}

// confirmPlan asks the user to proceed. --yes short-circuits; a nil Stdin
// or a closed one is an error pointing at --yes; an explicit "n" aborts
// cleanly.
func confirmPlan(o MigrateOptions) (bool, error) {
	if o.Yes {
		return true, nil
	}
	if o.Stdin == nil {
		return false, fmt.Errorf("confirmation required — pass --yes (non-interactive)")
	}
	fmt.Fprint(o.Stdout, "proceed? [y/N] ")
	line, err := bufio.NewReader(o.Stdin).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "" && err != nil {
		return false, fmt.Errorf("not confirmed — pass --yes (non-interactive)")
	}
	return answer == "y" || answer == "yes", nil
}

func executePlan(l *storage.Layout, cfg *config.Config, p *importPlan, o MigrateOptions) error {
	label := p.label
	receiptDir := l.BackupDirPath(p.receiptLabel)
	if err := os.MkdirAll(receiptDir, 0o755); err != nil {
		return fmt.Errorf("%s: create receipt dir %s: %w", label, receiptDir, err)
	}

	copied := make([]copyAction, 0, len(p.copies))
	for _, c := range p.copies {
		if err := copyFileReplacing(c.src, c.dst); err != nil {
			return fmt.Errorf("%s: copy %s: %w", label, c.src, err)
		}
		copied = append(copied, c)
	}

	var accountLines []string
	for _, a := range p.accounts {
		line, err := executeAccount(l, cfg, a)
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		accountLines = append(accountLines, line)
	}

	if err := config.Save(l.ConfigPath(), cfg); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	for _, n := range p.assemble {
		if err := EnsureLaunch(l, cfg, n); err != nil {
			return fmt.Errorf("%s: assemble launch dir for %s: %w", label, n, err)
		}
		fmt.Fprintf(o.Stdout, "✓ launch dir assembled at %s\n", filepath.Join(l.LaunchDir(), n))
	}

	receipt := filepath.Join(receiptDir, "RECEIPT.md")
	if err := writeReceipt(receipt, p, copied, accountLines, time.Now().UTC()); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	fmt.Fprintf(o.Stdout, "✓ %s complete: %d file(s) copied, %d conflict(s) kept, %d account(s) imported\n",
		label, len(copied), len(p.conflicts), len(accountLines))
	fmt.Fprintf(o.Stdout, "✓ receipt: %s\n", receipt)
	return nil
}

// executeAccount imports one account's credentials and config entry; it
// returns the receipt line describing it.
func executeAccount(l *storage.Layout, cfg *config.Config, a accountAction) (string, error) {
	dir := filepath.Join(l.AccountsDir(), a.name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	for _, c := range a.creds {
		if err := copyFileReplacing(c.src, c.dst); err != nil {
			return "", fmt.Errorf("copy %s: %w", c.src, err)
		}
	}
	if a.register {
		if err := cfg.AddAccount(a.name, dir); err != nil {
			return "", fmt.Errorf("register account %s: %w", a.name, err)
		}
	}
	if a.setDefault {
		if err := cfg.SetDefaultAccount(a.name); err != nil {
			return "", fmt.Errorf("set default account %s: %w", a.name, err)
		}
	}
	line := a.name
	if a.setDefault {
		line += " [default]"
	}
	if id, err := accounts.ReadIdentity(filepath.Join(dir, ".claude.json")); err == nil &&
		(id.Email != "" || id.AccountUUID != "" || id.OrganizationName != "" || id.Plan != "") {
		if err := cfg.SetAccountLogin(a.name, config.Login{
			Email:            id.Email,
			AccountUUID:      id.AccountUUID,
			OrganizationName: id.OrganizationName,
			Plan:             id.Plan,
		}); err != nil {
			return "", fmt.Errorf("record identity for %s: %w", a.name, err)
		}
		if id.Email != "" {
			line += " (" + id.Email + ")"
		}
	}
	return line, nil
}

func writeReceipt(path string, p *importPlan, copied []copyAction, accountLines []string, now time.Time) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# cwm %s receipt\n\n", p.label)
	fmt.Fprintf(&b, "- date: %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(&b, "- source: %s\n", p.source)
	for _, a := range accountLines {
		fmt.Fprintf(&b, "- account: %s\n", a)
	}
	b.WriteString("\n## copied\n\n")
	if len(copied) == 0 {
		b.WriteString("- (none)\n")
	}
	for _, c := range copied {
		fmt.Fprintf(&b, "- %s -> %s\n", c.src, c.dst)
	}
	b.WriteString("\n## conflicts (existing content kept)\n\n")
	if len(p.conflicts) == 0 {
		b.WriteString("- (none)\n")
	}
	for _, c := range p.conflicts {
		fmt.Fprintf(&b, "- %s (source: %s)\n", c.dst, c.src)
	}
	return storage.WriteFileAtomic(path, []byte(b.String()), 0o644)
}

// copyFileReplacing copies src to dst, creating parent dirs and replacing
// any existing dst (merge semantics were decided at plan time).
func copyFileReplacing(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	return storage.CopyFile(src, dst)
}

func hasCredFiles(dir string) bool {
	for _, f := range accountLoginFiles {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

func isWorkspaceDirName(name string) bool {
	for _, d := range storage.WorkspaceDirs {
		if d == name {
			return true
		}
	}
	return false
}

func filesIdentical(a, b string) (bool, error) {
	ca, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	cb, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ca, cb), nil
}
