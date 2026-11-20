# cwm brand

Hand-written SVG, no fonts, no raster sources. The glyphs in the wordmark are
stroked paths, so the logo renders the same everywhere.

## The mark

A **hexagon** (a node: one workspace, many devices) framing a **terminal
prompt** — a `>` chevron and a block cursor `_`. The frame and chevron are the
accent colour; the cursor is the secondary (magenta). Every stroke is ≥ 5/64 of
the box, so the mark stays legible at 32 px; at 16 px a dedicated pixel-grid
icon replaces it (below).

| File | Use |
|---|---|
| `cwm-mark.svg` | Mark only, transparent. Adapts to `prefers-color-scheme` (dark/light palettes below). |
| `cwm-logo.svg` | Mark + `cwm` wordmark, transparent, 180×64 viewBox. Adapts to `prefers-color-scheme`. |
| `cwm-logo-on-dark.svg`, `cwm-logo-on-light.svg` | The same logo with a fixed palette (dark or light), for places that choose the variant themselves, such as the README's `<picture>` element, which follows the GitHub theme rather than the OS setting. |
| `../../internal/gui/web/favicon.svg` | App icon: the mark on a near-black rounded tile. Same on any background (used as the dashboard favicon). |
| `cwm-icon-512.png`, `cwm-icon-192.png`, `cwm-icon-32.png` | PNG renders of the app icon (headless Chromium). |
| `cwm-favicon-16.svg` (= `../../internal/gui/web/favicon-16.svg`) | The 16 px icon (v0.7.2, designer review §3): a filled hexagon in `--accent-fill` with the prompt and cursor knocked out in the background colour. A filled shape survives 16 px where the stroked frame blurred. Use it only at 16 px; 32 px and up keep the full hex mark. (`cwm-icon-16.png` and the .ico still carry the v0.7.1 pixel-grid frame until they are re-rendered.) |
| `cwm-favicon.ico` | Windows/legacy icon: the 16 px hand-tuned frame plus the 32 px hex render, both PNG-encoded. |
| `cwm-logo-dark.png`, `cwm-logo-light.png` | Preview renders of logo + mark on both backgrounds. |

### Colour overrides

When the SVG is inlined in a page, it reads CSS custom properties
before falling back to its built-in palette:

```css
--cwm-frame   /* hexagon stroke   */
--cwm-prompt  /* chevron stroke   */
--cwm-cursor  /* cursor fill      */
--cwm-text    /* wordmark stroke (cwm-logo.svg) */
```

**Single colour:** set all four to the same value (for example `currentColor`,
black or white). The mark has no gradients and no fine detail, so it works as
a one-colour stamp, an embroidery or a favicon mask.

### Don'ts

- Don't put the neon-green (dark palette) version on a light background: it
  fails contrast. Use the light palette instead (it switches by itself where
  `prefers-color-scheme` applies).
- Don't add glows, gradients or shadows to the file itself. The dashboard adds
  a glow in CSS.
- Keep clear space of at least ¼ of the mark's width on every side.

## Palette

Text colours meet WCAG AA in both themes. Measured contrast is shown in brackets.

| Token | Dark ("cyber") | Light | Role |
|---|---|---|---|
| `--bg` | `#05080d` | `#f3f6fa` | page background |
| `--surface-solid` | `#0b111a` | `#ffffff` | panels, inputs |
| `--text` | `#d9e4f2` (15.6) | `#0b1220` (17.3) | body text |
| `--muted` | `#8593a8` (6.4) | `#4a5568` (6.9) | secondary text |
| `--accent` | `#00ff9c` terminal green (15.1) | `#007a4d` (5.0) | primary, links, focus, mark |
| `--accent-2` | `#00e5ff` cyan (12.3) | `#007c91` (4.9) | code, IDs, secondary lines |
| `--magenta` | `#ff3cd8` (6.6) | `#c4148f` (5.5) | cursor, beta tag, branch glyph |
| `--ok` | `#00ff9c` | `#007a4d` (5.0) | status ok |
| `--warn` | `#ffc857` (12.3) | `#8a5a00` (5.0) | status warn |
| `--fail` | `#ff5c7a` (6.4) | `#c0182f` (5.3) | status fail |

Type: no web fonts. Display/UI text uses the system monospace stack
(`ui-monospace, "JetBrains Mono", "Cascadia Code", "SF Mono", Menlo, Consolas,
"Liberation Mono", "DejaVu Sans Mono", monospace`). Body text uses the
system sans stack.

## Regenerating the PNGs

The PNGs come from the SVGs through headless Chromium (Playwright): each SVG
is loaded at the target size on a transparent page and screenshotted with
`omitBackground`. No other tooling is involved. Re-render after changing an SVG.
