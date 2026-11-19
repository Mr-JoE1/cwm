# SPEC v0.6.1: Windows field fixes

Source: the first real Windows 11 test of v0.6.0 (Windows PowerShell 5.1,
`irm https://mr-joe1.github.io/cwm/install.ps1 | iex`). Two defects:

1. The installer worked, but cwm's own lines came out as mojibake:
   `Γ£ô cwm home initialized at C:\Users\u\.cwm`. That is "✓" (UTF-8
   `E2 9C 93`) decoded as code page 437.
2. "Add account" in the dashboard opened a terminal that failed and closed:
   `accounts: claude not found on PATH — install Claude Code first: exec:
   "claude": executable file not found in %PATH%`. Claude Code was installed,
   but its folder was not on the PATH the agent inherited.

Decisions D49–D53 are logged in `docs/PLAN.md`. Every v0.6 contract stays
backward-compatible: flags, env vars, exit codes and `/api/*` shapes only
gain fields.

## A: claude resolver (D49)

New package `internal/claudebin`, used by every caller that runs claude:
`account add` (and `init --account`), `open` / direct launch / `sessions
--pick` (via `launcher`), `doctor`, and the dashboard (wizard status and the
Add-account precheck). `storage.ClaudeBin` and its `state/claude-path` cache
are removed; `storage.MinVersion` / `VersionAtLeast` stay.

Lookup order:

1. `$CWM_CLAUDE`. It must point at an executable file, or the lookup fails
   with an error naming the variable.
2. `config.toml` `claude_path`. Skipped when it is stale (the file is gone or
   not executable).
3. `exec.LookPath("claude")` (PATHEXT on Windows, so `claude.cmd` counts).
4. Well-known install locations, in order:
   - Windows: `%USERPROFILE%\.local\bin\claude.exe`,
     `%LOCALAPPDATA%\Programs\claude\claude.exe`,
     `%LOCALAPPDATA%\AnthropicClaude\claude.exe`, `%APPDATA%\npm\claude.cmd`,
     `%LOCALAPPDATA%\Microsoft\WinGet\Links\claude.exe`,
     `%USERPROFILE%\scoop\shims\claude.exe`.
   - Linux/macOS: `~/.local/bin/claude`, `~/.claude/local/claude`,
     `~/.npm-global/bin/claude`, `/usr/local/bin/claude`,
     `/opt/homebrew/bin/claude`, `~/.bun/bin/claude`, `~/.volta/bin/claude`,
     then `$NVM_DIR` (default `~/.nvm`) `versions/node/*/bin/claude`, newest
     node version first.
5. `npm prefix -g` (when npm exists; 3 s timeout; no console window):
   `<prefix>\claude.cmd|.exe` on Windows, `<prefix>/bin/claude` elsewhere.

On POSIX a candidate must be a regular file with an exec bit. On Windows a
regular file is enough. `CWM_CLAUDE_NO_SEARCH=1` disables steps 4–5; tests
set it so they can never reach a real claude.

Not found: the error lists every searched path, the install command
(`irm https://claude.ai/install.ps1 | iex` on Windows,
`curl -fsSL https://claude.ai/install.sh | bash` elsewhere), and both
override knobs.

`Result.Source` is `env`, `config`, `path` or `discovered` (steps 4–5);
`Source.Label()` is its human name for doctor and the debug bundle.

