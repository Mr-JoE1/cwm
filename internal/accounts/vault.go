package accounts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// IdentityKeys are the account-specific top-level keys of .claude.json
// (SPEC-V0.8 research notes): the login's identity plus the caches Claude
// Code keys to that login. A default-account switch moves exactly these
// keys and never touches any other key (projects, mcpServers, userID,
// UI toggles…).
var IdentityKeys = []string{
	"oauthAccount",
	"primaryApiKey",
	"customApiKeyResponses",
	"modelAccessCache",
	"orgModelDefaultCache",
	"passesEligibilityCache",
	"groveConfigCache",
	"groveChoiceSeenAt",
	"cachedExtraUsageDisabledReason",
	"additionalModelOptionsCache",
	"additionalModelCostsCache",
	"clientDataCacheSlots",
}

// ErrNoCredentials means a credential store holds nothing for the paths.
var ErrNoCredentials = errors.New("accounts: no credentials stored")

// CredStore reads and writes one login's credentials wherever Claude Code
// keeps them for a given set of paths: the .credentials.json file
// (Linux, Windows, the macOS fallback) or the macOS Keychain.
type CredStore interface {
	Read(p claudepaths.Paths) ([]byte, error)
	Write(p claudepaths.Paths, data []byte) error
	// Location names where the credentials live, for messages (never the
	// value).
	Location(p claudepaths.Paths) string
}

// FileStore is the .credentials.json store.
type FileStore struct{}

func (FileStore) Read(p claudepaths.Paths) ([]byte, error) {
	data, err := os.ReadFile(p.Credentials)
	if os.IsNotExist(err) {
		return nil, ErrNoCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("accounts: read %s: %w", p.Credentials, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, ErrNoCredentials
	}
	return data, nil
}

func (FileStore) Write(p claudepaths.Paths, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p.Credentials), 0o700); err != nil {
		return fmt.Errorf("accounts: create %s: %w", filepath.Dir(p.Credentials), err)
	}
	if err := storage.WriteFileAtomic(p.Credentials, data, 0o600); err != nil {
		return fmt.Errorf("accounts: write %s: %w", p.Credentials, err)
	}
	return nil
}

func (FileStore) Location(p claudepaths.Paths) string { return claudepaths.Tilde(p.Credentials) }

// SecurityRunner runs the macOS `security` CLI with stdin; tests inject a
// fake.
type SecurityRunner func(stdin []byte, args ...string) ([]byte, error)

func runSecurity(stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command("security", args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("security %s: %w", args[0], err)
	}
	return out, nil
}

// KeychainStore is the macOS login Keychain, driven through `security`
// (untested on hardware; see SPEC-V0.8 research notes). A
// .credentials.json next to the paths wins: Claude Code writes that file
// only when the Keychain refused the write, and then reads it too.
type KeychainStore struct {
	Run  SecurityRunner
	User string
}

var keychainUserRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// KeychainUser is the Keychain account Claude Code uses: $USER, or
// claude-code-user when the name has other characters.
func KeychainUser() string {
	u := os.Getenv("USER")
	if u == "" || !keychainUserRe.MatchString(u) {
		return "claude-code-user"
	}
	return u
}

// KeychainService is the generic-password service Claude Code uses for
// paths: "Claude Code-credentials", plus "-<8 hex of sha256(dir)>" when
// CLAUDE_CONFIG_DIR is set for them.
func KeychainService(p claudepaths.Paths) string {
	const base = "Claude Code-credentials"
	if p.Env == "" {
		return base
	}
	sum := sha256.Sum256([]byte(p.Env))
	return base + "-" + hex.EncodeToString(sum[:])[:8]
}

func (k KeychainStore) run() SecurityRunner {
	if k.Run != nil {
		return k.Run
	}
	return runSecurity
}

func (k KeychainStore) user() string {
	if k.User != "" {
		return k.User
	}
	return KeychainUser()
}

func (k KeychainStore) Read(p claudepaths.Paths) ([]byte, error) {
	if data, err := (FileStore{}).Read(p); err == nil {
		return data, nil
	}
	out, err := k.run()(nil, "find-generic-password", "-a", k.user(), "-w", "-s", KeychainService(p))
	if err != nil {
		return nil, ErrNoCredentials
	}
	out = bytes.TrimRight(out, "\r\n")
	if len(out) == 0 {
		return nil, ErrNoCredentials
	}
	return out, nil
}

