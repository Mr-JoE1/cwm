# cwm architecture v2 — "install once, it just runs"

Status: PROPOSAL (2026-11-18). Supersedes the v0.6–v1.0 rows of the original roadmap.

## Why
v0.5 shipped a GUI that only exists while a terminal runs `cwm gui`. Target users
double-click, or paste one line, and expect a dashboard that is always there.

## Requirements → constraints

| Ask | Hard constraint | Resolution |
|---|---|---|
| One-line install (`curl … \| sh`, `irm … \| iex`) | Must verify checksums; needs a stable URL | `install.sh` / `install.ps1` in repo, served from GitHub Pages; fetch latest release, verify `checksums.txt`, install per-user (no admin/sudo) |
| Auto-start + open browser "on boot" | No browser exists before login. A system service (systemd system unit, Windows service in session 0) **cannot** open the user's browser | **User-level** autostart at login: systemd `--user` unit + XDG autostart (Linux), Task Scheduler at-logon task (Windows), LaunchAgent (macOS). Opens default browser once per login (toggle, default ON) |
| macOS (`install.sh` covers it) | Not built today | Add darwin amd64/arm64 builds; terminal launch via `osascript` |
| Windows without a flashing console | Console-subsystem exe always opens a window when started by Explorer/scheduler | Ship second exe **`cwmd.exe`** (same code, `-H=windowsgui`): silent background agent; double-click = start agent + open dashboard |
| Bookmarkable dashboard that survives restarts | v0.5 token is per-launch | Persistent token in `~/.cwm/server.toml` (0600), rotatable via `cwm service rotate-token`; login page also accepts it |
| Docker: pull + run | Container can't open host terminals or host browser; `~/.claude` lives on the host | Two-phase: **hub mode** first (dashboard + Syncthing + WIP, always-on on NAS/VPS/desktop), **web terminal** later so resume/login run *inside the browser* → full feature parity in Docker |
| Modern cyber UI, light/dark, logo | Single binary, no CDN, must not burn CPU | Keep no-build-step (D23). Canvas background paused when tab hidden + `prefers-reduced-motion`; CSS design tokens for both themes; SVG logo embedded |

## Target architecture

```
                 ┌────────────────────── cwm (one Go codebase) ─────────────────────┐
 install.sh ───▶ │ cmd/cwm   (console)  CLI — every feature, scripts, terminals     │
 install.ps1     │ cmd/cwmd  (win GUI)  silent agent entry (Windows only)           │
                 │                                                                   │
                 │ internal/agent    long-running server: HTTP API + SSE live feed,  │
                 │                   persistent token, single-instance lock          │
                 │ internal/service  autostart per OS (systemd-user / schtasks /     │
                 │                   LaunchAgent) + "open browser at login"          │
                 │ internal/webterm  (v0.9) PTY ⇄ WebSocket terminal in dashboard    │
                 │ internal/gui/web  cyber UI: tokens, themes, canvas bg, logo       │
                 │ existing: status, sessions, accounts, workspace, syncengine, …    │
                 └───────────────────────────────────────────────────────────────────┘
 Docker image ─▶ same binary, `cwm agent --bind 0.0.0.0 --data /data` + bundled syncthing
```

New commands:
- `cwm agent [--bind ADDR] [--port N] [--open]` — the always-on server (`cwm gui` becomes an alias that runs it in the foreground).
- `cwm service install|uninstall|status|start|stop|rotate-token` — autostart management.
- `cwm dashboard` — open the browser at the running agent (starts it if needed).
- `cwm --version` / `-v`; single-dash long flags accepted.

Installer flow (both OSes): detect OS/arch → download latest release archive → verify
SHA-256 against `checksums.txt` → install to `~/.local/bin` / `%LOCALAPPDATA%\cwm` →
add to PATH → `cwm init` (non-interactive) → `cwm service install` → open dashboard →
wizard does account login. Flags: `--no-service`, `--version X`, `--uninstall`.
Side effect: script downloads carry no Mark-of-the-Web → no SmartScreen prompt.

Docker:
- Image `cwm` (Docker Hub + `ghcr.io/mr-joe1/cwm`), multi-arch amd64/arm64, distroless/alpine,
  non-root, volume `/data`, HEALTHCHECK, `restart: unless-stopped` in shipped compose file.
- Bind `127.0.0.1:47800` on the host by default; token printed in `docker logs` and stored in `/data`.
- Auto-open on host login: `cwm service install --open-only --url http://127.0.0.1:47800`
  (host-side opener, no agent) — the container itself cannot open a browser.

Security deltas (D24 extended): agent binds loopback by default; non-loopback bind
requires explicit `--bind` + is refused without the persistent token; Host allow-list
becomes configurable for Docker/reverse-proxy; web terminal (v0.9) is the highest-risk
surface — loopback-only default, CSRF + token, idle timeout, audit log.

## New roadmap

| Version | Theme | Scope | Exit test |
|---|---|---|---|
| **v0.6** (ASAP) | One-line install + always-on | install.sh/ps1 + Pages hosting; darwin builds; `cwm agent` + persistent token + single-instance; `cwm service …` (3 OSes); `cwmd.exe`; open-browser-at-login; double-click behaviour; `--version` | Fresh Win/Linux/mac: paste one line → dashboard opens; reboot+login → dashboard opens again |
| **v0.7** | Cyber UI + brand | Logo (SVG + favicon + icons); design tokens; dark/light/system toggle; animated canvas bg (grid/particle/glyph), transitions, toasts; SSE live updates; fix v0.5 nits | Lighthouse a11y ≥ 90; idle CPU < 2% with bg on; both themes screenshot-verified |
| **v0.8** | Docker (hub mode) | Dockerfile, multi-arch build in release CI, Docker Hub + GHCR push, compose file, bundled Syncthing, healthcheck, host opener | `docker run` → dashboard + Syncthing hub reachable; survives host reboot |
| **v0.9** | Web terminal | In-dashboard terminal (PTY ⇄ WebSocket) for resume / account login / any cwm command; Docker reaches full parity; headless hosts no longer need 501 fallback | Resume a session and log in to an account entirely in the browser, native and Docker |
| **v1.0** | Launch | Old v0.6 items (statusline, scheduled-task portability, doctor extensions), legacy bash kit removed, docs site, real-hardware matrix (Win/Linux/mac + 2-device pair) | 100 external installs |

v0.6 and v0.7 touch disjoint code (service/installer vs `internal/gui/web`) and can be built in parallel.

## Decisions (maintainer, 2026-11-18)
- **D28 — Registries:** GHCR (`ghcr.io/mr-joe1/cwm`, built-in Actions token) + Docker Hub
  (`mrjoe1/cwm`, secrets `DOCKERHUB_USERNAME` / `DOCKERHUB_TOKEN`). Docker Hub push is
  skipped cleanly when the secrets are absent.
- **D29 — Install URL:** GitHub Pages `https://mr-joe1.github.io/cwm/install.sh|.ps1` until
  the project landing domain exists.
- **D30 — Dependencies:** v0.9 may add exactly two deps: a PTY library (with Windows ConPTY)
  and a WebSocket library. Everything else stays stdlib + BurntSushi/toml.
- v0.6 and v0.7 are built in parallel on separate branches and merged by the maintainer.
