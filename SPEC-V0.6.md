# SPEC v0.6 — one-line install + always-on agent

Contract: `docs/ARCH-V2.md` (v0.6 row). Exit test: on a fresh Windows / Linux /
macOS box, paste one line → the dashboard opens; reboot + log in → it opens again.
Decisions D31–D39 are logged in `docs/PLAN.md`. `internal/gui/web/*` (frontend) is
out of scope here (v0.7 runs in parallel); every v0.5 `/api/*` contract stays
backwards-compatible — v0.6 only **adds** endpoints and fields.

## A — `cwm agent` (D31, D32, D33)

`cwm agent [--bind ADDR] [--port N] [--open|--no-open] [--allow-host H]...`

1. **Persistent state** `<cwm home>/server.toml` (`$CWM_HOME` or `~/.cwm`), written
   atomically with mode 0600 (Windows: the file lives under the user profile, whose
   inherited ACL already limits it to the user, SYSTEM and Administrators):
   ```toml
   token = "<64 hex>"   # dashboard key; rotate with `cwm service rotate-token`
   port  = 47800        # the port the agent actually bound (read by `cwm dashboard`)
   bind  = "127.0.0.1"
   pid   = 12345
   started = 2026-11-18T10:00:00Z
   ```
   The token is created on first run (crypto/rand, 32 bytes hex) and kept across
   restarts; a malformed token is regenerated.
2. **Single instance**: an OS file lock on `<home>/state/agent.lock` (`flock` on
   POSIX, exclusive share-mode-0 `CreateFile` on Windows; both vanish with the
   process, so no stale locks). A second launch while the lock is held does not bind
   anything: it waits up to 5 s for the running agent to answer
   `GET /api/agent` (Bearer token from server.toml), prints its URL, opens the
   browser there unless `--no-open`, and exits **0**. Lock held but agent never
   answers → exit 1 with a hint.
3. **Port**: `--port N` = exactly N (busy → error, no fallback). Otherwise the
   port recorded in server.toml (if any) first, then 47800, then 47801–47809.
   The bound port is written back to server.toml.
4. **Bind**: default `127.0.0.1`. Any other address is accepted **only** from the
   explicit `--bind` flag (never from server.toml or the environment) and prints a
   warning (plain HTTP — put TLS in front). `cwm gui` never accepts `--bind`.
5. **Host allow-list** (DNS-rebinding guard, D24 extended): `127.0.0.1:<port>`,
   `localhost:<port>`, the concrete `--bind` IP (`<ip>:<port>`), and every
   `--allow-host H` (`H` alone matches `H:<port>`; `H:P` matches exactly). Anything
   else → 403.
6. **Auth**: the v0.5 matrix is unchanged; the token is now the persistent one.
   `/?t=<token>` still exchanges for the cookie and redirects to `/`; in agent mode
   the cookie is persistent (`Max-Age` 30 days, still HttpOnly + SameSite=Strict), so
   a bookmarked `/` keeps working across browser and agent restarts.
   `GET /login` is a server-rendered page (no JS) whose form submits `/?t=…` — a
   way in after the cookie expired. `cwm service rotate-token` writes a new token;
   the running agent notices the changed server.toml within 2 s and switches —
   old cookies, bookmarks and Bearer tokens get 401 from then on.
7. **New endpoints** (additive):
   - `GET /healthz` — unauthenticated, `{"app":"cwm","ok":true}` (probes / Docker).
   - `GET /api/agent` — `{agent:true, pid, port, bind, started, version}`.
   - `GET /api/events` — `text/event-stream`. On connect: `retry: 3000`, then
     `event: hello` (`{snapshot}`) and an immediate `event: heartbeat`
     (`{"time": RFC3339}`); a heartbeat every 15 s; every 5 s a cheap snapshot
     (no network, no git: `initialized`, account names, project count, session
     count, newest session mtime) is diffed and `event: state`
     `{"changed":["accounts",…],"snapshot":{…}}` is sent when anything changed.
     Snapshots are cached server-wide for the poll interval, so N tabs cost one
     scan. Streams end when the client disconnects or the agent shuts down.
8. Ctrl-C / SIGTERM = graceful shutdown (open SSE streams are closed first).
   Logs go to stdout/stderr (journald / launchd log / cwmd log file).

