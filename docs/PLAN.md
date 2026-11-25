# cwm — decision log

Dated entries, newest first. Public-safe: no private infrastructure or personal
details. Decisions are binding until superseded by a newer entry.

## 2026-11-25 (v0.8.3 — patch release, the first real Windows migration)

- **D103: the desktop app's copy is found in more layouts, and every
  bundled family is probed.** On the owner's machine B,
  `%APPDATA%\Claude\claude-code\2.1.328` existed, but no candidate came
  from it. The research (SPEC-V0.8 research notes) confirms
  `<ver>\claude.exe` on Windows, with the real file under the MSIX
  package's `LocalCache\Roaming` when the app runs virtualised. It also
  shows that macOS uses `<ver>/claude.app/Contents/MacOS/claude`, which
  the D98 pattern missed. The lookup now tries, under each claude-code
  folder (`%APPDATA%`, the MSIX `LocalCache`, macOS Application Support,
  and the Linux builds' `~/.config/Claude`): `<ver>/claude(.exe)` and
  one and two levels below it, plus the app bundle. That is four fixed
  globs and no walk. Within one version the shallowest path wins. The
  candidates are tiered: the newest copy of every family (MSIX /
  roaming, Squirrel, editor extensions) is probed before any second
  copy. A survey (doctor, `/api/claude`, the debug bundle) always probes
  that first tier, even past `SurveyLimit`. When several bundled copies
  of one tier verify, the highest probed Claude Code version wins, and
  on a tie the desktop app's copy wins (more stable than an editor
  extension's). This supersedes D98's "desktop before extension". The
  result's `why` ("chosen: … the newest (2.1.330); the Claude desktop
  app has 2.1.328") goes into doctor's `claude-bundled` line and the
  bundle's `choice:` line. For the newest `<ver>` folder that holds no
  executable, the debug bundle adds `bundled: desktop app dir found
  (~\AppData\Roaming\Claude\claude-code\2.1.328) but no claude.exe
  inside; entries: …` (up to five names, dirs marked).
  `$CWM_CLAUDE_NO_SEARCH=1` still turns the bundled lookup off.
- **D102: a migration adopts the live login into the account the user
  already has.** The owner's config named one account, `tat`
  (`default_account = tat`), with an empty vault; the live
  `~/.claude/.credentials.json` was tat's, yet the plan imported it as a
  new account `default`. When no account's recorded identity matches the
  live login (accountUuid, else email, from the live `.claude.json`), the
  plan now picks a target: the account named with `--account` if it
  exists; else the only account; else `default_account`. A target with no
  saved login (in the vault, or saved by this plan from a v0.7 launch
  dir) receives the live login and stays the default. A target whose
  saved login has a provably different identity leaves the live login
  to a new account (`default`, or `--account`'s new name) with a plan
  warning; one whose saved login cannot be compared still receives it,
  with a warning (the saved copy stays in the backup). No accounts at
  all: `default`, as before. The plan line says who receives the login
  and why: `import tat (~/.claude/.credentials.json -> ~/.cwm/accounts/tat/.credentials.json)
  — the live login is tat's (the only account, no saved login yet)`;
  login lines use `~` paths, warnings are printed, and neither carries
  a credential value or an email. `cwm doctor --fix` on a pre-v0.8
  config now saves the live login into the default account's empty
  vault entry (the doctor's own suggestion; the vault path is the same
  in both layouts) and leaves everything else for the migration.
- **D101: a migration pauses the running agent; the backup skips what
  the agent or claude holds open.** The first real Windows migration
  (0.8.1) failed in the backup: the running agent holds
  `~/.cwm/state/agent.lock` with share mode 0, so reading it is a
  sharing violation, and the run left a partial `backups/migrate-<id>`
  behind. Changes: (1) never backed up and never rolled back: in
  `~/.cwm` the backups, logs, `cache/` (the rebuildable stats cache),
  `state/migrations.json`, `state/agent.lock`, `state/*.pid`,
  `state/*.sock`, `server.toml` (the dashboard token and port must
  outlive a rollback), every `*.lock`, and claude's `session-env` /
  `shell-snapshots` inside the isolated root, login-only dirs and v0.7
  launch dirs; in `~/.claude` every `*.lock`, `session-env` and
  `shell-snapshots`. The plan's backup size uses the same rules.
  (2) A source file that stays locked (sharing or lock violation)
  through four retries (about 2 s) is recorded in the manifest as
  `skipped: locked`, reported as a warning and left alone by a
  rollback, unless the plan reads or writes it: then the backup fails
  with the file and its likely holder named. (3) The manifest is
  written first with `incomplete: true` and the pid, and rewritten at
  the end; a backup that fails before the apply is removed. A
  `migrate-*` dir with no manifest (a pre-0.8.3 failure, older than
  10 minutes) or with an incomplete one whose process is gone is never
  a rollback target; every migration removes such dirs before it
  starts, `cwm migrate list` shows them, and `cwm migrate clean`
  removes them. (4) Coordination: option (a), pause over the local
  API, because the agent API made it small and the dashboard's own
  apply needs the same pause. `POST /api/agent/pause {holder, why,
  seconds}` takes a lease on the maintenance tick (mirror, MCP bridge,
  conflict watcher, `.stignore`) and returns once no tick is running;
  the CLI renews it every 30 s, the lease lasts 2 min, so a killed
  `cwm migrate` never leaves the agent paused. `POST
  /api/agent/resume` ends it. Both skip the action lock, so a pause
  during a dashboard migration answers 409 at once; a dashboard apply
  during a CLI pause answers 409 too. `cwm migrate` and `cwm migrate
  rollback` pause after the confirmation, plan again, and stop if the
  plan changed. With no agent running they hold `agent.lock`
  themselves, so none starts half-way. An agent that holds the lock
  but does not answer, or one older than v0.8.3 (no pause endpoint),
  gets option (b): "the cwm agent is running — stop it first: cwm
  service stop (start it again afterwards: cwm service start)".
  `GET /api/agent` gains `paused_by`.

## 2026-11-25 (v0.8.1 — patch release, GitHub issue #1)

- **D100: a one-field bug form, and "report a bug" fills it.** The owner
  found the old form asked too much. `bug_report.yml` now has one
  required field ("What went wrong?"), an optional "Debug logs" and an
  optional `cwm version` input (id `version`, prefilled by URL). The OS,
  arch, install method and Claude Code version are gone because the
  debug bundle carries them. The feature form has one required field and
  one optional. The dashboard's "report a bug" (statusline, Health,
  About) copies the bundle first, while the page still has focus for the
  clipboard, then opens
  `…/issues/new?template=bug_report.yml&version=<cwm version>&title=bug:%20`
  in a new tab. It is the only external URL the page opens, and the CSP
  is unchanged. A blocked clipboard leaves a toast with "copy debug logs";
  a blocked pop-up leaves a link. The statusline's "copy debug logs" became
  "report a bug"; copy stays on Health, the palette, the menus and `d`.
- **D99: project folders are numbered in debug reports; the redactor
  knows the dash-encoded form.** A v0.8.0 bundle listed session folders
  such as `F--Home-<User>-Projects-…`. That is Claude Code's encoding of
  a path (every character outside `[A-Za-z0-9]` becomes `-`), and the
  user name was in it, because the redactor only matched path forms.
  The redactor now also replaces (a) the home dir and the login name in
  that encoded form (case-insensitive, the name bounded by dashes or the
  end of the word, so `annual` keeps its `ann`); (b) any
  `-home-<x>`, `-Users-<x>`, `<drive>--Users-<x>` or `<drive>--Home-<x>`
  segment; (c) Windows homes on any drive under `Users` or `Home`
  (`F:\Home\<u>\…`), not only `C:\Users`. By default every session
  folder name is shown as `<project-N>` (name order) with its session
  count, and the same alias replaces the name anywhere in the report
  (doctor, logs, API errors), longest name first, whole words only,
  names under 4 characters only in the list. So a public issue carries
  neither the user name nor repo names. `cwm debug --show-projects`
  prints the names for local use; the user name inside them stays
  redacted. The dashboard's bundle always numbers them.
- **D98: Claude Code bundled with the desktop app or the editor
  extension counts as claude.** Neither the Claude desktop app nor the
  Claude Code extension for VS Code (and its forks) puts `claude` on
  PATH; each ships its own copy. A machine with only those reported
  "claude not found" (issue #1). The resolver now looks there after
  the standalone folders and the npm prefix. Order: `$CWM_CLAUDE`,
  `claude_path`, PATH, standalone folders, desktop app, editor
  extension. Families: the desktop app (Windows MSIX
  `%APPDATA%\Claude\claude-code\<ver>\claude.exe`, also under the
  package's virtualised `LocalCache\Roaming`; legacy Squirrel
  `%LOCALAPPDATA%\AnthropicClaude\app-<ver>\…\claude.exe` one to three
  levels down, never `app-<ver>\claude.exe` itself, which is the
  desktop app and would open its window; macOS `~/Library/Application
  Support/Claude/claude-code/<ver>/claude`, same shape as Windows, not
  confirmed on hardware) and the extension
  (`<editor>/extensions/anthropic.claude-code-<ver>[-<platform>]/resources/native-binary/claude`
  for `.vscode`, `.vscode-insiders`, `.cursor`, `.windsurf`,
  `.vscode-oss`, `.vscode-server`, `.vscode-server-insiders`). Each
  family is globbed and sorted by a numeric version compare (a
  platform suffix is not a pre-release), and at most two per family
  are probed with the D54 `--version` check. Bundled hits are never
  pinned into `claude_path`: their folder changes on every app update.
  They get no PATH advice for the same reason. Doctor gives a WARN
  (`claude-bundled`, exit 0) with the standalone install command. The
  source is reported as `desktop-app` / `vscode-extension`, the old
  `discovered` is renamed `standalone`, and `/api/claude` gains
  `bundled` and `bundle`. The not-found message says that the desktop
  app and the extension do not put claude on PATH, lists where cwm
  looked (grouped, `~` for home) and ends with the install command.
  The claude check is now doctor's first row (`--json` order changes:
  `claude` before `workspace`), so a missing claude is the first thing
  Health shows. A bundled copy's Health row offers "use this", which
  saves nothing: it says which copy cwm uses (from `/api/claude`) and
  repeats the standalone install command.

## 2026-11-24 (v0.8 — one workspace, SPEC-V0.8)

The owner-approved spec `SPEC-V0.8.md` is the input; its OWNER DECISIONS
are binding and reverse the per-account launch-dir design of D11.

- **D97: the agent service gets the installing PATH (v0.8 polish).** A
  service-started agent had the manager's short PATH, so doctor's bare
  `cwm hook …` check warned with the generic hook copy and `cwm init` as the
  fix, which changed nothing. `cwm service install` now writes the PATH it
  runs with, cwm's own folder first, into the systemd unit
  (`Environment=PATH=…`) and the launchd plist (`EnvironmentVariables`);
  an empty PATH leaves the manager default. Windows logon tasks inherit the
  user Path, so the fix there puts cwm's folder on it. The warning has its
  own id `cwm-path` and Health copy ("background service can't find cwm")
  with the one-command fix per OS: `cwm service install` (macOS; launchd
  reloads the agent), `cwm service install && systemctl --user restart
  cwm-agent` (Linux), the user-Path PowerShell line (Windows).
- **D96: the default account's login is always live (P9 QA).** On a
  machine without a Claude login, the first `cwm account add` made the
  account the default but left its login only in the vault, so plain
  `claude` and cwm's own default launches (no `CLAUDE_CONFIG_DIR` in
  capture mode) ran logged out while Health said OK. Now a login of the
  default account is installed live: after `account add` only when the
  live location is empty (adding never replaces a live login), after
  `account login` always (the user asked for that login; the replaced
  files are backed up first). The `vault` check warns when the vault has
  the default's login but the live location does not, and `cwm doctor
  --fix` fills an empty live location from the vault. Also from QA:
  every command answers `--help`; a bare `cwm hook …` entry that the
  checking process cannot find on its own PATH (an agent started by a
  service manager) is one warning instead of a failure per hook.
- **D95: the Settings tab and its API (P8).** The dashboard edits the
  same keys as `cwm config set`, through one shared code path
  (`PUT /api/settings`, CSRF), so a switch means the same thing in both
  places and runs the same side effects (service install, `.stignore`
  regeneration, hook merge). Keys whose effect reaches outside
  config.toml (autostart, the login opener, private sync) are previewed
  with `dry_run` and confirmed first. The mode is never a plain setting:
  the page opens the migration preview and applies it as a background
  job whose progress is polled and shown in the terminal-log component.
  Live updates ride the existing SSE stream (a `settings` topic keyed by
  the config hash). "running now" on account cards is shown only where
  it can be known without privileges (Linux `/proc`); elsewhere the
  field is absent rather than guessed. The 200 KB budget is kept by
  compacting existing assets, not by raising it. Atomic writes retry
  the rename briefly on Windows, where a concurrent reader of
  config.toml makes it fail with "Access denied".
- **D94: the v0.8 health checks (P7).** The v0.7 "CLAUDE_CONFIG_DIR not
  exported in ~/.bashrc" check is gone. `claude-reads-from` lists the
  live root, `.claude.json` and credentials (with `~`) and, in capture
  mode, FAILs on a persistent CLAUDE_CONFIG_DIR: an uncommented sh, csh
  or fish assignment in `~/.bashrc`, `~/.profile`, `~/.zshrc`,
  `~/.zprofile`, `~/.bash_profile` or `~/.zshenv` (each with a `sed` line
  to remove it), the Windows HKCU environment (`reg query`, read only)
  or launchd (`launchctl getenv`); a pre-v0.8 config only warns
  (migrate first), isolated mode is OK. New: `migration` (pre-v0.8
  config), `stignore` compares with the generated allow-list,
  `conflicts`, `mcp-bridge` (once `mcp.json` exists), `cwm-hooks`
  (settings.json agrees with features.hooks), `vault` (the default's
  login is saved; checked by existence only, the Keychain item without
  `-w`, so no prompt and no secret read) and `unknown-items`, an ok-level
  INFO line in capture mode. Fixes point to one command, `cwm doctor
  --fix`: one agent maintenance pass now, plus saving the live login
  into an EMPTY vault entry (a filled entry is left alone: the live
  login may be one the user switched to by hand). The exit code is
  unchanged: any FAIL exits 1.
- **D93: every feature switch is honoured.** `features.stats` off:
  no stats engine is created (no scan); `GET /api/stats` and
  `cwm stats --json` return `{"disabled": true, "reason": …}`, and
  `/api/state` gains `stats: false` so the page hides the Stats tab.
  `features.update_check = "off"`: `/api/about` reports
  `update.disabled`, the button is hidden and the check endpoint
  answers 403 without any network call. `features.autostart` and
  `features.open_dashboard_at_login` drive the per-user service:
  `cwm config set` installs (with or without the login opener) or
  uninstalls it, `--dry-run` prints the plan; the opener switch is only
  recorded while autostart is off. `cwm service install|uninstall`
  record the result back into `[features]`, so a v0.7-style install
  (opener on) shows as such in Settings. Every switch is read per
  request or tick, so no agent restart is needed.
- **D92: where path mirroring keeps its records, and how resume finds
  the local clone.** The session -> repo record is synced but written
  per device (`session-repos/<host>.tsv`, rides with `sync.sessions`):
  one writer per file means Syncthing never makes a conflict copy. The
  remote -> clone path map is machine-local state. Lookup order: the
  recorded path, `<repos root>/<name>` with the same origin, the source
  path when it exists here with the same origin, else clone on demand
  from `repos.tsv`. `cwm sessions resume <id>` is the capture-mode
  resume for the CLI and the dashboard (the path-derived folder is
  never put in an argv); `cwm sessions mirror` runs the agent's pass by
  hand. Every mirror write goes to this machine's own folder only, so
  each copy has exactly one writing device. The list dedupes by id and
  adds `copies`/`devices` (additive). Mirroring only runs on a config
  that records `mode = "capture"`: a pre-v0.8 workspace still has pinned
  folders.
- **D91: cross-device resume by path mirroring (owner-approved, P6b).**
  Capture mode files sessions under Claude Code's path-derived folder
  (D86), so a session from another device's clone at a different path is
  invisible to claude here. cwm materialises a local copy under the
  folder this machine's path produces (`internal/pathmirror`, Claude
  Code's exact name algorithm, see SPEC-V0.8 research notes), rewriting
  every `cwd` equal to or under the source repo path. Copies compare by
  line key (`uuid`, else the line hash without `cwd`), never by mtime, so
  mirroring cannot ping-pong: a copy that is a prefix of another is
  older and gets extended; two copies that are not on one chain have
  diverged, are both kept untouched and raise a Health warning, and the
  one whose last entry is newest is resumed. The source copy is never
  written. Repo identity is the normalised git remote (host lower-cased,
  scheme/user/port/`.git` dropped); the remote -> local path map is
  machine-local state, never synced. `features.path_mirror` switches it
  (default on in capture mode; isolated mode pins names and needs none).
- **D90: how `cwm migrate` merges, what it backs up, what rollback
  restores.** One engine (`internal/migrate`) serves `cwm migrate`,
  `cwm mode set` (and `cwm config set mode`, which needs `--yes` when
  non-interactive), `cwm init`'s and the dashboard's first run. Sources
  are the other mode's root and any real (unlinked) item of a v0.7
  launch dir; only allow-list items move, whatever the `[sync]` switches
  say (they decide what crosses devices, not what this machine keeps).
  Merge rules: a missing file is copied (mtime kept); a session file
  that is a prefix of the other copy is extended, never conflicted; a
  session that diverged is kept as `<id>.conflict-<host>.jsonl`; every
  other differing file keeps the target and stores the source version
  as a `*.sync-conflict-<stamp>-MIGRATE*` copy, so Health's "pick one"
  action resolves it; `history.jsonl` merges by line (timestamp order),
  `repos.tsv` by line, `settings.json`/`mcp.json` deep-merge (target wins
  scalars, arrays such as hook matchers and permission lists are
  unioned). Logins: a fresher v0.7 launch-dir copy refreshes the vault;
  the old mode's live login is saved back into the default's vault
  entry; a login already live at the target stays live (plain `claude`
  keeps its account) and is imported as `default` when no account
  matches its identity; otherwise the default's login is swapped in.
  The v0.7 default's `.claude.json` project entries (trust, allowed
  tools) are added to the live one when absent. A real migration backs
  up `~/.claude`, `~/.claude.json` and `~/.cwm`; adopt-in-place and a
  fresh first run back up only what they write (`~/.cwm`,
  `~/.claude.json`, `~/.claude/.stignore` and `.credentials.json`), so
  the installer never copies a large `~/.claude`. The manifest records
  files (sha256, mode), dirs and links (never followed: login-only dirs
  link into the workspace). Rollback undoes only the newest migration
  still in effect, restores every manifest entry and moves anything
  newer into `~/.cwm/backups/rollback-<id>-<stamp>/`; it leaves
  `~/.cwm/backups`, `~/.cwm/logs` and `state/migrations.json` (the record
  of the rollback itself) alone. macOS Keychain items are not files, so
  they are outside the backup (documented). Verify fails when any session
  id seen in a source is missing from the root; the mode is recorded only
  after verify passes.
- **D76: one workspace, two modes; D11 is superseded.** Sessions, history,
  memory, skills, agents, commands, rules, output styles, scheduled tasks,
  plugins, MCP servers and settings belong to the workspace, never to an
  account. Capture mode (the default) makes the standard Claude config dir
  (`~/.claude`) the workspace, so everything Claude Code does on the
  machine is captured whether or not cwm launched it, and never sets a
  global `CLAUDE_CONFIG_DIR`. Isolated mode (opt-in) keeps the workspace in
  `~/.cwm/workspace` and only cwm-launched sessions use it. The assembled
  `~/.cwm/launch/<account>/` dirs of D11 are gone; migration moves them
  into a backup (D83).
- **D77: one root resolver.** `internal/claudepaths` is the only code that
  turns (mode, account) into paths: the workspace root, the live
  `.claude.json`, the live credentials and the `CLAUDE_CONFIG_DIR` value to
  set (none for the default account in capture mode). Every caller (stats,
  sessions, list, open, handoff, doctor, the dashboard, the MCP bridge,
  Syncthing) receives a `storage.Layout` whose workspace root came from it.
- **D78: config.toml grows, its version does not.** `mode`, `[sync]`,
  `[features]` and `[display]` are additive keys of schema version 1, so a
  v0.7 binary still reads a v0.8 config (unknown keys are tolerated). A
  config written before v0.8 has no `mode`: it loads as `capture` and is
  flagged "needs migration" until `cwm migrate` records one. Every key is
  reachable from `cwm config list|get|set`; the dashboard writes the same
  keys through the same setter.
- **D79: accounts are a credential vault.** `~/.cwm/accounts/<name>/`
  keeps a login's credentials and its `.claude.json` identity keys, never
  data. Switching the default saves the live credentials back into the
  old default's entry first (Claude refreshes tokens in place), then swaps
  the new one in, merging only identity keys into the live `.claude.json`.
  Both writes are atomic and the replaced files are backed up. macOS
  Keychain access sits behind an interface with a fake and is marked
  untested on hardware.
- **D80: parallel accounts use login-only dirs.** A non-default account
  launched with `cwm run --account <name>` gets `CLAUDE_CONFIG_DIR` =
  `~/.cwm/accounts/<name>/`, which holds its credentials, its own
  `.claude.json` and links to every shared workspace item (dirs: symlink
  or junction; files on Windows: symlink, else a hardlink that every run
  and agent tick re-verifies and merges back when an atomic rename broke
  it). The default account uses the root directly.
- **D81: the `.stignore` is an allow-list.** It is generated from
  `[sync]`: one `!/<item>` line per enabled item and a final `*`, so
  anything a future Claude Code adds stays local. Turning a switch off
  only ignores the item; nothing is deleted.
- **D82: `mcp.json` is the synced MCP source of truth.** A 3-way merge
  against a stored last-synced snapshot bridges it into every live
  `.claude.json` (deletions propagate both ways, the workspace wins
  conflicts and logs them) on each agent tick and cwm launch. Other
  `.claude.json` keys are never touched.
- **D83: migration is backup, plan, apply, verify, record.** The backup
  copies `~/.claude`, `~/.claude.json` and `~/.cwm` (minus backups) with a
  sha256 manifest; rollback restores from it byte for byte. Nothing is
  deleted, only moved into the backup. The agent never migrates silently:
  it shows a banner.
- **D84: `PUT /api/settings` is the first PUT.** Settings are a document,
  so the dashboard writes them with PUT under the same CSRF header,
  content-type and body-size rules as every POST action; the change is
  broadcast on the event stream.
- **D85: `repos.tsv` rides with `sync.sessions`.** The spec's allow-list
  has no entry for cwm's repo manifest, but `cwm open` and session resume
  need it on the other device to clone a session's repo. It is cwm's own
  file, so it syncs whenever sessions do.
- **D86: capture mode gives up project-name pinning for the default
  account.** Claude Code ignores `CLAUDE_CODE_PROJECT_DIR_NAME` unless
  `CLAUDE_CONFIG_DIR` is set (SPEC-V0.8 research notes), and capture mode
  must not set it for the default account, or plain `claude` and
  cwm-launched sessions would read different `.claude.json` files and, on
  macOS, different Keychain entries. Those sessions use Claude's
  path-derived folder names; isolated mode and parallel accounts keep
  pinning. Documented as a known limitation.
- **D87: conflict copies are settled in the workspace, never lost.**
  The agent's maintenance tick (once a minute, and only when the config
  records a v0.8 mode and the root exists, so it never touches `~/.claude`
  before a migration was reviewed) regenerates the `.stignore` and folds
  `history.sync-conflict-*.jsonl` into `history.jsonl`: exact-line
  dedupe, ordered by the `timestamp` field (a line without one stays after
  its predecessor). Every other conflict copy inside a synced item waits
  for a choice (`cwm conflicts`, `GET /api/conflicts`,
  `POST /api/conflicts/resolve`): keep the current file, take a copy, or
  keep both merged by line — refused for JSON documents, where a line
  union would break the file. Merged copies and losing versions move to
  `~/.cwm/backups/conflicts/<stamp>/`. `conflicts` becomes a reserved
  name.
- **D88: private sync is enforced on, restored off only on request.**
  With `features.private_sync` on, `cwm pair` and `cwm config set` set
  Syncthing's `globalAnnounceEnabled` and `relaysEnabled` to false and
  `localAnnounceEnabled` to true, keeping every other option. Turning the
  switch off restores those three to Syncthing's defaults once; a plain
  `cwm pair` with the switch off never touches them, so a user's own
  Syncthing tuning survives. `cwm pair <id> --address tcp://host:22000`
  (repeatable; `POST /api/pair` takes `addresses`) replaces the peer's
  address list; without it the list is left alone.
