// Package accounts implements `cwm account add|list|use|remove`: logging a
// claude account in via `claude auth login` into a private per-account dir,
// extracting the login identity, and registering the account in config.toml.
package accounts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Mr-JoE1/cwm/internal/config"
)

// ClaudeConfigDirEnv is the env var claude reads for its config dir; cwm
// points it at a private account dir during login and at the assembled
// launch dir during open.
const ClaudeConfigDirEnv = "CLAUDE_CONFIG_DIR"

// Identity is the oauthAccount block of an account's .claude.json. Every
// field is optional — real files routinely lack organization_name or the
// whole block.
type Identity struct {
	Email            string `json:"email"`
	AccountUUID      string `json:"account_uuid"`
	OrganizationName string `json:"organization_name"`
	Plan             string `json:"plan"`
}

// wireIdentity accepts both spellings of the oauthAccount keys: Claude
// Code itself writes camelCase (accountUuid, emailAddress,
// organizationName); the snake_case names are what cwm read first and
// what older fixtures use. snake_case wins when both are set.
type wireIdentity struct {
	Identity
	EmailAddress          string `json:"emailAddress"`
	AccountUUIDCamel      string `json:"accountUuid"`
	OrganizationNameCamel string `json:"organizationName"`
}

func (w wireIdentity) merged() Identity {
	id := w.Identity
	if id.Email == "" {
		id.Email = w.EmailAddress
	}
	if id.AccountUUID == "" {
		id.AccountUUID = w.AccountUUIDCamel
	}
	if id.OrganizationName == "" {
		id.OrganizationName = w.OrganizationNameCamel
	}
	return id
}

// ReadIdentity extracts the top-level oauthAccount (plus a top-level plan
// fallback) from a .claude.json file. The file can be many megabytes of
// per-project state, so it is scanned token by token and only the values of
// interest are materialized. A missing oauthAccount block is not an error:
// a zero Identity comes back (callers warn and keep the account).
func ReadIdentity(path string) (Identity, error) {
	f, err := os.Open(path)
	if err != nil {
		return Identity{}, fmt.Errorf("accounts: open %s: %w", path, err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	tok, err := dec.Token()
	if err != nil {
		return Identity{}, fmt.Errorf("accounts: parse %s: %w", path, err)
	}
	if tok != json.Delim('{') {
		return Identity{}, fmt.Errorf("accounts: %s: top-level JSON value is not an object", path)
	}

	var (
		id      Identity
		topPlan string
	)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return Identity{}, fmt.Errorf("accounts: parse %s: %w", path, err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return Identity{}, fmt.Errorf("accounts: %s: unexpected object key %v", path, keyTok)
		}
		switch key {
		case "oauthAccount":
			var w wireIdentity
			if err := dec.Decode(&w); err != nil {
				return Identity{}, fmt.Errorf("accounts: %s: decode oauthAccount: %w", path, err)
			}
			id = w.merged()
		case "plan":
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return Identity{}, fmt.Errorf("accounts: %s: decode plan: %w", path, err)
			}
			_ = json.Unmarshal(raw, &topPlan) // non-string plan: ignore
		default:
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return Identity{}, fmt.Errorf("accounts: %s: decode %q: %w", path, key, err)
			}
		}
	}
	if id.Plan == "" {
		id.Plan = topPlan
	}
	return id, nil
}

// UUIDMap maps Claude account uuids to cwm account names, for attributing
// transcripts by their session-owner entries (D62). An account's uuid is
// the one recorded in config.toml ([account.login] account_uuid); an
// account with none recorded falls back to the oauthAccount.accountUuid
// of <account dir>/.claude.json. Missing or broken files, and accounts
// without a uuid, are skipped, never an error. When two accounts share a
// uuid the first in config order keeps it. A nil cfg yields an empty map.
func UUIDMap(cfg *config.Config) map[string]string {
	out := map[string]string{}
	if cfg == nil {
		return out
	}
	for _, a := range cfg.Accounts {
		uuid := ""
		if a.Login != nil {
			uuid = a.Login.AccountUUID
		}
		if uuid == "" {
			if id, err := ReadIdentity(filepath.Join(a.ExpandedDir(), ".claude.json")); err == nil {
				uuid = id.AccountUUID
			}
		}
		if uuid == "" {
			continue
		}
		if _, taken := out[uuid]; !taken {
			out[uuid] = a.Name
		}
	}
	return out
}
