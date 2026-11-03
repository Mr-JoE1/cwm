// Package syncthing discovers the local syncthing instance (D17) and drives
// it over the REST API. cwm never manages the daemon lifecycle: it discovers
// a running instance from config.xml, or takes explicit env overrides.
package syncthing

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Env overrides (tests, power users). When both are set they win outright;
// a single one patches the discovered endpoint.
const (
	EnvURL    = "CWM_SYNCTHING_URL"
	EnvAPIKey = "CWM_SYNCTHING_API_KEY"
)

// Endpoint is a resolved syncthing GUI/API endpoint.
type Endpoint struct {
	// BaseURL is scheme://address, e.g. http://127.0.0.1:8384.
	BaseURL string
	// APIKey is the syncthing GUI API key.
	APIKey string
}

var (
	// ErrNotConfigured: no syncthing config.xml found at any OS-specific path.
	ErrNotConfigured = errors.New("syncthing not configured — install and start it once (or run `cwm pair --start`)")
	// ErrNoAPIKey: config.xml exists but carries no <apikey>.
	ErrNoAPIKey = errors.New("syncthing config has no API key — start syncthing once to generate it")
)

// defaultGUIAddress is syncthing's default GUI listen address.
const defaultGUIAddress = "127.0.0.1:8384"

// Discover resolves the local syncthing endpoint. Env overrides win; else the
// OS-specific config.xml is parsed (gui element only). STHOMEDIR/--home
// installs are unsupported.
func Discover() (*Endpoint, error) {
	envURL := os.Getenv(EnvURL)
	envKey := os.Getenv(EnvAPIKey)
	if envURL != "" && envKey != "" {
		return &Endpoint{BaseURL: envURL, APIKey: envKey}, nil
	}
	ep, err := discoverForOS(runtime.GOOS)
	if err != nil {
		return nil, err
	}
	if envURL != "" {
		ep.BaseURL = envURL
	}
	if envKey != "" {
		ep.APIKey = envKey
	}
	return ep, nil
}

// discoverForOS locates config.xml for goos and parses the gui element.
func discoverForOS(goos string) (*Endpoint, error) {
	home := ""
	if goos != "windows" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("syncthing: cannot determine home dir: %w", err)
		}
	}
	for _, path := range configCandidatesFor(goos, home, os.Getenv("LOCALAPPDATA")) {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		return endpointFromXML(path)
	}
	return nil, ErrNotConfigured
}

// configCandidatesFor returns the OS-specific config.xml candidates in
// priority order (first existing wins).
func configCandidatesFor(goos, home, localAppData string) []string {
	switch goos {
	case "windows":
		if localAppData == "" {
			return nil
		}
		return []string{filepath.Join(localAppData, "Syncthing", "config.xml")}
	case "darwin":
		return []string{
			filepath.Join(home, "Library", "Application Support", "Syncthing", "config.xml"),
		}
	default: // linux and everything else
		return []string{
			filepath.Join(home, ".local", "state", "syncthing", "config.xml"),
			filepath.Join(home, ".config", "syncthing", "config.xml"),
		}
	}
}

// configXML captures only the gui element of syncthing's config.xml.
type configXML struct {
	GUI guiConfig `xml:"gui"`
}

type guiConfig struct {
	Address string `xml:"address"`
	APIKey  string `xml:"apikey"`
	TLS     bool   `xml:"tls,attr"`
}

// endpointFromXML parses the gui element of the config.xml at path.
func endpointFromXML(path string) (*Endpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("syncthing: read config %s: %w", path, err)
	}
	var cfg configXML
	if err := xml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("syncthing: parse config %s: %w", path, err)
	}
	if cfg.GUI.APIKey == "" {
		return nil, fmt.Errorf("%s: %w", path, ErrNoAPIKey)
	}
	addr := cfg.GUI.Address
	if addr == "" {
		addr = defaultGUIAddress
	}
	scheme := "http"
	if cfg.GUI.TLS {
		scheme = "https"
	}
	return &Endpoint{BaseURL: scheme + "://" + addr, APIKey: cfg.GUI.APIKey}, nil
}
