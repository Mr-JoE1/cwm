# cwm — decision log

Dated entries, newest first. Public-safe: no private infrastructure or personal
details. Decisions are binding until superseded by a newer entry.

## 2026-11-18 (v0.7)

UI-only decisions for the v0.7 "cyber UI + brand" branch (`feat/v0.7-ui`),
built in parallel with v0.6. Numbering starts at D40 so it cannot collide with
v0.6 decisions. No `/api` contract change; the CSP is unchanged.

- **D40 — brand: hexagon + prompt mark.** A hexagon (a node: one workspace,
  many devices) framing a `>` chevron and a block cursor. Hand-written SVG,
  wordmark glyphs as stroked paths (no font dependency), legible at 16 px,
  single-colour friendly. The logo files adapt to `prefers-color-scheme`
  and take `--cwm-*` overrides when inlined. The favicon is the mark on a
  near-black tile, so it reads on any tab bar. PNG icons (512/192/32) are
  rendered by headless Chromium. Files and palette: `docs/brand/`.
- **D41 — design tokens, dark-first.** Every colour, radius and timing is a
  CSS custom property. Dark "cyber" (`#05080d`, `#00ff9c`, `#00e5ff`,
  `#ff3cd8`) is the default. The light theme darkens the same hues until all
  text pairs pass WCAG AA (≥ 4.5:1). Only system font stacks (monospace for
  display/UI, sans for body); no web fonts.
- **D42 — no-flash theme via a synchronous external script.** The pre-paint
  logic lives in `/theme.js`, loaded without `defer` in `<head>`, because the
  D24 CSP (`script-src 'self'`) forbids inline scripts. Loosening the CSP with
  a hash or nonce for cosmetics was rejected. Preferences: `localStorage`
  `cwm-theme` (system | dark | light) and `cwm-bg` (on | off).
- **D43 — the background must cost nothing when nobody is looking.** One
  canvas with a particle network, ≤ 30 fps, DPR capped at 2, paused when
  hidden, a still frame under reduced motion, a persisted off switch. It also
  **freezes after 30 s without input** and resumes on any pointer, key,
  wheel, touch or scroll. Decorative CSS loops are finite (pill pulses,
  caret/cursor blink, one live-dot ping per refresh). Reason: in measurement,
  every *continuously* running animation, even a 7 px dot, makes the browser
  composite frames nonstop. That is tolerable while someone is using the
  page, but not for a dashboard that is meant to stay open all day (v0.6
  opens it at every login).
- **D44 — feedback via toasts; the 501 fallback is a sticky toast.** Resume
  results moved from an inline cell to toasts, which keeps the session table
  aligned and stable under live refresh. The wizard and pair forms keep their
  inline results and also toast. A 501 toast stays until it is closed and
  carries the command with a Copy button.
- **D45 — live updates: feature-detected SSE, else 15 s polling.** The page
  probes `GET /api/events`. Only a 200 with `text/event-stream` opens an
  `EventSource`; anything else polls every 15 s while visible. The event
  vocabulary belongs to v0.6, so the client is tolerant: default or named
  events, an optional JSON `topic|type|kind|event` field, and heartbeats
  ignored. Bursts are coalesced, and only the affected tabs are marked stale.
  Silent refreshes skip identical payloads, so focus, hover and scroll
  survive. Known cosmetic effect until v0.6 merges: the probe's 404 shows up
  as one "Failed to load resource" network line in the devtools console. It
  is not a script error.
- **D46 — asset rules enforced by test.** The static-asset test walks every
  embedded file and enforces: no external references, no
  `innerHTML`/`outerHTML`/`insertAdjacentHTML`/`document.write`/`eval` in any
  script, no inline `<script>`/`style=` in `index.html`, a synchronous
  `/theme.js`, and a 150 KB budget for all web assets (v0.7 ships about
  84 KB). Doctor and error details render `` `cmd` `` spans as `<code>` by
  splitting the string into text and `<code>` nodes, never by parsing HTML.

