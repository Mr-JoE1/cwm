// Zero-dependency CDP driver: signs in to a local cwm dashboard, captures
// every tab and dialog at 1440/1024/390/320 px in dark and light, and dumps
// DOM audit metrics (audit.js) plus the console. Read-only: it only switches
// tabs and opens/closes dialogs; it never clicks a mutating control.
//
//   node tools/ux/capture.mjs <outdir> [--compare <baseline-audit.json>] [--strict]
//   node tools/ux/capture.mjs --compare <baseline-audit.json> --against <audit.json> [--strict]
//
// Env: CWM_UX_BASE (http://127.0.0.1:47800), CWM_UX_HOME (~/.cwm, holds
// server.toml), CWM_UX_TOKEN, CHROME, CWM_UX_CDP (debugging port; default
// a free one), CWM_UX_SIZES (d,t,m,m320), CWM_UX_THEMES (dark,light).
// Use against a demo home (tools/ux/demo.sh), never real data: the
// screenshots and audit.json contain whatever the dashboard shows.
import { mkdirSync, readFileSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { BASE, connect, isError, sleep, startChrome, token } from "./cdp.mjs";

// v0.9 tabs (D105); the page's own tab list wins, so older builds capture too.
const TABS = ["home", "sessions", "projects", "devices", "handoff", "health", "accounts", "settings"];
const SIZES = { d: [1440, 900, false], t: [1024, 768, false], m: [390, 844, true], m320: [320, 640, true] };

const argv = process.argv.slice(2);
const flag = (name) => { const i = argv.indexOf(name); return i < 0 ? null : argv[i + 1]; };
const strict = argv.includes("--strict");
const compareWith = flag("--compare");
const against = flag("--against");

// ---- compare: per-view deltas against a baseline audit.json

// baseline keys from the owner's v0.8.3 capture → this tool's keys
function normKey(k) {
	if (/^(ov-|m-ov-|focusTrail|reducedMotion|perf)/.test(k) || /^(d|t|m|m320)-(dark|light)-/.test(k)) return k;
	let m;
	if ((m = /^light-(.+)$/.exec(k))) return "d-light-" + m[1];
	if ((m = /^m320-(.+)$/.exec(k))) return "m320-dark-" + m[1];
	if ((m = /^m-(.+)$/.exec(k))) return "m-dark-" + m[1];
	return "d-dark-" + k;
}
function deviceW(key, a) {
	if (a && a.deviceW) return a.deviceW;
	const s = key.split("-")[0];
	return (SIZES[s] || SIZES.d)[0];
}
function loadAudit(file) {
	const raw = JSON.parse(readFileSync(file, "utf8"));
	const out = {};
	for (const [k, v] of Object.entries(raw)) if (v && typeof v === "object" && "vw" in v) out[normKey(k)] = v;
	const cf = join(dirname(file), "console.json");
	const errs = {};
	if (existsSync(cf)) for (const l of JSON.parse(readFileSync(cf, "utf8")).filter(isError)) errs[l.at || "*"] = (errs[l.at || "*"] || 0) + 1;
	return { views: out, errs };
}
export function compare(baseFile, newFile) {
	const base = loadAudit(baseFile);
	const next = loadAudit(newFile);
	// Stats moved into Home (D105): compare the old stats views with home
	for (const k of Object.keys(base.views)) {
		const alias = k.replace(/-stats$/, "-home");
		if (alias !== k && !next.views[k] && !base.views[alias]) { base.views[alias] = base.views[k]; delete base.views[k]; }
	}
	const keys = [...new Set([...Object.keys(base.views), ...Object.keys(next.views)])].sort();
	const rows = [];
	let regressions = 0, failures = 0;
	const pad = (s, n) => String(s).padEnd(n);
	console.log(pad("view", 26) + pad("vw/device (before → after)", 30) + pad("lowContrast", 14) + pad("noName", 10) + "console errors");
	for (const k of keys) {
		const b = base.views[k], n = next.views[k];
		const fmtVW = (a) => a ? a.vw + "/" + deviceW(k, a) : "–";
		const bad = [];
		const cell = (get) => {
			const x = b ? get(b) : null, y = n ? get(n) : null;
			if (x !== null && y !== null && y > x) { bad.push("regression"); regressions++; }
			if (y) failures++;
			return (x === null ? "–" : x) + " → " + (y === null ? "–" : y);
		};
		let vw = fmtVW(b) + " → " + fmtVW(n);
		if (n && n.vw !== deviceW(k, n)) {
			failures++;
			if (!b || b.vw === deviceW(k, b)) { regressions++; bad.push("regression"); }
			vw += " ✗";
		}
		const lc = cell((a) => a.lowContrastCount || 0);
		const nn = cell((a) => (a.noName || []).length);
		const er = cell((a) => a === b ? (base.errs[k] || 0) : (next.errs[k] || 0));
		rows.push(k);
		console.log(pad(k, 26) + pad(vw, 30) + pad(lc, 14) + pad(nn, 10) + er + (bad.length ? "   << " + [...new Set(bad)].join(", ") : ""));
	}
	const tot = (e) => Object.values(e).reduce((a, b) => a + b, 0);
	console.log("\nconsole errors total: " + tot(base.errs) + " → " + tot(next.errs));
	console.log(regressions ? `${regressions} regression(s)` : "no regressions", strict ? `; strict: ${failures} non-zero cell(s)` : "");
	return strict ? regressions + failures : regressions;
}

if (compareWith && against) process.exit(compare(compareWith, against) ? 1 : 0);

// ---- capture

const OUT = argv.find((a, i) => !a.startsWith("--") && !(argv[i - 1] || "").startsWith("--"));
if (!OUT) { console.error("usage: capture.mjs <outdir> [--compare <baseline-audit.json>] [--strict]"); process.exit(2); }
mkdirSync(OUT, { recursive: true });
const tok = token();
const chrome = await startChrome(join(OUT, "_profile"));
const cdp = await connect(chrome.port);
const { send, ev } = cdp;
const AUDIT = readFileSync(new URL("./audit.js", import.meta.url), "utf8");
const audits = {};
const want = (process.env.CWM_UX_SIZES || "d,t,m,m320").split(",");
const themes = (process.env.CWM_UX_THEMES || "dark,light").split(",");

async function viewport(size) {
	const [w, h, mobile] = SIZES[size];
	await send("Emulation.setDeviceMetricsOverride", { width: w, height: h, deviceScaleFactor: 1, mobile });
	await send("Emulation.setTouchEmulationEnabled", { enabled: mobile });
	await sleep(400);
}
async function shot(name, full = true) {
	const { w, h } = JSON.parse(await ev(`JSON.stringify({w: document.documentElement.scrollWidth, h: Math.min(document.documentElement.scrollHeight, 6000)})`));
	const r = await send("Page.captureScreenshot", { format: "png", captureBeyondViewport: full, clip: full ? { x: 0, y: 0, width: w, height: h, scale: 1 } : undefined });
	writeFileSync(join(OUT, name + ".png"), Buffer.from(r.result.data, "base64"));
	console.log("shot", name, w + "x" + h);
}
async function audit(key, size) {
	const a = JSON.parse(await ev(AUDIT));
	a.deviceW = SIZES[size][0];
	audits[key] = a;
}
async function goTab(t) {
	cdp.at = t;
	await ev(`(()=>{const b=document.getElementById('tab-${t}'); if(b) b.click(); else location.hash='#${t}'; return 1})()`);
	await sleep(1500);
	await ev(`window.scrollTo(0,0); 1`);
}
async function setTheme(t) {
	await ev(`(()=>{const b=document.querySelector('[data-theme-set="${t}"]'); if(b) b.click(); else document.documentElement.dataset.theme='${t}'; return 1})()`);
	await sleep(500);
}
// closeAll closes overlays the way a user does (Esc), then force-closes
// whatever is left, so no capture shows one overlay on top of another.
async function closeAll() {
	for (let i = 0; i < 4 && await ev(`document.querySelectorAll('dialog[open]').length`); i++) {
		for (const type of ["keyDown", "keyUp"]) await send("Input.dispatchKeyEvent", { type, key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
		await sleep(150);
	}
	await ev(`document.querySelectorAll('dialog[open]').forEach(d=>d.close()); const m=document.getElementById('menu'); if(m) m.hidden=true; 1`);
}

// ---- sign in
await viewport("d");
await send("Page.navigate", { url: `${BASE}/?t=${tok}` });
await sleep(3500);
writeFileSync(join(OUT, "_url.txt"), String(await ev("location.href")).replace(tok, "<tok>"));
await setTheme("dark");
await shot("00-landing-desktop-dark", false);
const pageTabs = JSON.parse(await ev(`JSON.stringify([...document.querySelectorAll('[role=tab][data-tab]')].map(t=>t.dataset.tab))`) || "[]");
const tabs = pageTabs.length ? pageTabs : TABS;

// ---- every size × theme × tab
for (const size of want) {
	await viewport(size);
	for (const theme of themes) {
		await setTheme(theme);
		for (const t of tabs) {
			await goTab(t);
			const key = `${size}-${theme}-${t}`;
			await shot(key);
			await audit(key, size);
		}
	}
}
await setTheme("dark");

// ---- overlays (desktop dark)
await viewport("d");
await goTab(tabs[0]);
cdp.at = "ov-menu";
await ev(`document.getElementById('menu-btn').click(); 1`); await sleep(500);
await shot("ov-menu", false); await audit("ov-menu", "d");
await closeAll(); await sleep(300);
for (const [name, sel] of [["palette", "[data-palette-open]"], ["keys", "[data-keys-open]"], ["about", "[data-about-open]"]]) {
	cdp.at = "ov-" + name;
	await ev(`document.querySelector('${sel}').click(); 1`); await sleep(900);
	await shot("ov-" + name, false);
	await audit("ov-" + name, "d");
	await closeAll(); await sleep(300);
}
cdp.at = "ov-palette-query";
await ev(`document.querySelector('[data-palette-open]').click(); 1`); await sleep(600);
await ev(`(()=>{const i=document.querySelector('#palette input'); if(i){i.value='acc'; i.dispatchEvent(new Event('input',{bubbles:true}));} return 1})()`); await sleep(500);
await shot("ov-palette-query", false);
await closeAll();

// ---- phone: the More sheet
if (want.includes("m")) {
	await viewport("m");
	cdp.at = "m-ov-more";
	await ev(`document.querySelector('[data-more-open]')?.click(); 1`); await sleep(800);
	await shot("m-ov-more", false); await audit("m-ov-more", "m");
	await closeAll();
}

// ---- keyboard focus visibility sample (desktop)
await viewport("d");
await closeAll();
await send("Page.navigate", { url: `${BASE}/#${tabs[0]}` });
await send("Page.reload"); // a clean page: a same-document hash change would keep dialogs open
await sleep(2500);
cdp.at = "focusTrail";
const focusTrail = [];
await ev(`document.activeElement.blur(); 1`);
for (let i = 0; i < 14; i++) {
	await send("Input.dispatchKeyEvent", { type: "keyDown", key: "Tab", code: "Tab", windowsVirtualKeyCode: 9 });
	await send("Input.dispatchKeyEvent", { type: "keyUp", key: "Tab", code: "Tab", windowsVirtualKeyCode: 9 });
	await sleep(120);
	focusTrail.push(JSON.parse(await ev(`(()=>{const e=document.activeElement; const s=getComputedStyle(e); return JSON.stringify({tag:e.tagName, id:e.id, cls:e.className&&String(e.className).slice(0,40), text:(e.innerText||e.getAttribute('aria-label')||'').trim().slice(0,40), outline:s.outlineStyle+' '+s.outlineWidth+' '+s.outlineColor, boxShadow:s.boxShadow.slice(0,60)})})()`)));
	if (i === 5) await shot("kb-focus-6", false);
}
audits.focusTrail = focusTrail;
// reduced motion
await send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
audits.reducedMotion = await ev(`JSON.stringify({motionAttr: document.documentElement.dataset.bg, canvasVisible: getComputedStyle(document.getElementById('bg')).display, scan: document.querySelector('.scan') ? getComputedStyle(document.querySelector('.scan')).animationName : null})`);
audits.perf = await ev(`JSON.stringify(performance.getEntriesByType('navigation').map(n=>({dcl:Math.round(n.domContentLoadedEventEnd), load:Math.round(n.loadEventEnd), transfer:n.transferSize}))[0])`);

writeFileSync(join(OUT, "audit.json"), JSON.stringify(audits, null, 1));
writeFileSync(join(OUT, "console.json"), JSON.stringify(cdp.logs, null, 1));
cdp.close(); chrome.kill();
console.log("done", Object.keys(audits).length, "audits;", cdp.logs.length, "console entries,", cdp.logs.filter(isError).length, "errors");
process.exit(compareWith ? (compare(compareWith, join(OUT, "audit.json")) ? 1 : 0) : 0);
