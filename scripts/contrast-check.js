#!/usr/bin/env node
// contrast-check (v0.7.2, D68): WCAG 2.x contrast of the dashboard's theme
// tokens. Parses the two theme blocks of internal/gui/web/app.css (only
// literal #hex / rgb[a]() values and var() references between tokens),
// flattens translucent colours over the surface below them, and checks:
//   text pairs     >= 4.5:1  (faint too, since v0.9 polish)
//   control pairs  >= 3.0:1  (input/button borders, WCAG 1.4.11)
//   chart marks    >= 3.0:1  (bars, swatches, the heatmap top vs empty)
// Prints a table; exits 1 when any pair fails. Node, no dependencies.
"use strict";
const fs = require("fs");
const path = require("path");

const cssPath = process.argv[2] || path.join(__dirname, "..", "internal", "gui", "web", "app.css");
const css = fs.readFileSync(cssPath, "utf8");

function block(selector) {
  const at = css.indexOf(selector + " {");
  if (at < 0) throw new Error("no block " + selector);
  const end = css.indexOf("\n}", at);
  const vars = {};
  const re = /--([a-z0-9-]+)\s*:\s*([^;]+);/g;
  let m;
  const body = css.slice(at, end);
  while ((m = re.exec(body)) !== null) vars[m[1]] = m[2].trim();
  return vars;
}

function parse(v, vars, depth) {
  v = v.trim();
  const ref = /^var\(--([a-z0-9-]+)\)$/.exec(v);
  if (ref) {
    if ((depth || 0) > 8 || !(ref[1] in vars)) throw new Error("unresolved " + v);
    return parse(vars[ref[1]], vars, (depth || 0) + 1);
  }
  if (v === "transparent") return [0, 0, 0, 0];
  let m = /^#([0-9a-f]{3,8})$/i.exec(v);
  if (m) {
    let h = m[1];
    if (h.length === 3 || h.length === 4) h = h.split("").map((c) => c + c).join("");
    const n = (i) => parseInt(h.slice(i, i + 2), 16);
    return [n(0), n(2), n(4), h.length === 8 ? n(6) / 255 : 1];
  }
  m = /^rgba?\(([^)]+)\)$/.exec(v);
  if (m) {
    const p = m[1].split(",").map((x) => parseFloat(x));
    return [p[0], p[1], p[2], p.length > 3 ? p[3] : 1];
  }
  throw new Error("cannot parse colour " + v);
}

function over(top, bottom) {
  const a = top[3];
  return [0, 1, 2].map((i) => top[i] * a + bottom[i] * (1 - a)).concat([1]);
}

function lum(c) {
  const f = (x) => { x /= 255; return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4); };
  return 0.2126 * f(c[0]) + 0.7152 * f(c[1]) + 0.0722 * f(c[2]);
}

function ratio(a, b) {
  const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
}

const TEXT = ["text", "text-strong", "muted", "accent", "accent-2", "code", "magenta", "ok", "warn", "fail"];
const SURFACES = ["bg", "surface", "surface-solid", "surface-2", "band"];

