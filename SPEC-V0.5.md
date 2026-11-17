# SPEC v0.5 — Web GUI beta

Goal (roadmap exit criterion): a non-terminal user completes setup and resumes a
session through the GUI only. Architecture per D7: GUI embedded in the single Go
binary (Syncthing model), localhost only, CLI stays first-class.

## Decisions (log in docs/PLAN.md)

- **D23 — GUI stack:** stdlib `net/http` + `embed` serving static HTML/CSS/vanilla
  JS. No frontend framework, no build step, no new Go deps (only dep stays
  BurntSushi/toml). Tray icon DROPPED from v0.5 (needs cgo/native deps); `cwm gui`
  opens the default browser instead.
- **D24 — GUI security:** bind `127.0.0.1` only (never 0.0.0.0, no flag to change
  it in v0.5). Random 32-byte token per launch (crypto/rand, hex). Browser opened at
  `http://127.0.0.1:<port>/?t=<token>`; first request sets an HttpOnly SameSite=Strict
  cookie and redirects to `/` (token stripped from URL). Every `/api/*` request
  requires the cookie or `Authorization: Bearer <token>`; else 401. State-changing
  endpoints are POST-only and additionally require header `X-CWM-CSRF: <token>`.
  `Host` header must be `127.0.0.1:<port>` or `localhost:<port>` (DNS-rebinding guard).
- **D25 — Installers:** tar.gz/zip (existing) + `.deb` via goreleaser `nfpms`.
  No scoop, no brew (maintainer decision). winget deferred to v1.0.
- **D26 — One data layer:** CLI `--json` output and GUI `/api/*` responses come from
  the same structs (package `internal/status`). No duplicated logic in handlers.

## Stages

### V0.5-A — data layer + read-only dashboard
1. `internal/status`: typed snapshot functions returning structs (JSON tags):
   - `Projects(layout)` → list rows (name, sessions, modified) — same data as `cwm list`
   - `Devices(client)` → wraps `syncengine.DevicesReport`
   - `Dirty(reposRoot)` → wraps the `cwm wip dirty` scan (move `scanDirtyRepos` here)
   - `Doctor(root, net)` → `[]Check{Name, Status ok|warn|fail, Detail}`; `doctor.Run`
     refactored to build this slice, text renderer unchanged byte-for-byte
   - `Sessions(...)` → reuse `internal/sessions`
2. `--json` on `cwm list`, `cwm devices`, `cwm wip dirty`, `cwm doctor`
   (doctor exit code semantics unchanged).
3. `cwm gui [--port N] [--no-open]`: server per D24; default port = first free in
   47800-47809; prints URL; opens browser unless `--no-open` (xdg-open / `cmd /c start`
   / skip if unavailable). Ctrl-C = graceful shutdown.
   Read-only endpoints: `GET /api/sessions`, `/api/projects`, `/api/devices`,
   `/api/wip`, `/api/doctor`, `/api/version`.
   Embedded dashboard (`internal/gui/web/`): single page, 5 tabs
   (Sessions, Projects, Devices, WIP, Health); light theme; no external CDN.
4. Tests: httptest for auth matrix (no token 401, bad token 401, cookie ok, bearer
   ok, bad Host 403, POST without CSRF 403), JSON shape tests, doctor text output
   golden-equal to pre-refactor. e2e: `cwm list --json`, `cwm doctor --json`.
   `gui` added to ReservedNames.

### V0.5-B — actions + setup wizard
POST `/api/sessions/resume` (spawns detached terminal running `cwm sessions --pick N`
equivalent — platform helper), `/api/init`, `/api/account/add` (runs auth login,
browser opens), `/api/pair`. Wizard flow: init → account → (optional) pair → done.

### V0.5-C — packaging
nfpms `.deb`; README GUI section with screenshots; tag v0.5.0.

## Constraints (unchanged)
Go ≥1.25, gofmt/vet clean, no panics, `%w` errors, t.TempDir, no network/real
claude in tests, legacy `bash tests/test.sh` 74/74, CI ubuntu+windows green.
