# cwm Web UI — designer review for v0.7.2 (copy of the authoritative spec)

Reviewed: the v0.6.2 design pack (all pages, dark/light, mobile) and the v0.7.1 Stats screens.

Priority tags:
- **MUST**: fix before the next release.
- **SHOULD**: next minor release.
- **NICE**: when time allows.

## OWNER DECISIONS (these override every "options/lean" below)

- **Desktop navigation = C.** tmux-style top tabs plus a Ctrl-K command palette styled as a shell prompt (`cwm ❯ resume api-gateway`).
- **About = a modal/drawer.** It opens from the statusline version segment, the "by maher" link, and the `?` help dialog. It is NOT a tab.
- **WIP tab is renamed "handoff".** Explainer: "Work you left on another machine that is not pushed yet. Open it here to continue."
- **Mobile (< 720 px) = bottom bar.** Items: sessions, projects, health, accounts, more. "more" opens a sheet with the remaining sections, theme and motion.
- **Author metadata: name "Maher", GitHub https://github.com/Mr-JoE1 only.**
  - Repo link https://github.com/Mr-JoE1/cwm is fine.
  - NO LinkedIn, NO full name, NO company in the About view or /api/about.
  - The statusline says "by maher".
  - The author fields are hard-coded constants in internal/buildinfo.
- **Heatmap always shows 12 months, titled "activity, last 12 months".** It ignores the range selector, which applies only to the KPIs and breakdowns.
- **Update check: manual only.** No automatic network calls.
  - /api/about returns update.enabled=false by default.
  - The About view has a "check for updates" button. Only on click does the AGENT (never the browser) query https://api.github.com/repos/Mr-JoE1/cwm/releases/latest with a 5 s timeout, and the result is shown inline.
  - If that is too complex, omit the button entirely and keep enabled=false.
- **Token kinds = option A.** Two bars: "billed" (input + output) and "cache" (write + read).
- Paths always replace the home folder with `~`.

## 1. Summary

The base is strong:
- a real token system, with the design variables kept in one place
- both themes
- keyboard shortcuts
- reduced-motion support
- small asset size

The "cyber terminal" direction is right for the audience. The style is not the problem. These are:

- **The theme is decoration, not vocabulary.** It lives in colours and glow, but the structure is a generic SaaS dashboard: cards, tables, uppercase labels.
  - Fix it by borrowing real terminal conventions developers already read fluently: the tmux window list, the systemd boot log, `git log --graph`, neofetch, the vim/tmux statusline and shell prompt paths.
- **Light mode has no identity.** It is dark mode with inverted colours. It needs its own hacker reference: the "green-bar printout".
- **Copy is written from the system's point of view.** For example, Health says "no workspace settings.json (not used: the Go kit writes…)". Users need: what is wrong, why it matters, and one command to fix it.
- **Some data contradicts itself.** An Accounts card shows "LOGGED IN" with "last login: never". Devices shows an error message inside the device-ID field.

### Contrast audit (WCAG)

| Pair | Ratio | Result |
|---|---|---|
| dark muted #8593a8 on card | 6.07 | passes AA |
| light muted #4a5568 on card | 7.53 | passes |
| light accent-2 #007c91 on white | 4.89 | passes, barely |
| light card/input border #d4dde8 on white | 1.37 | **fails** 3:1 for controls (WCAG 1.4.11) |
| light "cache read" swatch on white | 1.66 | **fails** 3:1 for chart marks |
| dark accent #00ff9c on card | 14.23 | passes, but glares on large fills |

The "faint grey text" comes from two things: (a) 10–11 px uppercase letter-spaced mono labels, and (b) background canvas lines showing through cards that are only 80–88 % opaque. Fix size and opacity first.

## 2. Design direction

### 2.1 Dark — "phosphor terminal"
- Keep near-black with mint, cyan and magenta.
- Use #00ff9c only for text and small marks. Large fills use `--accent-fill: #00c97b`, with ink #00170d (8.5:1).
- Scanlines in dark only, at opacity about .28.
- Glow (text-shadow) only on the wordmark and the active tab.

