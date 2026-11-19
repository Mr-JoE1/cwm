# SPEC v0.6.2: accounts tab, verified claude, quiet fresh installs

Source: a real Windows 11 test of v0.6.1. Claude Code was installed at
`%LOCALAPPDATA%\AnthropicClaude\claude.exe` (version 4.23118.1), and
v0.6.1's resolver found it and saved it as `claude_path`. Four findings:

1. cwm trusts a claude candidate because of where it is (PATH, a folder
   name), never because of what it is. A stale or wrong `claude_path`
   stays pinned forever.
2. Dashboard "Add account" opened a terminal; afterwards the account
   existed, but the only trace was "0 with a recorded login" in the debug
   bundle. The login state is invisible and the dashboard cannot manage
   accounts at all.
3. A fresh Windows install showed five doctor warnings, most of them
   legacy-kit noise, and `GET /api/devices` answered 503 while Syncthing was
   simply not set up yet, which also landed in "recent API errors".
4. The install.ps1 banner came out slightly mangled under Windows
   PowerShell 5.1.

Decisions D54–D59 are logged in `docs/PLAN.md`. Compatibility rule as in
v0.6.1: flags, env vars, exit codes and existing `/api/*` shapes only gain
fields. Deliberate exceptions are listed in "Behaviour changes" at the end.

## A: claude is verified by behaviour (D54)

The real output of `claude --version` is one line, `<semver> (Claude Code)`,
for example `2.1.319 (Claude Code)`. The verifier is built around that
shape but does not depend on the exact suffix.

`claudebin.Probe(path)`:
- runs `path --version` with a 5 s timeout, stdin from the null device, no
  console window on Windows, and kills it on timeout (`WaitDelay` 1 s, so a
  grandchild holding the pipe cannot hang the probe);
- keeps at most 4 KiB of combined stdout and stderr;
- **verified** = exit status 0 **and** some line of the output carries both a
  semantic version (`\d+\.\d+\.\d+`, any pre-release or build suffix allowed)
  and the word `claude` (any case).
- `ProbeResult{Verified, Version, Output, Reason}`. `Reason` is a short text
  for a failure: `exit status 1`, `timed out after 5s`, `no Claude Code
  version line in its --version output`, `could not run: …`.
- Results are cached per process by (path, size, mtime), so the
  dashboard's 5 s state poll does not spawn claude every time.

Resolver order is unchanged (`$CWM_CLAUDE` → `config.toml` `claude_path` →
PATH → well-known folders → `npm prefix -g`), but a candidate only counts when
it verifies:
- `$CWM_CLAUDE`: an explicit setting is never papered over. Not an
  executable file → error (as before); executable but unverified → error
  `CWM_CLAUDE=<p> is not Claude Code (<reason>)`.
- `claude_path`: a missing file falls through (as before). A file that
  fails verification is **cleared from config.toml** (by `Find` and
  `Lookup`; only the key, only when config.toml exists) and the search
  continues. One line is kept in `state/claude-cleared.txt` (time, path,
  reason). doctor and the debug bundle show it.
- PATH: every `claude` on PATH is a candidate, in PATH order (PATHEXT
  `.com .exe .bat .cmd` on Windows), not only the first. The first one
  that verifies wins.
- Location is never a reason to reject. Any folder is fine, including
  `%LOCALAPPDATA%\AnthropicClaude`. Only the probe decides.
- Not found: the error says how many candidates were found but failed
  verification, besides listing the searched folders.

`Result` gains `Version`, `ProbeOutput` (the first 200 characters) and
`Candidates` (the probed candidates in order: path, source, on PATH,
verified, short result). `Survey` mode (used by `/api/claude`, doctor and
the debug bundle) keeps probing after the winner, up to 5 existing
candidates, so a report shows the shadowed ones too. Commands that only
need a binary (`Find`) stop at the first verified one.

`GET /api/claude` gains `verified` (bool), `version`, `probe_output` (200
characters at most) and `candidates` (up to 5: `{path, on_path, verified,
result}`), plus `cleared` (the last cleared `claude_path` note, when there
is one).

doctor:
- `claude`: `ok: claude 2.1.319 on PATH (>= 2.1.234: …)` as before, using
  the probe's version (no second spawn).
- `claude-binary`: `ok: claude binary: <path> (source: PATH, verified:
  2.1.319 (Claude Code))`. The PATH warning stays only when it is verified
  but off PATH. Nothing verifies: `warn: claude not found…`, and when
  candidates existed but failed: `… (N candidate(s) found but not Claude
  Code: <path>: <reason>)`.
