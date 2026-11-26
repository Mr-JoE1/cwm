# tools/ux — dashboard capture and audit

Zero-dependency tooling (Node ≥ 22, a Chrome or Chromium) for checking the
dashboard: screenshots of every tab and dialog at 1440 / 1024 / 390 / 320 px
in dark and light, a DOM audit per view, the console log, and a comparison
against an earlier audit. Read-only: it switches tabs and opens dialogs, and
never clicks a control that changes anything.

**Run it against demo data only.** Screenshots and `audit.json` contain
whatever the dashboard shows, including project names, prompts and paths.
`demo.sh` gives you a dashboard filled with invented data in a throwaway
HOME. Output directories (`ux-out/`, `tools/ux/out/`, `tools/ux/baseline*/`)
are git-ignored; keep captures out of the repo.

## Files

| file | what |
|---|---|
| `demo.sh` | builds cwm from this checkout, makes a temp HOME with `demo-data.js`, starts `cwm agent` on a free port, prints `CWM_UX_BASE` / `CWM_UX_HOME` |
| `demo-data.js` | the invented demo home (accounts, sessions, stats history, two dirty clones) |
| `capture.mjs` | the capture driver and `--compare` |
| `audit.js` | the per-view DOM audit (evaluated in the page) |
| `footer.mjs` | 2x close-ups of the status line and header, plus the key/type shape of the GET endpoints |
| `cdp.mjs` | shared helpers: token, Chrome lookup, a minimal CDP client |

## Use

```sh
tools/ux/demo.sh                  # or --fresh (first-run wizard), --legacy (needs migration)
source /tmp/cwm-ux-demo.XXXXXX/env.sh
node tools/ux/capture.mjs ux-out/now
node tools/ux/capture.mjs ux-out/now --compare ux-out/before/audit.json
node tools/ux/capture.mjs --compare ux-out/before/audit.json --against ux-out/now/audit.json --strict
node tools/ux/footer.mjs ux-out/now
tools/ux/demo.sh stop /tmp/cwm-ux-demo.XXXXXX
```

Environment:

| variable | default | |
|---|---|---|
| `CWM_UX_BASE` | `http://127.0.0.1:47800` | the dashboard |
| `CWM_UX_HOME` | `~/.cwm` | where `server.toml` (the launch token) lives |
| `CWM_UX_TOKEN` | – | the token, instead of reading `server.toml` |
| `CHROME` | auto | Linux: Playwright's `chrome-headless-shell` or `chromium` under `~/.cache/ms-playwright`, then `/usr/bin/chromium*` / `google-chrome`; Windows and macOS: the stock Chrome (or Edge) |
| `CWM_UX_CDP` | a free port | Chrome's debugging port; the tool starts its own Chrome and kills only that |
| `CWM_UX_SIZES` | `d,t,m,m320` | 1440×900, 1024×768, 390×844 (touch), 320×640 (touch) |
| `CWM_UX_THEMES` | `dark,light` | |

## Output

`<outdir>/<size>-<theme>-<tab>.png` for every tab, `ov-*.png` for the menu,
palette, keys and About dialogs, `m-ov-more.png` for the phone sheet,
`kb-focus-6.png`, and:

- `audit.json`: per view `vw` (layout width) and `deviceW`, overflowing
  elements, font sizes and families, `lowContrast` (text below 4.5:1, or
  3:1 when large, measured against the composited background), small and
  sub-44 px targets, `noName` (interactive elements without an accessible
  name), truncated text, headings, empty states, the panel text; plus the
  Tab-key focus trail and the reduced-motion state.
- `console.json`: every console message, exception and browser log entry,
  tagged with the view (`at`) it happened on.

## --compare

Prints one row per view: `vw/device` before → after, `lowContrast`,
`noName` and console errors. Keys from an older capture (`sessions`,
`light-sessions`, `m-sessions`, `m320-sessions`) are mapped to this tool's
names, and the old Stats views are compared with Home (D105). It exits 1
on a regression: a count that went up, or a phone layout that became wider
than the device. `--strict` also fails on any non-zero count or width
mismatch, which is the release bar (0 lowContrast, 0 noName, 0 console
errors, `vw` = device width everywhere).