func (k KeychainStore) Write(p claudepaths.Paths, data []byte) error {
	if _, err := os.Stat(p.Credentials); err == nil {
		return FileStore{}.Write(p, data)
	}
	// `security -i` reads the command from stdin and -X takes the
	// password hex-encoded, so the secret is never in any argv (ps) and
	// needs no quoting.
	line := fmt.Sprintf("add-generic-password -U -a %q -s %q -X %s\n", k.user(), KeychainService(p), hex.EncodeToString(data))
	if _, err := k.run()([]byte(line), "-i"); err != nil {
		return fmt.Errorf("accounts: keychain write %s: %w", KeychainService(p), err)
	}
	return nil
}

// Forget deletes the Keychain item for p (account remove).
func (k KeychainStore) Forget(p claudepaths.Paths) error {
	if _, err := k.run()(nil, "delete-generic-password", "-a", k.user(), "-s", KeychainService(p)); err != nil {
		return fmt.Errorf("accounts: keychain delete %s: %w", KeychainService(p), err)
	}
	return nil
}

func (k KeychainStore) Location(p claudepaths.Paths) string {
	if _, err := os.Stat(p.Credentials); err == nil {
		return claudepaths.Tilde(p.Credentials)
	}
	return "macOS Keychain (" + KeychainService(p) + ")"
}

// DefaultStore is the platform store: the Keychain on macOS, the file
// elsewhere.
func DefaultStore() CredStore {
	if runtime.GOOS == "darwin" {
		return KeychainStore{}
	}
	return FileStore{}
}

// VaultPaths are where account name's vault entry lives: its login-only
// dir ~/.cwm/accounts/<name>, addressed as Claude Code would with
// CLAUDE_CONFIG_DIR pointing there (so the Keychain service matches what
// a parallel `cwm run --account <name>` uses).
func VaultPaths(l *storage.Layout, mode, name string) (claudepaths.Paths, error) {
	return claudepaths.Live(l, mode, name, "")
}

// readTop decodes a .claude.json into its raw top-level keys; a missing
// or empty file is an empty object.
func readTop(path string) (map[string]json.RawMessage, error) {
	top := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return top, nil
	}
	if err != nil {
		return nil, fmt.Errorf("accounts: read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return top, nil
	}
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("accounts: %s is not a JSON object: %w", path, err)
	}
	if top == nil {
		top = map[string]json.RawMessage{}
	}
	return top, nil
}

func writeTop(path string, top map[string]json.RawMessage) error {
	data, err := json.Marshal(top)
	if err != nil {
		return fmt.Errorf("accounts: encode %s: %w", path, err)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		return fmt.Errorf("accounts: indent %s: %w", path, err)
	}
	out.WriteByte('\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("accounts: create %s: %w", filepath.Dir(path), err)
	}
	if err := storage.WriteFileAtomic(path, out.Bytes(), 0o600); err != nil {
		return fmt.Errorf("accounts: write %s: %w", path, err)
	}
	return nil
}

// MergeIdentity copies the identity keys from src into dst (both
// .claude.json files): every identity key present in src is written, every
// identity key src lacks is removed from dst, and no other key of dst
// changes. A missing dst is created.
func MergeIdentity(src, dst string) error {
	from, err := readTop(src)
	if err != nil {
		return err
	}
	to, err := readTop(dst)
	if err != nil {
		return err
	}
	for _, k := range IdentityKeys {
		if v, ok := from[k]; ok {
			to[k] = v
		} else {
			delete(to, k)
		}
	}
	return writeTop(dst, to)
}

// SwitchResult reports a default-account switch.
type SwitchResult struct {
	From, To string
	// Backup holds the replaced live files ("" = nothing was replaced).
	Backup string
	// SavedBack is set when the old default's live login was saved into
	// its vault entry first.
	SavedBack bool
	// Running lists claude processes that keep using From until they are
	// restarted (best effort; empty = none found or unknown).
	Running []int
}

