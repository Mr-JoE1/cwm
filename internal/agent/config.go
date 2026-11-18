// Package agent holds the pieces of the always-on dashboard agent (v0.6,
// D31) that are independent of the HTTP handlers: the persistent
// server.toml (token + bound port), the single-instance lock, the probe a
// second launch uses to find the running agent, port selection and the
// detached spawn used by `cwm dashboard`.
package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/Mr-JoE1/cwm/internal/storage"
)

// ConfigName is the server state file under the cwm home.
const ConfigName = "server.toml"

// tokenBytes matches the D24 token size (64 hex chars).
const tokenBytes = 32

// Config is server.toml. Token is the persistent dashboard key; Port, Bind,
// PID and Started describe the agent that last bound (so `cwm dashboard`
// and a second launch can find it).
type Config struct {
	Token   string    `toml:"token"`
	Port    int       `toml:"port,omitempty"`
	Bind    string    `toml:"bind,omitempty"`
	PID     int       `toml:"pid,omitempty"`
	Started time.Time `toml:"started,omitempty"`
}

// ConfigPath is <root>/server.toml.
func ConfigPath(root string) string { return filepath.Join(root, ConfigName) }

// ValidToken reports whether t is a 64-char lowercase hex token.
func ValidToken(t string) bool {
	if len(t) != 2*tokenBytes {
		return false
	}
	for _, r := range t {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// NewToken returns a fresh crypto/rand token, hex-encoded.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("agent: generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Load reads server.toml. A missing file is not an error: it returns the
// zero Config (no token yet).
func Load(root string) (Config, error) {
	var c Config
	data, err := os.ReadFile(ConfigPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("agent: read %s: %w", ConfigPath(root), err)
	}
	if _, err := toml.Decode(string(data), &c); err != nil {
		return Config{}, fmt.Errorf("agent: parse %s: %w", ConfigPath(root), err)
	}
	return c, nil
}

// header is written above the TOML body: the file is a credential.
const header = "# cwm agent state — managed by cwm. `token` is the dashboard key:\n" +
	"# keep it private; rotate it with `cwm service rotate-token`.\n"

// Save writes server.toml atomically with mode 0600 (on Windows the file
// sits under the user profile, whose inherited ACL limits it to the user,
// SYSTEM and Administrators).
func Save(root string, c Config) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("agent: create %s: %w", root, err)
	}
	var b bytes.Buffer
	b.WriteString(header)
	if err := toml.NewEncoder(&b).Encode(c); err != nil {
		return fmt.Errorf("agent: encode %s: %w", ConfigName, err)
	}
	if err := storage.WriteFileAtomic(ConfigPath(root), b.Bytes(), 0o600); err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	return nil
}

// EnsureToken loads server.toml and creates (or repairs) the token so the
// returned Config always carries a valid one. A malformed file is replaced
// rather than blocking the agent forever.
func EnsureToken(root string) (Config, error) {
	c, err := Load(root)
	if err != nil {
		c = Config{}
	}
	if ValidToken(c.Token) && err == nil {
		return c, nil
	}
	tok, terr := NewToken()
	if terr != nil {
		return Config{}, terr
	}
	c.Token = tok
	if err := Save(root, c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// RotateToken replaces the token (keeping port/bind/pid) and returns the
// new Config. A running agent picks the change up by watching the file.
func RotateToken(root string) (Config, error) {
	c, err := Load(root)
	if err != nil {
		c = Config{}
	}
	tok, err := NewToken()
	if err != nil {
		return Config{}, err
	}
	c.Token = tok
	if err := Save(root, c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// URL is the token-exchange URL for an agent on host:port.
func URL(host string, port int, token string) string {
	return fmt.Sprintf("http://%s/?t=%s", hostPort(host, port), token)
}

// BaseURL is the dashboard root without a token.
func BaseURL(host string, port int) string {
	return fmt.Sprintf("http://%s/", hostPort(host, port))
}

func hostPort(host string, port int) string {
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// Watch polls server.toml every interval and calls onChange with the new
// Config whenever its modification time or size changes and it carries a
// valid token — the running agent's half of `cwm service rotate-token`.
// It returns when ctx is done.
func Watch(ctx context.Context, root string, interval time.Duration, onChange func(Config)) {
	stamp := func() (time.Time, int64) {
		fi, err := os.Stat(ConfigPath(root))
		if err != nil {
			return time.Time{}, -1
		}
		return fi.ModTime(), fi.Size()
	}
	lastT, lastN := stamp()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		mt, n := stamp()
		if mt.Equal(lastT) && n == lastN {
			continue
		}
		lastT, lastN = mt, n
		if c, err := Load(root); err == nil && ValidToken(c.Token) {
			onChange(c)
		}
	}
}
