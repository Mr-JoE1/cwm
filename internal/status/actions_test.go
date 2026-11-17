package status

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
	"github.com/Mr-JoE1/cwm/internal/syncthing"
)

const (
	actMyID   = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	actPeerID = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

func TestSetupAndInit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	layout := storage.NewLayoutAt(filepath.Join(t.TempDir(), "home"))
	connect := func() (syncengine.Client, error) { return fakeSync{myID: actMyID, peer: actPeerID}, nil }

	st, err := Setup(layout, connect)
	if err != nil || st.Initialized || st.Accounts == nil || len(st.Accounts) != 0 ||
		!st.Syncthing || st.DeviceID != actMyID || st.Paired != 1 {
		t.Fatalf("Setup before init = %+v, %v", st, err)
	}
	if k := keys(t, st); k != "accounts,deviceID,initialized,paired,syncthing" {
		t.Errorf("state keys = %s", k)
	}

	var out bytes.Buffer
	res, err := Init(layout, &out)
	if err != nil || !res.Initialized || len(res.Checks) == 0 {
		t.Fatalf("Init = %+v, %v", res, err)
	}
	if !strings.Contains(out.String(), "cwm home initialized") {
		t.Errorf("Init output = %q (must be the same workspace.Init the CLI runs)", out.String())
	}
	if st, _ := Setup(layout, nil); !st.Initialized || st.Syncthing || st.DeviceID != "" {
		t.Errorf("Setup after init, no syncthing seam = %+v", st)
	}
	down := func() (syncengine.Client, error) { return nil, errors.New("unreachable") }
	if st, err := Setup(layout, down); err != nil || st.Syncthing || st.Paired != 0 {
		t.Errorf("Setup with syncthing down = %+v, %v", st, err)
	}
	if k := keys(t, mustSetup(t, layout, down)); k != "accounts,initialized,paired,syncthing" {
		t.Errorf("deviceID must be omitted when unknown: keys %s", k)
	}
}

func mustSetup(t *testing.T, l *storage.Layout, c func() (syncengine.Client, error)) State {
	t.Helper()
	st, err := Setup(l, c)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestAccountsRows(t *testing.T) {
	root := t.TempDir()
	layout := storage.NewLayoutAt(root)
	cfg := config.Default()
	dir := filepath.Join(layout.AccountsDir(), "work")
	writeFile(t, filepath.Join(dir, ".credentials.json"), `{"secret":"x"}`)
	if err := cfg.AddAccount("work", dir); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetAccountLogin("work", config.Login{Email: "w@example.com", Plan: "pro", AccountUUID: "u-1"}); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(layout.ConfigPath(), cfg); err != nil {
		t.Fatal(err)
	}
	list, err := Accounts(layout)
	if err != nil || len(list) != 1 || list[0].Name != "work" || list[0].Email != "w@example.com" || !list[0].Default || list[0].LastLogin == "" {
		t.Fatalf("Accounts = %+v, %v", list, err)
	}
	// The `cwm account list --json` contract: identity fields only (D12).
	if k := keys(t, list[0]); k != "default,email,last_login,name,plan" {
		t.Errorf("account keys = %s", k)
	}
}

// pairStub is a mutable syncthing config for Pair.
type pairStub struct {
	fakeSync
	cfg *syncthing.Config
}

func (p *pairStub) GetConfig() (*syncthing.Config, error) {
	c := *p.cfg
	c.Devices = append([]syncthing.Device(nil), p.cfg.Devices...)
	c.Folders = append([]syncthing.Folder(nil), p.cfg.Folders...)
	return &c, nil
}
func (p *pairStub) PutConfig(c *syncthing.Config) error { p.cfg = c; return nil }

func TestPairReportsChanges(t *testing.T) {
	st := &pairStub{fakeSync: fakeSync{myID: actMyID}, cfg: &syncthing.Config{Devices: []syncthing.Device{{DeviceID: actMyID}}}}
	res, err := Pair(st, actPeerID, "tower", "/ws")
	if err != nil || !res.Changed || !res.AddedDevice || !res.SharedFolder || res.PeerID != actPeerID || res.PeerName != "tower" {
		t.Fatalf("first Pair = %+v, %v", res, err)
	}
	res, err = Pair(st, actPeerID, "tower", "/ws")
	if err != nil || res.Changed || res.AddedDevice || res.SharedFolder {
		t.Errorf("second Pair = %+v, %v", res, err)
	}
	if _, err := Pair(st, "nope", "", "/ws"); err == nil {
		t.Error("Pair accepted an invalid device ID")
	}
}