### 2.2 Light — "green-bar printout"
- Paper #eef2f1, cards #ffffff, and band #eaf5ee on even table rows and log lines.
- No scanlines and no glow. Crisp ink, deep green #007a4d.
- Control borders #7d8ea3 (3.35:1). Decorative card borders may stay light.

### 2.3 Terminal vocabulary map

| Today | Proposal |
|---|---|
| Tabs `SESSIONS 1` | tmux window list: `[cwm] 1:sessions 2:projects … 7:stats*`. `1:` reads as a shortcut index, and `*` marks the active tab. |
| `# Sessions` title | shell path `~/cwm/sessions` |
| Health pills OK/WARN | systemd boot log `[  OK  ]` `[ WARN ]` `[FAILED]`, problems sorted first |
| Footer text line | tmux/vim statusline with segments |
| (none) | neofetch-style About panel |
| (none) | `git log --graph` release timeline |
| Uppercase tracked labels | lowercase mono labels |

### 2.4 Tokens

```json
{
  "type":   { "xs": 11, "sm": 12, "md": 13, "base": 14, "lg": 16, "xl": 20, "2xl": 26,
              "rule": "mono for data, labels, commands, titles; sans for prose. Min 12px for any text the user must read." },
  "space":  [4, 8, 12, 16, 24, 32, 48],
  "radius": { "lg": 10, "sm": 6, "xs": 3 },
  "dark": {
    "bg": "#05080d", "surface": "#0a1018f0", "surface-solid": "#0a1018", "surface-2": "#0f1722",
    "text": "#d9e4f2", "text-strong": "#f2f8ff", "muted": "#9aa8bd", "faint": "#74849c",
    "accent": "#00ff9c", "accent-fill": "#00c97b", "accent-ink": "#00170d",
    "accent-2": "#00e5ff", "magenta": "#ff3cd8", "ok": "#00ff9c", "warn": "#ffc857", "fail": "#ff5c7a",
    "line": "rgba(0,229,255,.16)", "control-line": "#526a88", "band": "transparent"
  },
  "light": {
    "bg": "#eef2f1", "surface": "#fffffff5", "surface-solid": "#ffffff", "surface-2": "#f6f9f8",
    "text": "#0b1220", "text-strong": "#050a14", "muted": "#4a5568", "faint": "#66758a",
    "accent": "#007a4d", "accent-fill": "#007a4d", "accent-ink": "#ffffff",
    "accent-2": "#007c91", "magenta": "#c4148f", "ok": "#007a4d", "warn": "#8a5a00", "fail": "#c0182f",
    "line": "#d3dde3", "control-line": "#7d8ea3", "band": "#eaf5ee"
  }
}
```

- All text pairs are ≥ 4.5:1.
- `faint` is ≥ 4.4:1 and is used only for non-essential hints.
- `control-line` is ≥ 3:1 in both themes.

## 3. Page-by-page

### Global frame
- **MUST** Cards are ≥ 94 % opaque, and the header and footer are solid. Background lines never cross text.
- **MUST** Remove the outdated "Ctrl-C in the terminal stops the server".
- **MUST** The footer becomes a statusline (§4).
  - When ldflags are missing, show "dev build <short commit>" from `debug.ReadBuildInfo()`.
  - Never show "dev … dev".
- **SHOULD** tmux tab bar. Show a badge only when action is needed, e.g. `5:health ③`.
- **SHOULD** Header icons:
  - The sparkle icon becomes a labelled "motion" toggle.
  - Remove the bug icon from the header; it lives in the statusline and Health.
- **SHOULD** Connection pill shows "live 2s ago" / "connecting…" / "offline – retry". When offline, also show a top banner with a Retry button.
- **NICE → DO (owner chose C)** Ctrl-K command palette styled as a shell prompt.

### Sessions
- **MUST** Lead with the prompt, then the project. Drop the N column.
- **SHOULD** Group rows by day (today / yesterday / this week / date) with a magenta divider row.
- **SHOULD** Resume button:
  - shows on row hover/focus, always on touch screens;
  - Enter on a focused row resumes.