**v0.7 note (2026-11-18, branch only, not merged).** Shipped on the branch:
brand assets, both themes with system/dark/light switch, canvas background,
toasts, skeletons, staggered reveals, typing tagline, status pills,
shortcuts (`1`–`5`, `/`, `t`, `b`, `r`, `?`), stacked-card layout to 360 px,
SSE/polling live updates, and the v0.5 nits (backtick → `<code>`, aligned
badge column). Checked by hand in headless Chromium on fake demo data (temp
HOME): Playwright behaviour checks (theme default/persistence/no-flash, 501
toast + command, shortcuts, filter, reduced motion), zero JS errors, and
screenshots in both themes plus mobile (`docs/img/gui-*-{dark,light}.png`).
Idle CPU (headless Chromium, software compositing, 1280×800, 10 s windows):
renderer main thread 0.03 % (bg on, idle > 30 s) / ≤ 0.4 % (bg off; a
15 s poll lands in the window); whole browser process tree 0.1 % to 3.6 %
by the same split. While the user is actively moving the mouse and the
background animates at 30 fps: main thread ~12 %, whole browser ~65 % in
this GPU-less VM. That number is software rendering. It is the reason for
the D43 idle freeze. Not yet verified: CPU in a real GPU-backed desktop
browser, a Lighthouse a11y run (target ≥ 90; contrast, labels, roles, focus
and reduced motion were checked by hand), and SSE against the real v0.6
`/api/events`.

## 2026-11-18 (v0.6 — one-line install + always-on, SPEC-V0.6)

- **D31 — the agent is the GUI server, made persistent.** `cwm agent` reuses
  `internal/gui` unchanged in its API and adds `internal/agent`: a persistent
  token in `<cwm home>/server.toml` (0600, atomic write; on Windows the user
  profile ACL is the protection), the bound port written back so `cwm
  dashboard` finds it (saved port first, then 47800–47809), and a
  single-instance OS file lock (`flock` / share-mode-0 `CreateFile`, both
  released by the kernel when the process dies — no stale-lock cleanup). A
  second launch probes the running agent (`GET /api/agent` with the
  token), opens the browser there and exits 0. `cwm gui` is the foreground
  agent with the browser opened — **contract change:** the token now
  survives restarts (was per launch); the agent cookie gets Max-Age 30 days
  so a bookmarked `/` keeps working. `cwm service rotate-token` rewrites the
  token and the running agent picks it up by polling server.toml (2 s):
  old cookies, bookmarks, Bearer tokens and open SSE streams die at once.
  The agent never logs the token (journald / launchd / agent.log); only
  `cwm gui` and `cwm dashboard --no-open` print the sign-in link.
- **D32 — non-loopback only by the explicit flag.** The bind address comes
  only from `--bind` (never from server.toml or env; the recorded bind is
  informational), prints a plain-HTTP warning, and the Host allow-list
  grows by exactly the concrete bind IP plus each `--allow-host` (`H` =
  `H:<port>`, `H:P` exact). Wildcard addresses are never an allowed Host.
  `cwm gui` keeps refusing `--bind`.
- **D33 — live updates via SSE from a cheap snapshot diff; additive endpoints.**
  `GET /api/events`: `hello` (snapshot + agent info), an immediate and then
  15-second `heartbeat`, and `state {changed, snapshot}` when a 5-second
  snapshot (initialized, account names, project/session counts, newest
  session mtime — no network, no git; cached server-wide) differs. Streams
  end on client disconnect, agent shutdown (request contexts derive from
  the serve context) or token rotation. Also new: `GET /api/agent`,
  unauthenticated `GET /healthz` (`{"app":"cwm","ok":true}`) and a
  server-rendered no-JS `GET /login` (form → `/?t=`), which lives in Go so
  the v0.7 frontend rewrite can link to it without owning it.
- **D34 — autostart is per user and split into agent + opener.** Linux:
  systemd `--user` unit `cwm-agent.service` (`agent --no-open`,
  `Restart=on-failure`) plus XDG autostart `cwm-dashboard.desktop`
  (`cwm dashboard --wait 30`) — the opener runs inside the graphical session,
  which a user unit may start before; without a systemd user session
  (detected from the filesystem: `systemctl` on PATH + the manager socket)
  the desktop entry alone starts the agent. Windows: a Task Scheduler task
  from generated UTF-16 XML whose LogonTrigger and principal are the current
  user (a standard user may create that; an any-user `/SC ONLOGON` needs
  admin), LeastPrivilege, InteractiveToken, no time limit, IgnoreNew,
  restart on failure, running `cwmd.exe agent --open`; when schtasks refuses,
  the HKCU Run key via `reg.exe`. macOS: two LaunchAgents (agent with
  `KeepAlive.SuccessfulExit=false`, opener with `RunAtLoad` only) loaded with
  `launchctl bootstrap gui/<uid>`. `--open-only --url U` installs just the
  opener running `cwm service open-url U` (waits up to 120 s for U). All files
  come from `text/template`s and plans are pure data (files, Pre/Commands,
  removals) applied through an injected Runner; tests never run real
  systemctl/schtasks/launchctl/reg. `service install` also makes sure an
  agent runs right away (the Windows task only fires at logon). Dry run:
  global `--dry-run` or `CWM_SERVICE_DRYRUN=1`.