- **D89: one upkeep pass, shared by the agent and every launch.**
  `internal/upkeep` holds the steps that keep a v0.8 workspace
  consistent: `.stignore`, the history merge, the login-only dir link
  re-verify, the MCP bridge and the hook merge. The agent runs them once
  a minute; `cwm open`, `cwm run` and dashboard launches run the bridge
  and the hook merge before claude starts and never fail the launch on
  them. The launcher stops passing `--mcp-config` once the account's
  `.claude.json` carries the servers (it would list each server twice)
  and keeps it as the fallback when that file does not exist yet or the
  bridge failed. Nothing runs on a config that still needs migration.
  Hook entries are recognised by their command string (the bare
  `cwm hook session-start|session-end`, or any older path to a cwm
  binary, which is replaced). With `features.hooks` off the tick leaves
  `settings.json` alone and `cwm config set features.hooks off` removes
  cwm's entries once, while `cwm hook` itself becomes a no-op on that
  machine; otherwise two devices with different switches would fight over
  the synced file. `features.handoff_push` off makes `session-end` skip
  the push. The bridge snapshot lives in `~/.cwm/state/mcp-bridge.json`
  (per target path, canonical JSON per server); a `.claude.json` that is
  not valid JSON fails the bridge and is never rewritten.

## 2026-11-20 (v0.7.2 — UI refresh, terminal vocabulary, SPEC-V0.7.2)