function check(theme, vars) {
  const rows = [];
  const bg = parse(vars.bg, vars);
  const flat = (name, under) => over(parse(vars[name], vars), under || bg);
  const surf = {};
  surf.bg = bg;
  surf.surface = flat("surface");
  surf["surface-solid"] = flat("surface-solid");
  surf["surface-2"] = flat("surface-2", surf.surface);
  surf.band = flat("band", surf.surface); // band rows sit on a card
  const add = (kind, fg, on, fgc, bgc, min) => {
    const r = ratio(fgc, bgc);
    rows.push({ theme, kind, pair: fg + " on " + on, ratio: r, min, ok: r >= min });
  };
  for (const s of SURFACES) {
    for (const t of TEXT) add("text", t, s, flat(t, surf[s]), surf[s], 4.5);
    add("text", "faint", s, flat("faint", surf[s]), surf[s], 4.5);
    add("control", "control-line", s, flat("control-line", surf[s]), surf[s], 3.0);
  }
  add("text", "accent-ink", "accent-fill", flat("accent-ink"), flat("accent-fill"), 4.5);
  add("text", "warn", "warn-soft", flat("warn", flat("warn-soft", surf.surface)), flat("warn-soft", surf.surface), 4.5);
  add("text", "fail", "fail-soft", flat("fail", flat("fail-soft", surf.surface)), flat("fail-soft", surf.surface), 4.5);
  add("text", "ok", "ok-soft", flat("ok", flat("ok-soft", surf.surface)), flat("ok-soft", surf.surface), 4.5);
  add("text", "accent", "accent-soft", flat("accent", flat("accent-soft", surf.surface)), flat("accent-soft", surf.surface), 4.5);
  // v0.9 G1: warn/fail check rows sit on their soft tint; inline code there
  // also has an accent-soft chip under it (I3).
  for (const tint of ["warn-soft", "fail-soft"]) {
    const row = flat(tint, surf.surface);
    for (const t of ["text-strong", "text", "muted", "code"]) add("text", t, tint, flat(t, row), row, 4.5);
    const chip = flat("accent-soft", row);
    add("text", "code", tint + "+chip", flat("code", chip), chip, 4.5);
  }
  // v0.9 polish (owner review): the status line (segments on surface-2 /
  // sl-step, warn/fail segments on their tint over the solid bar), the
  // yellow pills and [ WARN ] tags on pale yellow, a pill on a warn row,
  // and the phone's filled tab badge.
  surf["sl-step"] = flat("sl-step");
  for (const t of ["text", "muted", "ok", "warn", "fail", "accent-2"]) {
    add("text", t, "sl-step", flat(t, surf["sl-step"]), surf["sl-step"], 4.5);
  }
  for (const tint of ["warn", "fail"]) {
    const seg = flat(tint + "-soft", surf["surface-solid"]);
    add("text", tint, tint + "-soft on bar", flat(tint, seg), seg, 4.5);
    const row = flat(tint + "-soft", surf.surface);
    const pill = flat(tint + "-soft", row);
    add("text", tint, tint + "-soft pill on row", flat(tint, pill), pill, 4.5);
    add("text", tint, tint + "-soft on band", flat(tint, flat(tint + "-soft", surf.band)), flat(tint + "-soft", surf.band), 4.5);
    add("text", "surface-solid", tint + " (badge)", surf["surface-solid"], flat(tint, surf["surface-solid"]), 4.5);
  }
  add("text", "warn", "warn-soft on bg", flat("warn", flat("warn-soft", bg)), flat("warn-soft", bg), 4.5);
  for (const m of ["accent-2", "accent-fill", "chart-billed", "chart-cache", "magenta", "heat-4"]) {
    add("chart", m, "surface", flat(m, surf.surface), surf.surface, 3.0);
  }
  add("chart", "heat-4", "heat-0", flat("heat-4"), flat("heat-0"), 3.0);
  return rows;
}

let failed = 0;
const all = [];
for (const [theme, sel] of [["dark", ':root, [data-theme="dark"]'], ["light", '[data-theme="light"]']]) {
  all.push(...check(theme, block(sel)));
}
const pad = (s, n) => (s + " ".repeat(n)).slice(0, n);
console.log(pad("theme", 6) + " " + pad("kind", 8) + " " + pad("pair", 34) + " ratio   min   result");
for (const r of all) {
  if (!r.ok) failed++;
  if (process.argv.includes("--all") || !r.ok || r.kind !== "text" || /^(muted|faint|text) on (surface|band)$/.test(r.pair)) {
    console.log(pad(r.theme, 6) + " " + pad(r.kind, 8) + " " + pad(r.pair, 34) + " " + pad(r.ratio.toFixed(2), 7) + " " + pad(r.min.toFixed(1), 5) + " " + (r.ok ? "pass" : "FAIL"));
  }
}
console.log(all.length + " pairs checked, " + failed + " failed" + (process.argv.includes("--all") ? "" : " (--all lists every pair)"));
process.exit(failed ? 1 : 0);
