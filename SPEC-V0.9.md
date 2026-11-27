# cwm v0.9.0: dashboard UX (as implemented)

v0.9.0 is the dashboard UX release. It implements every P0 and P1 item of the
owner's UX brief (D107), adapted by the owner's decisions: Home replaces the
Stats tab (D105), the asset budget is raised (D106), the in-dashboard terminal
moves to v0.10 (D108), and the UX checks run on demo data with the repo's own
tools (D109). Running fixes from the dashboard is D110. This file records
what was built, the audit before and after, the deviations from the brief and
the release notes. The decision log (`docs/PLAN.md`) is binding where the two
differ.

## Release notes

### Changed: the tabs, and their shortcuts

- **Stats moved into Home.** Home is the new first tab and the default. It shows:
  - a status strip (health, sync, default account, agent);
  - Continue where you left off: the newest five sessions, with Resume on each;
  - Needs attention;
  - Uncommitted work;
  - **Activity**, which holds everything the Stats tab had.
- **The keyboard shortcuts are renumbered.** There are still 8 tabs on keys 1–8, but from Sessions on every key moved up by one:

  | key | v0.8 | v0.9 |
  |---|---|---|
  | 1 | Sessions | **Home** |
  | 2 | Projects | Sessions |
  | 3 | Devices | Projects |
  | 4 | Handoff | Devices |
  | 5 | Health | Handoff |
  | 6 | Accounts | Health |
  | 7 | Stats | Accounts |
  | 8 | Settings | Settings |

  A one-time toast says "Tabs renumbered: Sessions is now 2 · Stats moved into Home".
- Old settings and links are carried over once, when the agent starts:
  - `display.default_tab = "stats"` becomes `home`;
  - `"stats"` in `display.hidden_tabs` is dropped, and Activity is hidden instead (`display.home_activity = false`);
  - `#stats` links open Home, scrolled to Activity.
- On a phone the bottom bar is Home, Sessions, Projects, Health and More. Accounts moved into More, which shows a dot when a section behind it has a badge.

### New

- **Run fixes from the dashboard.** Checks that `cwm doctor --fix` repairs get a **Run** button next to Copy, on Health and in Home's Needs attention. Run first shows what the fix will do and asks before it applies anything. Fixes that need a terminal (installs, service restarts, logins, pairing) are copy-only; one line under the list says to run them in a terminal, and each Copy button says so too.
- **A live status line** in the footer, styled like a powerline. It shows the section, health, sync, the default account, live updates, uptime, the bind address and a clock. Each item opens its tab.
- **JetBrains Mono** is bundled (SIL OFL 1.1, a 32 KB subset, served by the dashboard itself), so terminal text looks the same everywhere.
- Sessions show the real first prompt. Current Claude Code transcripts used to read "(no prompt)". Slash commands read `/name args`. Projects are named by their repo basename (`billing-svc`, not `…/Acme-Corp-2026-billing-svc`), taken from the cwd the transcripts record; a parent folder is added only when two projects share a name, and the full path is on hover (D111). Both changes apply to the dashboard and to `cwm sessions`.
- Activity charts:
  - models read "Opus 5.5";
  - token kinds are one 100% bar ("Input + output" and "Cache"), the small part always visible;
  - the 30-day trend starts at 0 with headroom, three y ticks and dated x labels;
  - the range selector sits on the KPI row;
  - unpriced models and the price date are spelled out.

### Fixed

- The phone layout was 427 px wide on 390 px and 320 px screens (Sessions). It now fits the device.
- The focus ring was invisible on inputs, the palette and the More sheet.
- Health's "Details" toggles had no names a screen reader could tell apart.
- The palette's section labels had low contrast.
- Resume is visible without hovering. Empty columns are hidden. Dates use one format per group.
- Headings no longer jump levels. "Connection restored" is announced. Small links now have a 24 px minimum target.

### Quieter

- The migration notice appears in one place, not four.
- Sentence case throughout.
- Magenta is used only for the brand. Cyan marks what you can click, and green marks what is live or OK.
- The header holds only the logo, Refresh and the menu. Theme and motion moved into the menu, and their keys (`t`, `b`) are unchanged.

