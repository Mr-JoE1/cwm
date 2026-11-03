// Package syncengine orchestrates the syncthing config so two cwm devices
// share the workspace folder (v0.3, decisions D16–D18): ensure the fixed
// cwm-workspace folder exists with staggered 30-day versioning, pair peer
// devices, and report sync health. All orchestration goes through the small
// Client seam so tests inject a fake; cwm never manages the daemon
// lifecycle (D17) beyond the explicit StartDetached escape hatch.
package syncengine

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Mr-JoE1/cwm/internal/syncthing"
)

// FolderID is the fixed syncthing folder ID every cwm device shares (D18):
// the folder identity is constant; the path differs per device and that is
// fine.
const FolderID = "cwm-workspace"

// folderLabel is the human-readable folder name shown in the syncthing UI.
const folderLabel = "cwm workspace"

// Staggered versioning parameters (D16): deletions sync normally, but every
// change is recoverable from .stversions for 30 days.
const (
	versioningType     = "staggered"
	paramCleanInterval = "3600"    // clean-up interval, seconds
	paramMaxAge        = "2592000" // 30 days, seconds
)

// deviceIDCharset is the base32 alphabet syncthing device IDs use.
const deviceIDCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// Client is the seam into syncthing the engine depends on. *syncthing.Client
// satisfies it; tests inject a fake.
type Client interface {
	Ping() error
	Status() (*syncthing.SystemStatus, error)
	GetConfig() (*syncthing.Config, error)
	PutConfig(*syncthing.Config) error
	Connections() (*syncthing.Connections, error)
	Completion(device, folder string) (*syncthing.Completion, error)
}

// MyID returns the local syncthing device ID.
func MyID(cl Client) (string, error) {
	st, err := cl.Status()
	if err != nil {
		return "", fmt.Errorf("syncengine: read local device ID: %w", err)
	}
	if st.MyID == "" {
		return "", fmt.Errorf("syncengine: syncthing reported an empty device ID")
	}
	return st.MyID, nil
}

// ValidateDeviceID checks the shape of a peer device ID: 52–60 characters
// from the base32 alphabet (A–Z and 2–7, no dashes). Callers trim first.
func ValidateDeviceID(id string) error {
	if n := len(id); n < 52 || n > 60 {
		return fmt.Errorf("syncengine: invalid device ID %q: want 52-60 characters, base32 (A-Z and 2-7 only — remove any dashes); got %d", id, n)
	}
	for i := 0; i < len(id); i++ {
		if !strings.ContainsRune(deviceIDCharset, rune(id[i])) {
			return fmt.Errorf("syncengine: invalid device ID %q: character %q is not base32 (A-Z and 2-7 only — remove any dashes)", id, string(id[i]))
		}
	}
	return nil
}

// EnsureFolder makes the cwm-workspace folder exist with path wsPath,
// staggered 30-day versioning (D16) and ignoreDelete off (deletions sync;
// versioning is the safety net). Fixes are applied GET-modify-PUT style;
// returns whether the config changed.
func EnsureFolder(cl Client, wsPath string) (bool, error) {
	cfg, err := cl.GetConfig()
	if err != nil {
		return false, fmt.Errorf("syncengine: read config: %w", err)
	}
	if f := findFolder(cfg); f != nil {
		if !fixFolder(f, wsPath) {
			return false, nil
		}
	} else {
		myID, err := MyID(cl)
		if err != nil {
			return false, err
		}
		cfg.Folders = append(cfg.Folders, newFolder(myID, wsPath))
	}
	return putConfig(cl, cfg)
}

