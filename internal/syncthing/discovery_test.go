package syncthing

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const sampleConfigXML = `<configuration version="37">
  <folder id="x" path="/tmp/x" type="sendreceive"></folder>
  <gui enabled="true" tls="%s" debugging="false">
    <address>%s</address>
    <apikey>%s</apikey>
    <theme>default</theme>
    <user>admin</user>
  </gui>
</configuration>
`

func fakeConfigXML(tls, address, apikey string) string {
	return fmt.Sprintf(sampleConfigXML, tls, address, apikey)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// setFakeHome redirects home-dir resolution for every host OS.
func setFakeHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func clearOverrides(t *testing.T) {
	t.Helper()
	t.Setenv(EnvURL, "")
	t.Setenv(EnvAPIKey, "")
}

func TestDiscoverForOSConfigPaths(t *testing.T) {
	cases := []struct {
		name    string
		goos    string
		place   int // candidate index to write config.xml at
		tls     string
		address string
		apikey  string
		wantURL string
	}{
		{name: "linux state path", goos: "linux", place: 0, tls: "false", address: "127.0.0.1:8384", apikey: "key-state", wantURL: "http://127.0.0.1:8384"},
		{name: "linux legacy fallback", goos: "linux", place: 1, tls: "false", address: "192.168.1.10:8384", apikey: "key-legacy", wantURL: "http://192.168.1.10:8384"},
		{name: "windows localappdata", goos: "windows", place: 0, tls: "false", address: "127.0.0.1:8384", apikey: "key-win", wantURL: "http://127.0.0.1:8384"},
		{name: "darwin application support", goos: "darwin", place: 0, tls: "false", address: "127.0.0.1:8384", apikey: "key-mac", wantURL: "http://127.0.0.1:8384"},
		{name: "tls attr switches to https", goos: "linux", place: 0, tls: "true", address: "10.0.0.5:8384", apikey: "key-tls", wantURL: "https://10.0.0.5:8384"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			localAppData := filepath.Join(t.TempDir(), "local")
			setFakeHome(t, home)
			t.Setenv("LOCALAPPDATA", localAppData)
			clearOverrides(t)

			candidates := configCandidatesFor(tc.goos, home, localAppData)
			if len(candidates) <= tc.place {
				t.Fatalf("bad case: %d candidates, place %d", len(candidates), tc.place)
			}
			writeFile(t, candidates[tc.place], fakeConfigXML(tc.tls, tc.address, tc.apikey))

			ep, err := discoverForOS(tc.goos)
			if err != nil {
				t.Fatalf("discoverForOS(%s): %v", tc.goos, err)
			}
			if ep.BaseURL != tc.wantURL {
				t.Errorf("BaseURL = %q, want %q", ep.BaseURL, tc.wantURL)
			}
			if ep.APIKey != tc.apikey {
				t.Errorf("APIKey = %q, want %q", ep.APIKey, tc.apikey)
			}
		})
	}
}

func TestDiscoverDefaultAddress(t *testing.T) {
	home := t.TempDir()
	setFakeHome(t, home)
	clearOverrides(t)
	writeFile(t, filepath.Join(home, ".local", "state", "syncthing", "config.xml"), fakeConfigXML("false", "", "somekey"))

	ep, err := discoverForOS("linux")
	if err != nil {
		t.Fatalf("discoverForOS: %v", err)
	}
	if want := "http://127.0.0.1:8384"; ep.BaseURL != want {
		t.Errorf("BaseURL = %q, want %q (default address)", ep.BaseURL, want)
	}
}

func TestDiscoverFirstExistingWins(t *testing.T) {
	home := t.TempDir()
	setFakeHome(t, home)
	clearOverrides(t)
	writeFile(t, filepath.Join(home, ".local", "state", "syncthing", "config.xml"), fakeConfigXML("false", "127.0.0.1:9000", "state-key"))
	writeFile(t, filepath.Join(home, ".config", "syncthing", "config.xml"), fakeConfigXML("false", "127.0.0.1:9001", "legacy-key"))

	ep, err := discoverForOS("linux")
	if err != nil {
		t.Fatalf("discoverForOS: %v", err)
	}
	if ep.APIKey != "state-key" || ep.BaseURL != "http://127.0.0.1:9000" {
		t.Errorf("endpoint = %+v, want state-path values", ep)
	}
}

func TestDiscoverNotConfigured(t *testing.T) {
	home := t.TempDir()
	localAppData := filepath.Join(t.TempDir(), "local")
	setFakeHome(t, home)
	t.Setenv("LOCALAPPDATA", localAppData)
	clearOverrides(t)

	for _, goos := range []string{"linux", "windows", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			_, err := discoverForOS(goos)
			if !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("err = %v, want ErrNotConfigured", err)
			}
		})
	}
}

func TestDiscoverWindowsNoLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", "")
	clearOverrides(t)
	if _, err := discoverForOS("windows"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestDiscoverNoAPIKey(t *testing.T) {
	home := t.TempDir()
	setFakeHome(t, home)
	clearOverrides(t)
	writeFile(t, filepath.Join(home, ".config", "syncthing", "config.xml"), fakeConfigXML("false", "127.0.0.1:8384", ""))

	_, err := discoverForOS("linux")
	if !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("err = %v, want ErrNoAPIKey", err)
	}
}

func TestDiscoverEnvOverrides(t *testing.T) {
	home := t.TempDir()
	localAppData := filepath.Join(t.TempDir(), "local")
	setFakeHome(t, home)
	t.Setenv("LOCALAPPDATA", localAppData)
	clearOverrides(t)
	for _, goos := range []string{"linux", "windows", "darwin"} {
		for _, p := range configCandidatesFor(goos, home, localAppData) {
			writeFile(t, p, fakeConfigXML("false", "127.0.0.1:8384", "file-key"))
		}
	}

	t.Run("both set win over config", func(t *testing.T) {
		t.Setenv(EnvURL, "http://10.9.8.7:1234")
		t.Setenv(EnvAPIKey, "env-key")
		ep, err := Discover()
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if ep.BaseURL != "http://10.9.8.7:1234" || ep.APIKey != "env-key" {
			t.Errorf("endpoint = %+v, want env values", ep)
		}
	})

	t.Run("single api key override patches config endpoint", func(t *testing.T) {
		t.Setenv(EnvURL, "")
		t.Setenv(EnvAPIKey, "env-only-key")
		ep, err := Discover()
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if ep.BaseURL != "http://127.0.0.1:8384" || ep.APIKey != "env-only-key" {
			t.Errorf("endpoint = %+v, want config URL + env key", ep)
		}
	})

	t.Run("no env uses config", func(t *testing.T) {
		t.Setenv(EnvURL, "")
		t.Setenv(EnvAPIKey, "")
		ep, err := Discover()
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if ep.BaseURL != "http://127.0.0.1:8384" || ep.APIKey != "file-key" {
			t.Errorf("endpoint = %+v, want config values", ep)
		}
	})

	t.Run("no config and only url set is still not configured", func(t *testing.T) {
		empty := t.TempDir()
		setFakeHome(t, empty)
		t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "none"))
		t.Setenv(EnvURL, "http://10.9.8.7:1234")
		t.Setenv(EnvAPIKey, "")
		if _, err := Discover(); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("err = %v, want ErrNotConfigured", err)
		}
	})
}