- **SHOULD** Live marker "● open on laptop" if the data exists. Otherwise add the field to the API only if it is cheap; skip it if not.
- **SHOULD** Empty filter state: `no sessions match "foo"`, a "clear filter" button, and the hint "search covers project and first prompt".
- **NICE** Preview drawer on row click.

### Projects
- **SHOULD** Cards showing name, repo, last active, session count and a 30-day sparkline.
  - Fold handoff into Projects only if the tab count reaches 9. Today it stays a tab named "handoff".

### Devices
- **MUST** Never show an error inside the "This device" ID field. If Syncthing is down, hide the pair form and show only the setup card.
- **MUST** The setup card is onboarding, not a warning. Three numbered steps:
  1. install Syncthing
  2. `cwm pair --start`
  3. pair the other device
- **SHOULD** Per-device states: online / syncing 64 % (progress bar) / out of date 2h / offline since ….
- **SHOULD** The device ID is grouped (ABCDEFG-HIJKLMN-…) with a copy button.

### handoff (was WIP)
- **MUST** Rename it and add the explainer above.

### Health
- **MUST** Summary first: "3 things to fix" or "all 10 checks passed".
- **MUST** Sort fail → warn → ok, and collapse the passing checks into "7 checks passed (show)".
- **MUST** Each check reads: plain title, a one-line why, and a copyable command. Raw paths go behind a "details" toggle.
- **MUST** Build a standard copyable-command component and use it everywhere.
- **SHOULD** Boot-log status column.

### Accounts
- **MUST** Never show "logged in" next to "last login: never". Show "logged in, time unknown" instead.
- **MUST** Use the grid `repeat(auto-fill, minmax(320px,1fr))`. "+ add account" is a dashed card in the same grid.
- **MUST** Remove-confirmation modal:
  - explains what happens, including the backup path;
  - has an "also delete the folder" checkbox;
  - asks the user to type the account name to confirm.
- **SHOULD** The default account has a "default" tag and an accent left border. A ⋯ menu holds: log in again, make default, remove.
- **SHOULD** Add flow: name → login → success, ending with "make default?".

### First run
- **MUST** Two columns:
  - Left: a vertical step rail.
  - Right: a live terminal log of what each step is doing.
- **SHOULD** The Done screen shows next actions and key hints.
- **NICE** When Done appears, the hex logo strokes itself in once. Skipped with reduced motion.

### Stats
- **MUST** The heatmap is titled "activity, last 12 months" (owner decision).
- **MUST** "est. cost" uses `--text-strong` with a "≈" prefix. No status colours for money.
- **MUST** Token kinds are shown as two bars, billed and cache.
- **MUST** The light "cache read" swatch must be ≥ 3:1.
- **SHOULD** Heatmap colours:
  - a 5-step ramp from surface-2 to accent-fill, with no pure #00ff9c top level in dark;
  - empty cells must differ from level 1;
  - the grid fills the panel width.
- **SHOULD** The top projects/models bars are solid, with no gradient.
- **NICE** Sparklines in the KPI tiles.
- Mobile: tab fade edges, the active tab scrolled into view, and the range control on its own row under the title.

### Mobile
- **MUST** Bottom bar (owner decision).
- **MUST** The header holds the logo, a connection dot and a ⋯ menu. Theme, motion and debug go in the menu.
- **SHOULD** Session cards: drop N and use one meta line, `api-gateway · main · 86 msgs`. Long branch names are ellipsized with a title tooltip.