// Pair links this device with a peer: ensures the cwm-workspace folder
// (created or fixed as needed), adds the peer to the device list (naming it
// when a name is given and the entry is unnamed), and shares the folder
// with BOTH the local and the peer device. The whole mutation lands with a
// single PUT; a second run with correct state changes nothing.
func Pair(cl Client, peerID, peerName, wsPath string) (bool, error) {
	peerID = strings.TrimSpace(peerID)
	if err := ValidateDeviceID(peerID); err != nil {
		return false, err
	}
	myID, err := MyID(cl)
	if err != nil {
		return false, err
	}
	if peerID == myID {
		return false, fmt.Errorf("syncengine: %s is THIS device's ID — pair with the ID shown by `cwm pair` on the other device", peerID)
	}
	cfg, err := cl.GetConfig()
	if err != nil {
		return false, fmt.Errorf("syncengine: read config: %w", err)
	}
	changed := false
	if f := findFolder(cfg); f != nil {
		if fixFolder(f, wsPath) {
			changed = true
		}
	} else {
		cfg.Folders = append(cfg.Folders, newFolder(myID, wsPath))
		changed = true
	}
	if d := findDevice(cfg, peerID); d == nil {
		cfg.Devices = append(cfg.Devices, syncthing.Device{DeviceID: peerID, Name: peerName})
		changed = true
	} else if d.Name == "" && peerName != "" {
		d.Name = peerName
		changed = true
	}
	f := findFolder(cfg)
	for _, id := range []string{myID, peerID} {
		if !folderHasDevice(f, id) {
			f.Devices = append(f.Devices, syncthing.FolderDevice{DeviceID: id})
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	return putConfig(cl, cfg)
}

// DeviceRow is one line of the sync health report.
type DeviceRow struct {
	ID         string
	Name       string
	Connected  bool
	Address    string
	InBytes    int64
	OutBytes   int64
	Completion float64
}

// DevicesReport joins the configured devices with connection stats and
// workspace-folder completion. The local device is skipped: the report is
// about peers. Completion errors leave the percentage at 0 (a device that
// has never synced has nothing to report).
func DevicesReport(cl Client) ([]DeviceRow, error) {
	myID, err := MyID(cl)
	if err != nil {
		return nil, err
	}
	cfg, err := cl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("syncengine: read config: %w", err)
	}
	conns, err := cl.Connections()
	if err != nil {
		return nil, fmt.Errorf("syncengine: read connections: %w", err)
	}
	rows := make([]DeviceRow, 0, len(cfg.Devices))
	for _, d := range cfg.Devices {
		if d.DeviceID == myID {
			continue
		}
		row := DeviceRow{ID: d.DeviceID, Name: d.Name}
		if c, ok := conns.Connections[d.DeviceID]; ok {
			row.Connected = c.Connected
			row.Address = c.Address
			row.InBytes = c.InBytesTotal
			row.OutBytes = c.OutBytesTotal
		}
		if comp, err := cl.Completion(d.DeviceID, FolderID); err == nil {
			row.Completion = comp.Completion
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// newFolder builds a correct cwm-workspace folder entry shared with myID.
func newFolder(myID, wsPath string) syncthing.Folder {
	return syncthing.Folder{
		ID:         FolderID,
		Label:      folderLabel,
		Path:       filepath.Clean(wsPath),
		Type:       "sendreceive",
		Devices:    []syncthing.FolderDevice{{DeviceID: myID}},
		Versioning: staggeredVersioning(),
	}
}

// fixFolder corrects path, versioning and ignoreDelete in place; it reports
// whether anything changed. Folder devices are Pair's business.
func fixFolder(f *syncthing.Folder, wsPath string) bool {
	changed := false
	if want := filepath.Clean(wsPath); filepath.Clean(f.Path) != want {
		f.Path = want
		changed = true
	}
	if !versioningOK(f.Versioning) {
		f.Versioning = staggeredVersioning()
		changed = true
	}
	if f.IgnoreDelete != nil && *f.IgnoreDelete {
		f.IgnoreDelete = nil // absent == false: deletions sync (D16)
		changed = true
	}
	return changed
}

// staggeredVersioning is the D16 safety net: 30 days of recoverable history.
func staggeredVersioning() syncthing.Versioning {
	return syncthing.Versioning{
		Type: versioningType,
		Params: map[string]string{
			"cleanInterval": paramCleanInterval,
			"maxAge":        paramMaxAge,
		},
	}
}

// versioningOK reports whether v already matches the D16 policy.
func versioningOK(v syncthing.Versioning) bool {
	return v.Type == versioningType &&
		v.Params["cleanInterval"] == paramCleanInterval &&
		v.Params["maxAge"] == paramMaxAge
}

// findFolder returns the cwm-workspace folder from cfg, or nil.
func findFolder(cfg *syncthing.Config) *syncthing.Folder {
	for i := range cfg.Folders {
		if cfg.Folders[i].ID == FolderID {
			return &cfg.Folders[i]
		}
	}
	return nil
}

// findDevice returns the device entry with the given ID, or nil.
func findDevice(cfg *syncthing.Config, deviceID string) *syncthing.Device {
	for i := range cfg.Devices {
		if cfg.Devices[i].DeviceID == deviceID {
			return &cfg.Devices[i]
		}
	}
	return nil
}

// folderHasDevice reports whether f is shared with deviceID.
func folderHasDevice(f *syncthing.Folder, deviceID string) bool {
	for _, d := range f.Devices {
		if d.DeviceID == deviceID {
			return true
		}
	}
	return false
}

// putConfig writes cfg back and reports the change.
func putConfig(cl Client, cfg *syncthing.Config) (bool, error) {
	if err := cl.PutConfig(cfg); err != nil {
		return false, fmt.Errorf("syncengine: write config: %w", err)
	}
	return true, nil
}
