# cwm v0.3 — Device Sync Spec (Syncthing REST automation)

Goal: two-device workspace sync set up in under 5 minutes, zero Syncthing UI.
Build in three stages, each gated on the full verification matrix. The flaky-runner
tactics from v0.2 apply: read lists capped, retry on silent death.

## Design decisions (to be logged as D16–D17)

- **D16**: deletions SYNC (normal semantics); safety net = staggered versioning 30 days
  (`cleanInterval` 3600, `maxAge` 2592000). The v0.1 bash-kit advice "ignore-delete ON"
  is withdrawn: with versioning active, cascading deletes are recoverable from
  `.stversions`, while ignore-delete causes worse failure modes (orphans accumulate).
- **D17**: cwm never manages the syncthing daemon lifecycle. It discovers a running
  instance (GUI+API from config.xml), and `cwm pair --start` may spawn ONE detached
  `syncthing serve --no-browser` process when none is running — but no supervision,
  no autostart. Users running syncthing as a service (recommended) are untouched.
- **D18**: folder ID is the fixed string `cwm-workspace` (must match on every device —
  the folder identity; the PATH differs per device and that is fine).

## CLI surface

```
cwm pair                     # status: local syncthing reachable?, my device ID, folder
                             #   state, pairing instructions for the other device
cwm pair <device-id> [name]  # link with a peer: add device + share workspace folder
                             #   (idempotent; safe to re-run)
cwm pair --start             # as plain `pair`, but spawn detached syncthing when the
                             #   API is unreachable (unix: setsid; windows: detached)
cwm devices                  # sync health: per-device connected/address/bytes +
                             #   workspace-folder completion %
cwm doctor                   # gains check 12: syncthing GUI reachable (warn only)
```

Global env overrides (tests, power users): `CWM_SYNCTHING_URL`, `CWM_SYNCTHING_API_KEY`.

## internal/syncthing package — stage V0.3-1

### Discovery

