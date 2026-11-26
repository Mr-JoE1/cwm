// Shared, zero-dependency helpers for the UX tools: where the dashboard and
// its token live, which Chrome to start, and a minimal CDP client over the
// WebSocket built into Node >= 22. Nothing here talks to anything but
// 127.0.0.1.
import { spawn } from "node:child_process";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { createServer } from "node:net";
import { homedir, platform } from "node:os";
import { join } from "node:path";

export const BASE = (process.env.CWM_UX_BASE || "http://127.0.0.1:47800").replace(/\/+$/, "");
export const HOME = process.env.CWM_UX_HOME || join(homedir(), ".cwm");
export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// token: CWM_UX_TOKEN, else server.toml in CWM_UX_HOME.
export function token() {
	if (process.env.CWM_UX_TOKEN) return process.env.CWM_UX_TOKEN;
	const f = join(HOME, "server.toml");
	const m = readFileSync(f, "utf8").match(/token\s*=\s*"?([0-9a-f]{64})/);
	if (!m) throw new Error("no token in " + f + " (set CWM_UX_HOME or CWM_UX_TOKEN)");
	return m[1];
}

// newest first: "chromium_headless_shell-1234" sorts after "-1228"
function newest(dir, prefix, rel) {
	if (!existsSync(dir)) return "";
	const hits = readdirSync(dir).filter((d) => d.startsWith(prefix)).sort((a, b) =>
		Number(b.split("-").pop()) - Number(a.split("-").pop()));
	for (const d of hits) for (const r of rel) { const p = join(dir, d, r); if (existsSync(p)) return p; }
	return "";
}

// CHROME, else a Playwright download on Linux, else the stock install.
export function chromePath() {
	if (process.env.CHROME) return process.env.CHROME;
	const os = platform();
	if (os === "win32") {
		for (const base of [process.env["PROGRAMFILES"], process.env["PROGRAMFILES(X86)"], process.env.LOCALAPPDATA]) {
			for (const rel of ["Google/Chrome/Application/chrome.exe", "Chromium/Application/chrome.exe", "Microsoft/Edge/Application/msedge.exe"]) {
				if (base && existsSync(join(base, rel))) return join(base, rel);
			}
		}
	} else if (os === "darwin") {
		for (const p of ["/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Chromium.app/Contents/MacOS/Chromium"]) if (existsSync(p)) return p;
	} else {
		const pw = join(homedir(), ".cache", "ms-playwright");
		const p = newest(pw, "chromium_headless_shell-", ["chrome-headless-shell-linux64/chrome-headless-shell", "chrome-linux/headless_shell"]) ||
			newest(pw, "chromium-", ["chrome-linux64/chrome", "chrome-linux/chrome"]);
		if (p) return p;
		for (const q of ["/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/google-chrome", "/usr/bin/google-chrome-stable"]) if (existsSync(q)) return q;
	}
	throw new Error("no Chrome found: set CHROME=/path/to/chrome");
}

export function freePort() {
	return new Promise((res, rej) => {
		const s = createServer();
		s.on("error", rej);
		s.listen(0, "127.0.0.1", () => { const p = s.address().port; s.close(() => res(p)); });
	});
}

// startChrome launches a private headless Chrome on a free debugging port
// and returns { port, kill }. Only the process started here is killed.
export async function startChrome(profileDir) {
	const exe = chromePath();
	const port = Number(process.env.CWM_UX_CDP) || await freePort();
	const shell = /headless[_-]shell/.test(exe);
	const args = [shell ? "--headless" : "--headless=new", `--remote-debugging-port=${port}`, `--user-data-dir=${profileDir}`,
		"--no-first-run", "--no-default-browser-check", "--hide-scrollbars", "--force-device-scale-factor=1", "--disable-gpu"];
	if (platform() === "linux") args.push("--no-sandbox");
	const child = spawn(exe, [...args, "about:blank"], { stdio: "ignore" });
	for (let i = 0; i < 150; i++) {
		try { await fetch(`http://127.0.0.1:${port}/json/version`); return { port, kill: () => child.kill() }; } catch { await sleep(200); }
	}
	child.kill();
	throw new Error("Chrome did not open its debugging port: " + exe);
}

// connect attaches to the first page target; logs collects console output,
// exceptions and browser log entries tagged with the current `at` label.
export async function connect(port) {
	let targets = [];
	for (let i = 0; i < 50 && !targets.some((t) => t.type === "page"); i++) {
		try { targets = await (await fetch(`http://127.0.0.1:${port}/json`)).json(); } catch { /* not yet */ }
		if (!targets.some((t) => t.type === "page")) await sleep(200);
	}
	const page = targets.find((t) => t.type === "page");
	if (!page) throw new Error("no page target on port " + port);
	const ws = new WebSocket(page.webSocketDebuggerUrl);
	await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
	let id = 0;
	const pending = new Map();
	const cdp = { logs: [], at: "boot" };
	ws.onmessage = (m) => {
		const d = JSON.parse(m.data);
		if (d.id && pending.has(d.id)) { pending.get(d.id)(d); pending.delete(d.id); }
		const log = (type, text) => cdp.logs.push({ at: cdp.at, type, text });
		if (d.method === "Runtime.consoleAPICalled") log(d.params.type, d.params.args.map((a) => a.value ?? a.description).join(" "));
		if (d.method === "Runtime.exceptionThrown") log("exception", d.params.exceptionDetails.exception?.description || d.params.exceptionDetails.text);
		if (d.method === "Log.entryAdded") log("log-" + d.params.entry.level, d.params.entry.text + " " + (d.params.entry.url || ""));
	};
	cdp.send = (method, params = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })); });
	cdp.ev = async (expr) => {
		const r = await cdp.send("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true });
		return r.result?.result?.value ?? r.result?.exceptionDetails?.exception?.description;
	};
	cdp.close = () => ws.close();
	await cdp.send("Page.enable");
	await cdp.send("Runtime.enable");
	await cdp.send("Log.enable");
	return cdp;
}

// errors are the log entries that count as console errors in an audit.
export const isError = (l) => l.type === "error" || l.type === "exception" || l.type === "log-error" || l.type === "assert";