`cwm gui [--port N] [--no-open]` = `cwm agent --open` in the foreground (same
lock, token, port logic; prints the v0.5 lines incl. `cwm gui: stopped`). Changed
contract: two launches now share the persistent token (was per launch).

## B — `cwm dashboard [--no-open] [--wait SECONDS]` (D31)

Probe the agent from server.toml; if it answers, open (or with `--no-open` just
print) `http://127.0.0.1:<port>/?t=<token>`. Otherwise wait up to `--wait` seconds
(default 0) for one to appear, then start `cwm agent --no-open` **detached**
(POSIX: own session; Windows: `CREATE_NO_WINDOW|CREATE_NEW_PROCESS_GROUP`, i.e.
a hidden console that the agent's own children — git, syncthing — inherit
instead of each flashing a window; output appended to `<home>/logs/agent.log`),
wait up to 10 s for it to answer, then open. Exit 1 if it never comes up.
`--dry-run` prints what it would start.

## C — `cwm service …` (D34)

`cwm service install [--no-open] | install --open-only --url U | uninstall |
status | start | stop | rotate-token`. User level only — never sudo/admin.
`--dry-run` (global) or `CWM_SERVICE_DRYRUN=1` prints every file (path + content)
and command it would run, and changes nothing.

| OS | agent autostart | browser at login (default ON, `--no-open` = off) |
|---|---|---|
| Linux + systemd user session | `~/.config/systemd/user/cwm-agent.service` (`ExecStart=<cwm> agent --no-open`, `Restart=on-failure`, `WantedBy=default.target`) → `systemctl --user daemon-reload` + `enable --now` | `~/.config/autostart/cwm-dashboard.desktop` → `cwm dashboard --wait 30` |
| Linux, no systemd user session | — | `cwm-dashboard.desktop` → `cwm dashboard --wait 5` (starts the agent itself; `--no-open` added when off) |
| Windows | Task Scheduler task `cwm agent` from generated XML (UTF-16; LogonTrigger for the current user, LeastPrivilege, InteractiveToken, no time limit, IgnoreNew, restart on failure) via `schtasks /Create /XML … /F`, running `cwmd.exe agent --open|--no-open`. If schtasks refuses: HKCU `…\CurrentVersion\Run` value `cwm` via `reg.exe` | the same task (`--open`) |
| macOS | `~/Library/LaunchAgents/io.github.mr-joe1.cwm.agent.plist` (`RunAtLoad`, `KeepAlive.SuccessfulExit=false`) → `launchctl bootstrap gui/<uid>` | `io.github.mr-joe1.cwm.dashboard.plist` → `cwm dashboard --wait 30` |

