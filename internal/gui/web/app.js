// cwm dashboard — vanilla JS, no dependencies, no build step. Design notes:
// docs/PLAN.md (decision ids in the section headers below).
//
// Safety rule: API values are rendered through DOM nodes and textContent,
// never parsed as HTML (prompts, repo names and doctor details are user
// text). Actions send JSON with X-CWM-CSRF from /api/csrf (a derived value;
// the launch token stays in the HttpOnly cookie).
"use strict";

(function () {
	const TABS = ["sessions", "projects", "devices", "handoff", "health", "accounts", "stats", "settings"];
	const POLL_MS = 15000;
	const root = document.documentElement;
	const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
	const loaded = {}; // tab → loaded at least once
	const stale = {}; // tab → needs a reload when next shown
	const lastJSON = {}; // tab → last rendered payload (skip identical silent refreshes)
	let sessionsCache = [];

	const $ = (id) => document.getElementById(id);

	function el(tag, attrs, children) {
		const node = document.createElement(tag);
		if (attrs) {
			for (const [k, v] of Object.entries(attrs)) {
				if (v === null || v === undefined) continue;
				if (k === "class") node.className = v;
				else if (k === "text") node.textContent = v;
				else node.setAttribute(k, v);
			}
		}
		for (const child of children || []) {
			if (child === null || child === undefined || child === false) continue;
			node.appendChild(typeof child === "string" ? document.createTextNode(child) : child);
		}
		return node;
	}

	function richText(str) {
		const frag = document.createDocumentFragment();
		const s = String(str || "");
		const re = /`([^`\n]+)`/g;
		let at = 0;
		let m;
		while ((m = re.exec(s)) !== null) {
			if (m.index > at) frag.appendChild(document.createTextNode(s.slice(at, m.index)));
			frag.appendChild(el("code", { text: m[1] }));
			at = re.lastIndex;
		}
		if (at < s.length) frag.appendChild(document.createTextNode(s.slice(at)));
		return frag;
	}

	function content(name) {
		return document.querySelector('[data-content="' + name + '"]');
	}

	function show(name, node) {
		content(name).replaceChildren(node);
	}

	function message(cls, text, detail) {
		return el("div", { class: cls }, [text, detail ? el("span", { class: "detail" }, [richText(detail)]) : null]);
	}

	function skeleton(rows) {
		const box = el("div", { class: "skeleton", "aria-busy": "true", "aria-label": "Loading" });
		for (let i = 0; i < rows; i++) {
			box.appendChild(el("div", { class: "sk-row" }, [0, 1, 2, 3, 4].map(() => el("span", { class: "sk" }))));
		}
		return box;
	}

	// --- HTTP

	async function request(path, init) {
		let res;
		try {
			res = await fetch(path, Object.assign({ credentials: "same-origin" }, init));
		} catch (err) {
			return { ok: false, status: 0, error: "cwm gui is not reachable — is it still running?" };
		}
		let body = null;
		try {
			body = await res.json();
		} catch (err) {
			body = null;
		}
		if (res.status === 401) {
			$("auth-banner").hidden = false;
		}
		if (!res.ok) {
			return {
				ok: false,
				status: res.status,
				error: (body && body.error) || res.statusText || "request failed",
				command: (body && body.command) || "",
			};
		}
		return { ok: true, status: res.status, data: body };
	}

	function api(path) {
		return request(path, { headers: { Accept: "application/json" } });
	}

	let csrf = "";
	async function post(path, payload, method) {
		if (!csrf) {
			const c = await api("/api/csrf");
			if (!c.ok || !c.data || !c.data.csrf) {
				return { ok: false, status: c.status, error: c.error || "could not get a CSRF token", command: "" };
			}
			csrf = c.data.csrf;
		}
		return request(path, {
			method: method || "POST",
			headers: { Accept: "application/json", "Content-Type": "application/json", "X-CWM-CSRF": csrf },
			body: JSON.stringify(payload || {}),
		});
	}

	// --- copy, toasts, inline feedback

	function announce(text) {
		const r = $("sr-live");
		r.textContent = "";
		setTimeout(() => { r.textContent = text; }, 30);
	}

	function flash(btn, text, ms) {
		const old = btn.dataset.label || btn.textContent;
		btn.dataset.label = old;
		btn.textContent = text;
		announce(text);
		clearTimeout(btn._flash);
		btn._flash = setTimeout(() => { btn.textContent = old; }, ms || 1400);
	}

	async function copyText(text, btn) {
		const ok = await copyQuiet(text);
		if (btn) flash(btn, ok ? "copied" : "copy failed");
		if (!ok) toast("error", "Copy failed", "Select the text and copy it with Ctrl/Cmd-C.");
	}

	function commandBox(cmd) {
		const btn = el("button", { type: "button", class: "cmd-copy", text: "copy", "aria-label": "copy command: " + cmd });
		btn.addEventListener("click", () => copyText(cmd, btn));
		return el("div", { class: "cmd" }, [el("code", { class: "cmd-text", tabindex: "0" }, [el("span", { class: "cmd-ps", "aria-hidden": "true", text: "$ " }), cmd]), btn]);
	}

	function toast(kind, title, detail, opts) {
		opts = opts || {};
		const box = $("toasts");
		const close = el("button", { type: "button", class: "toast-close", "aria-label": "Dismiss", text: "×" });
		const node = el("div", { class: "toast toast-" + kind }, [
			el("div", { class: "toast-title", text: title }),
			close,
			detail ? el("div", { class: "detail" }, [richText(detail)]) : null,
			opts.cmd ? commandBox(opts.cmd) : null,
			opts.link ? el("a", { class: "toast-link", href: opts.link.href, target: "_blank", rel: "noopener noreferrer", text: opts.link.text }) : null,
			opts.btn ? btnDo(opts.btn[0], opts.btn[1]) : null,
		]);
		let timer = 0;
		const dismiss = () => {
			clearTimeout(timer);
			if (!node.isConnected || node.classList.contains("leaving")) return;
			node.classList.add("leaving");
			setTimeout(() => node.remove(), reduceMotion.matches ? 0 : 230);
		};
		close.addEventListener("click", dismiss);
		if (!opts.sticky) {
			const ms = kind === "error" ? 9000 : 4500;
			timer = setTimeout(dismiss, ms);
			node.addEventListener("mouseenter", () => clearTimeout(timer));
			node.addEventListener("mouseleave", () => { timer = setTimeout(dismiss, 2500); });
		}
		while (box.children.length >= 4) box.firstElementChild.remove();
		box.appendChild(node);
		return dismiss;
	}

	function say(box, kind, text, detail, cmd) {
		const cls = kind === "wait" ? "info" : kind;
		box.replaceChildren(el("div", { class: "msg msg-" + cls }, [
			kind === "wait" ? el("span", { class: "spinner", "aria-hidden": "true" }) : null,
			text,
			detail ? el("span", { class: "detail" }, [richText(detail)]) : null,
			cmd ? commandBox(cmd) : null,
		]));
	}

	function toastLaunch(r, okText) {
		if (r.ok) {
			toastPathHint(r.data);
			return toast("ok", okText, "running: " + r.data.command);
		}
		if (r.status === 424) return toastNoClaude(r);
		if (r.status === 501) {
			return toast("error", "No terminal window could be opened on this machine",
				"Run this command in a terminal instead:", { cmd: r.command, sticky: true });
		}
		return toast("error", "Could not start it", r.error, { cmd: r.command, sticky: !!r.command });
	}

	function toastNoClaude(r) {
		return toast("error", "Claude Code is needed first", r.error,
			{ cmd: r.command, sticky: true });
	}

	function toastPathHint(d) {
		if (!d || !d.pathHint) return;
		toast("info", "claude is not on your PATH", "cwm uses " + d.claudePath +
			". To run `claude` in your own terminals too, run this once and open a new terminal:", { cmd: d.pathHint, sticky: true });
	}

	// --- debug logs (D52) and report a bug (v0.8.1)

	let verText = ""; // `cwm version` style, prefilled into the form
	const bugURL = () => "https://github.com/Mr-JoE1/cwm/issues/new?template=bug_report.yml&version=" + encodeURIComponent(verText) + "&title=bug:%20";
	const nLines = (t) => t.replace(/\n+$/, "").split("\n").length;

	async function copyQuiet(text) {
		if (navigator.clipboard && navigator.clipboard.writeText && window.isSecureContext !== false) {
			try {
				await navigator.clipboard.writeText(text);
				return true;
			} catch (err) { /* lost the user gesture while fetching, or denied: fall through */ }
		}
		const area = el("textarea", { readonly: "", class: "offscreen", "aria-hidden": "true" });
		area.value = text;
		document.body.appendChild(area);
		area.select();
		let ok = false;
		try { ok = document.execCommand("copy"); } catch (err) { ok = false; }
		area.remove();
		return ok;
	}

	function selectAll(node) {
		const range = document.createRange();
		range.selectNodeContents(node);
		const sel = window.getSelection();
		sel.removeAllRanges();
		sel.addRange(range);
		node.focus();
	}

	function showDebugText(text) {
		$("debug-bug").href = bugURL();
		const pre = $("debug-text");
		pre.textContent = text;
		const dlg = $("debug-dialog");
		if (!dlg.open) dlg.showModal();
		selectAll(pre);
	}

	let debugBusy = false;
	async function fetchDebug() {
		if (debugBusy) return null;
		debugBusy = true;
		const buttons = document.querySelectorAll("[data-debug-copy],[data-bug-report]");
		buttons.forEach((b) => { b.disabled = true; });
		const done = toast("info", "Collecting debug logs…", "runs the health checks and reads the recent logs", { sticky: true });
		let res = null;
		let text = "";
		try {
			res = await fetch("/api/debug", { credentials: "same-origin", headers: { Accept: "text/plain" } });
			text = await res.text();
		} catch (err) {
			res = null;
		}
		done();
		buttons.forEach((b) => { b.disabled = false; });
		debugBusy = false;
		if (!res || !res.ok) {
			if (res && res.status === 401) $("auth-banner").hidden = false;
			let msg = "cwm is not reachable — is the agent still running?";
			if (res) {
				try { msg = JSON.parse(text).error || msg; } catch (err) { msg = "HTTP " + res.status; }
			}
			toast("error", "Could not collect debug logs", msg + " In a terminal, `cwm debug --copy` does the same.", { cmd: "cwm debug --copy" });
			return null;
		}
		return text;
	}

	async function copyDebugLogs() {
		const text = await fetchDebug();
		if (text === null) return;
		if (await copyQuiet(text)) {
			return toast("ok", "Debug logs copied (" + nLines(text) + " lines)", "Redacted; glance over it before posting.",
				{ link: { href: bugURL(), text: "report a bug →" } });
		}
		showDebugText(text);
	}

	// Copy first (the clipboard needs this page focused), then open the
	// form in a new tab; a blocked pop-up leaves a link in the toast.
	async function reportBug() {
		const text = await fetchDebug();
		const copied = text !== null && await copyQuiet(text);
		const w = window.open(bugURL(), "_blank");
		if (w) w.opener = null;
		const link = w ? null : { href: bugURL(), text: "open the bug form →" };
		if (copied) return toast("ok", "debug logs copied — paste them into the form", "Redacted; glance over it before posting.", { link: link, sticky: !w });
		if (text === null) return;
		toast("info", "the browser blocked the clipboard", "Copy the debug logs, then paste them into the form.", {
			link: link, sticky: true,
			btn: ["copy debug logs", async () => { if (await copyQuiet(text)) toast("ok", "debug logs copied — paste them into the form"); else showDebugText(text); }],
		});
	}

	function wireDebug() {
		document.querySelectorAll("[data-debug-copy]").forEach((b) => b.addEventListener("click", copyDebugLogs));
		document.querySelectorAll("[data-bug-report]").forEach((b) => b.addEventListener("click", reportBug));
		const dlg = $("debug-dialog");
		$("debug-close").addEventListener("click", () => dlg.close());
		$("debug-select").addEventListener("click", () => {
			const pre = $("debug-text");
			selectAll(pre);
			let ok = false;
			try { ok = document.execCommand("copy"); } catch (err) { ok = false; }
			if (ok) toast("ok", "Debug logs copied (" + nLines(pre.textContent) + " lines) — paste them into the bug form");
		});
	}

	function normalizeDeviceID(v) {
		return v.toUpperCase().replace(/[\s-]/g, "");
	}

	async function pairWith(idInput, nameInput, box, button) {
		const id = normalizeDeviceID(idInput.value);
		const name = nameInput.value.trim();
		if (!/^[A-Z2-7]{52,60}$/.test(id)) {
			say(box, "error", "That does not look like a Syncthing device ID.", "Paste the full ID shown by cwm pair (or the Syncthing UI) on the other device.");
			return false;
		}
		if (name && !/^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$/.test(name)) {
			say(box, "error", "Device names may use letters, digits, spaces, dots, dashes and underscores.");
			return false;
		}
		button.disabled = true;
		say(box, "wait", "Pairing…");
		const r = await post("/api/pair", { deviceID: id, name: name });
		button.disabled = false;
		if (!r.ok) {
			const text = r.status === 503
				? "Syncthing is not reachable on this machine — start Syncthing (or run cwm pair --start in a terminal) and try again."
				: "Pairing failed.";
			say(box, "error", text, r.error);
			toast("error", "Pairing failed", r.error);
			return false;
		}
		const d = r.data;
		const what = d.changed
			? "Paired with " + d.peerID.slice(0, 7) + (d.peerName ? " (" + d.peerName + ")" : "") + "."
			: "Already paired with " + d.peerID.slice(0, 7) + " — nothing to do.";
		say(box, "ok", what, "Now pair from the other device with this device's ID: " + d.deviceID);
		toast("ok", what);
		return true;
	}

	// --- table helpers

	function col(label, cls) {
		return { label: label, cls: cls || "" };
	}

	function table(cols, rows, animate) {
		const thead = el("thead", null, [el("tr", null, cols.map((c) =>
			el("th", { class: c.cls.split(" ").filter((x) => x === "num" || x === "status").join(" ") || null, scope: "col", text: c.label })))]);
		const body = rows.map((cells, i) => {
			const tr = el("tr", animate ? { class: "reveal" } : null, cells.map((cell, k) => {
				const c = cols[k];
				const td = cell instanceof Node && cell.nodeName === "TD" ? cell : el("td", null, [cell === null || cell === undefined ? "" : String(cell)]);
				if (c.cls) td.className = (td.className ? td.className + " " : "") + c.cls;
				if (c.label) td.setAttribute("data-label", c.label);
				return td;
			}));
			if (animate) tr.style.setProperty("--i", String(Math.min(i, 18)));
			return tr;
		});
		return el("table", null, [thead, el("tbody", null, body)]);
	}

	function relTime(iso) {
		if (!iso) return "never";
		const t = new Date(iso);
		if (isNaN(t.getTime())) return iso;
		const sec = Math.max(0, (Date.now() - t.getTime()) / 1000);
		if (sec < 60) return "just now";
		if (sec < 3600) return Math.floor(sec / 60) + "m ago";
		if (sec < 86400) return Math.floor(sec / 3600) + "h ago";
		if (sec <= 7 * 86400) return Math.floor(sec / 86400) + "d ago";
		return t.toISOString().slice(0, 10);
	}

	function whenCell(iso) {
		return el("td", { title: iso || "", text: relTime(iso) });
	}

	function bytes(n) {
		if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + " GiB";
		if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MiB";
		if (n >= 1 << 10) return (n / (1 << 10)).toFixed(1) + " KiB";
		return n + " B";
	}

	function badge(statusName, label) {
		const known = statusName === "ok" || statusName === "warn" || statusName === "fail";
		const cls = known ? statusName : "warn";
		return el("span", { class: "badge badge-" + cls, text: label || (statusName === "fail" ? "fail" : String(statusName)) });
	}

	// --- tabs

	function dayGroup(iso) {
		const t = new Date(iso);
		if (isNaN(t.getTime())) return "earlier";
		const day = (d) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
		const diff = Math.round((day(new Date()) - day(t)) / 86400000);
		if (diff <= 0) return "today";
		if (diff === 1) return "yesterday";
		if (diff < 7) return "this week";
		return WD[t.getDay()] + ", " + t.getDate() + " " + MON[t.getMonth()] + (t.getFullYear() !== new Date().getFullYear() ? " " + t.getFullYear() : "");
	}

	function clockTime(iso) {
		const t = new Date(iso);
		return isNaN(t.getTime()) ? "" : String(t.getHours()).padStart(2, "0") + ":" + String(t.getMinutes()).padStart(2, "0");
	}

	function renderSessions(animate) {
		const input = $("sessions-filter");
		const q = input.value.trim().toLowerCase();
		if (sessionsCache.length === 0) {
			return show("sessions", message("empty", "No sessions yet — open a repo with cwm open <name> to start one."));
		}
		const list = sessionsCache.filter((s) => !q || (s.project + " " + s.firstPrompt + " " + s.summary).toLowerCase().includes(q));
		if (!list.length) {
			const clear = el("button", { type: "button", class: "btn btn-small", text: "clear filter" });
			clear.addEventListener("click", () => { input.value = ""; renderSessions(false); input.focus(); });
			return show("sessions", el("div", { class: "empty-filter" }, [
				el("p", { class: "empty-q", text: "no sessions match \u201c" + input.value.trim() + "\u201d" }), clear,
				el("p", { class: "hint", text: "Search covers the project name and the first prompt." }),
			]));
		}
		const head = el("thead", null, [el("tr", null, ["prompt", "project", "branch", "msgs", "id", "when", ""].map((h, k) =>
			el("th", { scope: "col", class: k === 3 || k === 5 ? "num" : null, text: h })))]);
		const body = el("tbody");
		let group = "";
		list.forEach((s, i) => {
			const g = dayGroup(s.modified);
			if (g !== group) {
				group = g;
				body.appendChild(el("tr", { class: "day-row" }, [el("th", { colspan: "7", scope: "rowgroup", text: g })]));
			}
			const prompt = el("td", { class: "s-prompt" }, [s.firstPrompt || "(no prompt)"]);
			if (s.summary) prompt.title = s.summary;
			const tr = el("tr", { class: "s-row" + (animate ? " reveal" : ""), tabindex: "0", "aria-label": (s.firstPrompt || "(no prompt)") + ", " + s.project + ". Enter resumes." }, [
				prompt,
				el("td", { class: "s-proj", text: s.project }),
				el("td", { class: "s-branch" }, [s.branch ? el("span", { class: "branch", title: s.branch, text: s.branch }) : ""]),
				el("td", { class: "num s-msgs", text: s.messages ? nf.format(s.messages) : "" }),
				el("td", { class: "s-id", title: s.id, text: s.id.slice(0, 8) }),
				el("td", { class: "num s-when", title: s.modified || "", text: g === "today" || g === "yesterday" ? clockTime(s.modified) : relTime(s.modified) }),
				resumeCell(s),
			]);
			tr.addEventListener("keydown", (ev) => {
				if (ev.target !== tr) return;
				if (ev.key === "Enter") { ev.preventDefault(); tr.querySelector(".s-resume").click(); }
				else if (ev.key === "ArrowDown" || ev.key === "ArrowUp") {
					ev.preventDefault();
					const rows = [...body.querySelectorAll(".s-row")];
					const next = rows[rows.indexOf(tr) + (ev.key === "ArrowDown" ? 1 : -1)];
					if (next) next.focus();
				}
			});
			if (animate) tr.style.setProperty("--i", String(Math.min(i, 18)));
			body.appendChild(tr);
		});
		show("sessions", el("table", { class: "sessions" }, [head, body]));
	}

	function resumeCell(s) {
		const btn = el("button", { type: "button", class: "btn btn-small s-resume", text: "resume" });
		btn.title = "Open a terminal window resuming this session";
		btn.setAttribute("aria-label", "Resume session " + s.id.slice(0, 8) + " of " + s.project);
		btn.addEventListener("click", () => resumeSession(s, btn));
		return el("td", { class: "actions" }, [btn]);
	}

	async function resumeSession(s, btn) {
		let old = "";
		if (btn) {
			btn.disabled = true;
			old = btn.textContent;
			btn.replaceChildren(el("span", { class: "spinner", "aria-hidden": "true" }), "Opening…");
		}
		const r = await post("/api/sessions/resume", { project: s.project, id: s.id });
		if (btn) {
			btn.disabled = false;
			btn.textContent = old;
		}
		toastLaunch(r, "Opened a terminal for " + s.project + " · " + s.id.slice(0, 8));
	}

	function changed(name, data) {
		const key = JSON.stringify(data);
		if (lastJSON[name] === key) return false;
		lastJSON[name] = key;
		return true;
	}

	function loadError(name, r, silent, text, detail) {
		if (silent && r.status === 0) {
			setLive("offline");
			return;
		}
		delete lastJSON[name];
		show(name, message(r.status === 503 ? "notice" : "error", text, detail === undefined ? r.error : detail));
	}

	const loaders = {
		async stats(silent) {
			const url = (tz) => "/api/stats?range=" + encodeURIComponent(statsRange) + (tz ? "&tz=" + encodeURIComponent(statsTZ) : "");
			let r = await api(url(!!statsTZ));
			if (!r.ok && r.status === 400 && statsTZ) r = await api(url(false));
			if (!r.ok) return loadError("stats", r, silent, "Could not load stats.");
			if (r.data.disabled) return show("stats", message("notice", r.data.reason));
			if (!changed("stats", r.data)) return;
			renderStats(r.data);
		},

		async sessions(silent) {
			const r = await api("/api/sessions");
			if (!r.ok) return loadError("sessions", r, silent, "Could not load sessions.");
			sessionsCache = r.data || [];
			if (!changed("sessions", sessionsCache)) return;
			renderSessions(!silent);
		},

		async projects(silent) {
			const r = await api("/api/projects");
			if (!r.ok) return loadError("projects", r, silent, "Could not load projects.");
			const list = r.data || [];
			if (!changed("projects", list)) return;
			if (list.length === 0) {
				return show("projects", message("empty", "No projects yet — open a repo via cwm open <name> new to create one."));
			}
			const most = Math.max(1, ...list.map((p) => p.sessions || 0));
			show("projects", table([col("project", "lead"), col("sessions", "num"), col("activity"), col("last session", "when")],
				list.map((p) => {
					const fill = el("span");
					fill.style.width = Math.round(((p.sessions || 0) / most) * 100) + "%";
					return [
						el("td", null, [el("span", { class: "proj", text: p.name })]),
						p.sessions,
						el("td", { "aria-hidden": "true" }, [el("span", { class: "bar" }, [fill])]),
						whenCell(p.modified),
					];
				}), !silent));
		},

		async devices(silent) {
			const r = await api("/api/devices");
			if (!r.ok) {
				const text = r.status === 503
					? "Syncthing is not reachable on this machine (optional — run cwm pair to set up sync)."
					: "Could not load devices.";
				return loadError("devices", r, silent, text);
			}
			const setup = r.data && !Array.isArray(r.data) && r.data.status === "not_configured";
			$("pair-form").hidden = setup;
			if (setup) {
				if (!changed("devices", r.data)) return;
				return show("devices", syncSetupCard(r.data));
			}
			const list = r.data || [];
			if (!changed("devices", list)) return;
			if (list.length === 0) {
				return show("devices", message("empty", "No paired devices yet. Pair one with the form above, or run cwm pair on both devices."));
			}
			show("devices", table([
				col("device", "lead"), col("state", "status"), col("id", "mono"), col("address", "mono"),
				col("in", "num"), col("out", "num"),
			], list.map((d) => {
				const pct = Math.max(0, Math.min(100, Number(d.completion) || 0));
				let state;
				if (!d.connected) state = el("span", { class: "dev-state dev-off", text: "offline" });
				else if (pct < 100) {
					const fill = el("span");
					fill.style.width = pct + "%";
					state = el("span", { class: "dev-state dev-sync" }, ["syncing " + Math.floor(pct) + "%", el("span", { class: "bar", "aria-hidden": "true" }, [fill])]);
				} else state = el("span", { class: "dev-state dev-on", text: "online" });
				return [
					el("td", null, [el("span", { class: "proj", text: d.name || d.id.slice(0, 7) })]),
					el("td", null, [state]),
					el("td", { title: groupID(d.id), text: d.id.slice(0, 7) }),
					d.address || "",
					bytes(d.inBytes),
					bytes(d.outBytes),
				];
			}), !silent));
		},

		async handoff(silent) {
			const r = await api("/api/wip");
			if (!r.ok) return loadError("handoff", r, silent, "Could not scan repos.");
			const list = r.data || [];
			if (!changed("handoff", list)) return;
			if (list.length === 0) {
				return show("handoff", message("empty", "Nothing to hand off: every local clone is committed."));
			}
			show("handoff", table([col("repo", "lead"), col("changed", "status"), col("last commit", "muted wide")], list.map((d) => [
				el("td", null, [el("span", { class: "proj", text: d.name })]),
				el("td", null, [badge("warn", d.changed + " file" + (d.changed === 1 ? "" : "s"))]),
				d.lastCommit,
			]), !silent));
		},

		async health(silent) {
			const r = await api("/api/doctor");
			if (!r.ok) return loadError("health", r, silent, "Could not run health checks.");
			const list = r.data || [];
			healthBadge(list);
			const c = list.find((x) => x.name === "conflicts" && isProblem(x));
			const cr = c ? await api("/api/conflicts") : null;
			const cs = cr && cr.ok ? cr.data.conflicts : [];
			if (!changed("health", [list, cs])) return;
			const view = healthView(list, !silent);
			if (cs.length) view.insertBefore(conflictsCard(cs), view.children[1] || null);
			show("health", view);
		},

		async settings(silent) {
			const r = await loadSettings();
			if (!r.ok) return loadError("settings", r, silent, "Could not load settings.");
			if (!changed("settings", r.data)) return;
			renderSettings(r.data);
		},

		async accounts(silent) {
			const r = await api("/api/accounts");
			if (!r.ok) return loadError("accounts", r, silent, "Could not load accounts.");
			const list = r.data || [];
			scheduleAccountsPoll(list);
			renderAccountsDot(list);
			if (!changed("accounts", list)) return;
			renderAccounts(list, !silent);
		},
	};

	// --- health: a boot log, problems first (D72)

	const HEALTH = {
		home: ["cwm home cannot be found", "cwm cannot read or create its home folder.", "cwm init"],
		workspace: ["the workspace folder is missing", "Sessions and memory live there; nothing can be resumed without it.", "cwm init"],
		"hook-wired": ["a session hook is broken", "Claude Code runs it when a session starts or ends; it fails until it is fixed.", "cwm init"],
		"cwm-path": ["background service can't find cwm", "Its PATH has no cwm folder; Claude Code started the same way (desktop app, IDE) could not run cwm's hooks.", ""],
		claude: ["claude not found", "The desktop app and the VS Code extension do not put claude on PATH; cwm found no standalone CLI.", ""],
		"claude-bundled": ["claude comes with an app", "cwm uses it, but it updates with the app; a standalone CLI is more stable.", ""],
		"claude-binary": ["claude is not on your PATH", "cwm finds it, but your own terminals and IDEs will not.", ""],
		"claude-reads-from": ["claude reads another folder", "A persistent CLAUDE_CONFIG_DIR points plain claude and IDEs away from the workspace. Remove the line shown.", ""],
		migration: ["cwm now captures all Claude Code activity", "This workspace predates v0.8. Review the migration; nothing moves until you apply it.", "cwm migrate --dry-run"],
		settings: ["session hooks are not wired", "The workspace settings.json has no SessionStart hook, so the repo list and handoff are not recorded.", "cwm doctor --fix"],
		"cwm-hooks": ["cwm's hooks do not match the setting", "settings.json and features.hooks disagree.", "cwm doctor --fix"],
		"mcp-bridge": ["MCP servers are out of sync", "A .claude.json lacks the workspace MCP servers.", "cwm doctor --fix"],
		conflicts: ["two versions of a file", "Sync kept both; pick the one to keep.", "cwm conflicts"],
		vault: ["the default login is not in both places", "Switching needs a saved copy in the vault; plain claude needs the live one.", "cwm doctor --fix"],
		"hook-scripts": ["bash-kit hooks are wired but missing", "settings.json calls hook scripts that are not there.", "cwm init"],
		"repos-tsv": ["the repo list needs attention", "cwm clones repos on other devices from repos.tsv.", ""],
		"session-folders": ["some sessions were started outside cwm", "Plain `claude` created these folders; they do not follow you to your other devices.", "cwm sessions"],
		"diverged-sessions": ["a session was continued on two devices", "Both copies are kept; resume picks the newest.", "cwm doctor"],
		stignore: ["the sync ignore file is out of date", "It must match the sync switches, or Syncthing shares the wrong files.", "cwm doctor --fix"],
		syncthing: ["sync is not set up", "Optional. Syncthing keeps the workspace the same on all your devices.", "cwm pair --start"],
		ssh: ["GitHub SSH login failed", "Private repos cannot be cloned until your SSH key works.", "ssh -T git@github.com"],
	};
	const PASSED = {
		home: "cwm home", workspace: "workspace folder", "hook-wired": "session hooks", "hooks-executable": "hooks are executable",
		claude: "Claude Code installed", "claude-binary": "claude on PATH", "claude-cleared": "claude path re-checked", "claude-reads-from": "claude reads the workspace",
		"hook-scripts": "hook scripts", "repos-tsv": "repo list", "session-folders": "session folders", "diverged-sessions": "session copies", settings: "workspace settings",
		stignore: "sync ignore file", syncthing: "Syncthing reachable", ssh: "GitHub SSH", "wip-log": "handoff log", "path-duplicates": "PATH",
		"cwm-hooks": "cwm hooks", "mcp-bridge": "MCP bridge", conflicts: "no conflicts", vault: "default login saved", "unknown-items": "local-only items",
	};
	const BOOT = { ok: "[  OK  ]", warn: "[ WARN ]", fail: "[FAILED]", skip: "[ SKIP ]" };
	const SR_STATUS = { ok: "passed:", warn: "warning:", fail: "failed:", skip: "optional, not set up:" };
	const level = (c) => c.optional && c.status !== "ok" ? "skip" : BOOT[c.status] ? c.status : "warn";
	const isProblem = (c) => /^(warn|fail)$/.test(level(c));

	function fixCommand(c, fallback) {
		const d = String(c.detail || "");
		let m = /install Claude Code: (.+)$/.exec(d);
		if (m) return m[1].trim();
		m = /fix: `([^`]+)`/.exec(d) || /fix: (cwm [\w -]+)/.exec(d);
		if (m) return m[1].trim();
		m = /hook not executable: (\S+) \(chmod \+x\)/.exec(d);
		if (m) return "chmod +x " + m[1];
		if (c.name === "repos-tsv") return /not readable/.test(d) ? "cwm open <name>" : "";
		return fallback || "";
	}

	function checkRow(c, i, animate) {
		const st = level(c);
		const plain = st === "ok" ? null : HEALTH[c.name];
		const title = plain ? plain[0] : st === "ok" ? (PASSED[c.name] || c.name) : String(c.detail || c.name).split(" — ")[0];
		const cmd = st === "ok" ? "" : fixCommand(c, plain ? plain[2] : "");
		const raw = [c.detail, ...(c.lines || [])].filter(Boolean).join("\n");
		const li = el("li", { class: "check check-" + st + (animate ? " reveal" : "") }, [
			el("span", { class: "boot boot-" + st, "aria-hidden": "true", text: BOOT[st] }),
			el("div", { class: "check-body" }, [
				el("span", { class: "check-title" }, [el("span", { class: "sr-only", text: SR_STATUS[st] + " " }), title]),
				plain ? el("span", { class: "check-why" }, [richText(plain[1])]) : null,
				cmd ? commandBox(cmd) : null,
				c.name === "claude-bundled" && st !== "ok" ? btnDo("use this", useBundled) : null,
				el("details", { class: "check-details" }, [el("summary", { text: "details" }),
					el("pre", { class: "check-raw" }, [el("span", { class: "check-id", text: c.name + ": " }), raw])]),
			]),
		]);
		if (animate) li.style.setProperty("--i", String(Math.min(i, 18)));
		return li;
	}

	// D98: nothing to save; cwm already uses the bundled copy. Say which one.
	async function useBundled() {
		const r = await api("/api/claude");
		const d = r.ok ? r.data : {};
		if (!d.bundled) return load("health");
		toast("ok", "cwm uses the claude bundled with " + d.bundle, d.path + " — found afresh each time, nothing saved. More stable: the standalone CLI.",
			{ cmd: d.install[0].command, sticky: true });
	}

	function healthView(list, animate) {
		const rank = { fail: 0, warn: 1, skip: 2, ok: 3 };
		const sorted = list.map((c, i) => [c, i]).sort((a, b) => (rank[level(a[0])] - rank[level(b[0])]) || a[1] - b[1]).map((x) => x[0]);
		const bad = sorted.filter(isProblem);
		const rows = sorted.filter((c) => level(c) !== "ok");
		const good = sorted.filter((c) => level(c) === "ok");
		const counted = list.length - rows.length + bad.length; // skipped optional checks count for nothing
		const summary = bad.length
			? el("div", { class: "health-sum health-sum-warn", role: "status" }, [el("strong", { text: plural(bad.length, "thing") + " to fix" }),
				el("span", { class: "health-sum-sub", text: good.length + " of " + counted + " checks passed" })])
			: el("div", { class: "health-sum health-sum-ok", role: "status" }, [el("strong", { text: "all " + counted + " checks passed" })]);
		const box = el("div", { class: "health" }, [summary]);
		if (rows.length) box.appendChild(el("ul", { class: "checks" }, rows.map((c, i) => checkRow(c, i, animate))));
		if (good.length) {
			const det = el("details", { class: "passed" }, [el("summary", { class: "passed-sum", text: plural(good.length, "check") + " passed" }),
				el("ul", { class: "checks" }, good.map((c, i) => checkRow(c, i, false)))]);
			det.open = !bad.length;
			box.appendChild(det);
		}
		return box;
	}

	function conflictsCard(cs) {
		const pick = async (c, choice, b) => {
			b.disabled = true;
			const r = await post("/api/conflicts/resolve", { path: c.path, choice: choice });
			if (r.ok) toast("ok", "Kept one version of " + c.path, "The other went to " + r.data.backup);
			else toast("error", "Could not resolve " + c.path, r.error);
			load("health", true);
		};
		return el("div", { class: "card conflicts" }, [el("h3", { text: "pick a version" }), ...cs.map((c) => el("div", { class: "set-row" }, [
			el("div", { class: "set-text" }, [el("span", { class: "set-key", text: (c.copies.length + (c.exists ? 1 : 0)) + " versions of " + c.path + " — pick one" }),
				el("span", { class: "set-help", text: "The versions you do not keep are moved to backups, never deleted." })]),
			el("div", { class: "acct-actions" }, [
				c.exists ? btnDo("keep this device's", (b) => pick(c, "current", b)) : null,
				...c.copies.map((k) => btnDo("keep " + k.device + " " + k.when.slice(0, 8), (b) => pick(c, k.name, b))),
				c.mergeable ? btnDo("keep both merged", (b) => pick(c, "merge", b), "btn-primary") : null,
			]),
		]))]);
	}

	// --- devices: Syncthing not set up yet (D58)

	function groupID(id) {
		const bare = String(id || "").replace(/-/g, "");
		return bare.match(/.{1,7}/g) ? bare.match(/.{1,7}/g).join("-") : "";
	}

	function syncthingInstall() {
		const os = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || "";
		if (/win/i.test(os)) return "winget install Syncthing.Syncthing";
		if (/mac/i.test(os)) return "brew install syncthing";
		return "sudo apt install syncthing";
	}

	function syncSetupCard(d) {
		const step = (n, title, body) => el("li", { class: "onb-step" }, [el("span", { class: "onb-n", "aria-hidden": "true", text: String(n) }),
			el("div", { class: "onb-body" }, [el("strong", { text: title }), ...body])]);
		return el("div", { class: "setup-card" }, [
			el("h3", { text: "Keep this workspace in sync across your devices" }),
			el("p", { class: "hint" }, ["Optional. Syncthing copies the workspace peer to peer between your own devices; nothing goes through a server."]),
			el("ol", { class: "onb" }, [
				step(1, "install Syncthing", [commandBox(syncthingInstall()), el("p", { class: "hint onb-hint", text: "Any install works; on Linux a distribution package or the official one." })]),
				step(2, "start sync for cwm", [commandBox("cwm pair --start"), el("p", { class: "hint onb-hint", text: "Sets up the shared workspace folder and prints this device's ID." })]),
				step(3, "pair the other device", [commandBox("cwm pair <device-id>"), el("p", { class: "hint onb-hint", text: "Run steps 1 and 2 there too, then pair each device with the other's ID. The pair form appears here once Syncthing runs." })]),
			]),
			d.error ? el("details", { class: "check-details" }, [el("summary", { text: "details" }), el("pre", { class: "check-raw", text: d.error })]) : null,
		]);
	}

	// --- accounts (D55-D57)

	const LOGIN_PILL = {
		never: ["warn", "no login yet"],
		in_progress: ["info", "login in progress"],
		done: ["ok", "logged in"],
		failed: ["fail", "login failed"],
	};

	function loginPill(st) {
		const p = LOGIN_PILL[st] || ["warn", String(st || "unknown")];
		const pill = el("span", { class: "badge pill badge-" + p[0] }, [
			st === "in_progress" ? el("span", { class: "spinner", "aria-hidden": "true" }) : null,
			p[1],
		]);
		return pill;
	}

	function defaultWithoutLogin(list) {
		const d = list.find((a) => a.is_default);
		if (!list.length) return { none: true };
		if (d && !d.has_recorded_login) return { account: d };
		return null;
	}

	function renderAccountsDot(list) {
		const need = defaultWithoutLogin(list);
		setBadge("accounts", need ? "!" : "", need ? (need.none ? "no account yet" : "the default account has no login") : "");
	}

	function setBadge(tab, text, label) {
		for (const b of [$("badge-" + tab), $("bb-badge-" + tab)]) {
			if (!b) continue;
			b.hidden = !text;
			b.textContent = text || "";
			b.title = label || "";
			if (label) b.setAttribute("aria-label", label); else b.removeAttribute("aria-label");
		}
	}

	function healthBadge(list) {
		const n = list.filter(isProblem).length;
		setBadge("health", n ? String(n) : "", n ? n + (n === 1 ? " thing" : " things") + " to fix" : "");
	}

	async function refreshHealthBadge() {
		if (current() === "health" && loaded.health) return; // the tab sets it itself
		const r = await api("/api/doctor");
		if (r.ok && Array.isArray(r.data)) healthBadge(r.data);
	}

	let accountsPoll = 0;
	function scheduleAccountsPoll(list) {
		clearTimeout(accountsPoll);
		if (!list.some((a) => a.login_status === "in_progress")) return;
		accountsPoll = setTimeout(() => {
			if (!document.hidden && current() === "accounts" && dashboardVisible()) load("accounts", true);
			else stale.accounts = true;
		}, 2000);
	}

	function renderBanner(list) {
		const box = $("acct-banner");
		const need = defaultWithoutLogin(list);
		if (!need) {
			box.hidden = true;
			box.replaceChildren();
			return;
		}
		let title, text, btn;
		if (need.none) {
			title = "Add your first account";
			text = "cwm has no Claude account yet. Pick a short name below; a terminal window opens for the Claude login, and this page follows it.";
			btn = el("button", { type: "button", class: "btn btn-primary", text: "Add your first account" });
			btn.addEventListener("click", () => {
				const input = $("acct-name");
				if (!input.value) input.value = "work";
				input.focus();
				input.select();
			});
		} else {
			const a = need.account;
			const st = a.login_status;
			title = st === "in_progress" ? "Finishing the login of “" + a.name + "”…" : "Your default account “" + a.name + "” has no login yet";
			text = st === "in_progress"
				? "Finish the Claude login in the terminal window that opened. This page updates when it is done."
				: "cwm cannot start Claude Code with it until it is logged in. Log it in now, or add your first account under another name.";
			btn = st === "in_progress" ? null : accountLoginButton(a, st === "failed" ? "Retry login" : "Log in “" + a.name + "”", "btn-primary");
		}
		box.replaceChildren(
			el("div", { class: "first-banner-mark", "aria-hidden": "true", text: ">_" }),
			el("div", { class: "first-banner-text" }, [el("h3", { text: title }), el("p", { text: text })]),
			btn ? el("div", { class: "first-banner-actions" }, [btn]) : null,
		);
		box.hidden = false;
	}

	function accountLoginButton(a, label, extra) {
		const btn = el("button", { type: "button", class: "btn btn-small " + (extra || ""), text: label });
		btn.setAttribute("aria-label", label + " (account " + a.name + ")");
		btn.addEventListener("click", () => startLogin(a.name, btn));
		return btn;
	}

	function metaLine(label, value, title) {
		return el("div", { class: "acct-meta-row" }, [
			el("span", { class: "acct-meta-label", text: label }),
			el("span", { class: "acct-meta-value", title: title || "", text: value }),
		]);
	}

	function lastLoginText(a, st) {
		if (a.last_login) return relTime(a.last_login);
		if (st === "done") return "logged in, time unknown";
		return st === "never" ? "never" : "unknown";
	}

	function acctMenu(a, st) {
		const btn = el("button", { type: "button", class: "icon-btn acct-more", "aria-expanded": "false", "aria-label": "More actions for " + a.name, title: "More actions" }, [svgDots()]);
		const menu = el("div", { class: "menu acct-menu", hidden: "" });
		const item = (label, run, cls) => {
			const b = el("button", { type: "button", class: "menu-item" + (cls ? " " + cls : ""), text: label });
			b.addEventListener("click", () => { close(); run(b); });
			menu.appendChild(b);
		};
		const close = (refocus) => { menu.hidden = true; btn.setAttribute("aria-expanded", "false"); if (refocus) btn.focus(); };
		if (st !== "in_progress") item(st === "done" ? "log in again" : "log in", () => startLogin(a.name, null));
		if (!a.is_default) item("make default", (b) => setDefault(a.name, b));
		if (st === "done") item(a.is_default ? "start claude" : "run in parallel", () => runAccount(a));
		item("remove", () => askRemove(a, btn), "menu-danger");
		menu.lastChild.setAttribute("data-acct-remove", "");
		btn.addEventListener("click", (ev) => {
			ev.stopPropagation();
			const open = menu.hidden;
			document.querySelectorAll(".acct-menu").forEach((m) => { m.hidden = true; });
			menu.hidden = !open;
			btn.setAttribute("aria-expanded", String(open));
			if (open) menu.querySelector("button").focus();
		});
		menu.addEventListener("keydown", (ev) => { if (ev.key === "Escape") { ev.stopPropagation(); close(true); } });
		return el("div", { class: "acct-menu-wrap" }, [btn, menu]);
	}

	function svgDots() {
		const s = svg("svg", { class: "i", "aria-hidden": "true", focusable: "false" });
		s.appendChild(svg("use", { href: "#i-more" }));
		return s;
	}

	function accountCard(a, i, animate) {
		const st = a.login_status || "never";
		const head = el("div", { class: "acct-head" }, [
			el("span", { class: "acct-name", text: a.name }),
			a.is_default ? el("span", { class: "tag tag-default", text: "default" }) : null,
			a.running_now ? el("span", { class: "tag tag-run", text: "running now" }) : null,
			loginPill(st),
			acctMenu(a, st),
		]);
		const meta = el("div", { class: "acct-meta" }, [
			metaLine("login", a.email ? a.email + (a.plan ? " · " + a.plan : "") : "none recorded"),
			metaLine("last login", lastLoginText(a, st), a.last_login),
			metaLine("last used", a.last_used ? relTime(a.last_used) : "not launched yet", a.last_used),
			metaLine("runs", a.is_default ? "in the workspace root (plain claude uses it)" : "in parallel: cwm run --account " + a.name),
		]);
		const children = [head, meta];
		if (st === "failed" || (st === "in_progress" && a.login_error)) {
			children.push(el("div", { class: "msg msg-error acct-error" }, [
				"The last login did not finish.",
				a.login_error ? el("span", { class: "detail", text: a.login_error }) : null,
				el("div", { class: "acct-inline" }, [accountLoginButton(a, "retry login", "btn-primary")]),
			]));
		} else if (st === "in_progress") {
			children.push(el("div", { class: "msg msg-info acct-error" }, [
				el("span", { class: "spinner", "aria-hidden": "true" }),
				"Waiting for the login in its terminal window…",
				el("div", { class: "acct-inline" }, [accountLoginButton(a, "open a new login window")]),
			]));
		} else if (st === "never") {
			children.push(el("div", { class: "acct-actions" }, [accountLoginButton(a, "log in", "btn-primary")]));
		} else if (st === "done") {
			children.push(el("div", { class: "acct-actions" }, [
				btnDo(a.is_default ? "start claude" : "run in parallel", () => runAccount(a), a.is_default ? "" : "btn-primary"),
				a.is_default ? null : btnDo("make default", (b) => setDefault(a.name, b)),
			]));
		}
		const card = el("article", { class: "acct-card" + (a.is_default ? " is-default" : "") + (animate ? " reveal" : ""), "aria-label": "Account " + a.name }, children);
		if (animate) card.style.setProperty("--i", String(Math.min(i, 18)));
		return card;
	}

	let accountsCache = [];
	function renderAccounts(list, animate) {
		accountsCache = list;
		renderBanner(list);
		const box = content("accounts");
		const add = $("acct-add-card");
		box.replaceChildren(el("div", { class: "acct-grid" }, [...list.map((a, i) => accountCard(a, i, animate)), add]));
		add.hidden = false;
		if (pendingAdd) {
			const a = list.find((x) => x.name === pendingAdd);
			if (a && a.login_status === "done") {
				const name = pendingAdd;
				pendingAdd = "";
				const res = $("acct-add-result");
				if (a.is_default) return say(res, "ok", "“" + name + "” is logged in and is the default account.");
				const mk = el("button", { type: "button", class: "btn btn-small btn-primary", text: "make default" });
				mk.addEventListener("click", () => setDefault(name, mk));
				res.replaceChildren(el("div", { class: "msg msg-ok" }, ["“" + name + "” is logged in. Make it the default account?", el("div", { class: "acct-inline" }, [mk])]));
			}
		}
	}
	let pendingAdd = "";

	function validAccountName(name) {
		if (!name) return "Enter a name.";
		if (name.length > 32) return "At most 32 characters.";
		if (/[\\/]/.test(name)) return "No / or \\ in account names.";
		if (["add", "list", "use", "remove", "rm", "login"].includes(name)) return "“" + name + "” is reserved by cwm — pick another name.";
		if (!/^[a-z0-9][a-z0-9_-]*$/.test(name) || /^[0-9]+$/.test(name)) return "Use lowercase letters, digits, dashes or underscores, starting with a letter or digit (not all digits).";
		if (accountsCache.some((a) => a.name === name)) return "There already is an account “" + name + "”.";
		return "";
	}

	function reportLogin(box, name, r) {
		if (r.ok) {
			toastPathHint(r.data);
			toast("ok", "Opened a login window for “" + name + "”", "Finish the Claude login there — this page follows it.");
			if (box) say(box, "wait", "Finish the Claude login in the terminal window that just opened…", "If it closed without logging in, use Retry on the account card, or run:", r.data.command);
			return true;
		}
		if (r.status === 424) {
			toastNoClaude(r);
			if (box) say(box, "error", "Claude Code is needed first.", r.error, r.command);
			return false;
		}
		if (r.status === 501) {
			toast("info", "No terminal window could be opened", "Run the login yourself in a terminal:", { cmd: r.command, sticky: true });
			if (box) say(box, "info", "No terminal window could be opened on this machine. Run this command in a terminal — this page follows the login:", "", r.command);
			return true;
		}
		toast("error", "Could not start the login", r.error, { cmd: r.command, sticky: !!r.command });
		if (box) say(box, "error", "Could not start the login.", r.error, r.command);
		return false;
	}

	async function addAccount() {
		const input = $("acct-name");
		const box = $("acct-add-result");
		const btn = $("acct-add");
		const name = input.value.trim();
		const problem = validAccountName(name);
		if (problem) return say(box, "error", problem);
		btn.disabled = true;
		say(box, "wait", "Creating “" + name + "” and opening a login window…");
		const r = await post("/api/accounts/add", { name: name });
		btn.disabled = false;
		if (reportLogin(box, name, r) && r.status !== 424) { input.value = ""; pendingAdd = name; }
		await load("accounts", true);
		refreshState();
	}

	async function startLogin(name, btn) {
		if (btn) {
			btn.disabled = true;
			btn.replaceChildren(el("span", { class: "spinner", "aria-hidden": "true" }), "Opening…");
		}
		const r = await post("/api/accounts/login", { name: name });
		reportLogin(null, name, r);
		await load("accounts", true);
	}

	async function runAccount(a) {
		const r = await post("/api/accounts/run", { name: a.name });
		if (r.status === 424) return toastNoClaude(r);
		if (r.status === 501) return toast("info", "No terminal window could be opened", "Run it yourself in a terminal:", { cmd: r.command, sticky: true });
		toastLaunch(r, a.is_default ? "Started claude as “" + a.name + "”" : "Started “" + a.name + "” in parallel");
	}

	async function setDefault(name, btn) {
		const cur = accountsCache.find((a) => a.is_default);
		const busy = accountsCache.some((a) => a.running_now);
		if (!await confirmBox("make “" + name + "” the default", "Plain claude, cwm open and Resume log in as “" + name + "” from now on. cwm saves " +
			(cur ? "“" + cur.name + "”’s" : "the current") + " login first, so nothing is lost; sessions and settings are shared and stay.",
		[busy ? "Claude is running now: open sessions keep using " + (cur ? "“" + cur.name + "”" : "the old login") + " until restarted." :
			"Any open Claude session keeps the old login until it is restarted."], "make default")) return;
		btn.disabled = true;
		const r = await post("/api/accounts/set-default", { name: name });
		if (r.ok) toast(r.data.warning ? "info" : "ok", "“" + name + "” is the default account now", r.data.warning || "Plain claude, cwm open and Resume use it.");
		else toast("error", "Could not change the default account", r.error);
		await load("accounts", true);
		refreshState();
	}

	let removing = null;
	let removeReturn = null;
	let cwmHome = "";
	async function askRemove(a, trigger) {
		const dlg = $("acct-remove-dialog");
		removing = a;
		removeReturn = trigger || document.activeElement;
		if (!cwmHome) {
			const r = await api("/api/about");
			cwmHome = r.ok && r.data && r.data.workspace ? String(r.data.workspace.path).replace(/[\\/]workspace$/, "") : "~/.cwm";
		}
		const only = accountsCache.length <= 1;
		$("acct-remove-title").textContent = "remove account “" + a.name + "”";
		$("acct-remove-text").replaceChildren(...(only
			? ["“" + a.name + "” is the only account. cwm needs one to start Claude Code, so it cannot be removed — add another account first."]
			: ["cwm forgets this login: its saved credentials and login-only folder are copied to ", el("code", { text: cwmHome + "/backups/" }), " first. Sessions, memory and settings are shared and stay." +
				(a.is_default ? " It is the default account; the next remaining account becomes the default." : "")]));
		$("acct-remove-name").value = "";
		$("acct-remove-name").placeholder = a.name;
		$("acct-remove-name-label").textContent = "type “" + a.name + "” to confirm";
		$("acct-remove-form").hidden = only;
		$("acct-remove-confirm").disabled = true;
		dlg.showModal();
		if (!only) $("acct-remove-name").focus();
	}

	function wireAccounts() {
		$("acct-add").addEventListener("click", addAccount);
		$("acct-name").addEventListener("keydown", (ev) => { if (ev.key === "Enter") addAccount(); });
		$("acct-add-open").addEventListener("click", () => {
			$("acct-add-open").hidden = true;
			$("acct-add-form").hidden = false;
			$("acct-name").focus();
		});
		document.addEventListener("click", (ev) => {
			document.querySelectorAll(".acct-menu:not([hidden])").forEach((m) => {
				if (!m.parentNode.contains(ev.target)) { m.hidden = true; m.previousSibling.setAttribute("aria-expanded", "false"); }
			});
		});
		const dlg = $("acct-remove-dialog");
		dlg.addEventListener("close", () => { if (removeReturn && removeReturn.isConnected) removeReturn.focus(); removeReturn = null; });
		$("acct-remove-name").addEventListener("input", (ev) => {
			$("acct-remove-confirm").disabled = !removing || ev.target.value.trim() !== removing.name;
		});
		$("acct-remove-cancel").addEventListener("click", () => dlg.close());
		$("acct-remove-confirm").addEventListener("click", async () => {
			const a = removing;
			if (!a || $("acct-remove-name").value.trim() !== a.name) return;
			const btn = $("acct-remove-confirm");
			btn.disabled = true;
			const r = await post("/api/accounts/remove", { name: a.name, also_delete_config_dir: true });
			dlg.close();
			if (r.ok) {
				toast("ok", "Removed “" + a.name + "”", (r.data.backup ? "Backup: " + r.data.backup + ". " : "") +
"Default account: " + (r.data.default || "none") + ".");
			} else {
				toast("error", "Could not remove “" + a.name + "”", r.error);
			}
			await load("accounts", true);
			refreshState();
		});
	}

	// --- settings (v0.8, D95): every `cwm config` key, PUT /api/settings

	const PREVIEW = new Set(["features.autostart", "features.open_dashboard_at_login", "features.private_sync"]);
	const GROUPS = [["sync", "sync items", "What follows you to your other devices. Off only stops syncing it; nothing is deleted."],
		["features", "features", "What cwm does for you."], ["display", "display", "This dashboard."], ["advanced", "advanced", ""]];
	const MODES = {
		capture: "~/.claude is the workspace: every Claude Code run on this machine is captured and synced — plain claude, the IDE extensions, the desktop app.",
		isolated: "~/.cwm/workspace is the workspace: only sessions started through cwm use it; ~/.claude is left alone.",
	};
	let settings = null;
	let display = { hidden: [], def: "sessions" };

	function applyDisplay(v) {
		const get = (k) => (v.keys.find((x) => x.key === k) || {}).value || "";
		display = { hidden: get("display.hidden_tabs").split(",").filter(Boolean), def: get("display.default_tab") || "sessions" };
		if (state && state.stats === false) display.hidden.push("stats");
		for (const t of TABS) {
			const off = display.hidden.includes(t);
			$("tab-" + t).hidden = off;
			document.querySelectorAll('[data-tab-go="' + t + '"]').forEach((b) => { b.hidden = off; });
		}
		$("migrate-banner").hidden = !v.needs_migration;
		if (display.hidden.includes(current()) && dashboardVisible()) activate(current());
	}

	async function loadSettings() {
		const r = await api("/api/settings");
		if (r.ok && r.data && Array.isArray(r.data.keys)) { settings = r.data; applyDisplay(r.data); }
		return r;
	}

	function switchBtn(row) {
		const on = row.value === "true";
		const b = el("button", { type: "button", class: "set-sw", role: "switch", "aria-checked": String(on), "aria-label": row.key }, [el("span", { class: "toggle-box" })]);
		b.addEventListener("click", () => setSetting(row.key, on ? "off" : "on", b));
		return el("span", { class: "set-ctl" }, [el("span", { class: "set-state", "aria-hidden": "true", text: on ? "on" : "off" }), b]);
	}

	function choice(row, opts) {
		const sel = el("select", { class: "input set-ctl", "aria-label": row.key });
		for (const o of opts) sel.appendChild(el("option", { value: o, text: o }));
		sel.value = row.value;
		sel.addEventListener("change", () => setSetting(row.key, sel.value, sel));
		return sel;
	}

	function setRow(row, control, name, tag) {
		return el(tag || "label", { class: "set-row" + (row.na ? " is-na" : "") }, [
			el("span", { class: "set-text" }, [el("span", { class: "set-key", text: name || row.key.replace(/^\w+\./, "") }),
				el("span", { class: "set-help", text: row.na ? "n/a in isolated mode (session folders are pinned there)" : row.help })]),
			control,
		]);
	}

	const cardHead = (title, hint, ctl) => el("div", { class: "card-head" }, [el("div", null, [el("h3", { text: title }), hint ? el("p", { class: "hint", text: hint }) : null]), ctl]);
	// a path may wrap only after a separator
	const pathText = (s) => s.split(/(?<=[/\\])/).flatMap((p, i) => i ? [el("wbr"), p] : [p]);

	function renderSettings(v) {
		const kv = (k, val) => [el("dt", { text: k }), el("dd", null, [el("code", null, pathText(val))])];
		const seg = el("div", { class: "seg seg-text", role: "group", "aria-label": "Workspace mode" }, ["capture", "isolated"].map((m) => {
			const b = el("button", { type: "button", class: "seg-btn", "aria-pressed": String(v.mode === m), text: m });
			b.addEventListener("click", () => { if (v.mode !== m || v.needs_migration) openMigration(m); });
			return b;
		}));
		const out = [el("div", { class: "card set-wide" }, [
			cardHead("mode", "Where Claude Code's data lives on this machine.", seg),
			el("div", { class: "set-mode" }, [el("p", { text: MODES[v.mode] }),
				el("dl", { class: "set-kv" }, [...kv("workspace root", v.root), ...kv(".claude.json", v.live.claude_json), ...kv("credentials", v.live.credentials),
					...kv("CLAUDE_CONFIG_DIR", v.live.env || "not set")])]),
			v.needs_migration ? el("div", { class: "msg msg-warn" }, ["This workspace predates v0.8: review the migration before anything moves.",
				el("div", { class: "acct-inline" }, [btnDo("review migration", () => openMigration(v.mode), "btn-primary")])]) : null,
		])];
		for (const [g, title, hint] of GROUPS) {
			const rows = v.keys.filter((k) => k.group === g);
			if (!rows.length) continue;
			const items = [];
			for (const row of rows) {
				if (row.key === "display.hidden_tabs") {
					const hidden = row.value.split(",").filter(Boolean);
					const box = el("div", { class: "set-tabs", role: "group", "aria-label": "Visible tabs" }, row.choices.map((t) => {
						const on = !hidden.includes(t);
						const b = el("button", { type: "button", class: "menu-item toggle", role: "switch", "aria-checked": String(on), "aria-label": "show " + t },
							[el("span", { class: "toggle-box" }), t]);
						b.addEventListener("click", () => setSetting(row.key, (on ? hidden.concat(t) : hidden.filter((x) => x !== t)).join(","), b));
						return b;
					}));
					items.push(setRow(row, box, "visible tabs", "div"));
				} else items.push(setRow(row, row.kind === "bool" ? switchBtn(row) : choice(row, row.choices || [])));
			}
			out.push(el("div", { class: "card" + (g === "sync" || g === "features" ? "" : " set-wide") }, [cardHead(title, hint), ...items]));
		}
		show("settings", el("div", { class: "set-grid" }, out));
	}

	function btnDo(label, run, cls) {
		const b = el("button", { type: "button", class: "btn btn-small " + (cls || ""), text: label });
		b.addEventListener("click", () => run(b));
		return b;
	}

	let confirmDone = null;
	function confirmBox(title, text, lines, ok) {
		const dlg = $("confirm-dialog");
		$("confirm-title").textContent = title;
		$("confirm-text").textContent = text;
		$("confirm-lines").replaceChildren(...(lines || []).map((l) => el("li", { class: "wl wl-out", text: l })));
		$("confirm-lines").hidden = !(lines && lines.length);
		$("confirm-ok").textContent = ok || "apply";
		const back = document.activeElement;
		dlg.showModal();
		return new Promise((res) => {
			confirmDone = (v) => { confirmDone = null; dlg.close(); if (back && back.isConnected) back.focus(); res(v); };
		});
	}

	async function setSetting(key, value, ctl) {
		if (ctl) ctl.disabled = true;
		try {
			if (PREVIEW.has(key)) {
				const d = await put({ key: key, value: value, dry_run: true });
				if (!d.ok) return toast("error", "Could not preview " + key, d.error);
				const lines = d.data.notes.length ? d.data.notes : ["only config.toml changes"];
				if (!await confirmBox(key + " = " + (d.data.value === "true" ? "on" : d.data.value === "false" ? "off" : d.data.value), "This is what the change does on this machine:", lines)) return;
			}
			const r = await put({ key: key, value: value });
			if (!r.ok) return toast("error", "Not saved", r.error);
			toast("ok", "saved", key + " = " + r.data.value + (r.data.notes.length ? " — " + r.data.notes.join(" ") : ""));
		} finally {
			if (ctl && ctl.isConnected) ctl.disabled = false;
			const lbl = ctl && ctl.getAttribute("aria-label");
			await load("settings", true);
			const f = document.activeElement;
			if (lbl && (!f || f === document.body)) { const n = [...$("panel-settings").querySelectorAll("[aria-label]")].find((x) => x.getAttribute("aria-label") === lbl); if (n) n.focus(); }
			if (key === "features.stats") refreshState();
		}
	}

	function put(payload) {
		return post("/api/settings", payload, "PUT");
	}

	// --- migration preview + progress (the terminal-log component)

	let migTo = "";
	function mlog(kind, text) {
		const box = $("mig-log");
		const tag = { ok: "[  OK  ] ", fail: "[FAILED] ", cmd: "$ " }[kind] || "";
		box.appendChild(el("li", { class: "wl wl-" + kind }, [tag ? el("span", { class: "wl-tag", text: tag }) : null, text]));
		box.scrollTop = box.scrollHeight;
	}

	async function openMigration(to) {
		const dlg = $("mig-dialog");
		migTo = to;
		$("mig-title").textContent = "migrate to " + to + " mode";
		$("mig-log").replaceChildren();
		$("mig-sum").replaceChildren(el("p", { class: "hint", text: "Building the plan (nothing changes yet)…" }));
		$("mig-apply").hidden = false;
		$("mig-apply").disabled = true;
		$("mig-cancel").textContent = "cancel";
		if (!dlg.open) dlg.showModal();
		const r = await post("/api/migrate/preview", { to: to });
		if (!r.ok) return $("mig-sum").replaceChildren(el("div", { class: "msg msg-error", text: r.error }));
		const p = r.data;
		const counts = Object.keys(p.counts).sort().map((k) => k + " " + p.counts[k]);
		if (p.skipped_identical) counts.push("identical " + p.skipped_identical);
		const kv = (k, v) => [el("dt", { text: k }), el("dd", { text: v })];
		const none = p.kind === "none";
		$("mig-sum").replaceChildren(
			el("dl", { class: "set-kv" }, [...kv("from", (p.from_mode || "v0.7 layout") + " → " + p.to_mode), ...kv("root", p.root),
				...kv("sources", p.sources.join(", ") || "none"), ...kv("sessions", String(p.sessions.length) + " (all kept; verified after)"),
				...kv("backup", p.backup_roots.join(", ") + " (" + bytes(p.backup_bytes) + ", sha256 manifest)"),
				...kv("plan", none ? "nothing to do" : counts.join(" · ")), ...kv("conflicts", p.conflicts ? p.conflicts + " kept side by side" : "none")]),
			...(p.warnings || []).map((w) => el("div", { class: "msg msg-warn", text: w })),
			p.actions.length ? el("details", { class: "check-details" }, [el("summary", { text: "every step" + (p.truncated ? " (first " + p.actions.length + ")" : "") }),
				el("pre", { class: "check-raw", text: p.actions.map((a) => a.kind + "  " + (a.src || "") + (a.dst ? " → " + a.dst : "") + (a.detail ? "  # " + a.detail : "")).join("\n") })]) : null,
			el("p", { class: "hint", text: "Nothing is deleted: replaced files go into the backup. Undo with cwm migrate rollback." }),
		);
		$("mig-apply").disabled = none;
		$("mig-apply").hidden = none;
	}

	async function applyMigration() {
		$("mig-apply").disabled = true;
		mlog("cmd", "cwm migrate --to " + migTo + " --yes");
		const r = await post("/api/migrate/apply", { to: migTo });
		if (!r.ok) return mlog("fail", r.error);
		let seen = 0;
		for (;;) {
			await new Promise((res) => setTimeout(res, 400));
			const st = await api("/api/migrate");
			if (!st.ok) { mlog("fail", st.error); break; }
			st.data.lines.slice(seen).forEach((l) => mlog("out", l));
			seen = st.data.lines.length;
			if (st.data.running) continue;
			const rec = st.data.record;
			if (st.data.error) {
				mlog("fail", st.data.error);
				toast("error", "Migration failed", st.data.error, { sticky: true });
			} else {
				mlog("ok", rec ? "migrated to " + rec.to_mode + " mode; " + rec.sessions_after + " of " + rec.sessions_before + " sessions verified" : "nothing to migrate");
				if (rec) mlog("cmd", "undo: cwm migrate rollback " + rec.id);
				toast("ok", "Migration applied", rec ? "Undo any time: cwm migrate rollback " + rec.id : "");
			}
			break;
		}
		$("mig-apply").hidden = true;
		$("mig-cancel").textContent = "close";
		await load("settings", true);
		refreshState();
		refreshHealthBadge();
	}

	function wireSettings() {
		$("confirm-ok").addEventListener("click", () => confirmDone && confirmDone(true));
		$("confirm-cancel").addEventListener("click", () => confirmDone && confirmDone(false));
		$("confirm-dialog").addEventListener("cancel", () => confirmDone && confirmDone(false));
		$("mig-apply").addEventListener("click", applyMigration);
		$("mig-cancel").addEventListener("click", () => $("mig-dialog").close());
		$("migrate-review").addEventListener("click", () => openMigration(settings ? settings.mode : "capture"));
	}

	// --- setup state + wizard

	let state = null;
	async function refreshState() {
		const r = await api("/api/state");
		if (r.ok && r.data) {
			state = r.data;
			for (const id of ["wiz-own-id", "pair-own-id"]) {
				$(id).textContent = state.deviceID ? groupID(state.deviceID) : "—";
				$(id).title = state.deviceID ? "" : "Syncthing is not running on this device";
			}
			for (const id of ["wiz-own-id-copy", "pair-own-id-copy"]) $(id).disabled = !state.deviceID;
			renderClaude(state.claude);
			if (settings) applyDisplay(settings);
		}
		return r;
	}

	let claudeShown = "";
	function renderClaude(c) {
		const box = $("wiz-claude");
		if (!box || !c) return;
		const key = JSON.stringify(c);
		if (key === claudeShown) return; // polled every 2 s; keep text selection stable
		claudeShown = key;
		if (!c.found) {
			return say(box, "warn", "Claude Code was not found on this machine — install it first, then add the account.",
				"Run this in a terminal (or set `CWM_CLAUDE` / `claude_path` in config.toml if it lives somewhere unusual):", c.install);
		}
		if (!c.onPath) {
			return say(box, "info", "Claude Code found at " + c.path + " — cwm will use it.",
				"Its folder is not on your PATH, so a plain `claude` in a new terminal will not find it. To fix that, run:", c.pathHint);
		}
		box.replaceChildren();
	}

	const STEPS = ["init", "account", "pair", "done"];
	let accountPoll = null;

	function wlog(kind, text, fix) {
		const box = $("wiz-log");
		const tag = { ok: "[  OK  ] ", fail: "[FAILED] ", cmd: "$ ", note: "# " }[kind] || "";
		const li = el("li", { class: "wl wl-" + kind }, [tag ? el("span", { class: "wl-tag", text: tag }) : null, text]);
		if (fix) li.appendChild(commandBox(fix));
		box.appendChild(li);
		box.scrollTop = box.scrollHeight;
	}

	function gotoStep(name) {
		const at = STEPS.indexOf(name);
		document.querySelectorAll(".steps li").forEach((li) => {
			const i = STEPS.indexOf(li.dataset.step);
			li.className = i < at ? "done" : i === at ? "current" : "next";
			if (i === at) li.setAttribute("aria-current", "step"); else li.removeAttribute("aria-current");
		});
		document.querySelectorAll(".step").forEach((div) => { div.hidden = div.dataset.step !== name; });
		if (name === "done") {
			wlog("ok", "cwm is ready on this device");
			const btn = $("wiz-done");
			setTimeout(() => btn.focus(), 50);
		}
	}

	function showWizard(on) {
		$("wizard").hidden = !on;
		$("dashboard").hidden = on;
	}

	function leaveWizard() {
		if (accountPoll) clearInterval(accountPoll);
		accountPoll = null;
		showWizard(false);
		for (const k of Object.keys(loaded)) delete loaded[k]; // reload with the new state
		for (const k of Object.keys(lastJSON)) delete lastJSON[k];
		activate(current());
	}

	function hasAccount(name) {
		return !!(state && (state.accounts || []).some((a) => !name || a.name === name));
	}

	function afterAccountStep() {
		gotoStep("pair");
	}

	function wireWizard() {
		const initBtn = $("wiz-init");
		const initBox = $("wiz-init-result");
		initBtn.addEventListener("click", async () => {
			initBtn.disabled = true;
			say(initBox, "wait", "Initializing the workspace…");
			wlog("cmd", "cwm init");
			const r = await post("/api/init", {});
			initBtn.disabled = false;
			if (!r.ok) {
				toast("error", "Initialization failed", r.error);
				wlog("fail", "initialize: " + r.error, "cwm init");
				return say(initBox, "error", "Initialization failed.", r.error);
			}
			const checks = r.data.checks || [];
			const n = { ok: 0, warn: 0, fail: 0, skip: 0 };
			checks.forEach((c) => { const l = level(c); n[l]++; wlog(l === "ok" || l === "fail" ? l : "out", (l === "ok" || l === "fail" ? "" : BOOT[l] + " ") + c.name + ": " + String(c.detail || "").split(" — ")[0]); });
			wlog("ok", "workspace initialized");
			say(initBox, "ok", "Workspace initialized.",
				"health: " + n.ok + " ok, " + n.warn + " warn, " + n.fail + " fail" + (n.skip ? ", " + n.skip + " skipped" : "") + " (details in the Health tab)");
			toast("ok", "Workspace initialized");
			await refreshState();
			setTimeout(() => gotoStep(hasAccount() ? "pair" : "account"), 700);
		});

		const nameIn = $("wiz-account-name");
		const addBtn = $("wiz-account-add");
		const addBox = $("wiz-account-result");
		const add = async () => {
			const name = nameIn.value.trim();
			if (!/^[a-z0-9][a-z0-9-]{0,31}$/.test(name) || /^[0-9]+$/.test(name)) {
				return say(addBox, "error", "Use 1-32 lowercase letters, digits or dashes, starting with a letter or digit (not all digits).");
			}
			if (hasAccount(name)) return afterAccountStep();
			addBtn.disabled = true;
			say(addBox, "wait", "Opening a terminal…");
			wlog("cmd", "cwm account add " + name);
			const r = await post("/api/accounts", { name: name });
			addBtn.disabled = false;
			if (r.status === 424) {
				refreshState();
				toastNoClaude(r);
				wlog("fail", "Claude Code is not installed", r.command);
				return say(addBox, "error", "Claude Code is needed first.", r.error, r.command);
			}
			if (r.ok) toastPathHint(r.data);
			if (!r.ok && r.status !== 501) {
				wlog("fail", "add account: " + r.error, r.command);
				return say(addBox, "error", "Could not add the account.", r.error, r.command);
			}
			wlog("out", r.ok ? "opened a terminal for the Claude login; waiting for “" + name + "”…" : "no terminal window could be opened; run it yourself:");
			if (r.ok) {
				say(addBox, "wait", "Finish the Claude login in the terminal window that just opened — waiting for account “" + name + "” to appear…",
					"If the window closed without logging in, run this in a terminal:", r.data.command);
			} else {
				say(addBox, "wait", "No terminal window could be opened on this machine. Run this command in a terminal — this page waits for the account to appear:", "", r.command);
			}
			if (accountPoll) clearInterval(accountPoll);
			const started = Date.now();
			accountPoll = setInterval(async () => {
				const s = await refreshState();
				if (hasAccount(name)) {
					clearInterval(accountPoll);
					accountPoll = null;
					say(addBox, "ok", "Account “" + name + "” added.");
					wlog("ok", "account “" + name + "” added");
					toast("ok", "Account “" + name + "” added");
					setTimeout(afterAccountStep, 700);
				} else if (!s.ok || Date.now() - started > 10 * 60 * 1000) {
					clearInterval(accountPoll);
					accountPoll = null;
					say(addBox, "error", s.ok ? "Still no account “" + name + "” after 10 minutes." : "Lost contact with cwm gui.",
						s.ok ? "Run the login in a terminal, then press Add account again." : s.error,
						s.ok ? (r.ok ? r.data.command : r.command) : "");
				}
			}, 2000);
		};
		addBtn.addEventListener("click", add);
		nameIn.addEventListener("keydown", (ev) => { if (ev.key === "Enter") add(); });

		const pairBtn = $("wiz-pair");
		pairBtn.addEventListener("click", async () => {
			wlog("cmd", "cwm pair " + normalizeDeviceID($("wiz-peer-id").value).slice(0, 7) + "…");
			const ok = await pairWith($("wiz-peer-id"), $("wiz-peer-name"), $("wiz-pair-result"), pairBtn);
			wlog(ok ? "ok" : "fail", ok ? "paired" : "pairing did not work", ok ? "" : "cwm pair --start");
			if (ok) setTimeout(() => gotoStep("done"), 1500);
		});
		$("wiz-pair-skip").addEventListener("click", () => { wlog("note", "pairing skipped (cwm pair --start sets it up later)"); gotoStep("done"); });
		$("wiz-done").addEventListener("click", leaveWizard);
		$("wiz-dismiss").addEventListener("click", () => {
			try { sessionStorage.setItem("cwm-wizard-dismissed", "1"); } catch (err) { /* private mode */ }
			leaveWizard();
		});
		$("wiz-own-id-copy").addEventListener("click", (ev) => {
			if (state && state.deviceID) copyText(state.deviceID, ev.currentTarget);
		});
	}

	function wirePairForm() {
		const btn = $("pair-submit");
		btn.addEventListener("click", async () => {
			const ok = await pairWith($("pair-device-id"), $("pair-name"), $("pair-result"), btn);
			if (ok) load("devices");
		});
		$("pair-own-id-copy").addEventListener("click", (ev) => {
			if (state && state.deviceID) copyText(state.deviceID, ev.currentTarget);
		});
	}

	async function load(name, silent) {
		if (!silent) {
			delete lastJSON[name];
			show(name, skeleton(name === "health" ? 6 : 4));
		}
		loaded[name] = true;
		delete stale[name];
		await loaders[name](!!silent);
	}

	function activate(name) {
		if (!TABS.includes(name) || display.hidden.includes(name)) name = TABS.find((t) => !display.hidden.includes(t));
		for (const t of TABS) {
			const selected = t === name;
			const tab = $("tab-" + t);
			const panel = $("panel-" + t);
			tab.setAttribute("aria-selected", String(selected));
			tab.tabIndex = selected ? 0 : -1;
			if (selected && tab.scrollIntoView && tab.offsetParent) tab.scrollIntoView({ inline: "nearest", block: "nearest" });
			if (selected && panel.hidden) {
				panel.classList.remove("enter");
				void panel.offsetWidth; // restart the enter animation
				panel.classList.add("enter");
			}
			panel.hidden = !selected;
		}
		document.querySelectorAll("[data-tab-go]").forEach((b) => {
			if (b.dataset.tabGo === name) b.setAttribute("aria-current", "page"); else b.removeAttribute("aria-current");
		});
		const more = document.querySelector(".bb-item[data-more-open]");
		if (more) more.classList.toggle("is-active", !document.querySelector('.bb-item[data-tab-go="' + name + '"]'));
		if (location.hash !== "#" + name) history.replaceState(null, "", "#" + name);
		if (!loaded[name]) load(name);
		else if (stale[name]) load(name, true);
	}

	function current() {
		let name = location.hash.replace(/^#/, "");
		if (name === "wip") name = "handoff"; // v0.7.1 bookmarks (D70)
		if (!TABS.includes(name)) name = display.def;
		return display.hidden.includes(name) ? TABS.find((t) => !display.hidden.includes(t)) : name;
	}

	function dashboardVisible() {
		return !$("dashboard").hidden;
	}

	async function refreshNow(manual) {
		const btn = $("refresh");
		if (manual) btn.classList.add("spinning");
		if (manual && liveMode === "poll") lastSeen = Date.now();
		if (dashboardVisible()) await load(current(), !manual);
		if (manual) {
			setTimeout(() => btn.classList.remove("spinning"), 300);
		}
	}

	// --- theme + background toggles

	const systemLight = window.matchMedia("(prefers-color-scheme: light)");
	const THEMES = ["system", "dark", "light"];

	function store(key, value) {
		try { localStorage.setItem(key, value); } catch (err) { /* storage disabled */ }
	}

	function applyTheme(pref, persist) {
		if (!THEMES.includes(pref)) pref = "system";
		const light = pref === "light" || (pref === "system" && systemLight.matches);
		root.setAttribute("data-theme", light ? "light" : "dark");
		root.setAttribute("data-theme-pref", pref);
		document.querySelectorAll("[data-theme-set]").forEach((b) => {
			b.setAttribute("aria-pressed", String(b.dataset.themeSet === pref));
		});
		if (persist) store("cwm-theme", pref);
	}

	function cycleTheme() {
		const now = root.getAttribute("data-theme-pref") || "system";
		const next = THEMES[(THEMES.indexOf(now) + 1) % THEMES.length];
		applyTheme(next, true);
		toast("info", "Theme: " + next + (next === "system" ? " (" + (systemLight.matches ? "light" : "dark") + ")" : ""));
	}

	function applyBg(on, persist) {
		root.setAttribute("data-bg", on ? "on" : "off");
		document.querySelectorAll("[data-motion-toggle]").forEach((btn) => {
			btn.setAttribute("aria-pressed", String(on));
			btn.title = "Motion: animated background " + (on ? "on" : "off") + (reduceMotion.matches ? " (reduced motion: still image)" : "") + " (b)";
		});
		if (persist) store("cwm-bg", on ? "on" : "off");
	}

	function toggleMotion() {
		const on = root.getAttribute("data-bg") === "off";
		applyBg(on, true);
		toast("info", "Motion " + (on ? "on" : "off"));
	}

	function wireToggles() {
		const seg = document.querySelector(".hdr-full .seg");
		document.querySelectorAll("[data-theme-seg]").forEach((n) => n.replaceWith(seg.cloneNode(true)));
		applyTheme(root.getAttribute("data-theme-pref") || "system", false);
		document.querySelectorAll("[data-theme-set]").forEach((b) => {
			b.addEventListener("click", () => applyTheme(b.dataset.themeSet, true));
		});
		const onSystem = () => {
			if ((root.getAttribute("data-theme-pref") || "system") === "system") applyTheme("system", false);
		};
		if (systemLight.addEventListener) systemLight.addEventListener("change", onSystem);
		applyBg(root.getAttribute("data-bg") !== "off", false);
		document.querySelectorAll("[data-motion-toggle]").forEach((b) => b.addEventListener("click", toggleMotion));
	}

	// --- live updates: SSE when the server has /api/events, else polling

	let pollTimer = 0;
	let liveMode = "init";
	let lastRefresh = Date.now();
	let lastSeen = 0; // last sign of life from the agent (any event, any poll answer)
	let liveTick = 0;

	function beat() {
		lastSeen = Date.now();
		const box = $("live");
		box.classList.remove("beat");
		void box.offsetWidth;
		box.classList.add("beat");
		renderLive();
	}

	function ago(ms) {
		const sec = Math.max(0, Math.round(ms / 1000));
		if (sec < 60) return sec + "s ago";
		if (sec < 3600) return Math.floor(sec / 60) + "m ago";
		return Math.floor(sec / 3600) + "h ago";
	}

	function renderLive() {
		const box = $("live");
		const mode = box.dataset.mode;
		let label = "connecting…";
		if (mode === "offline") label = "offline – retry";
		else if ((mode === "sse" || mode === "poll") && lastSeen) label = "live " + ago(Date.now() - lastSeen);
		const text = $("live-text");
		if (text.textContent !== label) {
			text.textContent = label;
			box.setAttribute("aria-label", "connection: " + label);
		}
	}

	function setLive(mode) {
		liveMode = mode === "offline" ? liveMode : mode;
		const box = $("live");
		box.dataset.mode = mode;
		box.title = {
			sse: "Live updates from cwm (server-sent events)",
			poll: "Auto-refresh every 15 seconds (this cwm has no live event stream)",
			offline: "cwm is not reachable. Click to retry.",
		}[mode] || "Connecting to cwm";
		$("offline-banner").hidden = mode !== "offline";
		renderLive();
		clearInterval(liveTick);
		if (mode === "sse" || mode === "poll") {
			liveTick = setInterval(() => { if (!document.hidden) renderLive(); }, 1000);
		}
	}

	async function retryNow() {
		const v = await api("/api/version");
		if (!v.ok) return toast("error", "cwm is still not reachable", "Start it with `cwm dashboard`, then retry.");
		setLive(liveMode === "init" || liveMode === "offline" ? "poll" : liveMode);
		beat();
		for (const t of TABS) stale[t] = true;
		if (dashboardVisible()) load(current(), true);
	}

	async function sseAvailable() {
		if (!("EventSource" in window) || typeof AbortController === "undefined") return false;
		const ctl = new AbortController();
		const timer = setTimeout(() => ctl.abort(), 5000);
		try {
			const res = await fetch("/api/events", {
				credentials: "same-origin", headers: { Accept: "text/event-stream" }, signal: ctl.signal,
			});
			const type = res.headers.get("Content-Type") || "";
			return res.status === 200 && type.indexOf("text/event-stream") === 0;
		} catch (err) {
			return false;
		} finally {
			clearTimeout(timer);
			ctl.abort(); // close the probe stream; EventSource opens its own
		}
	}

	const TOPIC_TAB = { doctor: "health", health: "health", sessions: "sessions", projects: "projects", devices: "devices", wip: "handoff", accounts: "accounts", stats: "stats", settings: "settings" };
	const QUIET = new Set(["ping", "heartbeat", "hello", "keepalive", "open", "connected"]);
	let pendingTopics = new Set();
	let eventTimer = 0;

	function onServerEvent(ev) {
		let topic = ev.type === "message" ? "" : ev.type;
		let parts = [];
		try {
			const d = JSON.parse(ev.data);
			if (d && typeof d === "object") {
				topic = String(d.topic || d.type || d.kind || d.event || topic);
				if (Array.isArray(d.changed)) parts = d.changed.map((c) => String(c).toLowerCase()).filter((c) => TOPIC_TAB[c]);
			}
		} catch (err) { /* plain-text payloads are fine */ }
		topic = topic.toLowerCase();
		lastSeen = Date.now();
		if (QUIET.has(topic)) return;
		pendingTopics.add(topic || "*");
		parts.forEach((c) => pendingTopics.add(c));
		clearTimeout(eventTimer);
		eventTimer = setTimeout(flushEvents, 400); // coalesce bursts
	}

	function flushEvents() {
		const topics = pendingTopics;
		pendingTopics = new Set();
		let tabs = [];
		for (const t of topics) {
			if (t === "state" || t === "accounts" || t === "initialized") refreshState();
			if (t === "settings" && current() !== "settings") loadSettings();
			if (t === "doctor" || t === "health" || t === "*") refreshHealthBadge();
			if (TOPIC_TAB[t]) tabs.push(TOPIC_TAB[t]);
			else if (t !== "state") tabs = TABS.slice();
		}
		for (const t of new Set(tabs)) stale[t] = true;
		lastRefresh = Date.now();
		beat();
		if (dashboardVisible() && stale[current()]) load(current(), true);
	}

	function startPolling() {
		setLive("poll");
		beat();
		clearInterval(pollTimer);
		pollTimer = setInterval(() => {
			if (document.hidden) return;
			pollOnce();
		}, POLL_MS);
	}

	async function pollOnce() {
		lastRefresh = Date.now();
		const v = await api("/api/version");
		if (!v.ok && v.status === 0) return setLive("offline");
		if (liveMode !== $("live").dataset.mode) setLive(liveMode);
		beat();
		for (const t of TABS) stale[t] = true;
		if (dashboardVisible()) await load(current(), true);
	}

	function openStream() {
		const es = new EventSource("/api/events");
		let opened = false;
		es.onopen = () => { opened = true; setLive("sse"); beat(); };
		es.onmessage = onServerEvent;
		for (const name of ["heartbeat", "hello"]) {
			es.addEventListener(name, () => { lastSeen = Date.now(); if ($("live").dataset.mode === "offline") setLive("sse"); });
		}
		for (const name of ["change", "update", "refresh", "state", "accounts", "sessions", "projects", "devices", "wip", "doctor", "health"]) {
			es.addEventListener(name, onServerEvent);
		}
		es.onerror = () => {
			if (es.readyState === EventSource.CLOSED) {
				es.close();
				startPolling(); // the stream is gone for good: poll instead
			} else if (opened) {
				setLive("offline"); // EventSource reconnects on its own
			}
		};
	}

	async function startLive() {
		if (await sseAvailable()) openStream();
		else startPolling();
		document.addEventListener("visibilitychange", () => {
			if (!document.hidden && liveMode === "poll" && Date.now() - lastRefresh > POLL_MS) pollOnce();
		});
	}

	// --- keyboard shortcuts

	function typing(target) {
		if (!target || !target.tagName) return false;
		const tag = target.tagName;
		return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || target.isContentEditable;
	}

	function openKeys() {
		closeMenu(false);
		const dlg = $("keys");
		root.setAttribute("data-keys", "open");
		if (!dlg.open) dlg.showModal();
	}

	// --- header menu (narrow screens)

	function closeMenu(refocus) {
		const m = $("menu");
		if (m.hidden) return;
		m.hidden = true;
		$("menu-btn").setAttribute("aria-expanded", "false");
		if (refocus) $("menu-btn").focus();
	}

	function wireMenu() {
		const btn = $("menu-btn");
		const m = $("menu");
		btn.addEventListener("click", () => {
			const open = m.hidden;
			m.hidden = !open;
			btn.setAttribute("aria-expanded", String(open));
			if (open) { const first = m.querySelector("button"); if (first) first.focus(); }
		});
		document.addEventListener("click", (ev) => {
			if (!m.hidden && !m.contains(ev.target) && !btn.contains(ev.target)) closeMenu(false);
		});
		m.addEventListener("click", (ev) => {
			if (ev.target.closest(".menu-item")) closeMenu(false);
		});
	}

	// --- command palette (D67): a shell prompt over sections, actions, sessions

	let palReturn = null;
	let palItems = [];
	let palAt = 0;

	function palEntries() {
		const out = TABS.filter((t) => !display.hidden.includes(t)).map((t) => ({ kind: "section", label: t, hint: (TABS.indexOf(t) + 1) + ":", run: () => { activate(t); $("tab-" + t).focus({ preventScroll: true }); } }));
		const act = (label, hint, run) => out.push({ kind: "action", label: label, hint: hint, run: run });
		for (const g of ["mode", "sync items", "features", "display"]) act("settings: " + g, "8:", () => activate("settings"));
		act("review migration", "", () => openMigration(settings ? settings.mode : "capture"));
		act("theme auto", "t", () => applyTheme("system", true));
		act("theme dark", "t", () => applyTheme("dark", true));
		act("theme light", "t", () => applyTheme("light", true));
		act("motion on/off", "b", toggleMotion);
		act("refresh", "r", () => refreshNow(true));
		act("copy debug logs", "d", copyDebugLogs);
		act("filter sessions", "/", () => { activate("sessions"); $("sessions-filter").focus(); });
		act("keyboard shortcuts", "?", openKeys);
		if (window.cwmAbout) act("about cwm", "", window.cwmAbout);
		sessionsCache.forEach((s) => out.push({ kind: "session", label: "resume " + s.project, sub: s.firstPrompt || "(no prompt)", hint: s.id.slice(0, 8),
			run: () => resumeSession(s, null) }));
		return out;
	}

	function fuzzy(q, text) {
		if (!q) return 1;
		const t = text.toLowerCase();
		let at = -1, score = 0, run = 0;
		for (const ch of q) {
			const i = t.indexOf(ch, at + 1);
			if (i < 0) return 0;
			run = i === at + 1 ? run + 1 : 0;
			score += 1 + run * 2 + (i === 0 || t[i - 1] === " " || t[i - 1] === "-" ? 3 : 0);
			at = i;
		}
		return score - t.length / 100;
	}

	function renderPalette() {
		const q = $("pal-input").value.trim().toLowerCase().replace(/^cwm\s+/, "");
		const scored = palEntries().map((e) => ({ e: e, s: Math.max(fuzzy(q, e.label), e.sub ? fuzzy(q, e.label + " " + e.sub) * 0.9 : 0) }))
			.filter((x) => x.s > 0);
		if (q) scored.sort((a, b) => b.s - a.s);
		palItems = scored.slice(0, 12).map((x) => x.e);
		palAt = Math.min(palAt, Math.max(0, palItems.length - 1));
		const list = $("pal-list");
		list.replaceChildren(...palItems.map((e, i) => {
			const li = el("li", { role: "option", id: "pal-" + i, class: "pal-item pal-" + e.kind, "aria-selected": String(i === palAt) }, [
				el("span", { class: "pal-kind", text: e.kind }),
				el("span", { class: "pal-label" }, [e.label, e.sub ? el("span", { class: "pal-sub", text: e.sub }) : null]),
				e.hint ? el("span", { class: "pal-key", text: e.hint }) : null,
			]);
			li.addEventListener("mousemove", () => { if (palAt !== i) { palAt = i; markPalette(); } });
			li.addEventListener("click", () => runPalette(i));
			return li;
		}));
		if (!palItems.length) list.appendChild(el("li", { class: "pal-empty", text: "no match for \u201c" + q + "\u201d" }));
		markPalette();
	}

	function markPalette() {
		$("pal-list").querySelectorAll(".pal-item").forEach((li, i) => {
			li.setAttribute("aria-selected", String(i === palAt));
			if (i === palAt && li.scrollIntoView) li.scrollIntoView({ block: "nearest" });
		});
		const input = $("pal-input");
		if (palItems.length) input.setAttribute("aria-activedescendant", "pal-" + palAt); else input.removeAttribute("aria-activedescendant");
	}

	function runPalette(i) {
		const e = palItems[i];
		if (!e) return;
		closePalette(false);
		e.run();
	}

	function openPalette() {
		const dlg = $("palette");
		if (dlg.open) return;
		closeMenu(false);
		palReturn = document.activeElement;
		$("pal-input").value = "";
		palAt = 0;
		renderPalette();
		dlg.showModal();
		$("pal-input").focus();
		if (!sessionsCache.length) {
			api("/api/sessions").then((r) => {
				if (r.ok && Array.isArray(r.data)) { sessionsCache = r.data; if (dlg.open) renderPalette(); }
			});
		}
	}

	function closePalette(refocus) {
		const dlg = $("palette");
		if (dlg.open) dlg.close();
		if (refocus !== false && palReturn && palReturn.focus) palReturn.focus();
		palReturn = null;
	}

	function wirePalette() {
		const dlg = $("palette");
		const input = $("pal-input");
		document.querySelectorAll("[data-palette-open]").forEach((b) => b.addEventListener("click", openPalette));
		input.addEventListener("input", () => { palAt = 0; renderPalette(); });
		input.addEventListener("keydown", (ev) => {
			if (ev.key === "ArrowDown" || ev.key === "ArrowUp") {
				ev.preventDefault();
				const n = palItems.length;
				if (n) palAt = (palAt + (ev.key === "ArrowDown" ? 1 : n - 1)) % n;
				markPalette();
			} else if (ev.key === "Enter") {
				ev.preventDefault();
				runPalette(palAt);
			}
		});
		dlg.addEventListener("cancel", (ev) => { ev.preventDefault(); closePalette(); });
		dlg.addEventListener("click", (ev) => { if (ev.target === dlg) closePalette(); });
	}

	function wireKeys() {
		const dlg = $("keys");
		$("keys-close").addEventListener("click", () => dlg.close());
		dlg.addEventListener("close", () => root.removeAttribute("data-keys"));
		document.querySelectorAll("[data-keys-open]").forEach((b) => b.addEventListener("click", () => openKeys()));
		document.addEventListener("keydown", (ev) => {
			if ((ev.ctrlKey || ev.metaKey) && !ev.altKey && (ev.key === "k" || ev.key === "K")) {
				ev.preventDefault();
				if ($("palette").open) closePalette(); else openPalette();
				return;
			}
			if (ev.key === "Escape" && !$("menu").hidden) {
				closeMenu(true);
				return;
			}
			if (ev.defaultPrevented || ev.ctrlKey || ev.metaKey || ev.altKey) return;
			if (ev.key === "Escape" && ev.target === $("sessions-filter")) {
				ev.target.blur();
				return;
			}
			if (typing(ev.target) || document.querySelector("dialog[open]")) return;
			const k = ev.key;
			if (k >= "1" && k <= "8" && dashboardVisible()) {
				const name = TABS[Number(k) - 1];
				activate(name);
				$("tab-" + name).focus({ preventScroll: true });
			} else if (k === "/" && dashboardVisible()) {
				ev.preventDefault();
				activate("sessions");
				$("sessions-filter").focus();
			} else if (k === "t") {
				cycleTheme();
			} else if (k === "b") {
				toggleMotion();
			} else if (k === "r") {
				refreshNow(true);
			} else if (k === "d") {
				copyDebugLogs();
			} else if (k === "?") {
				openKeys();
			} else {
				return;
			}
			ev.preventDefault();
		});
	}

	// --- stats (v0.7.1, D65)

	const SVGNS = "http://www.w3.org/2000/svg";
	const RANGES = ["7d", "30d", "90d", "365d", "all"];
	const METRICS = { sessions: "sessions", messages: "messages", tokens: "tokens" };
	const WD = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
	const MON = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
	let statsData = null;
	let statsTZ = "";
	try { statsTZ = Intl.DateTimeFormat().resolvedOptions().timeZone || ""; } catch (err) { statsTZ = ""; }

	function pref(key, allowed, def) {
		let v = null;
		try { v = localStorage.getItem(key); } catch (err) { v = null; }
		return allowed.includes(v) ? v : def;
	}
	let statsRange = pref("cwm-stats-range", RANGES, "30d");
	let statsMetric = pref("cwm-stats-metric", Object.keys(METRICS), "tokens");

	const nf = new Intl.NumberFormat();
	function compact(n) {
		const a = Math.abs(n);
		for (const [v, s] of [[1e12, "T"], [1e9, "B"], [1e6, "M"], [1e3, "k"]]) {
			if (a >= v) {
				const x = n / v;
				return (x >= 100 ? x.toFixed(0) : x.toFixed(1).replace(/\.0$/, "")) + s;
			}
		}
		return String(n);
	}
	function plural(n, word) {
		return nf.format(n) + " " + word + (n === 1 ? "" : "s");
	}
	function dayDate(s) {
		const [y, m, d] = s.split("-").map(Number);
		return new Date(Date.UTC(y, m - 1, d));
	}
	function longDate(dt) {
		return WD[dt.getUTCDay()] + ", " + dt.getUTCDate() + " " + MON[dt.getUTCMonth()] + " " + dt.getUTCFullYear();
	}
	function weekStart() {
		try {
			const loc = new Intl.Locale(navigator.language || "en");
			const info = loc.getWeekInfo ? loc.getWeekInfo() : loc.weekInfo;
			if (info && info.firstDay === 7) return 0;
		} catch (err) { /* no Intl.Locale week data: Monday */ }
		return 1;
	}

	function svg(tag, attrs) {
		const n = document.createElementNS(SVGNS, tag);
		for (const [k, v] of Object.entries(attrs || {})) n.setAttribute(k, String(v));
		return n;
	}

	function kpi(label, value, sub, title) {
		return el("div", { class: "kpi" }, [
			el("div", { class: "kpi-label", text: label }),
			el("div", { class: "kpi-value", text: value, title: title || null }),
			sub ? el("div", { class: "kpi-sub", text: sub }) : null,
		]);
	}

	function statsKPIs(d) {
		const t = d.totals;
		const cost = d.cost_estimate || {};
		const unpriced = (d.unpriced || []).length ? "unpriced (counted $0): " + d.unpriced.join(", ") : "";
		const days = (d.daily || []).length;
		const st = d.streaks || {};
		return el("div", { class: "kpis" }, [
			kpi("sessions", nf.format(t.sessions), null),
			kpi("messages", compact(t.messages), nf.format(t.user_messages) + " prompts · " + nf.format(t.assistant_messages) + " replies", nf.format(t.messages)),
			kpi("tokens", compact(t.tokens.total), "in, out and cache", nf.format(t.tokens.total) + " tokens"),
			kpi("active days", nf.format(t.active_days), "of " + plural(days, "day")),
			kpi("streak", plural(st.current || 0, "day"), "longest " + plural(st.longest || 0, "day"),
				st.longest ? "longest: " + st.longest_start + " to " + st.longest_end : null),
			kpi("est. cost", "≈ $" + (cost.usd || 0).toFixed(2), cost.note || "est., list prices — your plan may be flat-rate",
				"API-equivalent at list prices as of " + (cost.prices_updated || "?") + (unpriced ? " · " + unpriced : "")),
		]);
	}

	function cellValue(c) {
		return statsMetric === "tokens" ? c.tokens : statsMetric === "messages" ? c.messages : c.sessions;
	}

	function levels(values) {
		const nz = values.filter((v) => v > 0).sort((a, b) => a - b);
		const q = (p) => nz.length ? nz[Math.min(nz.length - 1, Math.floor(p * (nz.length - 1)))] : 0;
		const cut = [q(0.25), q(0.5), q(0.75)];
		return (v) => v <= 0 ? 0 : v <= cut[0] ? 1 : v <= cut[1] ? 2 : v <= cut[2] ? 3 : 4;
	}

	function heatmap(d) {
		const cal = d.calendar || [];
		const card = el("div", { class: "card stats-card heat-card" });
		const toggle = el("div", { class: "seg seg-text", role: "group", "aria-label": "Heatmap metric" },
			Object.keys(METRICS).map((m) => {
				const b = el("button", { type: "button", class: "seg-btn", "aria-pressed": String(m === statsMetric), text: m });
				b.addEventListener("click", () => {
					statsMetric = m;
					store("cwm-stats-metric", m);
					const next = heatmap(statsData);
					card.replaceWith(next);
					const sc = next.querySelector(".heat-scroll");
					if (sc) sc.scrollLeft = sc.scrollWidth;
				});
				return b;
			}));
		card.appendChild(el("div", { class: "card-head" }, [el("h3", { text: "activity, last 12 months" }), toggle]));
		if (!cal.length) return card;

		const ws = weekStart();
		const last = cal.length - 1;
		const todayDow = dayDate(cal[last].date).getUTCDay();
		const startIdx = last - ((todayDow - ws + 7) % 7) - 52 * 7;
		const cells = cal.slice(Math.max(0, startIdx));
		const lv = levels(cells.map(cellValue));
		const grid = el("div", { class: "heat", role: "grid", "aria-label": "Daily " + statsMetric + ", last 12 months" });
		const tip = el("div", { class: "heat-tip", role: "tooltip", hidden: "" });
		const unit = statsMetric === "sessions" ? "session" : statsMetric === "messages" ? "message" : "token";
		const buttons = [];
		let prevMonth = -1;
		let lastLabelCol = -9;
		for (let r = 0; r < 7; r++) {
			if ((ws + r) % 7 % 2 === 1 && (ws + r) % 7 < 6) { // Mon, Wed, Fri
				const wl = el("span", { class: "heat-wd", text: WD[(ws + r) % 7], "aria-hidden": "true" });
				wl.style.gridRow = String(r + 2);
				grid.appendChild(wl);
			}
		}
		cells.forEach((c, i) => {
			const dt = dayDate(c.date);
			const col = Math.floor(i / 7);
			const row = i % 7;
			if (row === 0 || i === 0) {
				const mo = dt.getUTCMonth();
				if (mo !== prevMonth && dt.getUTCDate() <= 7 && col - lastLabelCol >= 3) {
					const ml = el("span", { class: "heat-mo", text: MON[mo], "aria-hidden": "true" });
					ml.style.gridColumn = String(col + 2) + " / span 3";
					grid.appendChild(ml);
					lastLabelCol = col;
				}
				prevMonth = mo;
			}
			const v = cellValue(c);
			const label = (v === 0 ? "No " + unit + "s" : plural(v, unit)) + " on " + longDate(dt);
			const b = el("button", { type: "button", class: "heat-cell", "data-l": String(lv(v)), "aria-label": label, tabindex: "-1", "data-date": c.date });
			b.style.gridRow = String(row + 2);
			b.style.gridColumn = String(col + 2);
			buttons.push(b);
			grid.appendChild(b);
		});
		const showTip = (b) => {
			tip.textContent = b.getAttribute("aria-label");
			tip.hidden = false;
			const box = tip.parentNode;
			const g = box.getBoundingClientRect();
			const r = b.getBoundingClientRect();
			tip.style.left = Math.round(r.left - g.left + box.scrollLeft + r.width / 2) + "px";
			tip.style.top = Math.round(r.top - g.top) + "px";
		};
		const focusAt = (i) => {
			i = Math.max(0, Math.min(buttons.length - 1, i));
			buttons.forEach((b, k) => { b.tabIndex = k === i ? 0 : -1; });
			buttons[i].focus();
		};
		if (buttons.length) buttons[buttons.length - 1].tabIndex = 0;
		grid.addEventListener("mouseover", (ev) => { if (ev.target.classList.contains("heat-cell")) showTip(ev.target); });
		grid.addEventListener("mouseleave", () => { tip.hidden = true; });
		grid.addEventListener("focusin", (ev) => { if (ev.target.classList.contains("heat-cell")) showTip(ev.target); });
		grid.addEventListener("focusout", () => { tip.hidden = true; });
		grid.addEventListener("keydown", (ev) => {
			const i = buttons.indexOf(ev.target);
			if (i < 0) return;
			const step = { ArrowUp: -1, ArrowDown: 1, ArrowLeft: -7, ArrowRight: 7 }[ev.key];
			if (step !== undefined) focusAt(i + step);
			else if (ev.key === "Home") focusAt(0);
			else if (ev.key === "End") focusAt(buttons.length - 1);
			else return;
			ev.preventDefault();
			ev.stopPropagation();
		});
		const legend = el("div", { class: "heat-legend", "aria-hidden": "true" }, ["less",
			...[0, 1, 2, 3, 4].map((l) => el("span", { class: "heat-cell", "data-l": String(l) })), "more"]);
		card.appendChild(el("div", { class: "heat-scroll" }, [grid, tip]));
		card.appendChild(legend);
		return card;
	}

	function barList(title, groups, sub) {
		const card = el("div", { class: "card stats-card" }, [el("h3", { text: title })]);
		if (!groups.length) {
			card.appendChild(el("p", { class: "hint", text: "Nothing in this range." }));
			return card;
		}
		const most = Math.max(1, ...groups.map((g) => g.tokens.total));
		card.appendChild(el("ol", { class: "hbars" }, groups.map((g) => {
			const fill = el("span");
			fill.style.width = Math.max(1, Math.round((g.tokens.total / most) * 100)) + "%";
			return el("li", null, [
				el("span", { class: "hb-name", text: g.name, title: g.name }),
				el("span", { class: "bar", "aria-hidden": "true" }, [fill]),
				el("span", { class: "hb-val", text: compact(g.tokens.total), title: nf.format(g.tokens.total) + " tokens · " + plural(g.messages, "message") + " · " + plural(g.sessions, "session") }),
				sub ? el("span", { class: "hb-sub", text: sub(g) }) : null,
			]);
		})));
		return card;
	}

	function tokenSplit(tok) {
		const card = el("div", { class: "card stats-card" }, [el("h3", { text: "token kinds" })]);
		const rows = [
			["billed", tok.input + tok.output, "input " + compact(tok.input) + " · output " + compact(tok.output), "chart-billed"],
			["cache", tok.cache_write + tok.cache_read, "write " + compact(tok.cache_write) + " · read " + compact(tok.cache_read), "chart-cache"],
		];
		const most = Math.max(1, rows[0][1], rows[1][1]);
		card.appendChild(el("ol", { class: "hbars kinds" }, rows.map(([name, v, sub, cls]) => {
			const fill = el("span", { class: cls });
			fill.style.width = Math.max(1, Math.round((v / most) * 100)) + "%";
			return el("li", null, [
				el("span", { class: "hb-name" }, [el("span", { class: "sw " + cls, "aria-hidden": "true" }), name]),
				el("span", { class: "bar", "aria-hidden": "true" }, [fill]),
				el("span", { class: "hb-val", text: compact(v), title: nf.format(v) + " tokens" }),
				el("span", { class: "hb-sub", text: sub + " · " + Math.round((v / Math.max(1, tok.total)) * 100) + "%" }),
			]);
		})));
		return card;
	}

	function trend(d) {
		const days = (d.calendar || []).slice(-30);
		const card = el("div", { class: "card stats-card" }, [el("h3", { text: "tokens, last 30 days" })]);
		if (!days.length) return card;
		const W = 300, H = 80, P = 4;
		const peak = Math.max(1, ...days.map((x) => x.tokens));
		const pts = days.map((x, i) => [
			(P + (i * (W - 2 * P)) / Math.max(1, days.length - 1)).toFixed(1),
			(H - P - (x.tokens / peak) * (H - 2 * P)).toFixed(1),
		]);
		const line = "M" + pts.map((p) => p.join(" ")).join(" L");
		const s = svg("svg", { class: "trend", viewBox: "0 0 " + W + " " + H, preserveAspectRatio: "none", role: "img",
			"aria-label": "Daily tokens over the last 30 days, peak " + nf.format(peak) });
		s.appendChild(svg("path", { class: "trend-area", d: line + " L" + pts[pts.length - 1][0] + " " + (H - P) + " L" + pts[0][0] + " " + (H - P) + " Z" }));
		s.appendChild(svg("path", { class: "trend-line", d: line, "vector-effect": "non-scaling-stroke" }));
		card.appendChild(s);
		card.appendChild(el("div", { class: "trend-axis" }, [
			el("span", { text: longDate(dayDate(days[0].date)).slice(5) }),
			el("span", { text: "peak " + compact(peak), title: nf.format(peak) + " tokens" }),
			el("span", { text: "today" }),
		]));
		return card;
	}

	function renderStats(d) {
		statsData = d;
		if (!d.totals || !d.totals.messages) {
			const all = (d.calendar || []).some((c) => c.messages > 0);
			return show("stats", el("div", { class: "card stats-empty" }, [
				el("p", { class: "empty", text: all ? "No activity in this range — try a longer one." : "No sessions yet — start one and your activity appears here." }),
				all ? heatmap(d) : null,
			]));
		}
		const focused = document.activeElement && document.activeElement.dataset ? document.activeElement.dataset.date : "";
		show("stats", el("div", { class: "stats-grid" }, [
			statsKPIs(d),
			heatmap(d),
			el("div", { class: "stats-cols" }, [
				barList("top projects", d.top_projects || []),
				barList("top models", d.top_models || [], (g) => g.cost_usd ? "≈ $" + g.cost_usd.toFixed(2) + " est." : ""),
				tokenSplit(d.totals.tokens),
				trend(d),
				(d.accounts || []).length > 1 ? barList("accounts", d.accounts) : null,
			]),
		]));
		content("stats").querySelectorAll(".heat-scroll").forEach((sc) => { sc.scrollLeft = sc.scrollWidth; });
		if (focused) {
			const b = content("stats").querySelector('.heat-cell[data-date="' + focused + '"]');
			if (b) { b.tabIndex = 0; b.focus({ preventScroll: true }); }
		}
	}

	function syncRangeButtons() {
		document.querySelectorAll("#stats-range [data-range]").forEach((b) => {
			b.setAttribute("aria-pressed", String(b.dataset.range === statsRange));
		});
	}

	function wireStats() {
		syncRangeButtons();
		document.querySelectorAll("#stats-range [data-range]").forEach((b) => {
			b.addEventListener("click", () => {
				statsRange = b.dataset.range;
				store("cwm-stats-range", statsRange);
				syncRangeButtons();
				load("stats");
			});
		});
	}

	// --- About (D69): neofetch layout + release graph

	const SWATCHES = ["bg", "surface-2", "text", "muted", "accent", "accent-2", "magenta", "warn", "fail"];
	let aboutData = null;

	function aboutSummary(d) {
		return "cwm " + d.label + " (" + d.commit + (d.dirty ? "-dirty" : "") + ", " + d.os + "/" + d.arch + ", " + d.go + ")";
	}

	function shortTime(iso) {
		const t = new Date(iso);
		return isNaN(t.getTime()) ? "" : t.toISOString().slice(0, 16).replace("T", " ") + " UTC";
	}

	function renderAbout(d) {
		aboutData = d;
		const ws = d.workspace || {};
		const ag = d.agent || {};
		const n = (v, w) => v === null || v === undefined ? null : plural(v, w);
		const kv = [
			["version", [d.dev ? d.label : d.version + " ", d.dev ? null : el("span", { class: "tag", text: d.channel })]],
			["commit", [d.commit + (d.dirty ? " (modified tree)" : "") + (d.built_at ? " · built " + shortTime(d.built_at) : "")]],
			["runtime", [d.go + " " + d.os + "/" + d.arch]],
			["agent", ["pid " + ag.pid + " · " + ag.listen + " · up " + uptime(ag.started_at)]],
			["workspace", [[ws.path, n(ws.repos, "repo"), ws.devices === null ? "sync not set up" : n(ws.devices, "device"), n(ws.accounts, "account")].filter(Boolean).join(" · ")]],
			["author", [d.author.name]],
			["github", [el("a", { href: d.author.github, target: "_blank", rel: "noopener noreferrer", text: d.author.github.replace(/^https:\/\//, "") })]],
			["license", [d.license]],
		];
		$("about-kv").replaceChildren(...kv.flatMap(([k, v], i) => {
			const dt = el("dt", { text: k });
			const dd = el("dd", null, v);
			dt.style.setProperty("--i", String(i));
			dd.style.setProperty("--i", String(i));
			return [dt, dd];
		}));
		const cs = getComputedStyle(root);
		$("about-swatches").replaceChildren(...SWATCHES.map((t) => {
			const sw = el("span", { class: "about-sw", title: "--" + t + " " + cs.getPropertyValue("--" + t).trim() });
			sw.style.background = "var(--" + t + ")";
			return sw;
		}));
		$("about-releases").replaceChildren(...(d.releases || []).map((r) => el("li", { class: "graph-node" + (r.state ? " is-" + r.state : "") }, [
			el("span", { class: "graph-v", text: "v" + r.v }),
			el("span", { class: "graph-note", text: r.note }),
			r.state === "current" ? el("span", { class: "graph-tag graph-head", text: "HEAD → this build" }) : null,
			r.state === "next" ? el("span", { class: "graph-tag graph-next", text: "next" }) : null,
		])));
		$("about-update").hidden = !!(d.update && d.update.disabled);
		renderUpdate(d.update);
	}

	function renderUpdate(u) {
		const box = $("about-update-result");
		if (!u || !u.enabled || !u.latest) return box.replaceChildren();
		const when = u.checked_at ? " (checked " + relTime(u.checked_at) + ")" : "";
		box.replaceChildren(u.newer
			? el("span", { class: "about-newer" }, ["v" + u.latest + " is available" + when + " · ",
				el("a", { href: u.url, target: "_blank", rel: "noopener noreferrer", text: "release notes" })])
			: el("span", { text: "You have the latest release (v" + u.latest + ")" + when + "." }));
	}

	async function openAbout() {
		closeMenu(false);
		const k = $("keys");
		if (k.open) k.close();
		const dlg = $("about");
		const r = await api("/api/about");
		if (!r.ok) return toast("error", "Could not load the About view", r.error);
		renderAbout(r.data);
		dlg.classList.remove("enter");
		void dlg.offsetWidth;
		dlg.classList.add("enter");
		if (!dlg.open) dlg.showModal();
	}
	window.cwmAbout = openAbout;

	function wireAbout() {
		const dlg = $("about");
		document.querySelectorAll("[data-about-open]").forEach((b) => b.addEventListener("click", openAbout));
		$("about-close").addEventListener("click", () => dlg.close());
		dlg.addEventListener("click", (ev) => { if (ev.target === dlg) dlg.close(); });
		$("about-copy").addEventListener("click", async (ev) => {
			const btn = ev.currentTarget; // null once the await below yields
			if (!aboutData) return;
			const ok = await copyQuiet(aboutSummary(aboutData) + "\n" + JSON.stringify(aboutData, null, 2));
			flash(btn, ok ? "copied" : "copy failed");
		});
		$("about-update").addEventListener("click", async (ev) => {
			const btn = ev.currentTarget;
			btn.disabled = true;
			$("about-update-result").replaceChildren(el("span", { class: "spinner", "aria-hidden": "true" }), "asking GitHub…");
			const r = await post("/api/about/check-update", {});
			btn.disabled = false;
			if (!r.ok) {
				return $("about-update-result").replaceChildren(el("span", { class: "about-err", text: r.error + " — try again later, or open the release notes." }));
			}
			renderUpdate(r.data);
		});
	}

	// --- statusline (D67)

	function versionLabel(v) {
		const ver = String(v.version || "").replace(/^v/, "");
		const commit = v.commit && v.commit !== "dev" && v.commit !== "unknown" ? String(v.commit).slice(0, 7) : "";
		if (v.dev || !ver || ver === "dev") return "dev build" + (commit ? " " + commit : "");
		return "v" + ver;
	}

	function uptime(fromISO) {
		const t = new Date(fromISO).getTime();
		if (isNaN(t)) return "";
		const s = Math.max(0, Math.floor((Date.now() - t) / 1000)), m = Math.floor(s / 60), h = Math.floor(m / 60);
		if (s < 60) return s + "s";
		if (m < 60) return m + "m";
		if (h < 24) return h + "h" + String(m % 60).padStart(2, "0") + "m";
		return Math.floor(h / 24) + "d" + (h % 24) + "h";
	}

	let agentInfo = null;
	async function statusline() {
		const [v, a] = await Promise.all([api("/api/version"), api("/api/agent")]);
		if (v.ok && v.data) {
			$("sl-version").textContent = "[cwm " + versionLabel(v.data) + "]";
			verText = "cwm " + v.data.version + " (" + v.data.os + "/" + v.data.arch + ", " + (v.data.commit || "unknown") + ")";
			$("sl-os").textContent = v.data.os + "/" + v.data.arch;
		}
		if (a.ok && a.data) {
			agentInfo = a.data;
			const bind = a.data.bind || "127.0.0.1";
			const local = bind === "127.0.0.1" || bind === "localhost" || bind === "::1";
			const listen = $("sl-listen");
			listen.textContent = (bind.includes(":") ? "[" + bind + "]" : bind) + ":" + a.data.port + (local ? ", localhost only" : ", reachable from the network");
			listen.classList.toggle("sl-warn", !local);
			const tick = () => {
				$("sl-agent-text").textContent = "agent up " + uptime(agentInfo.started);
				setTimeout(tick, /s$/.test(uptime(agentInfo.started)) ? 1000 : 30000);
			};
			tick();
		}
	}

	// --- boot

	document.addEventListener("DOMContentLoaded", async () => {
		wireToggles();
		document.querySelectorAll(".tab").forEach((btn) => {
			btn.addEventListener("click", () => activate(btn.dataset.tab));
			btn.addEventListener("keydown", (ev) => {
				const i = TABS.indexOf(btn.dataset.tab);
				let next = null;
				if (ev.key === "ArrowRight") next = TABS[(i + 1) % TABS.length];
				if (ev.key === "ArrowLeft") next = TABS[(i + TABS.length - 1) % TABS.length];
				if (next) {
					ev.preventDefault();
					activate(next);
					$("tab-" + next).focus();
				}
			});
		});
		$("refresh").addEventListener("click", () => refreshNow(true));
		$("sessions-filter").addEventListener("input", () => renderSessions(false));
		window.addEventListener("hashchange", () => activate(current()));
		wireWizard();
		wirePairForm();
		wireAccounts();
		wireKeys();
		wireDebug();
		wireStats();
		wireMenu();
		wirePalette();
		wireSettings();
		const sheet = $("more-sheet");
		document.querySelectorAll("[data-more-open]").forEach((b) => b.addEventListener("click", (ev) => { sheet.showModal(); if (ev.detail) sheet.focus(); }));
		document.querySelectorAll("[data-tab-go]").forEach((b) => b.addEventListener("click", () => {
			if (sheet.open) sheet.close();
			activate(b.dataset.tabGo);
			window.scrollTo(0, 0);
		}));
		$("sheet-close").addEventListener("click", () => sheet.close());
		sheet.addEventListener("click", (ev) => { if (ev.target === sheet || ev.target.closest("[data-about-open], [data-debug-copy]")) sheet.close(); });
		wireAbout();
		document.querySelectorAll(".cmd-slot[data-cmd]").forEach((n) => n.replaceWith(commandBox(n.dataset.cmd)));
		$("live").addEventListener("click", retryNow);
		$("offline-retry").addEventListener("click", retryNow);

		statusline();
		startLive();
		const st = await refreshState();
		await loadSettings();
		let dismissed = false;
		try { dismissed = sessionStorage.getItem("cwm-wizard-dismissed") === "1"; } catch (err) { dismissed = false; }
		if (st.ok && state && (!state.initialized || !hasAccount()) && !dismissed) {
			showWizard(true);
			wlog("note", "cwm first run on this device");
			wlog("out", "workspace: " + (state.initialized ? "initialized" : "not initialized yet"));
			if (state.claude) wlog(state.claude.found ? "ok" : "fail", state.claude.found ? "Claude Code found" + (state.claude.path ? " at " + state.claude.path : "") : "Claude Code not found", state.claude.found ? "" : state.claude.install);
			gotoStep(!state.initialized ? "init" : "account");
			return;
		}
		activate(current());
		refreshHealthBadge();
	});
})();
