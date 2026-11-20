# SPEC v0.7.2: UI refresh, terminal vocabulary

v0.7.2 is a dashboard refresh. The authoritative design input is the
designer review copied to `docs/ui/DESIGN-REVIEW-V0.7.2.md`; its "OWNER
DECISIONS" section overrides every option or "lean" elsewhere in it. This
spec says how cwm implements it. Decisions D67–D74 are logged in
`docs/PLAN.md`. The companion `cwm-ui-reference.html` mentioned by the
designer was never delivered: everything is built from the written review.

Compatibility rule as before: flags, env vars, exit codes and existing
`/api/*` shapes only gain fields. `GET /api/about` is new. `/api/wip` keeps
its name although the tab is now called "handoff".

## Constraints (P0, always)

- No frameworks, no CDN, no runtime web fonts, no build step; the system
  font stacks only.
- Asset budget (D71): everything under `internal/gui/web/` stays at or
  below **200 KB raw** and **below 60 KB gzip** in total. Dead CSS/JS is
  deleted while refactoring.
- CSP unchanged (`default-src 'none'; script-src 'self'; style-src 'self';
  connect-src 'self'; img-src 'self' data:`…): no inline handlers or
  scripts, no external requests from the browser. The only external URLs
  in the assets are GitHub links opened with `target="_blank"
  rel="noopener noreferrer"`.
- API text reaches the page through DOM nodes and `textContent` only. A
  grep for `innerHTML`, `insertAdjacentHTML`, `outerHTML` and
  `document.write` across `internal/gui/web/*.js` finds nothing.
- 390–1920 px, keyboard-only use, a visible `:focus-visible` ring on every
  control, `prefers-reduced-motion` means no animation at all.
- Themes dark, light, system. Text pairs ≥ 4.5:1, control borders and
  chart marks ≥ 3:1, checked by `scripts/contrast-check.js` in CI.
- Component rules reference tokens only (no raw hex outside the token
  blocks).
- Copy: sentence case, plain verbs, lowercase mono labels, an action keeps
  its name through its flow ("copy" → "copied"), errors say what happened
  and give the fix command.

## A: tokens (P1, D68)

The token block of review §2.4 replaces the old one: type scale
`--fs-xs..--fs-2xl` (11/12/13/14/16/20/26 px), spacing `--s1..--s7`
(4/8/12/16/24/32/48 px), radii `--r-lg/--r-sm/--r-xs` (10/6/3 px) and the
colour tokens of both themes including `--accent-fill`, `--faint`,
`--control-line` and `--band`. The old names (`--radius`, `--radius-sm`,
`--gap`, `--line-strong`…) stay as aliases for this release. Surfaces are
≥ .94 alpha; the header and the statusline are solid. Readable text is
≥ 12 px; labels are lowercase mono with letter-spacing ≤ .02em.

