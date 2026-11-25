# cwm v0.8 — "one workspace" (OWNER-APPROVED SPEC)

This file is the owner-approved spec for v0.8, copied verbatim below.
Decisions D76 and later in `docs/PLAN.md` record how cwm implements it;
where an implementation note and the spec disagree, the spec wins.
D11 (per-account launch dirs) is superseded by D76.

This spec reverses the per-account launch-dir architecture (D11). The principle, in the owner's words: everything is local, kept out of the Claude cloud, synced across the user's devices, and independent of any account. Switching accounts must never risk data.

## OWNER DECISIONS (binding)
1. **Capture is the DEFAULT mode.**
   - cwm captures everything Claude Code does on the machine, whether or not cwm launched it: plain `claude` in any terminal, the VS Code extension, JetBrains, the desktop app.
   - Mechanism: the synced workspace IS the standard Claude config dir, `~/.claude` (Windows: `%USERPROFILE%\.claude`). It is synced directly by Syncthing.
   - Capture mode never sets a global `CLAUDE_CONFIG_DIR`.
2. **Isolated mode is opt-in, chosen by the user.**
   - The workspace is ONE folder, `~/.cwm/workspace`, kept separate from `~/.claude`.
   - Only cwm-launched sessions use it, via `CLAUDE_CONFIG_DIR`.
   - `~/.claude` is left untouched.
3. **Accounts are logins only.** There are no per-account data folders. Sessions, history, memory, skills, agents, commands, rules, output styles, scheduled tasks, plugins, MCP servers and settings are shared by every account.
4. **Parallel accounts are supported in BOTH modes.**
   - The default account uses the mode's workspace root directly.
   - Any other account running at the same time gets a login-only dir, `~/.cwm/accounts/<name>/`, which holds only:
     - its credentials;
     - its own `.claude.json` (whose `mcpServers` cwm keeps in sync with the workspace);
     - links (dirs: symlink or Windows junction; files: see the Windows note) to every shared workspace item.
   - The login-only dir stores NO data of its own. The launch command is `cwm run --account <name> [-- claude args]`; the dashboard and `cwm open`/resume pass `--account` through.
5. **`settings.json` syncs by default**, with a switch to keep it machine-local.
6. **Settings tab**, with every switch also in the CLI.
7. **Migration from v0.7.x:**
   - backup first;
   - a dry-run preview;
   - merge into the target;
   - one-command rollback;
   - NOTHING is deleted, only moved into the backup.

## Synced item allow-list
Each item has an on/off switch, `sync.<key>`. Everything NOT on this list stays machine-local, including anything a future Claude version adds.

| key | path(s) in the workspace root | default |
|---|---|---|
| sessions | `projects/` (also `repos.tsv` and `session-repos/`, D85/D92) | on |
| history | `history.jsonl` | on |
| memory | `CLAUDE.md` | on |
| skills | `skills/` | on |
| agents | `agents/` | on |
| commands | `commands/` | on |
| rules | `rules/` | on |
| output_styles | `output-styles/` | on |
| scheduled_tasks | `scheduled-tasks/` | on |
| plugins | `plugins/` (but NOT `plugins/cache` or any other cache) | on |
| settings | `settings.json` | on |
| mcp | `mcp.json` (cwm-managed; bridged into `.claude.json`, see below) | on |

- **Never synced, not switchable:**
  - `.credentials.json` and `.claude.json`;
  - cache, debug, telemetry, statsig, shell-snapshots, session-env, paste-cache, file-history, backups, downloads, ide, chrome, todos, tasks, sessions, worktrees, logs;
  - `*.lock`.
- **The `.stignore` is ALLOW-LIST form, regenerated from the switches.** Allowed entries are `!/projects`, `!/history.jsonl`, … and the last line is `*`.
- **A switch turned off** means: add it to the ignore list. Never delete files.

## Accounts = credential vault
- **Vault layout:** `~/.cwm/accounts/<name>/` (local, never synced) holds:
  - `.credentials.json` (Linux/Windows);
  - the account's identity block from `.claude.json` (the `oauthAccount` key, plus any other key that is account-specific; VERIFY this from a real `.claude.json`'s key names, and never copy the token values into logs or tests);
  - login status metadata (keep the v0.6.2 login-state module).
