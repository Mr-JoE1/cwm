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

Decisions D49–D51 are logged in `docs/PLAN.md`. Every v0.6 contract stays
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
- `doctor`: `ok: claude <ver> at <path> (…)` plus
  `warn: claude's folder is not on PATH (cwm still finds it) — to run claude yourself: <command>`.
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
- Wizard, account step: a warning box with the install command when claude
  is missing, an info box with the PATH command when it was found off PATH,
  and nothing when all is well. A 424 response shows "Claude Code is needed
  first." with the command.

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
- Gate = the v0.6 gate + both harnesses + `shellcheck -s sh` on install.sh.
  CI also runs on `fix/**` pushes.