`Discover() (*Endpoint, error)`:
1. Env overrides win.
2. Else read syncthing `config.xml` per OS (first existing wins):
   - Linux: `~/.local/state/syncthing/config.xml`, then `~/.config/syncthing/config.xml`
   - Windows: `%LOCALAPPDATA%\Syncthing\config.xml`
   - macOS: `~/Library/Application Support/Syncthing/config.xml`
   Parse ONLY the `<gui>` element: `<address>` (default `127.0.0.1:8384`) and
   `<apikey>` (missing → error "syncthing config has no API key — start syncthing once
   to generate it"). TLS flag `<gui tls="true">` → scheme https.
   `STHOMEDIR`/`--home` installs unsupported (documented).
3. No config.xml anywhere → error "syncthing not configured — install and start it once
   (or run `cwm pair --start`)".
4. Endpoint struct: BaseURL (scheme://address), APIKey.

### Client (REST, `X-API-Key` header, 5s timeout)

- `Status() (*SystemStatus, error)` — GET `/rest/system/status`; need `myID`.
- `GetConfig() (*Config, error)` — GET `/rest/config` (full config JSON:
  `{devices:[{deviceID,name,addresses:[..]}], folders:[{id,label,path,type:"sendreceive",
  devices:[{deviceID}], versioning:{type,params}, ignoreDelete? , paused?}], ...}` — parse
  tolerantly, keep unknown fields via `json.RawMessage` where practical; we GET-modify-PUT).
- `PutConfig(*Config) error` — PUT `/rest/config` (full body).
- `RestartRequired() (bool, error)` — GET `/rest/config/restart-required`.
- `Restart() error` — POST `/rest/system/restart`.
- `Ping() error` — GET `/rest/system/ping`.
- `Connections() (*Connections, error)` — GET `/rest/system/connections`
  (`{connections:{<id>:{connected,address,at,inBytesTotal,outBytesTotal}}, total:{...}}`).
- `Completion(device, folder string) (*Completion, error)` — GET
  `/rest/db/completion?device=..&folder=..` (`{completion: <float %>, globalBytes, ...}`).

Wait-for-API helper: `WaitReady(tries, delay)` polling Ping.

### Unit tests (V0.3-1)

- Discovery: temp fake config.xml per OS path shape (table-driven; override home),
  address+apikey+tls parsing, missing-file error, no-apikey error.
- Client: `httptest.Server` fake implementing the endpoints above with a mutable
  in-memory config; assert GET/PUT round-trip, myID parse, connections/completion
  shapes, header sent, error paths (500s, timeout skip).

## internal/syncengine package — stage V0.3-2

(Folder/device orchestration over the client; all logic testable against the same
httptest fake.)

- `EnsureFolder(c Client, wsPath string) (changed bool, err)`:
  folder `cwm-workspace` present with path==wsPath → ensure versioning staggered 30d,
  ignoreDelete absent/false; wrong path or missing → add/fix (GET-modify-PUT).
- `Pair(c Client, peerID, peerName, wsPath string) (changed bool, err)`:
  EnsureFolder + ensure local device list contains peer (name set if given), ensure
  folder.devices contains BOTH myID and peerID → PUT. Idempotent: no-op when correct.
- `StartDetached() error`: spawn `syncthing serve --no-browser` detached (unix:
  `SysProcAttr{Setsid:true}`, stdio → state dir logs/syncthing.log; windows:
  `CREATE_NEW_PROCESS_GROUP|DETACHED_FROM_PROCESSBAR` equivalent via
  `SysProcAttr{CreationFlags: DETACHED_PROCESS|CREATE_NEW_PROCESS_GROUP}`, stdio to
  NUL/log). Binary resolved from PATH (`syncthing` / `syncthing.exe`); missing →
  error with install hint (apt/brew/winget).
- `DevicesReport(c Client) ([]DeviceRow, error)`: joins config.devices +
  connections + completion(folder=cwm-workspace) → rows {ID(short), Name, Connected,
  Address, InBytes, OutBytes, Completion}.
- `MyID(c Client)`.

### CLI wiring (V0.3-2)

- `cwm pair`: workspace guard (must exist) → discover endpoint → Ping (unless --start:
  try Ping, on failure StartDetached + WaitReady 30×1s) → Status myID → EnsureFolder →
  print: my device ID, folder state, then instructions:
  "on the other device: cwm pair <this-id> <device-name>" and note both sides must run it.
- `cwm pair <id> [name]`: discover + Ping (+--start same) → validate 52-60 char
  device ID (charset base32; length check + trim; invalid → error) → Pair → print
  summary (added device X / shared folder cwm-workspace / nothing to do).
- `cwm devices`: discover + Ping (no --start) → DevicesReport → table; empty peers →
  "(no paired devices yet — run cwm pair on both devices)".
- `doctor` check 12: discover+Ping; reachable → `ok: syncthing API reachable at <url>`;
  not configured/running → `warn:` (never FAIL — sync is optional per machine).
- Reserved names: add `pair` and `devices`.

### Tests (V0.3-2)

syncengine unit tests against httptest fake: EnsureFolder idempotent + fixes path,
Pair adds both devices + shares folder + idempotent second run, DevicesReport rows,
StartDetached happy path with a fake `syncthing` stub script on PATH (writes a marker,
exits — assert spawned, no crash; no real daemon in tests). CLI-level tests for flag
parsing + guards (fake client injected via interface seam).

## Stage V0.3-3 — docs + release

- docs/PLAN.md: D16 (deletes sync; versioning is the net), D17 (no daemon
  supervision; --start spawns once, detached), D18 (fixed folder ID cwm-workspace).
- README: new "Device sync (v0.3)" section replacing the manual Syncthing pairing
  instructions — 4-step two-device walkthrough (install syncthing service → cwm pair
  on both → done), `cwm devices` example output, troubleshooting box (API key, TLS,
  service vs --start), remove the old "pair Syncthing on ~/.claude-me" text everywhere
  (incl. the bash-kit section — mark it as legacy-manual-pairing).
- Roadmap section: v0.3 shipped.
- Tag v0.3.0 → goreleaser (existing pipeline, no changes needed).

## Constraints

- Same Go rules as AGENT.md; no new third-party deps (stdlib net/http + encoding/json
  + encoding/xml for config.xml only).
- No real syncthing, no network in tests — httptest fakes only.
- Public-safe docs.
- Existing tests/e2e/legacy must pass unmodified (doctor gains a check → doctor tests
  may gain an assertion for check 12 warn when unreachable — that is an allowed edit,
  do it carefully).