- **Switching the default account (`cwm account use <name>` / dashboard):**
  1. Save the CURRENT live credentials and identity block back into the current default's vault entry. Claude refreshes tokens in place, so the live copy is the freshest.
  2. Write the new account's credentials and identity block into the live location: `<root>/.credentials.json`, and the identity keys inside the live `.claude.json`. Merge; never overwrite the rest of `.claude.json`.
  3. If a claude process is running, warn: "open sessions keep using <old> until restarted". Detect it best-effort, per OS.
  4. Write atomically. Keep a timestamped backup of the replaced files under `~/.cwm/backups/`.
- **macOS:** Claude Code stores credentials in the Keychain, not in `.credentials.json`.
  - Implement swap via the `security` CLI behind an interface, unit-tested with a fake.
  - Find the service name by researching current Claude Code behaviour (it may differ when `CLAUDE_CONFIG_DIR` is set). Document the research and the uncertainty in the spec.
  - Mark this "untested on hardware" in the docs.
- **`account add <name>`:** runs `claude auth login` (or whatever the existing flow does) with `CLAUDE_CONFIG_DIR` pointed at the new login-only dir, then copies the resulting credentials into the vault. Adding an account must NEVER log the default account out.
- **`account remove`:** removes only that vault entry and its login-only dir (with a backup). It never touches shared data.

## Live `.claude.json` location (VERIFY empirically or from current docs; do not guess)
- **`CLAUDE_CONFIG_DIR` unset:** Claude Code uses `~/.claude.json` in HOME (not inside `~/.claude`) and `~/.claude/.credentials.json`.
- **`CLAUDE_CONFIG_DIR` set:** both files live inside that dir.
- Code paths must resolve these through ONE function: `claudepaths.Live(mode, account)`.

## MCP bridge
- **`workspace/mcp.json` is the synced source of truth.**
- **Workspace → `.claude.json`:**
  - The agent and every cwm command that launches claude merge its `mcpServers` into each live `.claude.json`: the default one plus every login-only dir.
  - It is a 3-way merge, using a stored last-synced snapshot so deletions propagate.
  - It never touches other keys.
- **`.claude.json` → workspace:** servers the user adds with `claude mcp add --scope user` are picked up on the next agent tick or cwm launch, and written back to `mcp.json`.
- **Conflicts:** the workspace wins, and a log line is written.
- It is unit-tested with fixture `.claude.json` files.

## settings.json and hooks
- The synced `settings.json` contains the user's settings plus cwm's hook entries. Hook commands use the bare `cwm hook session-start|session-end`, so they work on every OS when cwm is on PATH.
- Merge the hooks idempotently: add or update only cwm's own entries, identified by the command string, and preserve every other key and hook.
- **`features.hooks=off`:** remove cwm's entries.
- **`sync.settings=off`:** `settings.json` is ignored, so each machine keeps its own copy. cwm still merges its hooks locally.

## Conflicts
- **`history.jsonl`:** the agent watches for Syncthing conflict copies (`history.sync-conflict-*.jsonl`). It merges their lines into `history.jsonl` (dedupe by exact line, order by the timestamp field when present) and moves each merged conflict file into `~/.cwm/backups/conflicts/`.
- **`CLAUDE.md`** and other text files: a Health warning, "2 versions of CLAUDE.md — pick one", with a dashboard action to choose a version or keep both merged. The losing version goes to backups.
- **Session files are unique per session**, so no merge is needed.

## Modes
- `config.toml` gains:
  - `mode = "capture" | "isolated"`;
  - a `[sync]` table (the keys above, bool);
  - a `[features]` table:
    - `hooks`
    - `handoff_push`
    - `stats`
    - `autostart`
    - `open_dashboard_at_login`
    - `private_sync`
    - `update_check = "manual" | "off"`
  - a `[display]` table (hidden_tabs list, default_tab).
- **Every flag must actually be honoured:**
  - handoff_push off → the session-end hook does not push;
  - stats off → no scan, the tab is hidden, and the API returns 404 or a disabled state;
  - autostart → service install/uninstall;
  - private_sync → Syncthing `globalAnnounceEnabled=false`, `relaysEnabled=false`, `localAnnounceEnabled=true`, and devices may carry static addresses (`cwm pair <id> --address tcp://host:22000`);
  - update_check off → the button is hidden.
- **Workspace root:** capture = `~/.claude`; isolated = `~/.cwm/workspace`. Everything (stats, sessions list, handoff, Syncthing folder path, MCP bridge, health) resolves the root through one function.
- **Switching mode** in Settings or with `cwm mode set capture|isolated`:
  - runs a migration preview first, then applies it;
  - moves or merges the synced items between the roots;
  - re-points the Syncthing folder path (the folder ID stays `cwm-workspace`).