## As implemented, by package

| package | commit | content |
|---|---|---|
| U0 | 7db23c9 | `tools/ux/` capture, audit, footer close-ups, `--compare`, `demo.sh` + `demo-data.js`; budget D106 |
| U1 | b1ba169 | Home replaces Stats (D105): routing, default tab, keys 1–8, toast, TAB_LABEL, TOPIC_TABS, server migration, phone bar, independent widgets, lazy Activity |
| U2 | 75e1762 | B1 prompts (server, client, CLI), B2 project labels, B3, B4, B6, C1, I1 |
| U3 | 79ed5b9 | B5, B7, C2–C8, D, G1, G4, H1–H6, I2–I5, J1, J2, J4–J6 |
| U4 | 6fb3b46 | E1–E6 status line, J3 header, F1–F5, F11 |
| U5 | ac559be | G2/G2a `POST /api/doctor/fix` (D110) and the Run/Copy actions |
| U6 | this commit | QA, screenshots, README, wiki, this file |

### `POST /api/doctor/fix` (D110)

- **Request:** `{"checks":["vault", …]}`.
  - Accepted names are `doctor.FixActions`: `stignore`, `mcp-bridge`, `cwm-hooks`, `settings`, `vault`.
  - Any other name, an empty list or more than 16 names answers 400, and nothing runs.
- **Gates:** the same as `/api/migrate/apply`:
  - the session cookie (401 without it);
  - the `X-CWM-CSRF` header (403 without it);
  - a JSON body;
  - `actionMu`.
- **`?dry_run=1`:** returns `{"dry_run":true,"results":[{name,status,detail}]}` and changes nothing. Each result's status is one of:
  - `planned`;
  - `skipped`, with the reason:
    - a pre-v0.8 config waits for its migration;
    - `features.hooks` is off;
    - there is no default account.
- **Apply:** runs the pass `cwm doctor --fix` runs: one `upkeep.Tick`, then the vault save and install.
  - It runs under the agent's tick lock, so it never overlaps the minute tick. A paused agent answers 409.
  - Each check comes back `ok`, or `failed` with the fresh doctor detail.
  - The response includes the full fresh check list in `checks`.
  - It bumps `Snapshot.doctor`, so every open page gets a `doctor` change on `/api/events`.
- **Tests** (`internal/gui/doctorfix_test.go`):
  - an allow-list rejection (400) for unknown names, a mixed list and command-like text;
  - no CSRF (403) and no cookie (401);
  - a dry run writes nothing and emits no event;
  - an apply fixes the check and emits a `doctor` change;
  - skips, and 409 while paused.
  - The endpoint is also in the shared CSRF/body matrix of `actions_test.go`.
- **The page:**
  - Checks that share a command get one action ("Fixes 3 issues · $ cmd · Copy · Run").
  - The plan opens in the confirm dialog.
  - While it runs, the button shows a spinner and `aria-busy`.
  - On success it shows `[  OK  ]`, announces the result and re-renders: the check clears and the badge drops without a reload.
  - On failure it shows `[FAILED]` and the detail, and keeps the Copy command.
  - Run is disabled while the agent is offline.
  - Focus returns to the same Run button, or else to the panel heading.

## Audit: before and after

Measured with `tools/ux/capture.mjs --compare` against the pre-change build
(v0.8.4) on the same demo data: 72 views, 1440/1024/390/320 × dark/light plus
the overlays.

| measure | before (v0.8.4) | after (v0.9.0) |
|---|---|---|
| page width = device width | 9 views wider (phone Sessions and the More sheet: 427 px at 390 and 320) | all 72 |
| low-contrast text | 1 (palette section label, 4.22:1) | 0 |
| unnamed controls | 12 in every Health view (Details toggles) | 0 |
| console errors | 0 | 0 |
| CSP violations (all tabs + overlays) | 0 | 0 |
| contrast pairs checked (`scripts/contrast-check.js`) | 134, 0 failed | 164, 0 failed |
| code (html+css+js+svg) raw / gzip | 204 348 / 55 603 B | 242 340 / 65 989 B (budget 266 240 / 76 800) |
| fonts | 0 | 32 300 B (budget 61 440) |