The designer review (`docs/ui/DESIGN-REVIEW-V0.7.2.md`) is the input; its
OWNER DECISIONS section wins over every option it lists. The companion
reference HTML was never delivered, so the written review is all there is.

- **D67: navigation is a tmux window list plus a Ctrl-K palette.** Desktop
  tabs read `[cwm] 1:sessions … 7:stats*` (option C of the review); the
  palette is a shell prompt `cwm ❯` over sections, actions and sessions.
  Under 720 px a bottom bar (sessions, projects, health, accounts, more)
  replaces the tab strip, and "more" opens a sheet with the rest plus
  theme and motion. The footer becomes a solid, fixed statusline. Rejected:
  a sidebar (costs width on the tables) and About as an eighth tab (it is
  a modal: About is reference, not a place you work in).
- **D68: one token set, checked by a script.** Review §2.4 replaces the
  token block; old names stay as aliases for one release. Contrast is not
  reviewed by eye any more: `scripts/contrast-check.js` (node, no deps)
  parses the two theme blocks of `app.css`, flattens translucent surfaces
  over `--bg` and fails CI when a text pair is under 4.5:1 or a control /
  chart-mark pair under 3:1. Node is already on the CI runners; the script
  is not part of the Go build.
- **D69: build info lives in `internal/buildinfo`; About is local.**
  `Version`, `Commit`, `BuildDate` come from the release ldflags; without
  them `debug.ReadBuildInfo()` gives the vcs revision, time and dirty
  flag, and the UI says `dev build a1b2c3d`. `cli.Version/Commit` remain
  as overrides so older build scripts keep working. Author metadata is
  three hard-coded constants (name, GitHub profile, repo), nothing more.
  `GET /api/about` never touches the network; the only update check is a
  CSRF-protected POST the user triggers, made by the agent (never the
  browser) with a 5 s timeout through an injectable client.