- **D35 — Windows without consoles.** `cwmd.exe` = `cmd/cwmd` linked with
  `-H=windowsgui` (goreleaser build id `cwmd`, shipped in the Windows
  archives). It runs the sibling `cwm.exe` with a *hidden* console
  (`CREATE_NO_WINDOW`) and waits for it, rather than serving in-process: a
  GUI-subsystem process has no console, so every console child the agent
  starts (git, syncthing, `claude --version`) would otherwise pop a window.
  Output → `<home>/logs/agent.log`, failures → a message box. `cwm.exe`
  double-clicked from Explorer (no args, `GetConsoleProcessList` == 1) runs
  `cwm dashboard` instead of flashing the help; a failure keeps the window
  open until Enter.
- **D36 — macOS is a first-class target.** darwin amd64/arm64 tar.gz builds
  (no .deb); the terminal launcher drives Terminal.app via `osascript … do
  script`, superseding D27's macOS refusal: the command string is built only
  from argv by POSIX single-quoting each element (`env CWM_HOLD_ON_ERROR=1`
  prefix, since Terminal's new shell does not inherit our env) and then
  escaping the line as an AppleScript literal; a unit test decodes both
  layers back to the original argv, hostile inputs included.
- **D37 — installers verify or refuse.** `scripts/install.sh` (POSIX sh) and
  `scripts/install.ps1` (5.1+, ASCII, CRLF) install per user only, always
  check the archive's SHA-256 against the release `checksums.txt` and abort
  — before anything is written — on a mismatch, a missing entry or a missing
  hash tool. PATH changes are single marked lines (`# added by cwm
  installer`) / one user-Path entry, so `--uninstall` removes exactly them;
  the workspace is never deleted. Windows uses the zip + `Expand-Archive`:
  `Invoke-WebRequest` downloads carry no Mark-of-the-Web. The archive name is
  pinned in `.goreleaser.yaml` because both scripts build it.
  `scripts/test-install.sh` runs both scripts (install.ps1 under pwsh) against a
  local fake release.
- **D38 — CLI conveniences.** `--version`/`-v`; single-dash long flags are
  rewritten to `--` form for global flags and cwm's own subcommands, but
  never for the direct-launch form, `open`, `hook`, `mcp` or anything after
  `--`, whose arguments belong to claude or other programs. `agent`,
  `service`, `dashboard` join the reserved names.
- **D39 — CI covers what v0.6 ships.** CI also runs on pushes to `feat/**`
  (and manually), cross-builds and vets darwin/windows, runs the legacy bash
  kit, shellcheck and the installer harness on ubuntu, and parses install.ps1
  with Windows PowerShell 5.1 on windows. Pages (`pages.yml`) publishes the
  installers from main; enabling Pages ("Source: GitHub Actions") is a
  one-time manual repo setting.

## 2026-11-17 (v0.5)

- **D27 — GUI actions open a real terminal; argv arrays only.** Claude Code
  and `claude auth login` are interactive TUIs, so the GUI never hosts them:
  `internal/gui/term` (`Launcher{Open(argv []string) error}`) opens a
  terminal window running the right cwm command; everything else (init,
  pairing) runs in-process through the same functions the CLI uses (D26).
  The argv is always an exec argument array, never a shell string built
  from input. Windows: `wt.exe -- <argv>` when on PATH, else
  `cmd.exe /c start "cwm" <argv>` with each argument quoted individually via
  `syscall.EscapeArg` (characters cmd.exe expands even inside quotes —
  `%`, `!`, `"` — are refused). Linux: `$TERMINAL` (one program name, never
  word-split), then `x-terminal-emulator`, `gnome-terminal --`,
  `konsole -e`, `xfce4-terminal -x`, `alacritty -e`, `kitty`, `xterm -e`;
  no `DISPLAY`/`WAYLAND_DISPLAY` counts as headless. macOS has no terminal
  path in v0.5 (Terminal.app only takes a command string). No terminal →
  `501 {"error":"no terminal found","command":"<copyable cmd>"}` and the UI
  shows the command with a copy button. The resume command is the D20
  passthrough `cwm open <project> new --resume <id>`, with the project
  required in the manifest and the id looked up via `internal/sessions`
  (never trusted from input). Terminals start detached (own session /
  process group) so Ctrl-C on `cwm gui` never reaches them, and set
  `CWM_HOLD_ON_ERROR=1` so a failing command keeps its window open until
  Enter. The page's CSRF header uses an HMAC of the launch token served by
  `GET /api/csrf`, so the HttpOnly token never appears in a response body;
  the raw token stays valid for scripted callers. Tests use a fake
  Launcher — a real terminal is never spawned in tests.
- **D23 — GUI stack: stdlib `net/http` + `embed`.** Static HTML/CSS/vanilla
  JS under `internal/gui/web/`, no frontend framework, no build step, no new
  Go deps (BurntSushi/toml stays the only one). The tray icon is dropped from
  v0.5 (it needs cgo/native deps); `cwm gui` opens the default browser
  instead (`xdg-open` / `open` / `cmd /c start`, skipped when unavailable).
- **D24 — GUI security model.** Bind `127.0.0.1` only — never `0.0.0.0`, and
  no flag to change it in v0.5. A random 32-byte token (crypto/rand, hex) per
  launch; the browser opens `http://127.0.0.1:<port>/?t=<token>`, the first
  request sets an HttpOnly SameSite=Strict cookie and redirects to `/` (token
  stripped from the URL). Every `/api/*` request needs that cookie or
  `Authorization: Bearer <token>`, else 401. State-changing endpoints are
  POST-only and additionally require `X-CWM-CSRF: <token>` (else 403). The
  `Host` header must be `127.0.0.1:<port>` or `localhost:<port>` (else 403) —
  the DNS-rebinding guard.
- **D25 — installers: tar.gz/zip (existing) + `.deb` via goreleaser
  `nfpms`.** No scoop, no brew (maintainer decision); winget deferred to v1.0.
- **D26 — one data layer.** CLI `--json` output and GUI `/api/*` bodies are
  the same structs from `internal/status`; handlers and CLI renderers only
  format them. `doctor.Run` builds a `[]Check` slice that the unchanged text
  renderer prints (pinned byte-for-byte by golden files captured before the
  refactor).

**v0.5 closing note (2026-11-18, v0.5.0 beta).** Shipped: `cwm gui`
(localhost dashboard with Sessions / Projects / Devices / WIP / Health tabs),
the four-step setup wizard, Resume and Pair from the browser, `--json` on
`list` / `devices` / `wip dirty` / `doctor`, and `.deb` packages for linux
amd64/arm64 (goreleaser `nfpms`, binary in `/usr/bin`, recommends `git` and
`syncthing`, listed in `checksums.txt`). Covered by tests: the D24 auth
matrix, input validation, JSON shapes and the terminal launcher's argv
building (fake Launcher); checked by hand: a headless Chromium render of the
dashboard tabs and the wizard (the README screenshots, taken on fake demo
data). Not yet verified: a real terminal window
opening from the GUI on a Linux desktop or Windows (every check so far ran
headless or with a fake launcher), and a full run in a real browser on
Windows. Both stay open until v1.0; the "beta" label stays until they are
confirmed.

## 2026-11-09 (v0.4.1)

- **D22 — CI platform contract.** windows-latest runs build/vet/platform-logic
  tests; POSIX-stub behavioral tests skip on windows with an explicit message
  (coverage stays on ubuntu, incl. e2e); real-Windows junction/linker
  verification is an opt-in integration test (`CWM_WIN_INTEGRATION=1`) to run
  on a real Windows box, not CI.

## 2026-11-04 (v0.4)

- **D19 — session discovery: raw `projects/*/<uuid>.jsonl` at depth 1 is the
  source of truth.** `sessions-index.json` is enrichment only — a missing,
  stale or malformed index never hides a session; the raw scan still lists
  it. Subagents (`agent-*.jsonl`) and sidechains (`isSidechain` index
  entries) are excluded.
- **D20 — the resume path reuses the launcher passthrough.** `cwm sessions
  --pick [N]` resolves a row and launches `claude --resume <id>` through the
  existing `Open` path — no second launch path; clone-on-demand, name pinning
  and sync hints apply unchanged.
- **D21 — `cwm wip dirty` scans the repos root only.** Repos absent locally
  are clean by definition on that machine — the view answers "what should I
  push before switching devices", not "what is dirty anywhere".

## 2026-11-03 (v0.3)

- **D16 — deletions SYNC (normal semantics); the safety net is staggered
  versioning, 30 days** (`cleanInterval` 3600, `maxAge` 2592000). The v0.1
  bash-kit advice "ignore-delete ON" is withdrawn: with versioning active,
  cascading deletes are recoverable from `.stversions`, while ignore-delete
  causes worse failure modes (orphans accumulate on peers).
- **D17 — cwm never supervises the syncthing daemon.** It discovers a
  running instance (GUI+API read from `config.xml`); `cwm pair --start`
  spawns ONE detached `syncthing serve --no-browser` process when none is
  running — no supervision, no autostart. Users running syncthing as a
  service (the recommended setup) are untouched.
- **D18 — folder ID is the fixed string `cwm-workspace`**, identical on
  every device — it is the folder identity. The local PATH differs per
  device and that is fine.

## 2026-11-03

- **D12 — usage lens v0.2 = identity + login freshness only.** The account
  list shows who you are logged in as and how fresh that login is; live usage
  numbers are deferred — the underlying endpoint is undocumented and needs
  research, and cwm never fabricates data.
- **D13 — MCP = `workspace/mcp.json` + `--mcp-config` at launch.** cwm manages
  server entries in `workspace/mcp.json` (`cwm mcp add|list|remove`) and
  appends `--mcp-config` when launching; the account's `.claude.json` is
  never rewritten by cwm.
- **D14 — link modes symlink/junction/copy, probed at init** and stored in
  `config.toml` (`link_mode = auto|symlink|junction|copy`); launch-dir
  assembly uses the probed mode per item type.
- **D15 — hooks are `cwm hook` subcommands.** The generated `settings.json`
  invokes `<abs cwm binary> hook session-start|session-end`; the bash hooks
  remain for the legacy kit only.

## 2026-11-02 (second session)

- **D10 — cwm is all-in-one, not a ccam companion.** cwm absorbs every
  account-switcher feature (multiple logins, switching, per-account usage,
  import, shortcuts) as a *subset* of its workspace model. No dependency on
  ccam or any other tool; "use ccam alongside cwm" positioning is withdrawn.
  README rewritten accordingly.
- **D11 — workspace-first account architecture.** The workspace (sessions,
  memory, skills) is the primary object; accounts are pluggable logins
  attached to it. Layout: `~/.cwm/workspace/` (synced content) +
  `~/.cwm/accounts/<name>/` (credentials only, never synced by default) +
  `~/.cwm/launch/<account>/` (assembled at launch — workspace items +
  that account's credentials — and set as `CLAUDE_CONFIG_DIR`). All accounts
  attached to a workspace share it fully by default; isolation comes from
  creating separate workspaces, not from per-item sharing matrices.
  Open question for v0.2 implementation: Windows file-symlink constraint for
  shared single files (junctions cover dirs; dev-mode or copy-sync needed for
  `CLAUDE.md` / `history.jsonl` / `settings.json`).

## 2026-11-02

- **D1 — product name `cwm`** (Claude Workspace Manager), repo `Mr-JoE1/cwm`,
  released under the MIT license.
- **D2 — workspace dir stays `~/.claude-me` in v0.1** — stability over
  cosmetics; existing synced folders keep working. v0.2 makes the directory
  configurable, with migration from `~/.claude-me`.
- **D3 — session identity via `CLAUDE_CODE_PROJECT_DIR_NAME` pinning**, not
  OS-specific path slugs — this is the cross-OS core of cwm.
- **D4 — `repos.tsv` TSV manifest** (name, tab, origin URL) — no JSON
  dependency in bash; written by the SessionStart hook.
- **D5 — WIP handoff via `sync/<sanitized-hostname>` branches** — non-force
  pushes only, hooks always exit 0 so a failed push never kills a session.
- **D6 — sync engine is Syncthing P2P**; v0.3 automates pairing via its REST
  API. No cloud service, ever.
- **D7 — GUI is an embedded localhost web UI in a Go binary** (the Syncthing
  model), decided for v0.5. The CLI stays first-class at all times.
- **D8 — `cl` is kept as a legacy alias for `cwm`** for one major version,
  then it is deprecated.
- **D9 — doctor is local-only by default** (`--net` opts into the SSH probe)
  so it stays fast and testable offline.
