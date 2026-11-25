// Stage V0.3-2: `cwm pair` and `cwm devices` — two-device workspace sync
// over the syncthing REST API (SPEC-V0.3.md, decisions D16–D18).
package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Mr-JoE1/cwm/internal/claudepaths"
	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/status"
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

const pairUsage = "usage: cwm pair [--start] [<device-id> [name] [--address tcp://host:22000]...]"

func cmdPair(e *Env, args []string) error {
	start := false
	var rest, addrs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--start":
			start = true
		case a == "--address":
			if i+1 >= len(args) {
				return fmt.Errorf("--address needs a value (%s)", pairUsage)
			}
			i++
			addrs = append(addrs, args[i])
		case strings.HasPrefix(a, "--address="):
			addrs = append(addrs, strings.TrimPrefix(a, "--address="))
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q (%s)", a, pairUsage)
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) > 2 {
		return errors.New(pairUsage)
	}
	if len(addrs) > 0 && len(rest) == 0 {
		return fmt.Errorf("--address needs a device id (%s)", pairUsage)
	}
	for _, a := range addrs {
		if err := syncengine.ValidateAddress(a); err != nil {
			return err
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
	if err := applyPrivateSync(e, cl, false); err != nil {
		return err
	}
	if len(rest) == 0 {
		return pairStatus(e, cl, ws)
	}
	name := ""
	if len(rest) == 2 {
		name = rest[1]
	}
	return pairPeer(e, cl, ws, rest[0], name, addrs)
}

// applyPrivateSync makes Syncthing's discovery and relay options follow
// features.private_sync and says so when it changed something. Private
// sync on is enforced every time; off restores Syncthing's defaults only
// when asked (the switch was just turned off), so a user's own Syncthing
// tuning is never undone by a plain `cwm pair`.
func applyPrivateSync(e *Env, cl syncengine.Client, enforceOff bool) error {
	layout, err := storage.NewLayout()
	if err != nil {
		return err
	}
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		return err
	}
	if !cfg.Features.PrivateSync && !enforceOff {
		return nil
	}
	changed, err := syncengine.ApplyPrivacy(cl, cfg.Features.PrivateSync)
	if err != nil {
		return err
	}
	if changed {
		if cfg.Features.PrivateSync {
			fmt.Fprintln(e.Stdout, "private sync: Syncthing global discovery and relays turned off (LAN discovery stays on); pair remote devices with --address")
		} else {
			fmt.Fprintln(e.Stdout, "private sync off: Syncthing global discovery and relays turned back on")
		}
	}
	return nil
}

func cmdDevices(e *Env, args []string) error {
	asJSON, err := onlyJSONFlag(args, "usage: cwm devices [--json]")
	if err != nil {
		return err
	}
	cl, err := connectSyncClient(false) // no --start: devices never spawns
	if err != nil {
		return err
	}
	rows, err := status.Devices(cl)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(e.Stdout, rows)
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
	layout, err := claudepaths.NewLayout()
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
// report exactly what changed (status.Pair is shared with the GUI, D26).
func pairPeer(e *Env, cl syncengine.Client, ws, peerID, name string, addrs []string) error {
	res, err := status.PairAt(cl, peerID, name, ws, addrs)
	if err != nil {
		return err
	}
	if !res.Changed {
		fmt.Fprintln(e.Stdout, "already paired — nothing to do")
		return nil
	}
	if res.AddedDevice {
		if name != "" {
			fmt.Fprintf(e.Stdout, "added device %s (%s)\n", shortDeviceID(peerID), name)
		} else {
			fmt.Fprintf(e.Stdout, "added device %s\n", shortDeviceID(peerID))
		}
	}
	if res.SharedFolder {
		fmt.Fprintf(e.Stdout, "shared folder %s with %s\n", syncengine.FolderID, shortDeviceID(peerID))
	}
	if len(addrs) > 0 {
		fmt.Fprintf(e.Stdout, "addresses of %s: %s\n", shortDeviceID(peerID), strings.Join(addrs, ", "))
	}
	if !res.AddedDevice && !res.SharedFolder && len(addrs) == 0 {
		fmt.Fprintln(e.Stdout, "updated workspace folder config")
	}
	fmt.Fprintln(e.Stdout, "done — check `cwm devices` to watch them sync")
	return nil
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