- **D70: "WIP" is called "handoff".** It is what the tab is for: work left
  on another machine. The API (`/api/wip`), the CLI (`cwm wip`) and the
  event topic keep their names; `#wip` redirects to `#handoff`. Projects
  does not absorb it until the tab count would reach nine.
- **D71: asset budget 200 KB raw, < 60 KB gzip.** The owner raised the hard
  cap from 150 KB for the refresh (command palette, About, sprite, bottom
  bar). Gzip is the number that crosses the wire; the raw cap keeps the
  embedded binary honest. A Go test enforces both. Still no frameworks,
  no CDN, no runtime fonts, no external requests from the browser.
- **D72: one copyable-command component, plain Health copy.** Every
  command in the UI is `[$ command | copy]` with copy → copied (1.4 s,
  announced). Health reads like a boot log, problems first; the plain
  title / why / fix copy lives in the dashboard keyed by the doctor check
  id, so `cwm doctor`, `/api/doctor` and the ids do not change, and the
  raw detail stays one click away.
- **D73: the heatmap is always 12 months.** Titled "activity, last 12
  months", it ignores the range selector, which only drives the KPIs and
  breakdowns. Token kinds are two bars, billed (input + output) and cache
  (write + read). Money is `--text-strong` with "≈", never a status
  colour.
