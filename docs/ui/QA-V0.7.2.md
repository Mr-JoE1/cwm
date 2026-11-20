# v0.7.2 final QA (P9)

Run against generated demo data (6 projects, 25 sessions, 2 accounts, 12
months of stats, a fresh box's health warnings, Syncthing not set up) with a
headless Chromium driven over CDP: real key events, emulated
`prefers-color-scheme` and `prefers-reduced-motion`.

| # | Check | Result |
|---|---|---|
| 1 | `scripts/contrast-check.js`, both themes | 134 pairs, 0 failed |
| 2 | Keyboard-only walk: every tab at 1440 and 390 px; `1`–`7`, arrow keys, `#wip` → `#handoff`; Ctrl-K open / run / Esc (focus returns); `?` help; About from the statusline (focus stays in the modal, copy → copied, Esc returns focus); accounts ⋯ menu and the typed remove modal; "more" sheet; header ⋯ menu | pass after fixes below |
| 3 | Reduced motion: no running animations in any view or dialog, no `requestAnimationFrame` loop; with motion allowed the canvas runs and stops when motion is switched off | pass |
| 4 | CSP: no inline handlers, inline scripts or style attributes; no CSP violations, console errors or non-local requests at runtime; external links only `target=_blank rel=noopener noreferrer` to github.com/Mr-JoE1 | pass after fixes below |
| 5 | Asset budget (D71: 200 KB raw, < 60 KB gzip) | 200,814 B raw, 52,794 B gzip |
| 6 | Screenshots, every view × 390/768/1440 × dark/light, plus About, Ctrl-K, remove modal, first-run wizard and done screen, mobile "more" sheet | taken and reviewed |

## Failures found and fixed

| View | Issue | Fix |
|---|---|---|
| sessions, devices (all widths) | A row or button reached by Tab could sit under the fixed statusline (desktop) or the bottom bar (mobile) | `scroll-padding` on `html` for the sticky header, statusline and bottom bar |
| About | "copy version info" never changed to "copied": the handler read `ev.currentTarget` after an `await` (null by then) and threw | capture the button before the `await` |
| health, devices, accounts (< 720 px) | "details" toggles, the "report a bug" link and the ⋯ account menu were under 40 px tall | 40 px minimum on touch layouts |
| sessions (721–1100 px) | prompt column squeezed to a few words per line by the branch and id columns | prompt min width, narrower branch, id column hidden in that range |
| health | copyable-command boxes had uneven widths | the box sizes to its command, capped at 640 px |
| projects, handoff, devices | Title Case column labels and pair-form labels | lowercase labels |

## Accepted

| View | Note |
|---|---|
| stats (390 px) | Heatmap cells are 9 px (the P7 minimum) and below the 40 px tap size; the grid is reached with one Tab stop and the arrow keys (roving tabindex), and every cell has a tooltip. |