- `claude-cleared` (ok, informational) when `state/claude-cleared.txt` exists.

## B: Accounts tab (D55–D57)

### Login state (D55)
Per account: `never | in_progress | done | failed`.

- Marker: `~/.cwm/state/account-login/<name>.json` (0600, atomic) with
  `{status, started, updated, pid, by, error}`.
- Writers:
  - `cwm account add` / the new `cwm account login`: `in_progress` with
    its own pid at start, `done` after the config is saved, `failed` with
    the error otherwise. That is "driven by the launched process": the
    command inside the dashboard's terminal reports back itself.
  - The agent: `in_progress` (`by: dashboard`, no pid) right before it
    opens the terminal. If the terminal could not be opened, `failed`
    (launch error) or the marker is removed (501: nothing runs).
- Reader (agent, on every `GET /api/accounts` and on the event-stream
  poll) derives the status and persists transitions:
  - no marker: `done` when the account has a recorded login (the CLI's
    existing signal: `config.toml` `[account.login]` with an e-mail),
    otherwise `never`;
  - `in_progress` turns into `done` when a recorded login exists and the
    account's `.credentials.json` is newer than the marker's start; into
    `failed` when its pid is gone ("the login window was closed before the
    login finished"), when no cwm process took it over within 90 s ("the
    terminal did not start the login"), or after 15 minutes ("timed out").
- In memory: the agent keeps the last derived state per account and
  publishes a change on `/api/events` (topic `accounts`).

### CLI
- New `cwm account login <name>`: re-runs `claude auth login` into the
  existing account dir (recreated if it is missing), refreshes the
  identity in config.toml and writes the marker.
- `cwm account add <name>` on an existing account points at
  `cwm account login <name>`.
- New account names must also not be a reserved cwm word (`cwm help`'s
  list, plus `add list use remove rm login`).
- `cwm account remove <name> [--delete-dir] [--yes]`: refuses the last
  account. `--delete-dir` deletes `accounts/<name>` and `launch/<name>`
  after the backup. Removing the default makes the first remaining account
  the default and says so.

### API (D56)
All follow the existing conventions: authenticated, POST only with
`X-CWM-CSRF` and `Content-Type: application/json`, unknown fields are 400,
serialized with the other actions. Field names are snake_case, like
`/api/claude`.

- `GET /api/accounts`: the array keeps its v0.5 fields (`name email plan
  default last_login`) and gains `is_default`, `has_recorded_login`,
  `last_used` (mtime of `launch/<name>`, set on each launch), `login_status`,
  `login_error`, `login_updated`, and on the default account
  `config_dir_ready` (its account dir exists, so a launch can assemble).
- `POST /api/accounts/add {name}`: validation in order: non-empty, at
  most 32 characters, no `/` or `\`, not reserved, the account-name pattern
  (`[a-z0-9][a-z0-9_-]*`, not all digits), unique (409). The workspace must
  be initialised (409). A missing claude → 424 with the install command,
  nothing created. Then: account dir (0700) and config entry (the first
  account becomes the default), marker `in_progress`, terminal running
  `cwm account login <name>`. No terminal → **501 with the exact
  copy-paste command** (the account exists with `never`; running the
  command moves it on). Success: `{launched, command, account,
  login_status}` plus the v0.6.1 PATH-hint fields.
- `POST /api/accounts/login {name}`: unknown → 404; a login whose
  process is still alive → 409; else as above, for an existing account.
- `POST /api/accounts/set-default {name}`: unknown → 404. Answers `{default}`.
- `POST /api/accounts/remove {name, also_delete_config_dir}`: unknown →
  404, last account → 409. Always backs up first (the existing
  `backups/<ts>-account-<name>/` copy; a failed backup aborts with
  nothing removed), then deregisters, optionally deletes the dirs, drops
  the marker. Answers `{removed, backup, deleted_config_dir, default}`.
- `POST /api/accounts` (the wizard's v0.5 endpoint) is unchanged.

### Dashboard (D57)
- Sixth tab **Accounts**; shortcuts `1`–`6`, the `?` help lists them.
- One card per account (a grid that stacks on mobile): name, default
  badge, e-mail and plan when recorded, status pill (`no login yet`,
  `login in progress`, `logged in`, `login failed`), last login, last
  used, the login error inline. Actions: `Log in again` (or `Retry login` /
  `Log in`), `Make default`, `Remove`.
- Failed or stuck logins show an inline Retry button.
- When the default account has no recorded login, a prominent banner sits
  at the top. With no account at all its action is "Add your first
  account" (focuses the name field, prefilled `work`). When a default
  account exists but is not logged in, the action is "Log in “name”" (or
  "Retry login" after a failure), and while that login runs the banner says
  so. The tab label gets an attention dot.
- Add form: name input with client-side checks mirroring the server's,
  then the 501 case shows the command with Copy.
- Remove opens a confirm dialog (a native `<dialog>`) with the
  "also delete the config dir" checkbox. The text says a backup is always
  made first.
- While any login is `in_progress` the tab refreshes every 2 s; otherwise
  it follows the normal live updates.
- Unchanged rules: DOM nodes and `textContent` only, no external requests,
  same CSP, assets under 150 KB in total, v0.7 tokens, cards on mobile.

## C: fresh-install noise (D58)
- doctor `settings`: a missing `workspace/settings.json` is **ok** (`not
  used: the Go kit writes settings.json into each launch dir when it
  assembles one`). When the file exists, every v0.6 check applies as before.
- doctor `hook-scripts`: ok when `workspace/hooks` has files (as before).
  Warn only when the workspace's settings.json wires bash-kit
  `…/hooks/*.sh` scripts and the folder is empty. Otherwise ok, `not
  needed: the Go kit runs hooks as cwm hook …`.
- doctor `config-dir` (CLAUDE_CONFIG_DIR in a shell profile): not run on
  Windows. The claude-binary check covers resolution there.
- doctor `path-duplicates` (ok, informational, never a warning): when the
  process PATH has duplicate entries (case-insensitive and
  trailing-separator tolerant on Windows): `PATH has N duplicate
  entr(y|ies) (harmless; cwm does not change PATH)`. Only the count is
  shown. The debug bundle's PATH listing marks duplicates.
- `GET /api/devices`: when Syncthing is not configured or not reachable
  (no client, or the client cannot be built: discovery or ping failed) →
  **200** `{"status":"not_configured","error":"…","hint":"…"}`. It is no
  longer an API error, so it is not in "recent API errors". A reachable
  Syncthing still answers the device array; a failing API call is still
  502. The Devices tab renders `not_configured` as a setup card (what
  Syncthing is for, `cwm pair --start`, the pair form below stays).

## D: installer banner guard (D59)
- The banner art lives in `scripts/banner.txt` (4 lines, ASCII). Both
  installers print it verbatim.
- install.ps1 builds the two characters PowerShell can misread, the
  backtick (its escape character) and the apostrophe, from code points
  (`[char]0x60`, `[char]0x27`). No banner line contains a literal
  backtick, so no quoting context (`-File`, `irm | iex`,
  `[scriptblock]::Create`) can eat one.
- `scripts/test-install.sh` (Linux CI): the sh banner and the pwsh 7
  banner both equal `scripts/banner.txt` line by line, so they equal
  each other.
- `scripts/test-install-windows.ps1` (windows-latest, Windows PowerShell
  5.1): captures the installer's output under `-File` **and** under
  `irm | iex` from the local HTTP server (the tester's exact command),
  and asserts each banner line is byte-identical to `scripts/banner.txt`.

## Tests / gate
- claudebin: table-driven probes with fake binaries in `t.TempDir()` (good
  version, version with a build suffix, bare version without the marker,
  exit 1, a hang ending in the timeout, no output), the stale-saved-path
  scenario (cleared, the note written, the search continues to PATH), the
  `%LOCALAPPDATA%\AnthropicClaude` candidate verified by probe only, the
  PATH walk past an unverified first hit, survey mode, `$CWM_CLAUDE`
  unverified → error, the cache. HOME, USERPROFILE, LOCALAPPDATA and
  APPDATA are isolated, PATH points at an empty dir. POSIX script fakes
  skip on Windows (D22).
- accounts: login state derivation (all transitions, dead pid, 90 s,
  15 min, credentials newer than the start), marker round trip,
  `Login`, name validation, remove last → error, `--delete-dir`, default
  promotion.
- gui: every new endpoint's auth/CSRF/body rules, add (skeleton plus
  launch, 501 command, 424 leaves nothing, 409 duplicate and
  uninitialised), login retry and the 409 alive case, set-default, remove
  (backup, last-account 409), the failed state seen through `GET
  /api/accounts` and the fake binary, `/api/devices` not_configured is
  200 and not recorded, `/api/claude` new fields, 6 tabs in the page.
- doctor: goldens updated (fresh workspace: no settings/hook warnings),
  the Windows skip, the PATH-duplicates note.
- Gate = the v0.6.1 gate.
