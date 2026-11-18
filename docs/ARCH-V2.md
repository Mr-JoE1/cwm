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
| ~~Docker: pull + run~~ | **Dropped (D28).** A container can't open host terminals or the host browser, and the IDE never sees its Claude config | Native install only |
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
                 │ internal/webterm  (v0.8) PTY ⇄ WebSocket terminal in dashboard    │
                 │ internal/gui/web  cyber UI: tokens, themes, canvas bg, logo       │
                 │ existing: status, sessions, accounts, workspace, syncengine, …    │
                 └───────────────────────────────────────────────────────────────────┘
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

Security deltas (D24 extended): agent binds loopback by default; non-loopback bind
requires explicit `--bind` + is refused without the persistent token; Host allow-list
stays fixed to loopback names; web terminal (v0.8) is the highest-risk
surface — loopback-only default, CSRF + token, idle timeout, audit log.

## New roadmap

| Version | Theme | Scope | Exit test |
|---|---|---|---|
| **v0.6** (ASAP) | One-line install + always-on | install.sh/ps1 + Pages hosting; darwin builds; `cwm agent` + persistent token + single-instance; `cwm service …` (3 OSes); `cwmd.exe`; open-browser-at-login; double-click behaviour; `--version` | Fresh Win/Linux/mac: paste one line → dashboard opens; reboot+login → dashboard opens again |
| **v0.7** | Cyber UI + brand | Logo (SVG + favicon + icons); design tokens; dark/light/system toggle; animated canvas bg (grid/particle/glyph), transitions, toasts; SSE live updates; fix v0.5 nits | Lighthouse a11y ≥ 90; idle CPU < 2% with bg on; both themes screenshot-verified |
| **v0.8** | Web-only: everything in the browser | (a) In-dashboard terminal (PTY ⇄ WebSocket, xterm-style, tabs) served by the agent: Resume runs `claude --resume` inside the page, Add account runs `claude auth login` inside the page; desktop terminal launcher (D27) removed from the UI. (b) Full CLI parity in the web UI: accounts list/use/remove, MCP add/list/remove, import, WIP status/push, sessions search, pair/devices, health, service settings (autostart, open-at-login, rotate token) | A user installs with one line and never opens a terminal again: setup, login, resume, MCP, pairing all done in the browser |
| **v1.0** | Launch | Old v0.6 items (statusline, scheduled-task portability, doctor extensions), legacy bash kit removed, docs site, real-hardware matrix (Win/Linux/mac + 2-device pair) | 100 external installs |

v0.6 and v0.7 touch disjoint code (service/installer vs `internal/gui/web`) and can be built in parallel.

## Decisions (maintainer, 2026-11-18)
- **D28 — No Docker:** dropped from the plan. Containers can't open the host browser or terminals, the IDE never sees the container's Claude config, and Docker Desktop adds a VM on Windows/macOS. Native install is the only supported mode.
- **D29 — Install URL:** GitHub Pages `https://mr-joe1.github.io/cwm/install.sh|.ps1` until
  the project landing domain exists.
- **D30 — Dependencies:** v0.8 (web terminal) may add exactly two deps: a PTY library (with Windows ConPTY)
  and a WebSocket library. Everything else stays stdlib + BurntSushi/toml.
- **D31 — Web-only UX:** the browser dashboard is the only user interface. No desktop terminal windows are opened from the UI; interactive Claude sessions and logins run in the in-dashboard terminal. The CLI stays for scripting and power users, but every action has a web equivalent.
- v0.6 and v0.7 are built in parallel on separate branches and merged by the maintainer.
