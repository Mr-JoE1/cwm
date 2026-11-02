// Package accounts implements `cwm account add|list|use|remove`: logging a
// claude account in via `claude auth login` into a private per-account dir,
// extracting the login identity, and registering the account in config.toml.
package accounts

import (
	"encoding/json"
	"fmt"
	"os"
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
			if err := dec.Decode(&id); err != nil {
				return Identity{}, fmt.Errorf("accounts: %s: decode oauthAccount: %w", path, err)
			}
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
