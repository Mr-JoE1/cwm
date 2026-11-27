// Package desktopapp reads which account the Claude desktop app is signed
// in to, and shares its Code-tab session list between accounts (v0.9.3,
// D121–D123).
//
// The desktop app is an Electron/Chromium profile. Its config.json holds
// a plain lastKnownAccountUuid next to the OS-encrypted oauth:tokenCache*
// blobs. cwm decodes only that one uuid (appConfig has no other field) and
// never reads, decrypts, copies or logs the oauth values, the cookies or
// Local Storage.
//
// The profile-root rules and the Code-tab sharing rules follow
// karthiknl0/claude-account-switcher (MIT, desktop-common.ps1:
// Get-SessionRoot, Sync-CodeSessions), re-implemented in Go.
package desktopapp

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/config"
)

// Env is the machine as detection sees it; tests fake Getenv, Home and
// Locked and point them at fixture profiles.
type Env struct {
	GOOS   string
	Getenv func(string) string
	Home   string
	// Locked reports whether another process holds path open (the running
	// app holds its cookie store open). nil = never locked.
	Locked func(path string) bool
}

// System is the real machine.
func System() Env {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		if p := os.Getenv("USERPROFILE"); p != "" {
			home = p
		}
	}
	return Env{GOOS: runtime.GOOS, Getenv: os.Getenv, Home: home, Locked: fileLocked}
}