### Favicon / icons
- **MUST** 16 px favicon: a filled hexagon with the prompt knocked out. Keep the full mark at ≥ 32 px.

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><path fill="#00c97b" d="M8 .5 14.5 4.25v7.5L8 15.5 1.5 11.75v-7.5Z"/><path d="m5 5.2 2.8 2.8L5 10.8" fill="none" stroke="#05080d" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><rect x="8.6" y="9.6" width="3.2" height="1.6" fill="#05080d"/></svg>
```

- **SHOULD** One SVG `<symbol>` sprite:
  - 16-unit grid, 1.5 px stroke, round caps, currentColor;
  - icons: copy, refresh, motion, bug, sun, moon, auto, play, branch, device, sync, account, warn, ok, fail, external, more, terminal.

## 4. Developer & version metadata

### Statusline (every page)
- Left: `[cwm v0.7.2] linux/amd64 │ ● agent up 3h12m │ 127.0.0.1:47800, localhost only`
- Right: `copy debug logs │ ? keys │ by maher`

### About view (modal, neofetch layout)
- Left: a big hex mark.
- Right:
  - `maher@cwm`, then a dashed rule;
  - key/value lines;
  - a row of 9 swatches read live from the theme tokens;
  - buttons: copy version info, release notes, report a bug, and the optional "check for updates".

### Release timeline (`git log --graph` style)
- v0.8: next (dashed)
- v0.7.2: HEAD → this build
- v0.7.1, v0.6.2, and earlier releases

### GET /api/about

```json
{
  "version": "0.7.2", "channel": "beta", "commit": "a1b2c3d", "dirty": false,
  "built_at": "…", "go": "go1.x", "os": "linux", "arch": "amd64",
  "agent": { "pid": 4182, "started_at": "…", "listen": "127.0.0.1:47800" },
  "workspace": { "path": "~/.cwm/workspace", "repos": 5, "devices": 3, "accounts": 2 },
  "author": { "name": "Maher", "github": "https://github.com/Mr-JoE1", "repo": "https://github.com/Mr-JoE1/cwm" },
  "license": "<SPDX id from LICENSE>",
  "releases": [ { "v": "0.8", "state": "next", "note": "In-browser terminal, full CLI parity" },
                { "v": "0.7.2", "state": "current", "note": "UI refresh: terminal vocabulary" },
                { "v": "0.7.1", "note": "Stats" }, { "v": "0.6.2", "note": "Accounts page, sync setup card" } ],
  "update": { "enabled": false, "latest": null, "checked_at": null }
}
```

## 5. Work packages (the designer's dev-agent prompts)

### P0 — context and hard constraints (never break)
- No frameworks, no CDN, no runtime web fonts. Use the existing system font stacks.
- Total web assets < 150 KB. They are already ~136 KB after v0.7.1, so the budget is TIGHT: refactor and delete dead CSS/JS as you go, and report the total after each package.
- Strict CSP:
  - no external requests;
  - no inline event handlers, no inline `<script>`;
  - JS lives in .js files and uses addEventListener.
- Layout and access:
  - works from 390 to 1920 px and keyboard-only;
  - every control has a visible `:focus-visible` ring;
  - respect prefers-reduced-motion: no animation at all when it is set.
- Themes: dark, light and system.
  - Every text pair ≥ 4.5:1.
  - Control borders and chart marks ≥ 3:1, in both themes.
- Components reference tokens only, with no raw hex in component rules.
- The background canvas pauses when idle and when motion is off.
- Copy rules:
  - sentence case and plain verbs;
  - lowercase mono labels;
  - an action keeps its name through the flow ("copy" → "copied");
  - errors say what happened and give the fix command.

### P1 — tokens and type/spacing scale (MUST)
- Replace the token block with §2.4:
  - `--fs-xs..--fs-2xl`, `--s1..--s7`, `--r-lg/--r-sm/--r-xs`;
  - `--accent-fill`, `--faint`, `--control-line`, `--band`.
- Keep the old names as aliases for one release, then migrate the components.
- `--accent-fill` is for large fills; `--control-line` is for input and button borders.
- Surfaces are ≥ .94 alpha.
- Minimum 12 px for readable text. Labels are lowercase mono with letter-spacing ≤ .02em.
- **Acceptance:** `scripts/contrast-check.js` (node, no deps)
  - computes WCAG ratios for every text/surface and control/surface pair in both themes;
  - fails if text < 4.5 or controls < 3.0;
  - runs in CI.

### P2 — global frame (MUST/SHOULD)
1. tmux tabs:
   - a `[cwm]` prefix in `--magenta`;
   - each tab reads `<k>:<name>`, with `<k>` in `--faint`;
   - the active tab is `--accent`, with a 2 px bottom border and a `*` suffix in `--magenta`;
   - badges only when action is needed;
   - horizontal scroll with mask-image fade edges, and `scrollIntoView({inline:'nearest'})` for the active tab;
   - keep role=tablist/tab, aria-selected, arrow-key navigation and the number shortcuts.
2. Page titles are shell paths: `~/cwm/` in `--accent` plus the name in `--text-strong` (h1, mono, `--fs-xl`).
3. Header right:
   - the connection pill (live Ns ago / connecting… / offline – retry, plus an offline banner with Retry);
   - the theme segmented control labelled auto/dark/light;
   - the labelled "motion" toggle;
   - refresh;
   - no bug icon.
   - Under 520 px: logo, connection dot, refresh and a ⋯ menu.
4. Statusline:
   - 28 px, solid, fixed to the bottom; main gets bottom padding;
   - the segments from §4;
   - hide low-priority segments at 860 px and 520 px;
   - delete the Ctrl-C text.
5. Ctrl-K command palette:
   - a shell-prompt input `cwm ❯`, fuzzy over sections, actions and session prompts/projects;
   - Enter runs the action; Esc closes and focus returns to the trigger.
- **Acceptance:**
  - a keyboard-only walk through all tabs works;
  - no overlaps at 390, 768 or 1440 px;
  - the statusline never covers the last content row.

### P3 — About modal, /api/about, release graph
- **Backend:**
  - `internal/buildinfo` with Version, Commit and BuildDate set via ldflags (wire them into the release ldflags too);
  - fall back to `debug.ReadBuildInfo()` (vcs.revision short 7, vcs.time, vcs.modified → dirty) and show "dev build a1b2c3d", never "dev (os/arch, dev)";
  - author constants as in the OWNER DECISIONS;
  - license = the SPDX id from the embedded LICENSE;
  - GET /api/about with the schema in §4, home path → `~`, no network unless the user clicks "check for updates".
- **Frontend (modal):**
  - left: the hex mark, ~148 px (84 px on mobile);
  - right: `maher@cwm` (`@` in `--magenta`, host in `--accent`), a dashed rule, then a `<dl>` of mono key/value lines with keys in `--accent`:
    - version (beta tag)
    - commit and build time
    - runtime
    - agent
    - workspace
    - author
    - github
    - license
  - the 9 live swatches;
  - buttons:
    - "copy version info" (primary): copies a one-line summary plus the JSON;
    - "release notes": an external link with target=_blank rel=noopener noreferrer;
    - "report a bug";
    - "check for updates" (optional).
- **Release graph:**
  - a vertical magenta line with ring nodes;
  - the current build has a filled accent node with a "HEAD → this build" tag;
  - the next release is drawn dashed with a grey ring and a "next" tag.
- **One motion moment:** the key/value lines fade in at 70 ms per line. None with reduced motion.
- **Acceptance:**
  - copy works by mouse and keyboard, and shows "copied" for 1.4 s;
  - links open in a new tab;
  - contrast passes in both themes;
  - no overflow at 390 px.

### P4 — Health boot log and the copyable-command component (MUST)
1. Copyable-command component:
   - `[$ command | copy]`: mono, `--surface-2`, `--control-line` border, horizontal scroll;
   - the button says "copy" → "copied" for 1.4 s, announced via aria-live=polite;
   - use it everywhere: Health, Devices, Accounts, First run and the claude toasts.
2. Health:
   - a summary bar: "N things to fix" in `--warn` on `--warn-soft`, or "all N checks passed" in `--ok`;
   - sort fail → warn → ok, with the passing checks collapsed;
   - each row: the status column `[  OK  ]` / `[ WARN ]` / `[FAILED]` (mono, bold, status colour), a plain title, a one-line why in sans `--muted`, then the copyable command;
   - technical detail goes in a `<details>` "details" toggle.
3. Rewrite the check copy and keep the check IDs. Examples:
   - claude: "claude not found" / "Claude Code is not on PATH or in the usual install folders." / the official install command for this OS
   - syncthing: "sync is not set up" / "Optional. Syncthing keeps the workspace the same on all your devices." / `cwm pair --start`
4. The light theme gets band rows.
- **Acceptance:**
  - the health tab badge equals the warn + fail count;
  - a screen reader reads the status, then the title.

### P5 — Sessions, Accounts, Devices, handoff (MUST/SHOULD)

Implement exactly the §3 items for these pages.

**Sessions**
- Columns: prompt (text-strong) | project (mono muted) | branch (magenta, ellipsis and title) | msgs (right-aligned) | id (accent-2) | resume.
- Day groups.
- Resume appears on hover/focus-within, and always under `@media (hover:none)`.
- Enter resumes.
- The empty filter state.
- Light-theme banding.

**Accounts**
- Fix the contradiction, use the grid with the dashed add card, the default tag and border, and the ⋯ menu.
- Remove modal:
  - type the name to enable the red "remove account" button;
  - Esc closes and focus returns to the trigger;
  - it uses the existing remove API with also_delete_config_dir.

**Devices**
- Setup-card onboarding with copyable commands.
- No error text in the ID field.
- Device states, and the grouped ID with a copy button.

**handoff**
- The rename and the explainer.
- Keep the old `#wip` hash working: it redirects to `#handoff`.