- `--open-only --url U` (Docker host): only the opener, running
  `cwm service open-url U` (waits ≤120 s for U to answer HTTP, then opens it). U must
  be an `http(s)` URL without whitespace, quotes, `%`, `\` or backticks.
- `status`: what is installed (paths / task), the service manager's view, and
  whether an agent answers (+ URL). `start`/`stop`: via the service manager; `stop`
  additionally asks a still-answering agent's PID (from `/api/agent`) to exit.
- `rotate-token`: new token in server.toml, prints the new URL.
- Executable path baked into units = `os.Executable()` resolved through symlinks.
  `CWM_HOME`, when set at install time, is baked in too.
- All generated files come from Go `text/template`s; tests assert their content
  and that the injected command runner received exactly the expected commands.
  Tests never call real systemctl / schtasks / launchctl / reg.

## D — Windows double-click (D35)

- `cmd/cwmd` (built with `-H=windowsgui`): no args = `cwm agent --open` (start or
  reuse + open browser); with args = run those cwm args. stdout/stderr → append to
  `<home>/logs/agent.log`. A failure shows a message box. On Windows it runs the
  sibling `cwm.exe` with a hidden console and waits for it (so the agent's child
  processes never pop console windows); without a sibling it serves in-process.
  Builds on every OS (elsewhere it runs in-process, logging to the file).
- `cwm.exe` with no args whose console is owned only by itself
  (`GetConsoleProcessList` == 1, i.e. started from Explorer) runs `cwm dashboard`
  instead of printing help.

## E — CLI polish (D38)

- `cwm --version` / `cwm -v` = `cwm version`.
- Single-dash long flags (`-port 1`, `-no-open`, `-json`) are rewritten to the
  double-dash form before dispatch — except the direct-launch form, `open`, `hook`,
  and everything after `--`, whose args belong to claude / other programs.
- New reserved names: `agent`, `service`, `dashboard`.

## F — packaging + macOS (D36)

- goreleaser: darwin amd64 + arm64 (tar.gz, no .deb); build id `cwmd`
  (`./cmd/cwmd`, windows only, `-H=windowsgui`), shipped inside the Windows archives
  next to `cwm.exe`.
- Terminal launcher on macOS: `osascript -e 'tell application "Terminal" to do
  script "<cmd>"' -e 'tell application "Terminal" to activate'`; `<cmd>` =
  `env CWM_HOLD_ON_ERROR=1 ` + each argv element POSIX-single-quoted, then the
  whole string escaped as an AppleScript literal (`\` → `\\`, `"` → `\"`).
  Unit test round-trips argv through both decoders.

## G — installers + Pages (D37)

`scripts/install.sh` (POSIX sh; Linux + macOS) and `scripts/install.ps1` (Windows
PowerShell 5.1+):
1. detect OS/arch (amd64/arm64; anything else → clear error);
2. version = `--version X` / `-Version X` / `$CWM_VERSION`, else latest via
   `GET https://api.github.com/repos/Mr-JoE1/cwm/releases/latest` (install.sh falls
   back to the `releases/latest` redirect);
3. download `cwm_<ver>_<os>_<arch>.tar.gz` (Windows: `.zip`) + `checksums.txt`;
   **verify SHA-256** — mismatch, missing entry or no hash tool → abort, nothing
   installed;
4. install per user: `~/.local/bin/cwm` (`$CWM_INSTALL_DIR`), Windows
   `%LOCALAPPDATA%\cwm\bin\cwm.exe` + `cwmd.exe`; an agent from a previous install is
   stopped first;
5. PATH: install.sh appends one marked line (`# added by cwm installer`) to
   `~/.profile` plus `~/.bashrc` / `~/.zshrc` / `~/.zprofile` when they exist;
   install.ps1 updates the user `Path` and the current session;
6. `cwm init` when `<home>/config.toml` is absent; `cwm service install` unless
   `--no-service`; `cwm dashboard` unless `--no-open`;
7. `--uninstall`: `cwm service uninstall`, remove binaries and the PATH lines; the
   workspace (`~/.cwm`) is kept and the script says so.
Test seams (env): `CWM_INSTALL_API`, `CWM_INSTALL_BASE_URL`, `CWM_INSTALL_DIR`.
No sudo/admin anywhere.

`scripts/test-install.sh`: builds a snapshot release for the host (goreleaser
names), serves it with `python3 -m http.server`, runs install.sh against it with a
temp `HOME` and `CWM_SERVICE_DRYRUN=1`, asserts binary + PATH line + `cwm init` +
dry-run service plan; then a tampered `checksums.txt` must abort with nothing
installed; then `--uninstall` must remove binary and PATH lines.

`.github/workflows/pages.yml` publishes `install.sh`, `install.ps1` and a minimal
`index.html` to `https://mr-joe1.github.io/cwm/` on pushes to main touching
`scripts/` (Pages source must be set to "GitHub Actions" once, by hand).

## Tests / gate
Unit: server.toml round-trip + perms + rotation; lock exclusivity; port preference;
host allow-list; persistent cookie; `/login`; `/healthz`; `/api/agent`; SSE hello +
heartbeat + state diff (short intervals injected); token hot reload; flag
normalization; `--version`; dashboard probe/spawn via seams; every service template
per OS + runner command sequences + dry-run; open-url URL validation; AppleScript
quoting round trip. e2e (`-tags e2e`): real binary — `cwm agent` in a temp home,
second launch exits 0, `/api/events` heartbeat, token survives restart.
Gate = the v0.5 gate + `GOOS=darwin GOARCH=arm64 go build ./...`,
`GOOS=windows go vet ./...`, `GOOS=darwin go vet ./...`, shellcheck, installer harness.