Found off PATH (steps 4–5):
- `Find` writes the path back to `claude_path`, but only when `config.toml`
  already exists. Creating it would make an uninitialised home look
  initialised to the installers. `account add` also records it in the
  in-memory config it saves (`Remember`), so its own save keeps the value.
  `Lookup` (doctor, the dashboard's state poll) never writes.
- `account add` prints
  `note: using claude at <path> (its folder is not on PATH; …)` followed by the
  copy-paste PATH command:
  - Windows: `[Environment]::SetEnvironmentVariable("Path", [Environment]::GetEnvironmentVariable("Path","User") + ";<dir>", "User")`
  - Linux: `echo 'export PATH="<dir>:$PATH"' >> ~/.profile`
  - macOS: the same line, written to `~/.zprofile` (zsh is the default shell)
- `doctor`: `ok: claude <ver> at <path> (…)` plus a `claude-binary` check
  naming the resolved file and its source:
  `ok: claude binary: <path> (source: PATH)`, or off PATH
  `warn: claude binary: <path> (source: found in a well-known install folder) — its folder is not on PATH, …; fix: <command>`.
  Not found: `warn: claude not found (PATH and the usual install folders) — install Claude Code: <command>`.

`account add` resolves claude before it creates `accounts/<name>`, so a
missing claude leaves nothing behind.

### Dashboard (additive)
- `GET /api/state` gains
  `claude: {found, path?, onPath, pathHint?, install, error?}`. It is cached
  for 5 s so the wizard's 2 s poll never spawns npm on every tick.
- `POST /api/accounts` resolves claude again (uncached) before opening a
  terminal. If claude is not found, the response is **424** with
  `{error, command: <install command>}` and no terminal opens.
- `POST /api/sessions/resume` runs the same precheck (424 + install
  command, no terminal). A successful launch of either action found off
  PATH gains `claudePath` and `pathHint` (additive).
- `GET /api/claude` (authenticated, same Host/token rules as every GET):
  `{found, path, source, source_label, on_path, hint, path_command,
  install: [{label, command}], error?, os}`, resolved afresh, read-only
  (never pins `claude_path`). `install` lists the official commands from
  the Claude Code setup docs (code.claude.com/docs/en/setup), recommended
  first: Windows `irm https://claude.ai/install.ps1 | iex`, CMD
  `curl -fsSL https://claude.ai/install.cmd -o install.cmd && install.cmd && del install.cmd`,
  `winget install Anthropic.ClaudeCode`; macOS/Linux
  `curl -fsSL https://claude.ai/install.sh | bash`, macOS
  `brew install --cask claude-code`; everywhere
  `npm install -g @anthropic-ai/claude-code`. This endpoint alone uses
  snake_case field names, as specified.
- Wizard, account step: a warning box with the install command when claude
  is missing, an info box with the PATH command when it was found off PATH,
  and nothing when all is well. A 424 response shows "Claude Code is needed
  first." with the command.
- Toasts (Add account and Resume): 424 → a sticky "Claude Code is needed
  first" toast with the install command and a Copy button; an off-PATH
  launch → a sticky "claude is not on your PATH" toast with the PATH
  command and a Copy button.
- Terminal windows stay open on failure (D53): the dashboard launches
  `cwm --hold-on-error <args…>` (the copyable command shown to the user
  stays without the flag). The new global flag `--hold-on-error` means the
  same as `CWM_HOLD_ON_ERROR=1`: after a failed command cwm prints the
  error (for a missing claude: the install command) and waits for Enter.
  The env var alone did not reach the command when the terminal hands it
  to an already-running instance (wt.exe, gnome-terminal-server).

## B: readable output on legacy Windows consoles (D50)

New package `internal/termout`. `cmd/cwm` wraps stdout and stderr with
`termout.Wrap`. On Windows, a stream carries Unicode glyphs only when
`GetConsoleOutputCP() == 65001`, or when the handle is a console inside
Windows Terminal (`WT_SESSION`). Otherwise it passes through an ASCII
writer, which maps cwm's typographic glyphs (`✓→ok ✗→[x] ⚠→! —→- →→->
…→...` and friends) and leaves every other byte, including user data such
as non-ASCII names, untouched. Split UTF-8 sequences across writes are
reassembled. `CWM_GLYPHS=ascii|unicode` overrides the choice on every OS.
Non-Windows behaviour is unchanged. kernel32 is bound with
`syscall.NewLazyDLL` (`golang.org/x/sys` is not a dependency).
`cmd/cwmd` (log file output) is untouched.

## C: installer look (D51)

`install.sh` and `install.ps1` share one layout:
- A banner: a 6-line ASCII hexagon-and-prompt mark next to the `cwm`
  wordmark, then `Claude Workspace Manager  installer … cwm <tag>`, under
  80 columns.
- Eight numbered steps, `[n/8] Title … <glyph> result <dim detail>`:
  Detecting platform (`os/arch`, `release <tag> (latest|pinned)`),
  Downloading `<archive>` (size), Verifying SHA-256 (`checksum OK <first 12
  hex>`, never all 64 on success), Installing, Updating PATH, Initializing
  workspace, Installing login autostart, Opening the dashboard. Skipped steps
  show `- skipped (--flag)`. Uninstall uses three steps.
- Child output (`cwm init`, `cwm service install`, `cwm dashboard`, `cwm
  service uninstall`) streams live, indented and dimmed under its step
  behind a `│`/`|` bar, followed by the step's own result line.
- A final box (its width follows the longest line) with binary, version, the
  dashboard URL taken from the agent's output (never the token), next steps
  (open dashboard, add account) and the uninstall command
  (`curl -fsSL …/install.sh | sh -s -- --uninstall` /
  `$env:CWM_UNINSTALL='1'; irm …/install.ps1 | iex`).
- Colours: green ok, yellow warn, red fail, cyan accents, dim details.
  `NO_COLOR` removes the colours and keeps the glyphs.
- Output that is not a terminal (sh: `! [ -t 1 ]` or `TERM=dumb`; ps1:
  `[Console]::IsOutputRedirected`) is plain ASCII with no escape sequences.
  `CWM_INSTALL_FANCY=1` forces the terminal look (test seam).
- Glyph sets: sh uses Unicode only with a UTF-8 locale. ps1 uses Unicode in
  Windows Terminal, VS Code, and non-Windows hosts. The legacy console gets
  `√`, `!`, `x` and single-line box drawing, all present in Consolas, Lucida
  Console and CP437. Plain mode uses `ok`, `!!`, `xx`, `|`, `+-+`. The
  installers export `CWM_GLYPHS` so cwm's own lines match.
- install.ps1 sets `[Console]::OutputEncoding` to UTF-8 (no BOM) for its run
  and restores the previous value afterwards, error paths included. The
  script source is pure ASCII: every glyph is built from its code point. A
  failure prints `✗ error: …`. Under `-File` it exits 1; under `irm | iex`
  it sets `$LASTEXITCODE = 1` and returns, because `exit` would close the
  user's window.
- Unchanged: flags, env vars, mandatory SHA-256 verification (missing entry,
  mismatch or no hash tool means abort with nothing installed), PS 5.1
  compatibility (no `??`, `?.` or ternary), POSIX sh, no sudo/admin.

## D: debug bundle (D52)

One plain-text report for bug reports, built by `internal/debugbundle`
for both callers, redacted as a whole before anyone sees it.

Sections, in order (`== title ==` headers):
- `version`: cwm version + commit, os/arch, Go version, the OS release
  (`/etc/os-release` + kernel; macOS `SystemVersion.plist`; Windows `ver`),
  and `SHELL TERM TERM_PROGRAM WT_SESSION(set) LANG CWM_GLYPHS NO_COLOR CWM_HOME`
  when set.
- `agent`: pid, port, bind, uptime, version (or "not running" + why).
- `service`: the `cwm service status` items (same `service.Status`).
- `claude`: the resolver result (`claudebin.Lookup`, never writes): path,
  source, on PATH, fix command, `CWM_CLAUDE*` when set, and the PATH
  entries of the process that built the report.
- `config.toml`: every key, flattened (`account[0].login.plan = max`);
  values whose key matches token/secret/key/password/credential/cookie/
  email/uuid/organization print as `<set, not shown>`.
- `workspace`: home, initialized, project names with session counts (30
  max), account names (default marked) and counts. Account directories
  are never read; `.credentials.json` is never opened.
- `syncthing`: reachable, paired and connected counts (no device IDs).
- `doctor`: the local checks (never the SSH probe), text format.
- `logs`: tails of the regular files in `~/.cwm/logs/`, newest first,
  about 200 lines in total (≥ 20 per file, at most 64 KiB read per file).
  Includes `terminal.log`, new: the dashboard's terminal launcher appends
  one line per launch and captures the terminal emulator's own
  stdout/stderr (except on the Windows `cmd.exe /c start` route, where
  only the line is written, because `start` may pass redirected handles on
  to the window's program). The file is only written when `logs/` exists.
- `recent API errors`: the agent's last 100 non-2xx `/api/*` answers
  (timestamp, method, endpoint, status, message), memory only. 401s are
  not recorded.

Redaction (`debugbundle.Redactor`, applied to the whole text, idempotent):
PEM private-key blocks (also unterminated and JSON-escaped), `key=value`
/ `"key": "value"` / `key: value` pairs whose key contains token, secret,
password, passwd, pwd, apikey, key, authorization, cookie, credential or
session id (any case; quoted values keep their quotes), `<apikey>`-style
XML, `Bearer`/`Basic` credentials, `?t=` / `token=` / `key=` … URL
parameters, well-known token shapes (`sk-ant-`, `ghp_`, `github_pat_`,
`xox?-`, `AKIA`), any run of 64+ hex characters, e-mail addresses, the
home dir (native, forward-slash, JSON-escaped, any case, only at a path
boundary) → `~`, other users' homes (`/home/<u>`, `/Users/<u>`,
`X:\Users\<u>`; Public/Default/Shared kept) → `<user>`, and the login
name where it is a whole path element.

Callers:
- `GET /api/debug` (authenticated, same Host/token rules as every GET):
  `text/plain; charset=utf-8`, `Cache-Control: no-store`. The agent adds
  itself, its API errors and the service status (`gui.Options.DebugSources`).
- `cwm debug [--copy]` (new reserved name `debug`): prints the bundle.
  When an agent answers, it also borrows two sections from the agent's
  `/api/debug`: `recent API errors (from the agent)` and `claude (as the
  agent sees it)`, because the agent's PATH can differ from the shell's.
  `--copy` writes it to the clipboard: `clip.exe` (UTF-16LE with BOM, CRLF)
  on Windows, `pbcopy` on macOS, and on Linux `wl-copy` (only with
  `WAYLAND_DISPLAY`), then `xclip -selection clipboard`, then
  `xsel --clipboard --input`. Success prints one line with the line count
  and the issue link; when no tool works the bundle goes to stdout, the
  reason to stderr, exit 0.
- Dashboard: a "Copy debug logs" button in the header (bug icon), in the
  footer and on the Health tab, plus the `d` shortcut. Click → fetch
  `/api/debug` → `navigator.clipboard.writeText` → else a hidden textarea +
  `execCommand("copy")` → else a modal with the text in a `<pre>`, already
  selected, and a Select-all button. Success toast: "Debug logs copied (N
  lines) — paste into a GitHub issue", with a Report a bug link. "Report a
  bug" links (Health tab, footer, modal) open
  `https://github.com/Mr-JoE1/cwm/issues/new?template=bug_report.yml` in a
  new tab: a navigation the user starts, the page itself makes no external
  request. The static-asset test allows exactly this one link. CSP
  unchanged, no HTML sinks, assets about 93 KB.
- `.github/ISSUE_TEMPLATE/bug_report.yml`: a "Debug logs (preferred)"
  field asking for the button / `cwm debug --copy`, with the redaction
  warning; `cwm doctor --json` and free-form logs stay as fallbacks.

## Tests / gate
- Unit: `claudebin` covers lookup order, stale config, env errors, the
  exec-bit and dir filters, the Windows candidate list, npm prefix (both
  OSes), nvm ordering and `$NVM_DIR`, the not-found message, PATH hints
  (quote escaping), write-back only into an existing config, `Lookup` never
  writing, `Remember`, `Hint` and `Version`. `termout` covers the decision
  matrix (the v0.6.0 case: pipe + CP 437 → ASCII), glyph rewriting,
  byte-by-byte split writes and invalid bytes. `accounts` covers the off-PATH
  find + pin + note, and that a missing claude leaves no dir. `doctor` covers
  the off-PATH ok + PATH warn and the new not-found text (golden updated).
  `gui` covers the 424 precheck (no terminal opened), `/api/state.claude`,
  and the cache vs. the uncached precheck.
- `scripts/test-install.sh` (Linux CI, plus pwsh 7 when present): every v0.6
  check, plus banner, `[1/8]…[8/8]`, the 12-char hash with the full hash
  absent, indented child output, the summary box, plain mode being pure ASCII
  without escapes, the fancy mode's colours and glyphs, `NO_COLOR`, and
  ps1's banner, hash, box and Unicode look.
- `scripts/test-install-windows.ps1` (windows-latest, **Windows PowerShell
  5.1**): install.ps1 `-File` end to end (dry-run service), the user Path,
  no mojibake; `cwm.exe init` piped under CP 437 → `ok …` (no `Γ`); UTF-8
  console encoding round-trips `✓`; a tampered checksum aborts; `-Uninstall`.
- Debug bundle: the redactor against nasty samples (bare and glued 64-hex,
  `?t=` URLs, quoted JSON with escapes and mixed-case keys, TOML/env/header
  forms, a key at end of line, Bearer, syncthing XML, multi-line, cut-off
  and JSON-escaped PEM, e-mails, POSIX and Windows homes in native,
  lower-case, forward-slash and JSON-escaped spellings, other users,
  `DOMAIN\user`, short user names, idempotence); `Build` on a seeded home
  (every section, config masking, log budget and order, no leak of the
  home, a token, an e-mail or `.credentials.json`); graceful degradation.
  `gui`: `/api/claude` (off PATH, not found on Windows with the four
  official commands, raw snake_case), `/api/debug` (text/plain, no-store,
  sections, recorded errors, no token/CSRF/home/hex64, 401 not recorded),
  the auth and Host gates and POST → 405 for both, the 100-entry ring, the
  Resume precheck and `pathHint` on both launches. `term`: the launch log.
  `cli`: `cwm debug` output and leak check, `--copy` order on Linux
  (`wl-copy` → `xclip` → `xsel`), xclip args, clip.exe UTF-16LE + BOM +
  CRLF, pbcopy, the stdout fallback, borrowing the agent's sections, and
  `--hold-on-error`. Tests that run doctor now use an empty PATH, so no
  test can reach a real claude.
- Gate = the v0.6 gate + both harnesses + `shellcheck -s sh` on install.sh.
  CI also runs on `fix/**` pushes.