func (e Env) getenv(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

// Candidates lists every folder the desktop app has used for its profile,
// newest layout first, that exists. The running app's --user-data-dir is
// not asked for: on Windows that takes a CIM query (1–2 s), and the cookie
// lock plus mtimes pick the same root (D121).
func Candidates(e Env) []string {
	var roots []string
	switch e.GOOS {
	case "windows":
		local, roaming := e.getenv("LOCALAPPDATA"), e.getenv("APPDATA")
		if local != "" {
			// Every sub-folder, not only claude_*: the name may change again.
			if ents, err := os.ReadDir(filepath.Join(local, "Claude-Data")); err == nil {
				for _, d := range ents {
					if d.IsDir() {
						roots = append(roots, filepath.Join(local, "Claude-Data", d.Name()))
					}
				}
			}
		}
		if roaming != "" {
			roots = append(roots, filepath.Join(roaming, "Claude"))
		}
		if local != "" {
			if m, err := filepath.Glob(filepath.Join(local, "Packages", "Claude_*")); err == nil {
				sort.Strings(m)
				for _, p := range m {
					roots = append(roots, filepath.Join(p, "LocalCache", "Roaming", "Claude"))
				}
			}
		}
	case "darwin":
		if e.Home != "" {
			roots = append(roots, filepath.Join(e.Home, "Library", "Application Support", "Claude"))
		}
	default:
		if e.Home != "" {
			roots = append(roots, filepath.Join(e.Home, ".config", "Claude"))
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, r := range roots {
		if fi, err := os.Stat(r); err == nil && fi.IsDir() && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// Abandoned: the app leaves a .user-data-left* marker in a folder it has
// moved its data out of.
func Abandoned(root string) bool {
	m, _ := filepath.Glob(filepath.Join(root, ".user-data-left*"))
	return len(m) > 0
}

// CookiesPath is the profile's cookie store ("" = none).
func CookiesPath(root string) string {
	for _, c := range []string{filepath.Join(root, "Network", "Cookies"), filepath.Join(root, "Cookies")} {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// HasProfile: root holds a Chromium profile (config.json, Local State or
// a cookie store).
func HasProfile(root string) bool {
	for _, f := range []string{"config.json", "Local State"} {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			return true
		}
	}
	return CookiesPath(root) != ""
}

// LiveRoot is the profile folder the app really uses: among the
// candidates with a profile (the abandoned ones only when nothing else is
// left), the one whose cookie store is locked (running = true), else the
// one with the newest config.json / Cookies / Local State. "" = none.
func LiveRoot(e Env) (root string, running bool) {
	var cands, current []string
	for _, r := range Candidates(e) {
		if !HasProfile(r) {
			continue
		}
		cands = append(cands, r)
		if !Abandoned(r) {
			current = append(current, r)
		}
	}
	if len(current) > 0 {
		cands = current
	}
	if len(cands) == 0 {
		return "", false
	}
	var newest string
	var newestT time.Time
	for _, r := range cands {
		ck := CookiesPath(r)
		if ck != "" && e.Locked != nil && e.Locked(ck) {
			return r, true
		}
		for _, f := range []string{filepath.Join(r, "config.json"), ck, filepath.Join(r, "Local State")} {
			if f == "" {
				continue
			}
			if fi, err := os.Stat(f); err == nil && fi.ModTime().After(newestT) {
				newest, newestT = r, fi.ModTime()
			}
		}
	}
	if newest != "" {
		return newest, false
	}
	return cands[0], false
}

// appConfig is the only part of the app's config.json cwm decodes. The
// file also holds the encrypted oauth:tokenCache* blobs; this struct has
// no field for them, so encoding/json skips their values and they never
// reach a Go value. Keep it to this one field (TestAppConfigOneField).
type appConfig struct {
	LastKnownAccountUUID string `json:"lastKnownAccountUuid"`
}

// maxConfig bounds the read of config.json (it is a few KB).
const maxConfig = 4 << 20

// ErrNoConfig means the profile has no config.json.
var ErrNoConfig = errors.New("desktopapp: no config.json")

// AccountUUID reads lastKnownAccountUuid from root's config.json ("" =
// none recorded).
func AccountUUID(root string) (string, error) {
	f, err := os.Open(filepath.Join(root, "config.json"))
	if os.IsNotExist(err) {
		return "", ErrNoConfig
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	var c appConfig
	if err := json.NewDecoder(io.LimitReader(f, maxConfig)).Decode(&c); err != nil {
		return "", errors.New("desktopapp: config.json is not readable JSON")
	}
	return strings.TrimSpace(c.LastKnownAccountUUID), nil
}

// State is who the app is signed in to, as cwm sees it.
type State string

const (
	// StateNone: the app is not installed or has no profile yet.
	StateNone State = "none"
	// StateSignedOut: a profile with no account recorded.
	StateSignedOut State = "signed-out"
	// StateAccount: signed in to a cwm account (Status.Account).
	StateAccount State = "account"
	// StateOther: signed in to an account cwm doesn't know.
	StateOther State = "other"
	// StateUnknown: config.json could not be read.
	StateUnknown State = "unknown"
)

// Count is the number of Code-tab entries one account's list holds. Name
// is the cwm account name, "" for an account cwm doesn't know (the uuid
// itself is never exposed).
type Count struct {
	Name    string `json:"name,omitempty"`
	Entries int    `json:"entries"`
	// Signed: this is the list of the account the app is signed in to.
	Signed bool `json:"signed,omitempty"`
}

// Status is the app's profile and sign-in. It never carries a uuid in an
// exported field.
type Status struct {
	Root  string `json:"root,omitempty"`
	State State  `json:"state"`
	// Account is the cwm account name when State is StateAccount.
	Account string `json:"account,omitempty"`
	// Running: the app holds its cookie store open (Windows only; false
	// elsewhere or when unknown).
	Running bool    `json:"running"`
	Lists   []Count `json:"lists,omitempty"`
	uuid    string
}

// UUID is the signed-in account's uuid ("" = none), for the sharing step;
// never print it.
func (s Status) UUID() string { return s.uuid }

// Detect reads the live profile and maps its account uuid to a cwm account
// through names (account uuid -> cwm name, accounts.UUIDMap).
func Detect(e Env, names map[string]string) Status {
	root, running := LiveRoot(e)
	if root == "" {
		return Status{State: StateNone}
	}
	st := Status{Root: root, Running: running}
	uuid, err := AccountUUID(root)
	switch {
	case errors.Is(err, ErrNoConfig):
		st.State = StateSignedOut
	case err != nil:
		st.State = StateUnknown
	case uuid == "":
		st.State = StateSignedOut
	default:
		st.uuid = uuid
		if n, ok := names[uuid]; ok {
			st.State, st.Account = StateAccount, n
		} else {
			st.State = StateOther
		}
	}
	for _, l := range lists(root) {
		st.Lists = append(st.Lists, Count{Name: names[l.uuid], Entries: l.entries, Signed: l.uuid != "" && l.uuid == uuid})
	}
	return st
}

type list struct {
	uuid    string
	entries int
}

// lists counts local_*.json entries per account under
// <root>/claude-code-sessions/<account>/<org>/.
func lists(root string) []list {
	base := filepath.Join(root, SessionsDir)
	accts, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []list
	for _, a := range accts {
		if !a.IsDir() {
			continue
		}
		l := list{uuid: a.Name()}
		for _, org := range subdirs(filepath.Join(base, a.Name())) {
			l.entries += len(entries(org))
		}
		out = append(out, l)
	}
	return out
}

// Who is the account the status names, for messages: the cwm name,
// "another account" or "nobody".
func (s Status) Who() string {
	switch s.State {
	case StateAccount:
		return s.Account
	case StateOther, StateUnknown:
		return "another account"
	}
	return "nobody"
}

// Line is the one-line summary doctor and `cwm desktop status` print. It
// names accounts, never a uuid.
func (s Status) Line() string {
	var b strings.Builder
	switch s.State {
	case StateNone:
		return "not installed (no app profile found)"
	case StateSignedOut:
		b.WriteString("signed out")
	case StateAccount:
		b.WriteString("signed in as account " + s.Account)
	case StateOther:
		b.WriteString("signed in to an account cwm doesn't know")
	default:
		b.WriteString("sign-in unreadable (config.json is not valid JSON)")
	}
	if s.Running {
		b.WriteString(" · running")
	}
	if c := s.CountsText(); c != "" {
		b.WriteString(" · Code-tab entries: " + c)
	}
	return b.String()
}

// CountsText is "work 12, default 3, another account 1" ("" = no lists).
func (s Status) CountsText() string {
	var parts []string
	for _, c := range s.Lists {
		n := c.Name
		if n == "" {
			n = "another account"
		}
		parts = append(parts, n+" "+strconv.Itoa(c.Entries))
	}
	return strings.Join(parts, ", ")
}

// SessionsDir is where the app lists Code-tab sessions, per account and
// organisation: <root>/claude-code-sessions/<account-uuid>/<org-id>/.
const SessionsDir = "claude-code-sessions"

func subdirs(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range ents {
		if d.IsDir() {
			out = append(out, filepath.Join(dir, d.Name()))
		}
	}
	return out
}

// entries are the local_*.json index entries of one list directory, as
// file names.
func entries(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range ents {
		n := f.Name()
		if f.Type().IsRegular() && strings.HasPrefix(n, "local_") && strings.HasSuffix(n, ".json") {
			out = append(out, n)
		}
	}
	return out
}

// ForConfig is Detect on this machine with cfg's accounts (their recorded
// login uuid, accounts.UUIDMap).
func ForConfig(cfg *config.Config) Status {
	return Detect(System(), accounts.UUIDMap(cfg))
}