### P6 — First-run wizard (MUST)
- Two columns at ≥ 960 px:
  - left: the step rail, with done/current/next states;
  - right: the step card above a live terminal-log panel (`$ cwm init`, its output lines, then `[  OK  ]` or `[FAILED]` plus the fix command).
- "Skip setup" sits under the rail.
- Done screen: "cwm is ready", the next actions and the key hints.
- On Done only, the hex stroke draws in (600 ms). None with reduced motion.
- **Acceptance:**
  - fills 1440×900 with no large empty areas;
  - works keyboard-only;
  - every command is copyable.

### P7 — Stats fixes (MUST/SHOULD)
- The heatmap is titled "activity, last 12 months" and keeps its fixed 12 months.
- Heatmap colours:
  - a 5-step ramp per theme from `--surface-2` to `--accent-fill`;
  - empty cells differ from level 1 (≥ 3:1 between empty and the top level).
- The grid stretches to the panel width: cell size computed from width, min 9 px, gap 2 px. Keep the tooltip and the roving-tabindex keyboard focus.
- est. cost: `--text-strong` with a "≈" prefix.
- Two token bars, billed and cache, with value labels. Legend swatches ≥ 3:1.
- Bars are solid `--accent-2` with no gradient, a 4 px rounded end, and values in `--text-strong`.
- Mobile: the range control on its own row.
- NICE: sparklines.

### P8 — Mobile navigation, favicon, icon sprite (MUST/SHOULD)
- Bottom bar under 720 px:
  - 56 px tall, icons 20 px with labels;
  - safe-area-inset padding;
  - the active item in `--accent`;
  - "more" opens a sheet.
- Header under 520 px: logo, connection dot and a ⋯ menu.
- Session cards on mobile:
  - one meta line;
  - Resume always visible.
- The 16 px favicon exactly as in §3, keeping the full mark for 32 px and up.
- The icon sprite.
- Every icon-only button gets an aria-label and a title.
- **Acceptance:**
  - 390×844 screenshots of every tab in both themes;
  - nothing closer than 8 px to the screen edge;
  - tap targets ≥ 40 px.

### P9 — final QA
1. The contrast script, in both themes.
2. A keyboard-only walk through every tab, modal and menu.
3. Reduced motion: no animation runs and the canvas is stopped.
4. CSP: zero inline handlers or scripts, and zero external URLs except the target=_blank links.
5. Asset total < 150 KB.
6. Screenshots at 390, 768 and 1440 px, dark and light, for every view.

Report failures as a table: view | issue | fix.