Other checks:
- **Keyboard only:**
  - keys 1–8 select the 8 tabs in order;
  - `?` opens the keys dialog, Ctrl-K opens the palette with its input focused, and Esc closes both;
  - every tab stop on Home, Health and Sessions has a visible ring.
- **Reduced motion and `data-bg=off`:** no infinite animation is running.
- **Run fixes:** checked end to end on a demo home where the live login was missing:
  - Health and Home both showed the Run button;
  - the plan opened in the dialog;
  - apply restored the login, the check cleared and the badge went from 3 to 2;
  - a forced failure showed `[FAILED]` with the detail;
  - Run was disabled when the agent stopped.

### Visual review (U6)

All 72 screenshots were reviewed by eye. These were fixed in U6:

- **Phone command boxes** broke inside words and pushed `$` onto its own line. They now wrap at spaces with a hanging `$`, in a slightly smaller font.
- **Settings at 320 px.** The base rules came after the phone media block in the stylesheet, so they won over it: the Mode description was one word wide and paths broke mid-word. The narrow rules now come last; card headers stack, and controls take their natural width.
- **Phone Sessions** showed a branch cut down to two characters, or an empty slot between two dots. The meta line is now project · msgs · time; the branch stays in the desktop table and in the tooltip.
- **Accounts cards.** The login state has its own line, so both cards' headers and rows line up.
- **Home's Needs attention** cut commands off. They now wrap.
- **Activity.** "≈ $27.43" and "19 days" no longer break. "Totals for" takes its own line under 480 px, so the range selector fits at 320 px.
- **"Details" toggles** get a ▸/▾ marker. Hints and empty states use `text-wrap: pretty`, and tile values use `balance`, to avoid orphans.
- **Tablet nav.** The lone palette glyph is hidden at 721–1100 px; Ctrl-K and the menu still open the palette.
- **About timeline.** It still showed v0.7.2 as this build and "0.8 next"; it now lists 0.10 (next), 0.9 and 0.8.
- **The `display.hidden_tabs` help** said "comma-separated" next to a set of switches. It now reads "tabs the dashboard hides (a comma-separated list from the CLI)".
- **Capture tool.**
  - Overlays are now closed with Esc between captures, so the keys dialog is no longer captured over the palette.
  - The keyboard sample reloads the page, so a phone sheet no longer leaks onto desktop.
  - The landing shot is taken in the theme its name says.

Kept on purpose:

- **A single word longer than the command box** scrolls sideways inside the box instead of breaking (polish pass).
- **On phone Health**, "Report a bug" stays a text link next to the "Copy debug logs" button. Both have 44 px targets.
- **The phone heatmap** shows the most recent months and scrolls sideways for the rest (H6).

### Polish pass (D111)

From the owner's review of the final captures:

1. **One set of health numbers.** Footer, Home tile, tab badges, Needs attention and the Health summary all use `healthSum()`: the worst level as the tag and "1 failed · 1 warning" as the text. Skipped checks never count. The phone badge turns red on a failure.
2. **Footer.** "live" with "last event 11s ago" in its tooltip, "agent up 1s", "account work" (with a label). Tags are the fixed 8-character `[  OK  ]`, `[ WARN ]`, `[FAILED]`, `[ SKIP ]` everywhere.
3. **Account tile** shows the default account and its login state only. A missing Claude Code is folded into Health (its `claude` check), not a fifth tile.
4. **Project names** are the repo basename in Sessions, Home, Activity, the palette, `cwm sessions` and `cwm stats`; names that must be shortened lose their middle, never their end.
5. **Dates.** Every Sessions group header carries the day (today, yesterday, then "Tue, 24 Nov"); cells show HH:MM; the full timestamp is the tooltip.
6. **Untitled sessions** read "untitled" in the table (the ID has its own column) and "untitled · id8" on phone cards, Home and the palette.
7. **Commands** wrap only between words, share one width within a list and hang the `$`. "Run it in a terminal" appears once per list and on the Copy button.
8. **Phone targets** are at least 44 px: header buttons, toggles, range buttons, Resume, command boxes, the filter and the "All … →" links.
9. **Activity.** One number rule (7.0M, 4.6M, 72.2k, 144k), 3 px minimum bars, "Input + output" with a minimum segment and a label beside the bar, a bottom grid of named areas (no orphan row at 1440 or 1024), a sublabel on every KPI, "prices as of" inside the cost card, month labels on the week that holds the 1st, a fade edge plus "← scroll" on phone, and a 30-day chart from 0 with headroom and three ticks.
10. **Home.** Continue and Needs attention share the row height; Resume is centred.
11. **Uncommitted work** has dividers between rows only.
12. **Header and tab bar** are solid (`--surface-solid`), so the background canvas shows only in the gutters.
13. **Palette hint** uses the plain `>` prompt, on the baseline of `ctrl k`.
14. **Phone Health badge** is 16 px, filled, and sits clear of the icon.