Dark ("phosphor terminal"): `#00ff9c` only for text and small marks, large
fills use `--accent-fill` with `--accent-ink`; scanlines in dark only;
glow only on the wordmark and the active tab. Light ("green-bar
printout"): paper `#eef2f1`, white cards, `--band` on even rows and log
lines, no scanlines, no glow.

`scripts/contrast-check.js` (node, no deps) reads the token blocks out of
`app.css`, composites translucent surfaces over `--bg`, and checks every
text/surface pair (≥ 4.5) and every control/chart-mark pair (≥ 3.0) in both
themes. `--faint` is checked at 4.4 (non-essential hints only). It exits
non-zero on a failure and runs in CI.

## B: frame (P2, D67)

- **Tabs** are a tmux window list: `[cwm]` in `--magenta`, then
  `1:sessions 2:projects 3:devices 4:handoff 5:health 6:accounts 7:stats`;
  the digit in `--faint`, the active tab in `--accent` with a 2 px bottom
  border and a `*` in `--magenta`. A badge appears only when action is
  needed (health: warn + fail count; accounts: default account without a
  login). The bar scrolls horizontally with mask-image fade edges and keeps
  the active tab in view. role=tablist/tab, aria-selected, arrow keys and
  `1`–`7` stay.
- **Page titles** are shell paths, `~/cwm/` in `--accent` plus the name in
  `--text-strong` (h1, mono, `--fs-xl`).
- **Header right**: connection pill (`live 2s ago` / `connecting…` /
  `offline – retry`; offline also shows a banner with a Retry button), the
  theme control labelled auto / dark / light, a labelled `motion` toggle,
  refresh. The bug icon moves to the statusline and Health.
- **Statusline**: 28 px, solid, fixed to the bottom; main reserves its
  height. Left `[cwm v0.7.2] linux/amd64 │ ● agent up 3h12m │
  127.0.0.1:47800, localhost only`; right `copy debug logs │ ? keys │ by
  maher`. Uptime reads `12s` under a minute, then `3m`, `3h12m`, `2d4h`
  (one formatter, also in About). Low-priority segments hide at 860 px and 520 px. The old "Ctrl-C
  in the terminal stops the server" text is gone.
- **Ctrl-K palette** (also `Cmd-K`): a dialog with a shell-prompt input
  `cwm ❯`, fuzzy-matching sections, actions (theme, motion, refresh, copy
  debug logs, about, keys) and sessions (prompt + project → resume). Enter
  runs the highlighted entry, arrows move, Esc closes and focus returns to
  the element that opened it.

## C: build info and About (P3, D69)

`internal/buildinfo` holds `Version`, `Commit` and `BuildDate`, set by the
release ldflags (`.goreleaser.yaml`, both builds; the install harnesses set
them too). `cli.Version` / `cli.Commit` keep working as overrides for
older build scripts. Without ldflags, `debug.ReadBuildInfo()` supplies
`vcs.revision` (short, 7), `vcs.time` and `vcs.modified` (dirty); the
version is then reported as a dev build and the UI shows `dev build
a1b2c3d`, never `dev … dev`. `cwm version` keeps its format `cwm <ver>
(<goos>/<goarch>, <commit>)`.

Author constants (owner decision): name `Maher`, GitHub
`https://github.com/Mr-JoE1`, repo `https://github.com/Mr-JoE1/cwm`.
Nothing else about the author is shipped.

`GET /api/about` (authenticated like every GET):

```json
{
  "version": "0.7.2", "channel": "beta", "commit": "a1b2c3d", "dirty": false, "dev": false,
  "built_at": "2026-11-21T10:00:00Z", "go": "go1.25.x", "os": "linux", "arch": "amd64",
  "agent": { "pid": 4182, "started_at": "…", "listen": "127.0.0.1:47800", "uptime_s": 11520 },
  "workspace": { "path": "~/.cwm/workspace", "repos": 5, "devices": 3, "accounts": 2 },
  "author": { "name": "Maher", "github": "https://github.com/Mr-JoE1", "repo": "https://github.com/Mr-JoE1/cwm" },
  "license": "MIT",
  "releases": [ { "v": "0.8", "state": "next", "note": "…" }, { "v": "0.7.2", "state": "current", "note": "…" }, … ],
  "update": { "enabled": false, "latest": null, "checked_at": null }
}
```

Paths replace the home folder with `~`. `license` is the SPDX id
recognised from the embedded LICENSE text. `devices` is null when
Syncthing is not reachable. Nothing touches the network.

`POST /api/about/check-update` (CSRF like every POST) is the only network
call the agent makes for the dashboard, and only when the user presses
"check for updates": `GET https://api.github.com/repos/Mr-JoE1/cwm/releases/latest`
with a 5 s timeout, through an injectable HTTP client (tests use a fake
transport). The answer is `update` with `enabled: true`, `latest` (tag
without `v`), `newer`, `url` and `checked_at`, or a 502 with the error.
The browser never contacts GitHub itself.

The About modal opens from the statusline version segment, the `by maher`
link, the `?` dialog and the palette. Layout (neofetch): hex mark left
(148 px, 84 px on mobile); right `maher@cwm`, a dashed rule, a `<dl>` of
mono key/value lines (version + beta tag, commit + build time, runtime,
agent, workspace, author, github, license), nine live swatches read from
the theme tokens, and buttons: copy version info (one-line summary plus
the JSON), release notes, report a bug, check for updates, in a
deliberate 2×2 grid. Below, a `git log --graph` release timeline: the
next release dashed with a grey ring, a dashed connector down to this
build and a `next` tag, this build a filled accent node tagged `HEAD →
this build`; tags follow the description, so every description starts at
the same x. The key/value lines fade in 70 ms apart (none with reduced
motion).

## D: copyable commands and Health (P4, D72)

One component, `[$ command | copy]`: mono, `--surface-2`, a
`--control-line` border, horizontal scroll; the button says copy →
copied for 1.4 s, announced through a polite live region. Health, Devices,
Accounts, the first-run wizard and the toasts use it.

Health: a summary bar first ("N things to fix" in `--warn`, or "all N
checks passed" in `--ok`); fail → warn → skip → ok; passing checks
collapsed behind "N checks passed" (the disclosure triangle says the
rest). Each row: a boot-log status column `[  OK  ]` / `[ WARN ]` /
`[FAILED]` / `[ SKIP ]` (`--muted`), a plain title, a one-line why, the
copyable fix command, and the raw doctor detail behind a `details`
toggle. Every row has the same padding and the zebra band fills it. The
plain copy lives in the dashboard, keyed by the doctor check id; ids are
unchanged and `/api/doctor` only gains the additive `optional` field.
The health tab badge is the warn + fail count.

Doctor levels (polish pass, D75):
- A check that is optional by design carries `"optional": true` (today
  only `syncthing`). Not set up, it keeps `"status": "warn"` in `--json`
  and `/api/doctor` for compatibility, but it is `[ SKIP ]` in the
  dashboard, `skip:` in `cwm doctor`, and counts toward neither "N things
  to fix" nor "N of M checks passed" (M excludes it). The text summary
  gains ", N skipped" only when N > 0.
- `claude not found` (no verified Claude Code binary at all, with or
  without rejected candidates) is `FAIL:` / `[FAILED]`, so `cwm doctor`
  exits 1 without it. A verified claude that is only off PATH
  (`claude-binary`) and an old version stay warnings.
- No other check, output line or exit code changes.

## E: pages (P5, D70)

- Sessions: prompt | project | branch | msgs | id | resume; no N column;
  day groups (today / yesterday / this week / date); resume shown on
  hover and focus-within, always on touch; Enter on a focused row resumes;
  empty filter state `no sessions match "foo"` with a clear button.
- Accounts: "logged in, time unknown" instead of "last login: never";
  `repeat(auto-fill, minmax(320px, 1fr))` with a dashed "+ add account"
  card; default tag and accent left border; a ⋯ menu (log in again, make
  default, remove); the remove modal explains the backup, offers "also
  delete the folder" and needs the account name typed.
- Devices: the setup card is a three-step onboarding with copyable
  commands; the pair form only shows when Syncthing answers; no error
  text in the device-ID field; per-device state and a grouped ID with a
  copy button.
- handoff (was WIP): renamed, with the explainer "Work you left on another
  machine that is not pushed yet. Open it here to continue." `#wip`
  redirects to `#handoff`.

## F: first run (P6), stats (P7), mobile (P8)

- Wizard: two columns at ≥ 960 px, a step rail (done / current / next)
  and a step card above a terminal-log panel; "skip setup" under the rail;
  the done screen lists next actions and key hints and draws the hex
  stroke once (600 ms, none with reduced motion).
- Stats: the heatmap is always the last 12 months, titled "activity, last
  12 months", independent of the range selector; a 5-step ramp per theme
  from `--surface-2` to `--accent-fill`; cell size from the panel width
  (min 9 px, gap 2 px). est. cost in `--text-strong` with "≈". Token kinds
  as two bars, billed (input + output) and cache (write + read). Solid
  `--accent-2` bars with a 4 px rounded end. The range control gets its
  own row on mobile.
- Mobile (< 720 px): a 56 px bottom bar (sessions, projects, health,
  accounts, more) with safe-area padding; main's bottom padding is the bar
  height plus the safe area. "more" opens a sheet with the other sections,
  a full-width theme control (≥ 40 px segments), then full-width ≥ 44 px
  rows (motion with its switch on the right, about, copy debug logs,
  close) with a pressed state; one 16 px inset, a darker scrim, safe-area
  bottom padding, and no focus ring when opened by a tap. Under 520 px the
  header is logo, connection dot, refresh and a ⋯ menu, all three circles.
  Session cards use one meta line; the branch ellipsizes first so the time
  never wraps.
- Favicon 16 px exactly as review §3; the full mark stays at ≥ 32 px. One
  SVG `<symbol>` sprite for the icons; every icon-only button has an
  aria-label and a title.

## Tests / gate

The v0.7.1 gate plus: `node scripts/contrast-check.js`, the DOM-sink grep,
the external-URL check, `/api/about` (auth, shape, `~` paths, no network,
the update check through a fake client, CSRF), buildinfo fallbacks, asset
budget test (raw ≤ 200 KB, gzip < 60 KB).
