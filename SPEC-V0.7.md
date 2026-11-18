# SPEC v0.7 — Cyber UI + brand

Goal (ARCH-V2 roadmap): the dashboard looks like a product, not a debug page,
and it costs nothing when left open. It keeps the D23 stack: static
HTML/CSS/vanilla JS embedded in the binary, no framework, no build step, no
CDN. **No `/api` contract change.** Built in parallel with v0.6
(agent/service/installer). The only file outside `internal/gui/web/**` and
docs that changed is the asset test in `internal/gui/server_test.go`.

Decisions D40–D46 are logged in docs/PLAN.md.

## Brand (D40)
- Mark: a hexagon (node) framing a terminal prompt `>` with a block cursor.
  Hand-written SVG; the wordmark glyphs are stroked paths (no font
  dependency). Legible at 16 px and works as a single colour.
- Files: `docs/brand/cwm-mark.svg`, `docs/brand/cwm-logo.svg` (both adapt to
  `prefers-color-scheme` and accept `--cwm-*` colour overrides when inlined),
  `internal/gui/web/favicon.svg` (mark on a dark tile), PNG renders 512/192/32
  plus logo previews, rendered by headless Chromium. Usage and palette are in
  `docs/brand/README.md`.

## Design system (D41)
- Every colour, radius and timing is a CSS custom property. Components only
  reference tokens.
- **Dark "cyber"** (brand default): `#05080d` background, terminal-green
  accent `#00ff9c`, cyan `#00e5ff` secondary, magenta `#ff3cd8` highlight.
  Panels are translucent with a thin neon edge and a soft glow.
- **Light**: the same hues darkened until text passes contrast (`#007a4d`,
  `#007c91`, `#c4148f`). All text pairs pass WCAG AA (≥ 4.5:1; measured
  values are in the brand README).
- Type: system monospace stack for display/UI, system sans for body. No web
  fonts. `font-src` is not in the CSP, so fonts could not load anyway.
- Theme control: system / dark / light, persisted in `localStorage`
  (`cwm-theme`). `system` follows `prefers-color-scheme` live.
- **No flash** (D42): `/theme.js` is a tiny *synchronous* `<head>` script
  that sets `data-theme` before first paint. It is an external file, not an
  inline script, because the D24 CSP (`script-src 'self'`) forbids inline
  scripts and the CSP stays unchanged.

## Live background (D43)
- `bg.js`: one fixed `<canvas>` behind the content. A slow particle network
  (links bucketed by distance into 4 strokes per frame), a drifting scan
  band, and rare magenta/cyan glitch slices. A static CSS grid and scanlines
  sit underneath. Colours come from theme tokens (`--bg-node` /
  `--bg-link` / `--bg-glitch`) and update when the theme changes.
- Budget: ≤ 30 fps (rAF with a frame cap), devicePixelRatio capped at 2,
  paused while `document.hidden`, a single still frame under
  `prefers-reduced-motion`, and off entirely via a toggle (persisted as
  `cwm-bg`, shortcut `b`).
- **Idle freeze:** after 30 s without pointer, key, wheel, touch or scroll
  input, the canvas keeps its last frame and stops drawing. Any input resumes
  it. Decorative CSS loops are finite (status-pill pulses run a few cycles;
  the caret and logo cursor blink briefly; the live dot pings once per data
  refresh). An idle dashboard therefore produces no frames at all.

## Motion / UX (D44)
- Tab panels fade/slide in; table rows reveal with a stagger (capped at 18
  steps); skeleton shimmer rows while loading; toasts for every action (copy,
  resume, pair, init, account added). A 501 renders as a sticky toast with
  the command and a Copy button.
- Header tagline types itself once and then settles. Screen readers get the
  static text.
- Status pills: dot + label, fixed width so the column aligns; warn/fail
  dots pulse a few times.
- Keyboard: `1`–`5` tabs, `/` focuses the session filter, `t` cycles theme,
  `b` toggles the background, `r` refreshes, `?` opens the shortcut sheet,
  `Esc` closes it or leaves the filter. Shortcuts are ignored while typing
  in a field.
- Responsive to 360 px: below 720 px every table becomes stacked cards (cells
  carry a `data-label`), forms stack, and the toolbar collapses to icons.
- Kept: the wizard (all 4 steps), Resume, Pair form, filter, refresh, the 501
  copy-command fallback, all 5 tabs, the 401 banner. The CSRF header still
  comes from `GET /api/csrf`.
- v0.5 nits fixed: backticked `` `cmd` `` in doctor details (and in error
  details) renders as `<code>`. It is built by splitting the string into
  text nodes and `<code>` nodes, never parsed as HTML. The status badge
  column is aligned (fixed-width pills, grid layout).

## Live updates (D45)
- At startup the page probes `GET /api/events` (fetch with
  `Accept: text/event-stream`, aborted right after the headers arrive). A
  200 with `text/event-stream` opens an `EventSource`. Anything else
  (v0.5/v0.7 servers answer 404) falls back to polling every 15 s, only
  while the page is visible.
- The event format is not fixed yet (it lands with v0.6). The client accepts
  default `message` events and named events (`change`, `update`, `refresh`,
  `state`, `accounts`, `sessions`, `projects`, `devices`, `wip`, `doctor`,
  `health`). It reads an optional JSON `topic|type|kind|event` field and
  ignores `ping`/`heartbeat`. Bursts are coalesced (400 ms), only the affected
  tabs are marked stale, and only the visible one reloads.
- Silent refreshes never show a skeleton or re-animate. When the payload is
  unchanged they skip the re-render, so focus, hover and scroll survive. A
  header pill shows `live` (SSE), `auto · 15s` (polling) or `offline`.

## Safety (D46)
- No `innerHTML`, `outerHTML`, `insertAdjacentHTML`, `document.write` or
  `eval` anywhere. The asset test now walks **every** embedded file and
  enforces: no external references (`src`/`href`/`url()` to `//…`,
  `@import`), no HTML sinks in any `.js`, no inline `<script>`/`style=` in
  `index.html`, a synchronous `/theme.js` in `<head>`, a favicon served as
  `image/svg+xml`, and a **150 KB budget** for all web assets.
- CSP unchanged. Dynamic styles use the CSSOM (`style.setProperty`), which the
  CSP allows.

## Verification
- Gates: `gofmt`, `go vet`, `go build`, `go test ./...`,
  `go test -tags e2e ./test/`, `bash tests/test.sh`.
- `cwm gui --no-open` on fake demo data in a temp HOME (`env -i`, the real
  `~/.cwm` / `~/.claude*` never read). Playwright checks: theme default and
  persistence, the no-flash attribute before first paint, the 501 toast with
  its command, toast dismiss, all shortcuts, filter, backtick → `<code>`,
  reduced motion, zero JS errors. Screenshots at 1280×800 in both themes
  (sessions, projects, health, wizard) plus a 390×844 mobile sessions shot in
  `docs/img/`.
- CPU measured with CDP `Performance.getMetrics` (renderer main thread) and
  `/proc` (whole browser process tree) in headless Chromium. Numbers are in
  the PLAN v0.7 note.

## Out of scope
Web terminal (v0.9), Docker hub UI (v0.8), and changes to the SSE server side
(v0.6) or any `/api` shape. Lighthouse was not run; the a11y points it
checks are covered by hand: contrast, labels, roles, focus rings, reduced
motion.