// Warning is the running-claude message, "" when none applies.
func (r SwitchResult) Warning() string {
	if len(r.Running) == 0 || r.From == "" {
		return ""
	}
	return fmt.Sprintf("open sessions keep using %s until restarted (%d claude process(es) running)", r.From, len(r.Running))
}

// Switcher performs default-account switches (D79). Zero fields take the
// platform defaults.
type Switcher struct {
	Store CredStore
	// Running finds running claude processes (best effort).
	Running func() []int
	// Procs finds running claude processes with the CLAUDE_CONFIG_DIR
	// each runs with, for the Accounts tab's "running now" (best effort;
	// nil = ClaudeProcs).
	Procs func() []ClaudeProc
	Now   func() time.Time
}

// ClaudeProc is one running claude process. EnvKnown is false where the
// platform does not let cwm read another process's environment (macOS,
// Windows): the process is counted, but not attributed to an account.
type ClaudeProc struct {
	PID       int
	ConfigDir string
	EnvKnown  bool
}

// ClaudeProcs lists running claude processes; on Linux with the
// CLAUDE_CONFIG_DIR each runs with (/proc/<pid>/environ, readable for the
// user's own processes). Only that one variable is kept.
func ClaudeProcs() []ClaudeProc {
	if runtime.GOOS == "linux" {
		return procClaudeEnv("/proc")
	}
	var out []ClaudeProc
	for _, pid := range RunningClaude() {
		out = append(out, ClaudeProc{PID: pid})
	}
	return out
}

func procClaudeEnv(proc string) []ClaudeProc {
	var out []ClaudeProc
	for _, pid := range procClaude(proc) {
		env, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "environ"))
		if err != nil {
			out = append(out, ClaudeProc{PID: pid})
			continue
		}
		p := ClaudeProc{PID: pid, EnvKnown: true}
		for _, kv := range bytes.Split(env, []byte{0}) {
			if v, ok := bytes.CutPrefix(kv, []byte("CLAUDE_CONFIG_DIR=")); ok {
				p.ConfigDir = string(v)
			}
		}
		out = append(out, p)
	}
	return out
}

// ListProcs is s.Procs or the platform default.
func (s Switcher) ListProcs() []ClaudeProc {
	if s.Procs != nil {
		return s.Procs()
	}
	return ClaudeProcs()
}

