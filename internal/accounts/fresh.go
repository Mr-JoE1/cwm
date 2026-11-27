package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// Token freshness (D126). Claude Code rotates the OAuth refresh token when
// it renews the access token, and writes the new pair only where it runs:
// the live location for the default account, the account's vault dir
// (CLAUDE_CONFIG_DIR) for any other. The default account has a second
// copy in its vault entry, so two copies of one login can exist, and
// copying the older over the newer restores a spent refresh token (the
// next renewal fails and claude asks to log in again). The newest copy
// always wins: claudeAiOauth.expiresAt first, then the file's mtime.
// cswap (realiti4/claude-swap, MIT) solves the same problem by capturing a
// session's credentials back after it exits; cwm does it in SessionEnd and
// before every switch.

// credMeta is the one field of a credentials blob freshness needs.
type credMeta struct {
	OAuth struct {
		ExpiresAt int64 `json:"expiresAt"`
	} `json:"claudeAiOauth"`
}

// ExpiresAt is the blob's claudeAiOauth.expiresAt (ms since the epoch; 0 =
// none or not JSON).
func ExpiresAt(data []byte) int64 {
	var m credMeta
	if json.Unmarshal(data, &m) != nil {
		return 0
	}
	return m.OAuth.ExpiresAt
}

// credCopy is one copy of a login: its bytes and the credentials file's
// mtime (zero when it lives in the Keychain).
type credCopy struct {
	data  []byte
	mtime time.Time
}

func readCopy(store CredStore, p claudepaths.Paths) (credCopy, error) {
	data, err := store.Read(p)
	if err != nil {
		return credCopy{}, err
	}
	c := credCopy{data: data}
	if fi, err := os.Stat(p.Credentials); err == nil {
		c.mtime = fi.ModTime()
	}
	return c, nil
}

// newer reports whether copy a is newer than copy b (identical copies,
// or no way to tell, are not newer).
func (a credCopy) newer(b credCopy) bool {
	if string(a.data) == string(b.data) {
		return false
	}
	ea, eb := ExpiresAt(a.data), ExpiresAt(b.data)
	if ea != 0 && eb != 0 && ea != eb {
		return ea > eb
	}
	if !a.mtime.IsZero() && !b.mtime.IsZero() {
		return a.mtime.Sub(b.mtime) > mtimeSlack
	}
	return false
}

// mtimeSlack is how much later an mtime must be to count: two writes in
// the same moment (a switch writes both copies) are a tie, not an order.
const mtimeSlack = 2 * time.Second

// Reconciled says what Reconcile did.
type Reconciled string

const (
	// ReconcileNone: nothing to do (one copy, same copy, or no way to
	// tell which is newer).
	ReconcileNone Reconciled = ""
	// ReconcileToVault: the live login was newer; saved into the vault.
	ReconcileToVault Reconciled = "to-vault"
	// ReconcileToLive: the vault copy was newer (a session that ran from
	// the vault dir renewed it); put live.
	ReconcileToLive Reconciled = "to-live"
	// ReconcileOtherAccount: the live login belongs to another account
	// than the default one; nothing was copied.
	ReconcileOtherAccount Reconciled = "other-account"
)

// Reconcile makes the newest copy of the default account's login win
// between the live location and its vault entry (credentials only: the
// identity keys are the same account's). It runs from SessionEnd and
// before a switch. A nil store is the platform store.
func Reconcile(l *storage.Layout, cfg *config.Config, store CredStore) (Reconciled, error) {
	if cfg == nil || cfg.DefaultAccount == "" {
		return ReconcileNone, nil
	}
	if store == nil {
		store = DefaultStore()
	}
	mode := cfg.EffectiveMode()
	live, err := claudepaths.Live(l, mode, "", "")
	if err != nil {
		return ReconcileNone, err
	}
	vault, err := VaultPaths(l, mode, cfg.DefaultAccount)
	if err != nil {
		return ReconcileNone, err
	}
	lc, err := readCopy(store, live)
	if err != nil {
		return ReconcileNone, ignoreMissing(err)
	}
	vc, err := readCopy(store, vault)
	if err != nil {
		return ReconcileNone, ignoreMissing(err)
	}
	if !sameAccount(live.ClaudeJSON, vault.ClaudeJSON) {
		return ReconcileOtherAccount, nil
	}
	switch {
	case lc.newer(vc):
		if err := store.Write(vault, lc.data); err != nil {
			return ReconcileNone, fmt.Errorf("accounts: save %s's renewed login into the vault: %w", cfg.DefaultAccount, err)
		}
		return ReconcileToVault, nil
	case vc.newer(lc):
		if err := store.Write(live, vc.data); err != nil {
			return ReconcileNone, fmt.Errorf("accounts: put %s's renewed login live: %w", cfg.DefaultAccount, err)
		}
		return ReconcileToLive, nil
	}
	return ReconcileNone, nil
}

func ignoreMissing(err error) error {
	if errors.Is(err, ErrNoCredentials) {
		return nil
	}
	return err
}

// sameAccount: the two .claude.json files do not name different accounts
// (an unknown side counts as the same: the vault entry's own identity is
// often all there is).
func sameAccount(a, b string) bool {
	ia, erra := ReadIdentity(a)
	ib, errb := ReadIdentity(b)
	if erra != nil || errb != nil || ia.AccountUUID == "" || ib.AccountUUID == "" {
		return true
	}
	return ia.AccountUUID == ib.AccountUUID
}