## Migration (`cwm migrate [--dry-run] [--to capture|isolated]`, `cwm migrate rollback [<id>]`)
- **Sources:**
  - a v0.7.x layout (`~/.cwm/workspace` + `~/.cwm/launch/<acct>/` + `~/.cwm/accounts/<acct>/`);
  - an existing `~/.claude` with its `~/.claude.json`;
  - a fresh machine.
- **Steps:**
  1. **Back up.** Copy `~/.claude`, `~/.claude.json` and `~/.cwm` (excluding backups) to `~/.cwm/backups/migrate-<timestamp>/`, with a manifest of every path and its sha256.
  2. **Plan.** List every file to move, merge or skip, and every conflict.
  3. **Apply:**
     - session files are copied when absent, and on the same name with a different hash, kept as `<name>.conflict-<host>.jsonl` and reported;
     - `history.jsonl` is merged by line;
     - `CLAUDE.md` / `settings.json` follow a merge rule (settings: JSON deep-merge, target wins on scalar conflicts, hooks union);
     - credentials go into the vault;
     - old launch dirs are moved into the backup.
  4. **Verify.** Session count before and after (every session from both sources is present), and a Syncthing folder path check.
  5. **Record.** Write `~/.cwm/state/migrations.json`.
- **Rollback** restores from the backup manifest exactly, and is tested.
- **v0.8.3 (D101):** runtime files (`*.lock`, `state/agent.lock`, `server.toml`, `cache/`, claude's `session-env` / `shell-snapshots`) are neither backed up nor rolled back; a file another process keeps locked is `skipped: locked` unless the plan needs it; a running agent pauses its maintenance through `POST /api/agent/pause` for the run; incomplete backups are never rollback targets and are cleaned up (`cwm migrate clean`).
- **The agent shows a dashboard banner** when an un-migrated v0.7.x layout is detected: "cwm now captures all Claude Code activity — review migration". It never migrates silently.
- **Fresh install with an existing `~/.claude`:** capture mode adopts it in place. Nothing is moved, the `.stignore` is written, and the default account is imported from the live credentials as `default` (or as a name the user gives).

## Health checks
- **Replace** the "CLAUDE_CONFIG_DIR not exported in ~/.bashrc" check.
- **New "claude reads from" check:**
  - Capture mode: FAIL if a persistent `CLAUDE_CONFIG_DIR` is set for the user, in shell profiles (`~/.bashrc`, `~/.profile`, `~/.zshrc`, `~/.zprofile`), the Windows HKCU Environment, or launchd. Fix: show the line to remove and a command.
  - Isolated mode: OK.
  - Also report the resolved live root, `.claude.json` and credentials paths, all with `~`.
- **Other new checks:**
  - workspace root exists;
  - `.stignore` matches the switches;
  - MCP bridge in sync;
  - conflict files present;
  - vault holds credentials for the default account;
  - unknown new top-level items in `~/.claude` (INFO: "Claude added X; it stays local").

## Dashboard
- **New tab `8:settings`.** The mobile "more" sheet gains it; Ctrl-K gets entries for it.
  - **mode:** capture/isolated with an explanation. Changing it opens the migration preview modal, and applying shows progress through the terminal-log component.
  - **sync items:** one switch per allow-list key, each with a one-line consequence.
  - **features:** each switch with a one-line consequence. Autostart and private sync show what they will change before applying.
  - **display:** hidden tabs and default tab.
  - Every change goes through PUT /api/settings (CSRF), shows a "saved" toast and is reflected live over SSE.
- **Accounts tab:**
  - Cards show login status, a default tag, and "running now" when known.
  - Actions:
    - make default (with the running-claude warning)
    - run in parallel (opens a terminal with `cwm run --account <name>`)
    - log in again
    - remove
  - No per-account data wording anywhere.
- **Health:** the new checks, and the conflict resolution action.
- **Keep:** the v0.7.2 design system and tokens, the 200 KB raw / 60 KB gzip budget, and the CSP, contrast and accessibility rules.

## Windows notes
- Directory links in login-only dirs are junctions; no admin rights are needed.
- **File links** (`history.jsonl`, `CLAUDE.md`, `settings.json`, `mcp.json`): try a symlink first. If it is not permitted, use a HARDLINK, because a copy would split the data.
- Atomic-rename writes by Claude can break a hardlink, so every `cwm run` and every agent tick re-verifies the links. When one is broken, the diverged file is merged back (history by line; others: newest wins, loser to backups) and re-linked.
- Document this as a known limitation.

## Cross-device resume (owner-approved addition, P6b)

**Problem:** Claude Code files sessions under `projects/<path-derived name>/`, and in capture mode the project-name pin is ignored (D86). A session made in `C:\Users\me\projects\api` therefore cannot be resumed by Claude in `/home/me/projects/api`.

**Fix: path mirroring.** cwm keeps a local copy of each session under the folder name THIS machine's path for the same repo would produce.

- **Repo identity:** the normalised git remote URL (strip `.git`, `ssh://`/`git@host:` → `host/path`, lowercase host). Sources:
  - the session's `cwd` field mapped through repos.tsv;
  - `git -C <cwd> remote get-url origin` at hook time.
  - Record `remote → local path` per device in a machine-local map, never synced.
  - The SessionStart/SessionEnd hooks already record repos; extend them to record each session's id → remote.
- **Folder name:** compute the local project folder name exactly as Claude Code does. Verify the algorithm from the current binary or docs, and record the research in the research notes. Known shape: the absolute path with every non-alphanumeric character replaced by `-`.
  - Long-path truncation and hashing: verify them.
  - Windows drive-letter case: verify it.
- **Materialise** (on `cwm sessions resume <id>`, dashboard resume and the agent tick):
  1. Find every copy of `<id>.jsonl` across `projects/*/`, plus its sidecar dir `projects/*/<id>/` if present (subagents, tool results).
  2. Pick the source with the prefix rule: if copy X's lines are a prefix of copy Y's, Y is newer.
     - Ignore the `cwd` rewrite when comparing; compare by a per-line key such as `uuid`, falling back to the line hash after removing `cwd`.
     - If neither is a prefix of the other, the copies DIVERGED: keep both, never overwrite, raise a Health warning "session <short> was continued on two devices", and resume the copy whose last entry is newest.
  3. Write or extend the local copy at `projects/<localname>/<id>.jsonl`, rewriting every `cwd` value equal to (or under) the source repo path to the local repo path.
     - Copy the sidecar files the same way.
     - Write atomically.
     - Never modify the source copy.
  4. Launch claude with cwd = the local repo path (clone on demand, as `cwm open` does) and `--resume <id>`.
- **Agent tick mirroring:** for every session whose repo exists locally at a different path, keep the local copy extended per the prefix rule. Plain `claude --resume`, the VS Code history and the desktop app on this machine then see sessions from other devices without going through cwm.
  - Rate-limit it.
  - Use size+mtime as a cheap change check before reading files.
  - Never ping-pong: comparisons are content-based, not mtime-based.
- **Dedupe:**
  - The sessions list and API show each session id once, with `devices`/`copies` info.
  - Stats already dedupe by message.id; add a test proving that mirrored copies do not double-count.
- **Switch:** `features.path_mirror` (default on in capture mode, n/a in isolated mode, where pinning works).

### As implemented (P6b, D91/D92)
- **Records.** The SessionStart hook reads Claude Code's stdin payload (`session_id`, `cwd`; bounded read, 2 s wait) and writes:
  - `session-repos/<host>.tsv` in the workspace root (`<id>\t<normalised remote>`): synced with `sync.sessions`, one file per device so Syncthing never sees two writers;
  - `~/.cwm/state/repo-paths.json` (`remote -> clone top-level`): machine-local, never synced.
- **Local clone lookup** for a session (`pathmirror.PlanResume`), first hit wins: the recorded remote -> path; `<repos root>/<name>` when its origin is the same repo; the source path itself when it exists here with the same origin; else a clone target `<repos root>/<name>` from `repos.tsv`. `<name>` is the base name of the session's first `cwd`.
- **`cwm sessions resume <id|prefix> [--dry-run] [--json]`** plans, clones on demand, materialises, records the path, then starts claude in the clone with `--resume <id>` and no project-name pin. In isolated mode, a pre-v0.8 config, or with `features.path_mirror` off, it is the D20 passthrough (`cwm open <project> new --resume <id>`).
- **Dashboard resume** in capture mode accepts the path-derived folder as the project and launches `cwm sessions resume <id>`; the folder never reaches the argv.
- **`cwm sessions mirror [--json]`** runs one mirroring pass now; the agent runs the same pass on its tick (at most every 30 s; only sessions whose copies changed size or mtime are read).
- **Health:** `diverged-sessions` warns with the short ids; it shows OK once mirrored copies exist, and is absent on a single-device workspace. In capture mode the dash-prefixed `session-folders` warning is downgraded to OK, because these folders are expected there.
- **Sessions list/API:** one row per id, the copy with the most messages (then the newest); additive fields `copies` (int) and `devices` (hosts from `session-repos/`).

### As implemented (P7, D93/D94)
- **`cwm doctor --fix`** runs one agent maintenance pass (the generated `.stignore`, the history merge, login-only dir links, the MCP bridge, cwm's hooks, path mirroring), saves the live login into an empty vault entry of the default account, then reports. It never touches a pre-v0.8 config. Every Health fix that cwm can do itself names this command.
- **Health check ids** (the `--json` / dashboard contract, additive): `claude-reads-from` (replaces `config-dir`), `migration`, `stignore`, `conflicts`, `mcp-bridge`, `cwm-hooks`, `vault`, `unknown-items`. The persistent-variable scan covers `~/.bash_profile` and `~/.zshenv` as well as the four profiles named above.
- **Features:** `stats` off returns `{"disabled": true}` from `/api/stats` and `cwm stats`; `update_check = "off"` sets `update.disabled` in `/api/about` and makes the check endpoint answer 403; `autostart` / `open_dashboard_at_login` install or remove the per-user service, and `cwm service install|uninstall` write the result back into `[features]`.

### As implemented (P8, D95)
- **API (additive):** `GET /api/settings` returns the mode, the live paths (`.claude.json`, credentials, `CLAUDE_CONFIG_DIR`), `needs_migration`, a config hash and one row per `cwm config` key (group, kind, value, choices, one-line help, `na` for keys that do not apply in the current mode). `PUT /api/settings {key, value, dry_run}` (CSRF) saves through the same code path as `cwm config set`; `dry_run` returns the notes of what would change (used for `features.autostart`, `features.open_dashboard_at_login` and `features.private_sync`, which confirm first). `mode` is refused with 409 and goes through `POST /api/migrate/preview {to}` → `POST /api/migrate/apply {to}` (202, background job) → `GET /api/migrate` (running, progress lines with `~` paths, the record with the rollback id). The SSE stream gains a `settings` topic (a config.toml change from the CLI or another tab updates the page). `/api/accounts` rows gain `running_now` (known on Linux only, from the process environment) and `parallel`; `POST /api/accounts/run {name}` opens a terminal with `cwm run --account <name>`.
- **UI:** `8:settings` (mode card with the migration modal, sync items, features, display), a confirm modal for the previewed keys, the migration banner on every tab while the config predates v0.8, hidden tabs and the default tab honoured by keys 1–8, Ctrl-K and the mobile "more" sheet. Account cards: "running now", run in parallel, make default (confirms with the running-claude warning), log in again, remove (the login-only dir always goes, after the backup). Health: the conflicts card resolves a conflict by picking a side (the loser goes into the backups).
- **Budget:** the feature needed ~18 KB; the existing CSS/JS were compacted (tabs for indentation, comment trimming) to stay under 200 KB raw.

### As implemented (P9, D96)
- **First account on a machine without a Claude login:** its login also becomes the live one, so plain `claude` is logged in. A later `cwm account add` never replaces the live login; `cwm account login <default>` does (after a backup). The `vault` health check and `cwm doctor --fix` cover a live location that is empty while the vault holds the default's login.
- **Docs:** README, and the wiki pages Getting-Started, Accounts, Device-Sync, Capture-vs-Isolated (new), Migration (new), Troubleshooting, Dashboard-Tour, plus every other page that still described launch dirs.

## Out of scope for v0.8
- The in-dashboard terminal (moves to v0.9).
- A bundled Syncthing.
- The encrypted relay peer.

## Research notes

Researched 2026-11-24 for P2 against Claude Code 2.1.319: the current docs
(`code.claude.com/docs/en/settings`, `iam`, `claude-directory`, `env-vars`,
`troubleshoot-install`), `claude --version` / `--help` only (never logged
in), a static read of the installed binary's bundled JavaScript (strings,
never executed), and the KEY NAMES of a copy of a real `.claude.json`
(values never printed; the copy was deleted at once).

**Locations (confirmed by the docs).**

| | `CLAUDE_CONFIG_DIR` unset | `CLAUDE_CONFIG_DIR=<dir>` |
|---|---|---|
| config dir | `~/.claude` (Windows `%USERPROFILE%\.claude`) | `<dir>` |
| `.claude.json` | `~/.claude.json` (in HOME, not inside `~/.claude`) | `<dir>/.claude.json` |
| credentials, Linux / Windows | `~/.claude/.credentials.json` (0600) | `<dir>/.credentials.json` |
| credentials, macOS | login Keychain; falls back to `~/.claude/.credentials.json` when the Keychain rejects the write (locked, SSH) | Keychain entry keyed to `<dir>`; fallback file `<dir>/.credentials.json` |

`claudepaths.Live` implements exactly this table.

**macOS Keychain service name (from the binary, not the docs).** A generic
password with account `$USER` (or `claude-code-user` when the name has
characters outside `[a-zA-Z0-9._-]`) and service
`Claude Code-credentials` when `CLAUDE_CONFIG_DIR` is unset, or
`Claude Code-credentials-<h>` when it is set, where `<h>` is the first 8
hex digits of sha256 of the config dir (Unicode NFC). Claude Code reads
it with `security find-generic-password -a <user> -w -s <service>` and
writes it with `security add-generic-password -U -a <user> -s <service>`.
An internal `CLAUDE_SECURESTORAGE_CONFIG_DIR` override exists and an
`OAUTH_FILE_SUFFIX` build constant is spliced in after `Claude Code`
(empty in release builds as far as the code shows). Uncertain: whether
the hashed dir string is normalised further (trailing separator, symlink
resolution); this is read from minified code of one version and may
change in any release. cwm therefore (a) computes the name in one place,
(b) prefers the `.credentials.json` fallback file when one exists, and
(c) is marked "untested on hardware" in the docs.

**Account-specific `.claude.json` keys.** Seen top-level keys (names
only): `oauthAccount` (`accountUuid`, `emailAddress`, `displayName`,
`organizationUuid`, `organizationName`, `organizationRole`,
`billingType`, rate-limit tiers, trial fields, …), plus account-scoped
caches `modelAccessCache`, `orgModelDefaultCache`,
`passesEligibilityCache`, `groveConfigCache`, `groveChoiceSeenAt`,
`cachedExtraUsageDisabledReason`, `additionalModelOptionsCache`,
`additionalModelCostsCache`, `clientDataCacheSlots`. API-key logins also
write `primaryApiKey` (a secret) and `customApiKeyResponses`. Machine or
workspace keys that must never move with an account: `userID`,
`machineID`, `projects`, `mcpServers`, `firstStartTime`,
`migrationVersion`, `skillUsage`, `pluginUsage`, `seenNotifications`,
the experiment/feature caches and every UI toggle. The vault's identity
block is the first group (see `accounts.IdentityKeys`); a swap replaces
exactly those keys and nothing else.

**Consequence for project-name pinning (D3).** The env-vars reference
says `CLAUDE_CODE_PROJECT_DIR_NAME` is ignored when `CLAUDE_CONFIG_DIR` is
unset. Capture mode launches the default account without
`CLAUDE_CONFIG_DIR` (owner decision 1), so those sessions are stored
under Claude Code's path-derived project folder like any plain `claude`
run; pinning still applies to isolated mode and to every parallel
(login-only dir) launch. Cross-OS resume of a capture-mode session works
when the repo sits at the same path on both machines; otherwise
`cwm sessions` still lists and resumes it on the machine where the path
matches. Recorded as a known limitation (D86).

**Other facts used.** `~/.claude/backups/` holds Claude's own
`.claude.json` copies (never synced); `.claude.json` is created on first
start, not by `--version`/`--help`; claude.ai connectors are configured
in the claude.ai account and are not files cwm could sync.

### Project folder names (researched 2026-11-24 for P6b, Claude Code 2.1.319)

Read from the shipped bundle's source text (the binary was not run): the
projects/ folder for a working dir is `WC(cwd) = CLAUDE_CODE_PROJECT_DIR_NAME
?? fL(cwd)`, where `fL` replaces every character outside `[a-zA-Z0-9]` with
`-` and, when the result is longer than 200 characters, keeps the first 200
and appends `-` plus the base-36 absolute value of a 32-bit string hash of
the ORIGINAL path (`h = (h<<5) - h + charCode | 0` over UTF-16 code units).
Consequences: `C:\Users\me\api` -> `C--Users-me-api`; the drive letter keeps
the case the process reports (no lower-casing, unverified on Windows
hardware); non-ASCII and non-BMP characters become one `-` per UTF-16 unit.
`internal/pathmirror.FolderName` reproduces it; its tests use vectors
produced by running the bundle's own two functions in node.