- **D74: screenshots come from generated demo data.** A throwaway HOME
  with invented projects, sessions, accounts and 12 months of stats; never
  a real workspace. The same generator feeds the README and wiki images.
- **D75: doctor levels: optional checks skip, a missing claude fails.**
  `doctor.Check` gains `optional` (additive in `--json` and `/api/doctor`).
  An optional check that is not set up (Syncthing, D17) keeps JSON status
  `warn` for existing scripts, but prints `skip:`, shows `[ SKIP ]` in
  `--muted` after the warnings, and counts in no total: not in "N things
  to fix", not in "N of M checks passed", not in the warn count; the text
  summary appends ", N skipped" when N > 0. The dashboard reads the flag,
  never the detail text. `claude not found` (no verified Claude Code
  binary at all) becomes `FAIL`, so `cwm doctor` exits 1: claude is the
  core dependency. A verified claude that is only off PATH stays a warn.
  These two checks are the only CLI output / exit-code changes.

## 2026-11-20 (v0.7.1 — stats & activity, SPEC-V0.7.1)

The roadmap row v0.7.1 under D48 ("stats are local and derived"): a Stats
tab, `cwm stats` and `GET /api/stats`, all computed from the session
transcripts already in the workspace. Nothing is fetched from an Anthropic
API and nothing leaves the machine.

