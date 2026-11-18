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

Principle: Claude Code and `claude auth login` are interactive TUIs, so the GUI never
hosts them — it **opens a real terminal window** running the right cwm command
(D27). Everything else runs in-process through the same functions the CLI uses (D26).

**D27 — Terminal launcher** (`internal/gui/term`, interface `Launcher{Open(argv []string) error}`):
- argv is always an exec argument array — never a shell string built from user input.
- Windows: `wt.exe` if on PATH (`wt.exe -- <argv>`), else `cmd.exe /c start "cwm" <argv>`
  with each arg individually quoted via `syscall.EscapeArg`.
- Linux: first found of `$TERMINAL`, `x-terminal-emulator`, `gnome-terminal --`,
  `konsole -e`, `xfce4-terminal -x`, `alacritty -e`, `kitty`, `xterm -e`.
- None found / headless → API returns 501 `{"error":"no terminal found","command":"<copyable cmd>"}`
  and the UI shows the command with a copy button.
- Tests use a fake Launcher; a real terminal is never spawned in tests.

**New endpoints** (all POST: auth + `X-CWM-CSRF` + JSON body; strict input validation, 400 on bad input):
- `GET /api/state` → `{initialized, accounts:[{name,email?}], deviceID?, paired:int}` — drives the wizard.
- `GET /api/accounts` → account list (identity fields only, D12; never credentials).
- `POST /api/init` → runs workspace init in-process (non-interactive path, no migrate);
  idempotent; returns doctor checks afterwards.
- `POST /api/accounts` `{name}` → name validated `^[a-z0-9][a-z0-9-]{0,31}$`; opens a
  terminal running `cwm account add <name>` (login happens there); UI polls `/api/state`.
- `POST /api/sessions/resume` `{project, id}` → project must exist in the manifest, id
  must be a known session of that project (looked up via internal/sessions, not trusted
  from input); opens a terminal running the D20 launcher passthrough
  (`cwm <project> --resume <id>`, or whatever the existing passthrough form is).
- `POST /api/pair` `{deviceID, name}` → `syncengine.ValidateDeviceID` + name validated;
  calls `syncengine.Pair`; 503 if Syncthing unreachable. Response includes this device's ID.

**UI:**
- Wizard shown when `state.initialized == false` or no accounts:
  1 Init → 2 Add account (button → terminal; spinner polls state until account appears)
  → 3 Pair (optional, skippable; shows own device ID with copy button + peer-ID input)
  → 4 Done → dashboard.
- Sessions tab: **Resume** button per row. Devices tab: **Pair device** form.
- Every action shows success/error inline; no silent failures.

**Tests:** handler tests for every endpoint: auth, CSRF, bad JSON, each validation rule
(path traversal / shell metachars in names rejected), unknown session id → 404, fake
Launcher receives the exact expected argv, 501 path when Launcher reports none found.
Launcher candidate selection unit-tested via injected PATH lookup. e2e: `/api/state`
before and after init.

Out of scope for B: account removal/switching, MCP editing, settings editing.

### V0.5-C — packaging
- `.goreleaser.yaml` `nfpms` (D25): `.deb` for linux amd64 + arm64, package `cwm`,
  binary `/usr/bin/cwm`, MIT (LICENSE → `/usr/share/doc/cwm/copyright`), homepage
  https://github.com/Mr-JoE1/cwm, maintainer `cwm maintainers <noreply@github.com>`,
  recommends `git`, `syncthing`. File name `cwm_<version>_linux_<arch>.deb`; listed in
  `checksums.txt` and uploaded with the archives. `dist/` gitignored.
- README "Web GUI (v0.5, beta)": screenshots in `docs/img/` (gui-sessions, gui-projects,
  gui-health, gui-wizard — headless Chromium 1280x800 on fake demo data only) and the
  `sudo apt install ./cwm_<version>_linux_amd64.deb` line; roadmap marks v0.5 shipped (beta).
- docs/PLAN.md: v0.5 closing note (shipped / not yet verified).
- Tag `v0.5.0` only after CI on main is green (ubuntu + windows).

## Constraints (unchanged)
Go ≥1.25, gofmt/vet clean, no panics, `%w` errors, t.TempDir, no network/real
claude in tests, legacy `bash tests/test.sh` 74/74, CI ubuntu+windows green.
