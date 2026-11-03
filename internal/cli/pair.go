// Stage V0.3-2: `cwm pair` and `cwm devices` — two-device workspace sync
// over the syncthing REST API (SPEC-V0.3.md, decisions D16–D18).
package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/storage"
	"github.com/Mr-JoE1/cwm/internal/syncengine"
	"github.com/Mr-JoE1/cwm/internal/syncthing"
)

// Seams overridable in tests: endpoint discovery, client construction and
// the detached daemon spawn all run through these vars.
var (
	stDiscover      = syncthing.Discover
	stNewClient     = func(ep *syncthing.Endpoint) syncengine.Client { return syncthing.NewClient(ep) }
	stStartDetached = syncengine.StartDetached
)

func cmdPair(e *Env, args []string) error {
	start := false
	var rest []string
	for _, a := range args {
		if a == "--start" {
			start = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) > 2 {
		return fmt.Errorf("usage: cwm pair [--start] [<device-id> [name]]")
	}
	for _, a := range rest {
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("unknown flag %q (usage: cwm pair [--start] [<device-id> [name]])", a)
		}
	}
	ws, err := requireSyncWorkspace()
	if err != nil {
		return err
	}
	cl, err := connectSyncClient(start)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return pairStatus(e, cl, ws)
	}
	name := ""
	if len(rest) == 2 {
		name = rest[1]
	}
	return pairPeer(e, cl, ws, rest[0], name)
}

func cmdDevices(e *Env, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: cwm devices")
	}
	cl, err := connectSyncClient(false) // no --start: devices never spawns
	if err != nil {
		return err
	}
	rows, err := syncengine.DevicesReport(cl)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(e.Stdout, "(no paired devices yet — run cwm pair on both devices)")
		return nil
	}
	fmt.Fprintf(e.Stdout, "%-7s  %-16s  %-9s  %-24s  %-9s  %-9s  %s\n",
		"ID", "NAME", "CONNECTED", "ADDRESS", "IN", "OUT", "COMPLETION")
	for _, r := range rows {
		fmt.Fprintf(e.Stdout, "%-7s  %-16.16s  %-9s  %-24.24s  %-9s  %-9s  %.1f%%\n",
			shortDeviceID(r.ID), r.Name, yesNo(r.Connected), r.Address,
			formatBytes(r.InBytes), formatBytes(r.OutBytes), r.Completion)
	}
	return nil
}

// requireSyncWorkspace mirrors the launcher's guard: pairing operates on
// the workspace folder, which must already exist.
func requireSyncWorkspace() (string, error) {
	layout, err := storage.NewLayout()
	if err != nil {
		return "", err
	}
	ws := layout.WorkspaceDir()
	if info, err := os.Stat(ws); err != nil || !info.IsDir() {
		return "", fmt.Errorf("workspace %s does not exist — run `cwm init` or pair Syncthing so the folder is restored ('cwm doctor' can diagnose)", ws)
	}
	return ws, nil
}

// connectSyncClient resolves the syncthing endpoint and returns a reachable
// client. With start=true, an unreachable API spawns ONE detached daemon
// (D17) which is then given up to 30×1s to come up.
func connectSyncClient(start bool) (syncengine.Client, error) {
	ep, err := stDiscover()
	if err != nil {
		return nil, err
	}
	cl := stNewClient(ep)
	pingErr := cl.Ping()
	if pingErr == nil {
		return cl, nil
	}
	if !start {
		return nil, fmt.Errorf("syncthing API not reachable at %s (%w) — start syncthing, or rerun with `cwm pair --start`", ep.BaseURL, pingErr)
	}
	if err := stStartDetached(); err != nil {
		return nil, err
	}
	if err := waitReady(cl, 30, time.Second); err != nil {
		return nil, fmt.Errorf("spawned syncthing but it never became reachable: %w", err)
	}
	return cl, nil
}

// waitReady polls Ping up to tries times, sleeping delay between attempts
// (same contract as syncthing.Client.WaitReady).
func waitReady(cl syncengine.Client, tries int, delay time.Duration) error {
	var lastErr error
	for i := 0; i < tries; i++ {
		err := cl.Ping()
		if err == nil {
			return nil
		}
		lastErr = err
		if i+1 < tries && delay > 0 {
			time.Sleep(delay)
		}
	}
	if lastErr == nil {
		lastErr = errors.New("unreachable")
	}
	return fmt.Errorf("syncthing not ready after %d tries: %w", tries, lastErr)
}

// pairStatus is plain `cwm pair`: local ID, folder state, pairing
// instructions for the other device.
func pairStatus(e *Env, cl syncengine.Client, ws string) error {
	myID, err := syncengine.MyID(cl)
	if err != nil {
		return err
	}
	changed, err := syncengine.EnsureFolder(cl, ws)
	if err != nil {
		return err
	}
	state := "already configured"
	if changed {
		state = "configured now"
	}
	fmt.Fprintf(e.Stdout, "device id: %s\n", myID)
	fmt.Fprintf(e.Stdout, "folder:   %s → %s (%s; sendreceive, staggered versioning 30d)\n",
		syncengine.FolderID, ws, state)
	fmt.Fprintf(e.Stdout, "\non the OTHER device run: cwm pair %s <a-name-for-this-device>\n", myID)
	fmt.Fprintln(e.Stdout, "both devices must run it — each side pairs once.")
	return nil
}

// pairPeer is `cwm pair <device-id> [name]`: link with the peer, then
// report exactly what changed.
func pairPeer(e *Env, cl syncengine.Client, ws, peerID, name string) error {
	peerKnown, folderShared, err := pairPreState(cl, peerID)
	if err != nil {
		return err
	}
	changed, err := syncengine.Pair(cl, peerID, name, ws)
	if err != nil {
		return err
	}
	if !changed {
		fmt.Fprintln(e.Stdout, "already paired — nothing to do")
		return nil
	}
	if !peerKnown {
		if name != "" {
			fmt.Fprintf(e.Stdout, "added device %s (%s)\n", shortDeviceID(peerID), name)
		} else {
			fmt.Fprintf(e.Stdout, "added device %s\n", shortDeviceID(peerID))
		}
	}
	if !folderShared {
		fmt.Fprintf(e.Stdout, "shared folder %s with %s\n", syncengine.FolderID, shortDeviceID(peerID))
	}
	if peerKnown && folderShared {
		fmt.Fprintln(e.Stdout, "updated workspace folder config")
	}
	fmt.Fprintln(e.Stdout, "done — check `cwm devices` to watch them sync")
	return nil
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

// shortDeviceID truncates a device ID to its 7-char short form.
func shortDeviceID(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// formatBytes renders byte counts in a compact human-readable form.
func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