Contrast: `scripts/contrast-check.js` now also checks the status line (segments, warn/fail tints), pills on tinted rows, `[ WARN ]` on pale yellow and the filled badge, and holds faint text to 4.5:1. Light `--warn` (#764c00) and `--accent-2` (#00717f) are darker for headroom.

The fixed status line and bottom bar appear mid-page in full-page captures: that is the capture, not the page. The real bottom padding clears them (bar + safe area + 24 px on phones, status line + 32 px on desktop).

## Deviations from the brief

- **Home replaces Stats; there is no separate "Last 7 days" widget.** This is the owner's decision (D105). Activity is the whole former tab.
- **`default_tab = "sessions"` also migrates to Home.** Every v0.8 init wrote that value, so a deliberate choice cannot be told apart from the old default. Choosing Sessions again after the upgrade sticks (D105).
- **G3 does not apply.** The orphaned "use this" button no longer exists since v0.8.4 (D104).
- **The status line is hidden on phones.** Home's status strip shows the same on a narrow screen.
- **Effects (F10) is v0.9.1.** The motion toggle moved into the menu unchanged, and F6–F9 are v0.9.1 too (D107).
- **B6 (one tab stop per row) is in 0.9.0**, although the brief marks it P2 (D107).
- **G2a runs only `cwm doctor --fix`.**
  - It is the only fix the CLI already performs without a terminal or credentials.
  - Every other command stays copy-only.
  - With `features.hooks` off, the `cwm-hooks` fix is skipped and the plan says why. The fix pass leaves settings.json alone in that state (D89).
- **Fixable checks rarely stay visible on a running agent.** The agent runs the same maintenance every minute, so `stignore`, `mcp-bridge` and `cwm-hooks` usually heal on their own. `vault` and `settings`, and any check that fails while the agent is paused, are the ones the button mostly serves.

## Out of scope (next)

- **v0.9.1 (P2):** HUD card chrome, segmented meters, the boot log, small touches, the Effects setting (F6–F10). Done; see the v0.9.1 section below.
- **v0.10:** the in-dashboard terminal.
- **v1.0:** the statusline, removal of the bash kit, a docs site.

---

# v0.9.1 (as implemented)

v0.9.1 is a patch release on v0.9.0. It covers Microsoft Store support (D112), issue #3 (D113), issue #2 (D115), the v0.9.0 leftovers, and the P2 items of the UX brief (F6–F10, D114).

## Release notes (v0.9.1)

### New

- **The Microsoft Store Claude desktop app.**
  - cwm finds the Claude Code that the Store build downloads, under `%LOCALAPPDATA%\Claude-Data\claude_<hash>\claude-code\<ver>\<build>\claude.exe`.
  - Reports call it "the Claude desktop app (Microsoft Store)".
  - When one version has several build folders, the newest by mtime wins.
  - The app itself (`WindowsApps\…\Claude.exe`) is never run.
  - When the Store app is installed but hasn't fetched Claude Code yet, the debug report says so.
- **Choose which Claude opens a session (#3).** The `launch.claude` setting is `auto`, `cli`, `vscode` or `desktop`, and you set it in Settings → Opening sessions or with `cwm config set launch.claude <v>`.
  - `auto` is the default. It opens the claude that doctor shows, by full path, so it works with no `claude` on PATH.
  - `cli` uses a standalone install only.
  - `vscode` opens the session's folder in VS Code (or Cursor, Windsurf, VSCodium) and resumes it in a Claude Code tab.
  - `desktop` hands the session to the Claude app (`claude --desktop --resume`). Where that's not available, it opens the app in the folder and says it can't resume the session.
  - An option that can't work on this machine is shown disabled, with the reason.
  - A ▾ next to Resume and Start claude picks another option for one click. On touch, long-press; with a keyboard, use the context-menu key on a row.
  - `cwm sessions resume <id>` and `cwm run` take `--with <option>`.
- **Effects: Off, Subtle or Full** (in the menu, the phone sheet and Settings → Display). This replaces the motion toggle, and your old on/off choice carries over as Full/Off.
  - Subtle keeps the look without motion. Reduced motion caps Effects at Subtle.
  - Full adds:
    - HUD corners on Home and Activity cards;
    - segmented LED meters and a glow on the busiest heatmap days;
    - KPIs that count up;
    - a short boot log the first time you open the dashboard in a browser session. It shows only what the agent really answered.

### Fixed

- **The sessions table is symmetric (#2).** Columns have fixed widths, so every row and day group lines up. A long prompt is one line with an ellipsis, and the full prompt is in the tooltip. Phone cards and Home's Continue show at most two lines.
- **Account status.** Home's Account tile and the status line no longer say only "logged in" when claude is missing. They now read "logged in · claude not found" and link to Health.
- **Agent uptime.** The Agent tile and the status line show the same uptime, from one tick.
- **Wrapping.** In the Home tiles, the status line and Needs attention, each "·" clause stays whole and the "·" stays with the clause before it, so a line breaks only after "· ". The Agent tile reads "port N · localhost only".
- **Opening sessions.** An option that can't work looks disabled (muted title, dashed radio, not-allowed cursor, `aria-disabled`) and gives its reason on its own `[ SKIP ]` line. When the selected option can't work, the section says so, and for `auto` it shows this OS's install command with a Copy button.
- **Needs attention.** The summary line is only the counts; the `[FAILED]` tag is on the first item alone.
- **Prompts.** A prompt cut by Claude Code's index reads "three…", not "three …" (dashboard and `cwm sessions`).
- **Token kinds.** The card has one legend; the duplicate share row is gone.
- **Light-theme labels.** Small labels in the light theme are measured in `contrast-check`: all are ≥ 5:1, and `--faint` is a little darker. `contrast-check --all` works again.

## Audit (v0.9.1, demo data)

- **Compare.** `capture.mjs --compare` against the v0.9.0 final audit covers 72 views, with no regressions. Every phone `vw` equals the device width, with 0 lowContrast, 0 noName and 0 console errors.
- **Assets.** Code is 261.5 KB raw and 71.6 KB gzip, within the 260 KiB / 75 KiB budget. Fonts are 32.3 KB.
- **Contrast.** contrast-check checks 208 pairs and 0 fail.
- **Deviations:**
  - The F9 glitch and the palette-prompt filter were cut for budget (D114).
  - The ▾ menu is hidden on phones, where long-press opens it instead.
  - P4d needed no change: no phone target is under 44 px apart from the heatmap cells.
  - QA found one defect: at 320 px the Agent tile left a "·" alone on its own line. The polish pass reworded the tile to "port N · localhost only".
- **Polish pass.** Code is 263.6 KB raw and 72.1 KB gzip. contrast-check checks 212 pairs and 0 fail; the new pairs are the launcher radio outline (≥ 3:1) and the not-available reason.

## Research notes

### The Microsoft Store desktop app (D112)

- **What a real machine showed.** Windows 11, Store package `Claude_6.28899.6.0_x64__<publisherhash>`. Its running Claude Code was
  `%LOCALAPPDATA%\Claude-Data\claude_<publisherhash>\claude-code\2.1.330\<buildid>\claude.exe`.
- **What is not Claude Code.** `C:\Program Files\WindowsApps\Claude_<ver>_x64__<hash>\app\Claude.exe` is the desktop app itself. cwm never probes it, since starting it opens the app.
- **The package data dir.** `%LOCALAPPDATA%\Packages\Claude_<hash>\LocalCache\Roaming\Claude` is where a virtualised MSIX app's `%APPDATA%` really lives. cwm searches `claude-code` there too, with the same depth bound as D103.
- **Which copy wins.** Several build folders under one version are ordered by mtime, newest first. Store and classic (`%APPDATA%\Claude\claude-code`) copies can both exist. The newer Claude Code wins, and on a tie either counts as the desktop app.
- **A fresh install.** A freshly installed Store app has no Claude Code yet. It downloads it the first time its Code tab runs a local session, and the debug bundle says so.

### Which Claude opens a session (D113, issue #3)

- **The VS Code extension.** We inspected `anthropic.claude-code` 2.1.332.
  - **The handler.** `extension.js` registers one URI handler, `/open?session=<id>&prompt=<text>` (plus `/install-plugin`).
  - **How the id is checked.** The session id must be non-empty, with no `/`, `\`, `..` or NUL, and is resumed "from the folder open in VS Code". An unknown id starts a new conversation.
  - **What it can't do.** There is no folder parameter, and no command takes an id from outside. `activationEvents` has `onStartupFinished` but not `onUri`.
  - **Sources.** Docs: code.claude.com/docs/en/vs-code ("Launch a VS Code tab from other tools"). The handler arrived in 2.1.72 (anthropics/claude-code#32687).
- **The Claude desktop app.** It registers `claude://`.
  - **The routes.** `claude://claude.ai/new`, `claude://claude.ai/chat/<id>`, `claude://code/new?folder=<abs>&q=<text>` and `claude://cowork/new`. A folder from a link is always confirmed in the app.
  - **No resume link.** "The Code and Cowork routes don't accept an ID for resuming an existing session" (support.claude.com, "Open Claude Desktop with a link").
  - **The CLI route.** `claude --desktop --resume <id>` hands a CLI session to the app. It needs Claude Code ≥ 2.1.285, macOS or x64 Windows, and a subscription login.
- **The decision.** `launch.claude = auto | cli | vscode | desktop` (see the release notes and PLAN D113).
  - **Argv vectors** (tests pin them per OS):
    - `vscode`: `[<editor cli>, <folder>]`, waited, then `[xdg-open|open, <scheme>://anthropic.claude-code/open?session=<id>]`, or `[rundll32.exe, url.dll,FileProtocolHandler, <uri>]` on Windows.
    - `desktop`: `cwm sessions resume <id> --with desktop`, which runs `claude --desktop --resume <id>` in the session's folder, or the OS opener on `claude://code/new?folder=<abs>`.
- **Unverified on a real machine.** These are flagged as risks:
  - `code.cmd <folder>` followed by the URI when VS Code was not running (the URI can reach the window before the folder finishes loading);
  - `claude --desktop --resume` on a Windows Store install.


---

# v0.9.2 (as implemented)

v0.9.2 is a bug-fix release on v0.9.1. It fixes three problems from a real Windows 11 machine running cwm 0.9.1. That machine has a standalone claude 2.1.332, the Store desktop app with Claude Code 2.1.330, and the VS Code extension. It has two accounts, `default` and `work`, with `default_account = work` and `launch.claude = desktop`.

## Release notes (v0.9.2)

### Fixed

- **Resume in the Claude desktop app works from the dashboard (D117).** It used to fail with "--desktop … can't run non-interactively (… or redirected output)". The reason: the dashboard ran `claude --desktop --resume <id>` as a background child with captured output.
  - **The fix.** The hand-off now opens in a terminal window, the same way Auto does. The window closes itself when the hand-off succeeds.
  - **When it can't hand off.** If no terminal can be opened, or the hand-off fails, cwm opens the app in the session's folder. It also shows `claude --resume <id>` for you to copy, and says what happened.
  - **From the CLI.** `cwm sessions resume <id> --with desktop` from your own terminal works as before.
- **Start claude for an account never opens the desktop app as someone else (D118).** The Claude desktop app has its own sign-in, and cwm's accounts don't reach it.
  - **What happens now.** When Start claude, or `cwm run --account <name>`, would use the desktop app, cwm opens that account in a terminal instead. The toast explains why: "the Claude desktop app keeps its own sign-in, so cwm can't open work there — opened it in a terminal instead".
  - **Labels.** In Settings, the desktop option says it "uses the app's own sign-in, not a cwm account". On an account's Start button, the ▾ menu marks it "(app's own login)".
  - **Resume is unchanged.** Resume in the desktop app is still allowed; the toast notes that the app uses its own sign-in.
- **The WIP auto-push no longer touches your branch (D119).** In cwm ≤ 0.9.1, the SessionEnd hook ran `git add -A` and committed `wip: <host> <time>` onto your current branch. That swept in nested repositories, including Claude Code's own `.claude/worktrees/…`, as gitlinks. When the push then failed, the commit stayed on your branch.
  - **How the snapshot works now.** It is built in a temporary index and committed with `git commit-tree`, so your HEAD, index and files are never changed. It is pushed to `sync/<host>`, non-force, as before. A rejected push leaves nothing behind except a line in `logs/wip.log`.
  - **What it skips.** Nested repositories and worktrees, Claude Code's `.claude/worktrees/`, and any gitlink your HEAD doesn't already have. The log says "skipped N nested repositories (…)".
  - **Old commits.** `cwm doctor` warns when a repo's HEAD is a WIP commit that cwm ≤ 0.9.1 left there. The warning gives the undo: `git reset HEAD~1`, which keeps your files.
  - **Picking up on another device** works the same way: `git pull origin sync/<host>`.

## Research notes (v0.9.2)

### What `claude --desktop` checks (D117)

- **Method.** We read the strings in Claude Code 2.1.319 (Linux, the copy on the build box) and ran it in a temp HOME, under `script` for a pty, with no login. Each run redirected one stream.
- **Results:**
  - Redirecting **stdout** gives "--desktop opens the Claude Desktop app and can't run non-interactively (`--print`, `--sdk-url`, `--init-only`, or redirected output)". That is the reported error.
  - Redirecting **stdin** gives `stdin_not_tty`: "--desktop doesn't take piped or redirected input yet".
  - Redirecting **stderr** is fine; the run reaches the platform check ("isn't available on this platform. It works on macOS and Windows (x64)").
- **Conclusion.** Both stdin and stdout must be terminals.
- **Why a terminal, not a hidden console.** Go's `os/exec` on Windows always passes standard handles. A nil `Stdin`/`Stdout` is the NUL device, never the console, so `CREATE_NEW_CONSOLE` with `SW_HIDE` would still fail. A real terminal (wt.exe, or `cmd /c start`) works, and it is what Auto already uses.

### Where the desktop app keeps its sign-in (D118)

- **Where it lives:**
  - Windows installer build: `%APPDATA%\Claude\`, with `config.json`, Local Storage and Cookies.
  - Microsoft Store build: the same tree under `%LOCALAPPDATA%\Packages\Claude_<hash>\LocalCache\Roaming\Claude\`.
  - macOS: `~/Library/Application Support/Claude/`.
- **What records the sign-in.** Only `oauth:tokenCache` in `config.json`. Anthropic's data-storage docs say tokens there are encrypted with the OS secure store (DPAPI or Keychain), and the cookies are secrets too. We found no plain account id or email field.
- **Conclusion.** cwm reads none of it, so it cannot tell which account the app uses.
- **The decision table** (`claudeopen.RouteAccount`, tested):

  | requested account | app's account | opens in |
  |---|---|---|
  | none | any | the desktop app |
  | `work` | unknown (always, today) | a terminal as `work` |
  | `work` | known, `work` | the desktop app |
  | `work` | known, another | a terminal as `work`, and the toast names the other account |
