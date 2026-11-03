package syncthing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultTimeout bounds every REST call.
const defaultTimeout = 5 * time.Second

// Client is a minimal syncthing REST client. Every request carries the
// X-API-Key header.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// NewClient builds a Client for ep with the default 5s timeout.
func NewClient(ep *Endpoint) *Client {
	return &Client{
		BaseURL: strings.TrimRight(ep.BaseURL, "/"),
		APIKey:  ep.APIKey,
		HTTP:    &http.Client{Timeout: defaultTimeout},
	}
}

// SystemStatus is the subset of /rest/system/status cwm needs.
type SystemStatus struct {
	MyID string `json:"myID"`
}

// Config is the subset of /rest/config cwm reads and writes. Unknown
// top-level fields are preserved across GET-modify-PUT via json.RawMessage.
type Config struct {
	Devices []Device `json:"devices"`
	Folders []Folder `json:"folders"`

	extra map[string]json.RawMessage
}

// Device is a syncthing device entry.
type Device struct {
	DeviceID  string   `json:"deviceID"`
	Name      string   `json:"name,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
}

// Folder is a syncthing folder entry.
type Folder struct {
	ID           string         `json:"id"`
	Label        string         `json:"label"`
	Path         string         `json:"path"`
	Type         string         `json:"type"`
	Devices      []FolderDevice `json:"devices"`
	Versioning   Versioning     `json:"versioning"`
	IgnoreDelete *bool          `json:"ignoreDelete,omitempty"`
	Paused       *bool          `json:"paused,omitempty"`
}

// FolderDevice is a device sharing a folder.
type FolderDevice struct {
	DeviceID string `json:"deviceID"`
}

// Versioning is the folder versioning config (D16: staggered, 30 days).
type Versioning struct {
	Type   string            `json:"type"`
	Params map[string]string `json:"params"`
}

// UnmarshalJSON decodes the full config and stashes unknown top-level fields.
func (c *Config) UnmarshalJSON(data []byte) error {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	extra := make(map[string]json.RawMessage, len(all))
	for k, v := range all {
		if k == "devices" || k == "folders" {
			continue
		}
		extra[k] = v
	}
	type plain Config
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	c.Devices = p.Devices
	c.Folders = p.Folders
	c.extra = extra
	return nil
}

// MarshalJSON reassembles the config: known fields plus preserved extras.
func (c Config) MarshalJSON() ([]byte, error) {
	out := make(map[string]json.RawMessage, len(c.extra)+2)
	for k, v := range c.extra {
		out[k] = v
	}
	devices := c.Devices
	if devices == nil {
		devices = []Device{}
	}
	folders := c.Folders
	if folders == nil {
		folders = []Folder{}
	}
	db, err := json.Marshal(devices)
	if err != nil {
		return nil, err
	}
	fb, err := json.Marshal(folders)
	if err != nil {
		return nil, err
	}
	out["devices"] = db
	out["folders"] = fb
	return json.Marshal(out)
}

// DeviceConnection is one entry of /rest/system/connections.
type DeviceConnection struct {
	Connected     bool      `json:"connected"`
	Address       string    `json:"address"`
	At            time.Time `json:"at"`
	InBytesTotal  int64     `json:"inBytesTotal"`
	OutBytesTotal int64     `json:"outBytesTotal"`
}

// Connections is /rest/system/connections.
type Connections struct {
	Connections map[string]DeviceConnection `json:"connections"`
	Total       DeviceConnection            `json:"total"`
}

// Completion is the subset of /rest/db/completion cwm needs.
type Completion struct {
	Completion  float64 `json:"completion"`
	GlobalBytes int64   `json:"globalBytes"`
}

// Ping checks API reachability (GET /rest/system/ping).
func (c *Client) Ping() error {
	req, err := c.newRequest(http.MethodGet, "/rest/system/ping", nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// Status fetches /rest/system/status (myID).
func (c *Client) Status() (*SystemStatus, error) {
	var st SystemStatus
	if err := c.getJSON("/rest/system/status", &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// GetConfig fetches the full config (GET /rest/config).
func (c *Client) GetConfig() (*Config, error) {
	var cfg Config
	if err := c.getJSON("/rest/config", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// PutConfig replaces the config (PUT /rest/config, full JSON body).
func (c *Client) PutConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("syncthing: PutConfig: config is nil")
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("syncthing: encode config: %w", err)
	}
	req, err := c.newRequest(http.MethodPut, "/rest/config", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, nil)
}

// RestartRequired reports whether pending config changes need a restart.
func (c *Client) RestartRequired() (bool, error) {
	var rr struct {
		RequiresRestart bool `json:"requiresRestart"`
	}
	if err := c.getJSON("/rest/config/restart-required", &rr); err != nil {
		return false, err
	}
	return rr.RequiresRestart, nil
}

// Restart asks syncthing to restart (POST /rest/system/restart).
func (c *Client) Restart() error {
	req, err := c.newRequest(http.MethodPost, "/rest/system/restart", nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// Connections fetches per-device connection stats.
func (c *Client) Connections() (*Connections, error) {
	var conns Connections
	if err := c.getJSON("/rest/system/connections", &conns); err != nil {
		return nil, err
	}
	return &conns, nil
}

// Completion fetches folder completion (percent) for a device.
func (c *Client) Completion(device, folder string) (*Completion, error) {
	q := url.Values{}
	q.Set("device", device)
	q.Set("folder", folder)
	var comp Completion
	if err := c.getJSON("/rest/db/completion?"+q.Encode(), &comp); err != nil {
		return nil, err
	}
	return &comp, nil
}

// WaitReady polls Ping up to tries times, sleeping delay between attempts.
func (c *Client) WaitReady(tries int, delay time.Duration) error {
	var lastErr error
	for i := 0; i < tries; i++ {
		err := c.Ping()
		if err == nil {
			return nil
		}
		lastErr = err
		if i+1 < tries && delay > 0 {
			time.Sleep(delay)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("unreachable")
	}
	return fmt.Errorf("syncthing: not ready after %d tries: %w", tries, lastErr)
}

func (c *Client) getJSON(path string, out interface{}) error {
	req, err := c.newRequest(http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) newRequest(method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return nil, fmt.Errorf("syncthing: build %s %s: %w", method, path, err)
	}
	req.Header.Set("X-API-Key", c.APIKey)
	return req, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: defaultTimeout}
}

func (c *Client) do(req *http.Request, out interface{}) error {
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("syncthing: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("syncthing: %s %s: HTTP %d: %s", req.Method, req.URL.Path, resp.StatusCode, readCapped(resp.Body))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("syncthing: %s %s: decode response: %w", req.Method, req.URL.Path, err)
	}
	return nil
}

// readCapped reads an error body capped at 4 KiB (read lists capped).
func readCapped(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 4096))
	return strings.TrimSpace(string(b))
}