- **D60: what counts, and the dedupe rule.** Only `projects/*/*.jsonl` at
  the top of each project dir (the `cwm sessions` discovery, D19; no
  `agent-*` sidechains, no `subagents/`). One API response is written as
  several lines sharing `message.id`, so every id counts once across all
  files, with usage/model/timestamp from the line with the largest
  `output_tokens` (ties: earliest timestamp, then scan order).
  `model: "<synthetic>"` (Claude Code's local error placeholder) is not a
  response. A user prompt is a `user` entry whose content is a string or
  has at least one non-`tool_result` block, not `isMeta`, once per `uuid`
  (resumed transcripts copy entries). Messages = prompts + distinct
  replies. Tokens are the four usage fields; the 1 h share of cache writes
  only feeds the cost. A line that does not parse is skipped, an
  unterminated last line is left for the next scan. The prefilter looks
  for the bare type values (`"assistant"`, `"user"`, `"session-owner"`),
  not `"type":"…"`: a writer that puts a space after the colon must not be
  silently uncounted (found by the smoke's own generator).
- **D61: incremental scan, aggregation split from scanning.** The cache
  (`<cwm home>/cache/stats.json`, 0600, atomic, versioned) keeps per file
  the size, mtime, parsed offset, a SHA-256 of the first 4 KiB, the resume
  state (last session-owner) and the file's events, varint-packed. Same
  size+mtime: reuse. Grown with the same head: resume at the offset.
  Anything else: reparse. Gone: dropped. Events sit in 15-minute UTC
  buckets (every current zone offset is a multiple of 15 minutes), so a
  range or time zone change is a re-aggregation of the in-memory index,
  never a rescan. A corrupt or foreign-version cache is a full rescan,
  never an error. Measured on this box (1000 sessions × ~200 lines, 32 MB):
  full scan ~0.6-0.7 s, rescan of an unchanged workspace with the cache
  loaded from disk ~120-160 ms, in memory ~40 ms.
- **D62: attribution only from what Claude Code recorded.** Each
  transcript carries `{"type":"session-owner","accountUuid":…}`; an entry
  belongs to the closest preceding owner line. The uuid maps to a cwm
  account through `[account.login] account_uuid`, else the account dir's
  `.claude.json` `oauthAccount.accountUuid` (`accounts.UUIDMap`). The
  identity reader now also takes Claude Code's camelCase keys
  (`accountUuid`, `emailAddress`, `organizationName`): before, real files
  never yielded a uuid. Anything else is `unattributed`: no guessing from
  the default account or the device. Raw uuids never reach the API, the
  CLI or the debug bundle.
- **D63: the cost is an estimate and says so everywhere.** One static
  table (`internal/stats/prices.go`, `PricesUpdated`) of public list
  prices per MTok: input, output, 5 min and 1 h cache write, cache read.
  Model ids match the longest key that equals them or is followed by a
  `-<date>` suffix. Unknown models cost $0 and are listed as `unpriced`.
  The label is always "est., list prices — your plan may be flat-rate".
  Rejected: fetching prices (network, D48) and per-plan modelling
  (subscriptions are flat-rate; the number is only an API-equivalent).
- **D64: one engine per agent, never a second loop.** `stats.Engine`
  keeps the index in memory; a scan runs in the background, single-flight,
  at most every 60 s. A request aggregates the current index (≈ms) and,
  when it is older than 60 s, kicks a refresh behind it; only the very
  first request of a process waits for the first scan. The `/api/events`
  snapshot gains a `stats` fingerprint (all-time messages/tokens/last
  activity), read from the cached index, so a change rides the existing
  5 s diff as `changed: ["stats"]`. `cwm stats --json` prints the API body
  with the same encoder (only the three scan fields differ). `time/tzdata`
  is embedded: Windows has no IANA database outside a Go install, and
  `?tz=` / `--tz` must work there (+~450 KB). The local zone is reported
  by its IANA name when `$TZ` or `/etc/localtime` tell it.
- **D65: the Stats tab.** Seventh tab, key `7`. KPI cards, a 53-week
  heatmap (weeks as columns, Monday first unless `Intl.Locale` week info
  says Sunday, levels from the quartiles of the non-zero days, every cell
  a `<button>` with an aria-label, one in the tab order, arrows move by
  day/week, Home/End to the ends), top projects/models/accounts as bars,
  the token-kind split as one stacked bar, a 30-day trend in SVG built with
  `createElementNS`. The browser's `Intl` zone goes in `?tz=`; a zone the
  agent rejects falls back to the agent's own. Range and metric are
  remembered in localStorage. Assets 136 KB of the 150 KB budget.
- **D66: polish carried over from the v0.6/v0.7 reviews.** A hand-tuned
  16 px favicon (pixel-grid `>` and cursor, no hexagon; the hex mark keeps
  32 px and up). Tab shortcut digits read as counts, so they became small
  key caps in the tab's corner, shown on hover, keyboard focus and while
  the `?` help is open. Surfaces are near-opaque (.96/.97) and a veil dims
  the canvas under the content column. Installer notes wrap at the
  terminal width inside the gutter. `cwm account remove --delete-config`
  (TTY prompt otherwise; only dirs inside the cwm home are ever deleted).
  `cwm mcp add/remove` round-trip unknown fields and gain `--env`, `--url`,
  `--type`, `--header`. Doctor no longer warns about a missing repos.tsv
  in a workspace without projects.

## 2026-11-19 (v0.6.2 — accounts tab, verified claude, quiet fresh installs, SPEC-V0.6.2)

From a real Windows 11 test of v0.6.1: Claude Code lived in
`%LOCALAPPDATA%\AnthropicClaude\claude.exe` and the resolver found it, but
the account added from the dashboard was invisible ("0 with a recorded
login" in the bundle), doctor showed five warnings, most of them legacy-kit
noise, `/api/devices` logged a 503 for a Syncthing that was simply not set
up, and the install.ps1 banner was slightly mangled under PowerShell 5.1.

- **D54: a claude counts because of what it does, never because of where
  it is.** Every candidate is probed: `<file> --version`, 5 s timeout,
  stdin from the null device, at most 4 KiB of output kept. It verifies when
  it exits 0 and a line carries a semantic version (any pre-release or
  build suffix) and the word "claude" (any case). The real output is one
  line, `2.1.319 (Claude Code)`; the rule keys on that shape, not on the
  suffix. The lookup order stays (D49), but every `claude` on PATH is now a
  candidate in PATH order, not only the first, and nothing is accepted or
  rejected for its folder: `%LOCALAPPDATA%\AnthropicClaude` is as good as
  any other place if the probe passes. A saved `claude_path` that stops
  verifying is cleared from config.toml, also by the read-only lookup
  (keeping a known-wrong pin helps nobody), and one line goes to
  `state/claude-cleared.txt` so doctor and the bundle can say what
  happened. `$CWM_CLAUDE` pointing at something that is not Claude Code
  is an error, as before. Verdicts are cached per (path, size, mtime) for
  the life of the process, so the agent's 5 s poll does not spawn claude
  each time. Reports (`/api/claude`, doctor, the bundle) run a survey that
  keeps probing past the winner, up to 5 candidates. Rejected alternative:
  a list of "trusted" install folders. That is the bug class itself: a
  folder name says nothing about the file in it.
- **D55: login state is reported by the process that runs the login.**
  The dashboard cannot see inside the terminal it opens (the emulator's
  exit status means nothing, and wt.exe or gnome-terminal-server even
  hand the command to another process). So `cwm account add|login` writes
  `state/account-login/<name>.json` itself: `in_progress` with its pid,
  then `done` after the config is saved, or `failed` with the reason. The
  agent writes `in_progress` (no pid) right before it opens the window and
  derives the rest: a dead pid means the window was closed, no pid after
  90 s means the terminal never ran the command, 15 minutes is a timeout,
  and a recorded login with fresh credentials means done. It persists
  those transitions and keeps the last state in memory. The event stream's
  snapshot carries `name=status`, so open dashboards hear about it. The
  "recorded login" signal stays the CLI's existing one (an e-mail in
  `[account.login]`).
- **D56: Accounts API = the CLI's semantics.** `/api/accounts/add` creates
  the account skeleton first (private dir + config entry, status
  `never`), so the account is visible and retryable at once, then opens
  `cwm account login <name>` in a terminal (the D27 launcher, with the
  D53 hold flag). A missing claude refuses before anything is created
  (424). No terminal means 501 with the exact command to paste, as for
  every launch. Remove is the same `accounts.RemoveWith` as `cwm account
  remove [--delete-dir]`: backup first (a failed backup aborts), refuse the
  last account in both, and make the first remaining account the default
  when the default goes. Before, removing the default left none, and every
  launch then failed with "no default account". `--delete-dir` only deletes
  dirs under `~/.cwm/accounts` (an imported dir elsewhere is left alone).
  New names must also not be a cwm command or a `cwm account` action. The
  new fields are snake_case, like `/api/claude`. `GET /api/accounts` keeps
  its v0.5 fields, and the wizard's `POST /api/accounts` is unchanged.
- **D57: the Accounts tab, pulled forward from v0.8.** It is the sixth tab
  (`6`), so the shortcuts users know keep their keys. Accounts are cards,
  not a table: each account has several actions, an inline error and a
  retry, and cards stack naturally on a phone. A status pill per account;
  a banner at the top when the default account has no recorded login
  ("Add your first account", or log in that account); an attention dot on
  the tab label. While a login runs, the tab polls every 2 s on top of
  the live updates. Remove confirms in a native `<dialog>` that says a
  backup is always made. Same rules as v0.7: DOM nodes only, no external
  requests, same CSP, assets 112 KB of the 150 KB budget.
- **D58: a fresh install shows no noise.** The Go kit never reads
  `workspace/settings.json` or `workspace/hooks/`; it writes settings.json
  into each launch dir when it assembles it. So their absence is ok, and
  they are only checked when they exist (settings.json) or when an
  existing settings.json wires bash-kit `hooks/*.sh` scripts (hooks).
  The CLAUDE_CONFIG_DIR shell-profile check is unix-only. Duplicate PATH
  entries get an informational ok line with the count (the bundle marks
  them), because cwm never rewrites PATH: that is the user's, and Windows
  keeps it in the registry where an edit applies to every program.
  `GET /api/devices` answers 200 `{"status":"not_configured"}` when there
  is no Syncthing client to talk to, and the Devices tab shows a setup
  card. A state the user has not chosen yet is not an error, so it does
  not belong in the debug bundle's "recent API errors". A reachable
  Syncthing whose API call fails is still 502.
- **D59: the banner is data, and CI compares it byte for byte.** The art
  lives in `scripts/banner.txt`. install.ps1 builds the two characters
  PowerShell can read as syntax (backtick, apostrophe) from code points,
  so no banner string contains either. We could not reproduce the 5.1
  mangling on Linux, so the fix removes the whole class: no quoting
  context can eat a character that is not in the source. The Windows
  harness (real 5.1) compares every banner line with banner.txt under
  `-File` (plain and colour layout) and under `irm | iex` from the local
  server, which is the tester's exact command. The Linux harness compares
  the sh banner and the pwsh 7 banner with the file, and with each other.
  Test packages now strip PATH entries that contain a real `claude`
  (`internal/testsafe`). With D54 a failing stub would otherwise let the
  resolver walk on to the developer's real claude, which happened once
  during development.

## 2026-11-19 (v0.6.1 — Windows field fixes + debug bundle, SPEC-V0.6.1)

From the first real Windows 11 + PowerShell 5.1 test of v0.6.0: the
installer's cwm lines were mojibake (`Γ£ô` for `✓`), and the dashboard's
Add account failed with "claude not found on PATH" although Claude Code was
installed.

- **D49: find claude where it is, then remember it.** PATH is the wrong
  source of truth on Windows. The Claude Code installer edits the *user* Path,
  but every process that was already running keeps the old one, and that
  includes the cwm agent and every terminal it opens. One resolver
  (`internal/claudebin`) now serves every caller: `$CWM_CLAUDE` →
  `config.toml` `claude_path` → PATH → well-known install folders per OS →
  `npm prefix -g` (3 s cap). An off-PATH hit is used at once, written back
  to `claude_path` (only into an existing config.toml, so the installer's
  "initialised?" test stays honest), and reported with the exact copy-paste
  command that puts its folder on PATH. The v0.4 PATH-hash cache
  (`state/claude-path`) is dropped: `claude_path` is the persistent answer,
  and a PATH lookup costs nothing. The dashboard checks before it opens a
  login terminal (424 + install command) instead of opening one that dies
  before the user can read it, and the wizard shows the claude status up
  front. `CWM_CLAUDE_NO_SEARCH=1` limits the lookup to PATH and the explicit
  settings; tests set it so they can never reach a real claude.
- **D50: ASCII glyphs where the console can't show them; never switch the
  console's code page.** The mojibake came from PowerShell decoding cwm's
  piped UTF-8 with the OEM code page. When stdout is the console itself, Go
  already writes UTF-16 (WriteConsoleW), but the legacy conhost font has no
  ✓/⚠/✗. So on Windows each output handle keeps Unicode only when
  `GetConsoleOutputCP() == 65001`, or when it is a console inside Windows
  Terminal. Otherwise an ASCII writer maps cwm's own glyphs (`✓→ok`,
  `✗→[x]`, `⚠→!`, `—→-`, …) and leaves all other bytes alone, so data such
  as non-ASCII user names is never rewritten. `CWM_GLYPHS=ascii|unicode`
  overrides. Rejected: `SetConsoleOutputCP(65001)` at startup. The code page
  belongs to the console, not the process, so it would stay switched in the
  user's shell after cwm exits, and it cannot fix a pipe whose reader has
  already chosen its decoder. kernel32 is bound with `syscall.NewLazyDLL`
  (no `golang.org/x/sys`).
- **D51: installers show steps, not logs.** Both installers share one layout:
  a small ASCII banner, `[n/8]` steps with ok/warn/fail glyphs, child
  output indented under its step, and a closing summary box (binary,
  version, dashboard URL, next steps, uninstall). Colours honour `NO_COLOR`.
  Non-terminal output is plain ASCII, so logs and CI stay greppable.
  install.ps1 switches `[Console]::OutputEncoding` to UTF-8 for its own
  run only (restored on every path), keeps its source pure ASCII
  (`irm | iex` decodes with whatever charset the server claims), and on
  failure does not `exit` under `iex`, because that would close the user's
  window. CI now runs install.ps1 end to end under Windows PowerShell 5.1
  (`scripts/test-install-windows.ps1`), the tester's exact shell.
- **D52: one redacted debug bundle, built in one place, redacted as a
  whole.** Bug reports so far meant asking for `cwm version`, `doctor
  --json`, a log or two and "which claude?", then hand-redacting each.
  `internal/debugbundle` builds a single plain-text report (versions,
  agent, service status, the claude lookup and the PATH it ran with,
  config keys, workspace and account names with counts, syncthing counts,
  doctor, about 200 lines of `~/.cwm/logs`, the agent's last 100 API errors
  from an in-memory ring) for both `GET /api/debug` and `cwm debug
  [--copy]`. The redactor runs over the finished text, not per section, so
  a section that forgets to be careful still cannot leak. Its rules are
  deliberately greedy (any secret-named key, any 64+ hex run, every e-mail,
  PEM private keys, the home dir and the login name inside paths): a false
  positive costs one unreadable value, a false negative publishes a secret
  in a public issue. Some things are never read in the first place: account
  dirs, `.credentials.json`, `server.toml` (it holds the dashboard token);
  config values under secret-looking keys print as `<set, not shown>`.
  `cwm debug` borrows the agent's error list and its claude view over the
  local API, because "works in my shell, fails in the dashboard" is
  exactly a PATH difference between the two processes (D49). Copying tries
  the Clipboard API, then `execCommand`, then shows the text, already
  selected, in a modal; the CLI tries the OS clipboard tools and falls back
  to stdout. The dashboard gets one external *link* (the bug-report form,
  opened by the user's click); it makes no external request, and the asset
  test allows exactly that URL. The terminal launcher now keeps the
  emulator's own output in `logs/terminal.log`, so a window that never
  opened leaves a trace in the bundle.
- **D53: the hold-open request travels in argv; Resume prechecks claude
  too.** v0.6.0's failing window closed even though cwm sets
  `CWM_HOLD_ON_ERROR=1` for it: terminals that hand the command to an
  already-running instance (wt.exe, gnome-terminal-server) start it with
  that instance's environment. The dashboard now launches `cwm
  --hold-on-error <args…>` (the command shown for copying stays without
  it), and the env var keeps working for older callers. Resume gets the
  same claude precheck as Add account (424 + install command, no window),
  and a launch whose claude was found off PATH returns the PATH command, so
  the dashboard can show it with a Copy button instead of leaving the user
  to discover it later.

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
- **D11 — workspace-first account architecture.** *(Superseded by D76 in
  v0.8: one workspace, accounts are logins only, no launch dirs.)* The workspace (sessions,
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