// Use makes name the default account: save the live login back into the
// current default's vault entry (Claude refreshes tokens in place, so the
// live copy is the freshest), back up the live files, then write name's
// credentials and identity keys into the live location. cfg is mutated;
// the caller persists it.
func (s Switcher) Use(l *storage.Layout, cfg *config.Config, name string) (SwitchResult, error) {
	res := SwitchResult{From: cfg.DefaultAccount, To: name}
	if _, ok := cfg.Account(name); !ok {
		return res, fmt.Errorf("accounts: account %q not found", name)
	}
	store := s.Store
	if store == nil {
		store = DefaultStore()
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	mode := cfg.EffectiveMode()
	live, err := claudepaths.Live(l, mode, "", "")
	if err != nil {
		return res, err
	}
	if name == cfg.DefaultAccount {
		return res, nil
	}
	target, err := VaultPaths(l, mode, name)
	if err != nil {
		return res, err
	}
	creds, err := store.Read(target)
	if err != nil {
		if errors.Is(err, ErrNoCredentials) {
			return res, fmt.Errorf("accounts: %s has no saved login — run `cwm account login %s` first", name, name)
		}
		return res, err
	}

	// 1. Save back the current default.
	if res.From != "" {
		from, err := VaultPaths(l, mode, res.From)
		if err != nil {
			return res, err
		}
		if cur, err := store.Read(live); err == nil {
			if err := store.Write(from, cur); err != nil {
				return res, fmt.Errorf("accounts: save %s's login back: %w", res.From, err)
			}
			res.SavedBack = true
		}
		if _, err := os.Stat(live.ClaudeJSON); err == nil {
			if err := MergeIdentity(live.ClaudeJSON, from.ClaudeJSON); err != nil {
				return res, fmt.Errorf("accounts: save %s's identity back: %w", res.From, err)
			}
		}
	}

	// 2. Back up what is about to be replaced.
	backup := filepath.Join(l.BackupsDir(), now().UTC().Format("20060102T150405Z")+"-switch-"+storage.SanitizedLabel(res.From)+"-to-"+storage.SanitizedLabel(name))
	for _, f := range []string{live.Credentials, live.ClaudeJSON} {
		if _, err := os.Stat(f); err != nil {
			continue
		}
		if err := os.MkdirAll(backup, 0o700); err != nil {
			return res, fmt.Errorf("accounts: create backup %s: %w", backup, err)
		}
		if err := storage.CopyFile(f, filepath.Join(backup, filepath.Base(f))); err != nil {
			return res, fmt.Errorf("accounts: back up %s: %w", f, err)
		}
		res.Backup = backup
	}

	// 3. Swap the new login in.
	if err := store.Write(live, creds); err != nil {
		return res, err
	}
	if err := MergeIdentity(target.ClaudeJSON, live.ClaudeJSON); err != nil {
		return res, err
	}
	if err := cfg.SetDefaultAccount(name); err != nil {
		return res, err
	}
	running := s.Running
	if running == nil {
		running = RunningClaude
	}
	res.Running = running()
	return res, nil
}

// RunningClaude lists running claude processes, best effort per OS: /proc
// on Linux, pgrep on macOS, tasklist on Windows. Any failure is "none
// found".
func RunningClaude() []int {
	switch runtime.GOOS {
	case "linux":
		return procClaude("/proc")
	case "windows":
		out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq claude.exe", "/FO", "CSV", "/NH").Output()
		if err != nil {
			return nil
		}
		return parseTasklist(string(out))
	default:
		out, err := exec.Command("pgrep", "-x", "claude").Output()
		if err != nil {
			return nil
		}
		return parsePids(string(out))
	}
}

// procClaude scans a /proc tree for processes whose executable name or
// argv[0] base is claude.
func procClaude(proc string) []int {
	entries, err := os.ReadDir(proc)
	if err != nil {
		return nil
	}
	var pids []int
	self := os.Getpid()
	for _, e := range entries {
		var pid int
		if _, err := fmt.Sscanf(e.Name(), "%d", &pid); err != nil || pid == self {
			continue
		}
		comm, _ := os.ReadFile(filepath.Join(proc, e.Name(), "comm"))
		cmd, _ := os.ReadFile(filepath.Join(proc, e.Name(), "cmdline"))
		argv0, _, _ := bytes.Cut(cmd, []byte{0})
		if strings.TrimSpace(string(comm)) == "claude" || filepath.Base(string(argv0)) == "claude" {
			pids = append(pids, pid)
		}
	}
	return pids
}

func parsePids(s string) []int {
	var pids []int
	for _, f := range strings.Fields(s) {
		var pid int
		if _, err := fmt.Sscanf(f, "%d", &pid); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// parseTasklist reads `tasklist /FO CSV /NH` rows: "claude.exe","1234",…
func parseTasklist(s string) []int {
	var pids []int
	for _, line := range strings.Split(s, "\n") {
		parts := strings.Split(strings.TrimSpace(line), ",")
		if len(parts) < 2 || !strings.EqualFold(strings.Trim(parts[0], `"`), "claude.exe") {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(strings.Trim(parts[1], `"`), "%d", &pid); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// SwitchFunc adapts s for RemoveOptions.Switch.
func (s Switcher) SwitchFunc(l *storage.Layout) func(cfg *config.Config, next string) error {
	return func(cfg *config.Config, next string) error {
		_, err := s.Use(l, cfg, next)
		return err
	}
}

// CredStore is s.Store or the platform default.
func (s Switcher) CredStore() CredStore {
	if s.Store != nil {
		return s.Store
	}
	return DefaultStore()
}

// Has reports whether store holds credentials for p without reading the
// secret: the file exists, or (Keychain) the item exists. `security
// find-generic-password` without -w prints only the item's attributes,
// so the health check never triggers a Keychain access prompt.
func Has(store CredStore, p claudepaths.Paths) bool {
	if info, err := os.Stat(p.Credentials); err == nil && !info.IsDir() && info.Size() > 0 {
		return true
	}
	k, ok := store.(KeychainStore)
	if !ok {
		return false
	}
	_, err := k.run()(nil, "find-generic-password", "-a", k.user(), "-s", KeychainService(p))
	return err == nil
}

// SaveLive copies the live login (credentials and identity keys) into
// the default account's vault entry when that entry is empty: the
// save-back step of a switch on its own (`cwm doctor --fix`). An entry
// that already holds a login is left alone (the live login could be one
// the user switched to by hand with /login; the next `cwm account use`
// saves back anyway). It reports whether anything was saved.
func SaveLive(l *storage.Layout, cfg *config.Config, store CredStore) (bool, error) {
	if cfg.DefaultAccount == "" {
		return false, nil
	}
	if store == nil {
		store = DefaultStore()
	}
	mode := cfg.EffectiveMode()
	live, err := claudepaths.Live(l, mode, "", "")
	if err != nil {
		return false, err
	}
	cur, err := store.Read(live)
	if err != nil {
		if errors.Is(err, ErrNoCredentials) {
			return false, nil
		}
		return false, err
	}
	vault, err := VaultPaths(l, mode, cfg.DefaultAccount)
	if err != nil {
		return false, err
	}
	if Has(store, vault) {
		return false, nil
	}
	if err := os.MkdirAll(vault.ConfigDir, 0o700); err != nil {
		return false, fmt.Errorf("accounts: create %s: %w", vault.ConfigDir, err)
	}
	if err := store.Write(vault, cur); err != nil {
		return false, fmt.Errorf("accounts: save %s's login: %w", cfg.DefaultAccount, err)
	}
	if _, err := os.Stat(live.ClaudeJSON); err == nil {
		if err := MergeIdentity(live.ClaudeJSON, vault.ClaudeJSON); err != nil {
			return false, fmt.Errorf("accounts: save %s's identity: %w", cfg.DefaultAccount, err)
		}
	}
	return true, nil
}

// InstallDefault puts the default account's vault login into the live
// location (credentials and identity keys), so plain claude and cwm's
// own default launches are logged in. It does nothing for any other
// account. Without force it only fills an empty live location: the first
// account added on a machine that had no Claude login. With force (the
// default account logged in again) the live files are replaced, after a
// timestamped backup under ~/.cwm/backups/. It reports whether anything
// was installed.
func InstallDefault(l *storage.Layout, cfg *config.Config, name string, store CredStore, force bool, now time.Time) (bool, error) {
	if name == "" || name != cfg.DefaultAccount {
		return false, nil
	}
	if store == nil {
		store = DefaultStore()
	}
	mode := cfg.EffectiveMode()
	live, err := claudepaths.Live(l, mode, "", "")
	if err != nil {
		return false, err
	}
	if !force && Has(store, live) {
		return false, nil
	}
	vault, err := VaultPaths(l, mode, name)
	if err != nil {
		return false, err
	}
	creds, err := store.Read(vault)
	if err != nil {
		if errors.Is(err, ErrNoCredentials) {
			return false, nil
		}
		return false, err
	}
	if cur, err := store.Read(live); err == nil && bytes.Equal(cur, creds) {
		return false, nil
	}
	backup := filepath.Join(l.BackupsDir(), now.UTC().Format("20060102T150405Z")+"-login-"+storage.SanitizedLabel(name))
	for _, f := range []string{live.Credentials, live.ClaudeJSON} {
		if _, err := os.Stat(f); err != nil {
			continue
		}
		if err := os.MkdirAll(backup, 0o700); err != nil {
			return false, fmt.Errorf("accounts: create backup %s: %w", backup, err)
		}
		if err := storage.CopyFile(f, filepath.Join(backup, filepath.Base(f))); err != nil {
			return false, fmt.Errorf("accounts: back up %s: %w", f, err)
		}
	}
	if err := os.MkdirAll(live.ConfigDir, 0o700); err != nil {
		return false, fmt.Errorf("accounts: create %s: %w", live.ConfigDir, err)
	}
	if err := store.Write(live, creds); err != nil {
		return false, fmt.Errorf("accounts: install %s's login: %w", name, err)
	}
	if _, err := os.Stat(vault.ClaudeJSON); err == nil {
		if err := MergeIdentity(vault.ClaudeJSON, live.ClaudeJSON); err != nil {
			return false, fmt.Errorf("accounts: install %s's identity: %w", name, err)
		}
	}
	return true, nil
}
