package status

import (
	"io"
	"os"

	"github.com/Mr-JoE1/cwm/internal/accounts"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
	"github.com/Mr-JoE1/cwm/internal/workspace"
)

// Account is one account row (alias: accounts builds them, D26). Identity
// and login freshness only (D12): no account dir, no credential content.
type Account = accounts.Row

// Accounts lists the configured accounts in config order (non-nil). A
// missing config.toml means no accounts yet, not an error.
func Accounts(layout *storage.Layout) ([]Account, error) {
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		return nil, err
	}
	return accounts.Rows(accounts.Gather(cfg, layout)), nil
}

// State is the setup snapshot that drives the GUI wizard. DeviceID and
// Paired come from syncthing when it is reachable (Syncthing reports it);
// an unreachable syncthing is a normal pre-pairing state, not an error.
type State struct {
	Initialized bool      `json:"initialized"`
	Accounts    []Account `json:"accounts"`
	DeviceID    string    `json:"deviceID,omitempty"`
	Paired      int       `json:"paired"`
	Syncthing   bool      `json:"syncthing"`
	// Claude is the claude lookup (D49), filled by the GUI; nil elsewhere.
	Claude *Claude `json:"claude,omitempty"`
}

// Claude tells the wizard whether Add account can work: where claude is,
// whether a fresh shell would find it, and the copy-paste commands to
// install it (Install) or put its folder on PATH (PathHint).
type Claude struct {
	Found    bool   `json:"found"`
	Path     string `json:"path,omitempty"`
	OnPath   bool   `json:"onPath"`
	PathHint string `json:"pathHint,omitempty"`
	Install  string `json:"install"`
	Error    string `json:"error,omitempty"`
}

// Initialized reports whether `cwm init` has run on this device: the
// workspace dir and config.toml both exist.
func Initialized(layout *storage.Layout) bool {
	info, err := os.Stat(layout.WorkspaceDir())
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(layout.ConfigPath())
	return err == nil
}

// Setup builds the wizard State. connect may be nil (no syncthing seam).
func Setup(layout *storage.Layout, connect func() (syncengine.Client, error)) (State, error) {
	list, err := Accounts(layout)
	if err != nil {
		return State{}, err
	}
	st := State{Initialized: Initialized(layout), Accounts: list}
	if connect == nil {
		return st, nil
	}
	cl, err := connect()
	if err != nil {
		return st, nil
	}
	myID, err := syncengine.MyID(cl)
	if err != nil {
		return st, nil
	}
	st.Syncthing, st.DeviceID = true, myID
	if cfg, err := cl.GetConfig(); err == nil {
		// Same peer set as `cwm devices`: every configured device but this one.
		for _, d := range cfg.Devices {
			if d.DeviceID != myID {
				st.Paired++
			}
		}
	}
	return st, nil
}

// InitResult is the outcome of a GUI init: the workspace state plus the
// doctor checks run right after it.
type InitResult struct {
	Initialized bool    `json:"initialized"`
	Checks      []Check `json:"checks"`
}

// Init runs the same workspace.Init the `cwm init` command runs, on its
// non-interactive path (no account login, no migrate); it is idempotent.
// out receives the human progress lines.
func Init(layout *storage.Layout, out io.Writer) (InitResult, error) {
	if err := workspace.Init(layout, "", false, out); err != nil {
		return InitResult{}, err
	}
	return InitResult{Initialized: Initialized(layout), Checks: Doctor(layout.Root, false)}, nil
}

// PairResult reports what a pairing changed. DeviceID (this device) is
// filled by callers that already read it; Pair itself never needs it.
type PairResult struct {
	DeviceID     string `json:"deviceID,omitempty"`
	PeerID       string `json:"peerID"`
	PeerName     string `json:"peerName,omitempty"`
	Changed      bool   `json:"changed"`
	AddedDevice  bool   `json:"addedDevice"`
	SharedFolder bool   `json:"sharedFolder"`
}

// Pair links this device with peerID (syncengine.Pair) and reports what
// it changed — the one implementation behind `cwm pair <id>` and
// POST /api/pair (D26).
func Pair(cl syncengine.Client, peerID, name, ws string) (PairResult, error) {
	peerKnown, folderShared, err := pairPreState(cl, peerID)
	if err != nil {
		return PairResult{}, err
	}
	changed, err := syncengine.Pair(cl, peerID, name, ws)
	if err != nil {
		return PairResult{}, err
	}
	return PairResult{
		PeerID:       peerID,
		PeerName:     name,
		Changed:      changed,
		AddedDevice:  changed && !peerKnown,
		SharedFolder: changed && !folderShared,
	}, nil
}

// pairPreState reports what a Pair call still has to do.
func pairPreState(cl syncengine.Client, peerID string) (peerKnown, folderShared bool, err error) {
	cfg, err := cl.GetConfig()
	if err != nil {
		return false, false, err
	}
	for _, d := range cfg.Devices {
		if d.DeviceID == peerID {
			peerKnown = true
		}
	}
	for _, f := range cfg.Folders {
		if f.ID != syncengine.FolderID {
			continue
		}
		for _, fd := range f.Devices {
			if fd.DeviceID == peerID {
				folderShared = true
			}
		}
	}
	return peerKnown, folderShared, nil
}

// RequireWorkspace returns the workspace dir or a *NoWorkspaceError —
// action callers need the path (pairing operates on the folder).
func RequireWorkspace(layout *storage.Layout) (string, error) {
	return requireWorkspace(layout)
}
