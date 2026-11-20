// cwm dashboard (v0.7) — read-only views, the setup wizard and actions,
// plus the cyber UI layer: themes, toasts, skeletons, live updates and
// keyboard shortcuts. Vanilla JS, no dependencies, no build step.
//
// Safety rule: every value from the API is rendered through DOM nodes and
// textContent — never parsed as HTML — because session prompts, repo names
// and doctor details are user-controlled text. Actions POST JSON with the
// X-CWM-CSRF header; its value comes from /api/csrf (a derived value — the
// launch token itself stays in the HttpOnly cookie).
"use strict";

(function () {
  const TABS = ["sessions", "projects", "devices", "wip", "health", "accounts"];
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

  // richText renders `backticked` spans as <code> and everything else as
  // text nodes. The input is split, never parsed: an unpaired backtick
  // stays a literal character.
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

  // --- HTTP ---------------------------------------------------------------

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
  async function post(path, payload) {
    if (!csrf) {
      const c = await api("/api/csrf");
      if (!c.ok || !c.data || !c.data.csrf) {
        return { ok: false, status: c.status, error: c.error || "could not get a CSRF token", command: "" };
      }
      csrf = c.data.csrf;
    }
    return request(path, {
      method: "POST",
      headers: { Accept: "application/json", "Content-Type": "application/json", "X-CWM-CSRF": csrf },
      body: JSON.stringify(payload || {}),
    });
  }

  // --- copy, toasts, inline feedback ---------------------------------------

  function copyText(text, btn) {
    const done = (ok) => {
      if (btn) {
        const old = btn.textContent;
        btn.textContent = ok ? "Copied" : "Select and copy manually";
        setTimeout(() => { btn.textContent = old; }, 1600);
      }
      toast(ok ? "ok" : "error", ok ? "Copied to clipboard" : "Copy failed — select the text and copy it manually");
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(() => done(true), () => done(false));
      return;
    }
    const area = el("textarea", { readonly: "", class: "offscreen" });
    area.value = text;
    document.body.appendChild(area);
    area.select();
    let ok = false;
    try { ok = document.execCommand("copy"); } catch (err) { ok = false; }
    area.remove();
    done(ok);
  }

  function copyButton(getText) {
    const btn = el("button", { type: "button", class: "btn btn-small", text: "Copy" });
    btn.addEventListener("click", () => copyText(getText(), btn));
    return btn;
  }

  function commandBox(cmd) {
    return el("div", { class: "cmd" }, [el("code", { text: cmd }), copyButton(() => cmd)]);
  }

  // toast shows a transient notification. kind: ok | error | info.
  // opts.cmd adds a copyable command; opts.sticky keeps it until closed.
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

  // say renders one inline outcome: kind is ok | error | info | wait.
  function say(box, kind, text, detail, cmd) {
    const cls = kind === "wait" ? "info" : kind;
    box.replaceChildren(el("div", { class: "msg msg-" + cls }, [
      kind === "wait" ? el("span", { class: "spinner", "aria-hidden": "true" }) : null,
      text,
      detail ? el("span", { class: "detail" }, [richText(detail)]) : null,
      cmd ? commandBox(cmd) : null,
    ]));
  }

  // toastLaunch reports a terminal-launch action (D27): success, the 501
  // copy-the-command fallback, or an error.
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

  // toastNoClaude: the launch was refused because claude cannot be found
  // (D49) — the install command, copyable, instead of a terminal window
  // that fails and closes.
  function toastNoClaude(r) {
    return toast("error", "Claude Code is needed first", r.error,
      { cmd: r.command, sticky: true });
  }

  // toastPathHint: claude was found outside PATH. cwm works, but the user's
  // own terminals will not find `claude` until its folder is on PATH.
  function toastPathHint(d) {
    if (!d || !d.pathHint) return;
    toast("info", "claude is not on your PATH", "cwm uses " + d.claudePath +
      ". To run `claude` in your own terminals too, run this once and open a new terminal:", { cmd: d.pathHint, sticky: true });
  }

  // --- debug logs (D52) -----------------------------------------------------------

  const BUG_URL = document.querySelector("a[href*='/issues/new']") ? document.querySelector("a[href*='/issues/new']").href : "";

  // copyQuiet tries the async Clipboard API, then a hidden textarea +
  // execCommand("copy"). Resolves true when either worked.
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
    const pre = $("debug-text");
    pre.textContent = text;
    const dlg = $("debug-dialog");
    if (!dlg.open) dlg.showModal();
    selectAll(pre);
  }

  let debugBusy = false;
  async function copyDebugLogs() {
    if (debugBusy) return;
    debugBusy = true;
    const buttons = document.querySelectorAll("[data-debug-copy]");
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
      return toast("error", "Could not collect debug logs", msg + " In a terminal, `cwm debug --copy` does the same.", { cmd: "cwm debug --copy" });
    }
    const lines = text.replace(/\n+$/, "").split("\n").length;
    if (await copyQuiet(text)) {
      return toast("ok", "Debug logs copied (" + lines + " lines) — paste into a GitHub issue", "Redacted: no tokens, e-mails, keys or user names. Give it a quick read before posting.",
        BUG_URL ? { link: { href: BUG_URL, text: "Report a bug →" } } : null);
    }
    showDebugText(text);
  }

  function wireDebug() {
    document.querySelectorAll("[data-debug-copy]").forEach((b) => b.addEventListener("click", copyDebugLogs));
    const dlg = $("debug-dialog");
    $("debug-close").addEventListener("click", () => dlg.close());
    $("debug-select").addEventListener("click", () => {
      const pre = $("debug-text");
      selectAll(pre);
      let ok = false;
      try { ok = document.execCommand("copy"); } catch (err) { ok = false; }
      if (ok) toast("ok", "Debug logs copied (" + pre.textContent.replace(/\n+$/, "").split("\n").length + " lines) — paste into a GitHub issue");
    });
  }

  // Device IDs are shown dashed by Syncthing; the API wants them bare.
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

  // --- table helpers -------------------------------------------------------

  // col describes a column: label (header + mobile card label) and class.
  function col(label, cls) {
    return { label: label, cls: cls || "" };
  }

  // table builds a <table>; each row is an array of cells (strings or <td>
  // nodes). Cells get the column's class and a data-label for the stacked
  // mobile layout. animate staggers the row reveal.
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

  // --- tabs ----------------------------------------------------------------

  function renderSessions(animate) {
    const q = $("sessions-filter").value.trim().toLowerCase();
    const rows = [];
    sessionsCache.forEach((s, i) => {
      const n = i + 1; // row number matches `cwm sessions --pick N` (unfiltered order)
      const hay = (s.project + " " + s.firstPrompt + " " + s.summary).toLowerCase();
      if (q && !hay.includes(q)) return;
      const prompt = el("td", null, [s.firstPrompt || "(no prompt)"]);
      if (s.summary) prompt.title = s.summary;
      rows.push([
        n,
        el("td", null, [el("span", { class: "proj", text: s.project })]),
        el("td", { title: s.id, text: s.id.slice(0, 8) }),
        prompt,
        s.messages,
        el("td", null, [s.branch ? el("span", { class: "branch", text: s.branch }) : ""]),
        whenCell(s.modified),
        resumeCell(s),
      ]);
    });
    if (sessionsCache.length === 0) {
      show("sessions", message("empty", "No sessions yet — open a repo via cwm open <name> to create one."));
    } else if (rows.length === 0) {
      show("sessions", message("empty", "No session matches the filter."));
    } else {
      show("sessions", table([
        col("N", "num"), col("Project", "lead"), col("ID", "mono"), col("First prompt", "prompt wide"),
        col("Msgs", "num"), col("Branch"), col("Modified", "when"), col("", "actions"),
      ], rows, animate));
    }
  }

  function resumeCell(s) {
    const btn = el("button", { type: "button", class: "btn btn-small", text: "Resume" });
    btn.title = "Open a terminal window resuming this session";
    btn.setAttribute("aria-label", "Resume session " + s.id.slice(0, 8) + " of " + s.project);
    btn.addEventListener("click", async () => {
      btn.disabled = true;
      const old = btn.textContent;
      btn.replaceChildren(el("span", { class: "spinner", "aria-hidden": "true" }), "Opening…");
      const r = await post("/api/sessions/resume", { project: s.project, id: s.id });
      btn.disabled = false;
      btn.textContent = old;
      toastLaunch(r, "Opened a terminal for " + s.project + " · " + s.id.slice(0, 8));
    });
    return el("td", null, [btn]);
  }

  // Each loader fetches one endpoint and renders it. silent = background
  // refresh: no skeleton, no row animation, and no re-render at all when
  // the payload did not change (keeps focus, scroll and hover intact).
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
      show("projects", table([col("Project", "lead"), col("Sessions", "num"), col("Activity"), col("Last session", "when")],
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
      if (r.data && !Array.isArray(r.data) && r.data.status === "not_configured") {
        if (!changed("devices", r.data)) return;
        return show("devices", syncSetupCard(r.data));
      }
      const list = r.data || [];
      if (!changed("devices", list)) return;
      if (list.length === 0) {
        return show("devices", message("empty", "No paired devices yet — run cwm pair on both devices."));
      }
      show("devices", table([
        col("Device", "mono lead"), col("Name"), col("Connected", "status"), col("Address", "mono"),
        col("In", "num"), col("Out", "num"), col("Completion"),
      ], list.map((d) => {
        const pct = Math.max(0, Math.min(100, Number(d.completion) || 0));
        const fill = el("span");
        fill.style.width = pct + "%";
        const pill = badge(d.connected ? "ok" : "warn", d.connected ? "online" : "offline");
        if (d.connected) pill.classList.add("badge-live");
        return [
          el("td", { title: d.id, text: d.id.slice(0, 7) }),
          d.name || "",
          el("td", null, [pill]),
          d.address || "",
          bytes(d.inBytes),
          bytes(d.outBytes),
          el("td", null, [el("span", { class: "bar" }, [fill]), pct.toFixed(1) + "%"]),
        ];
      }), !silent));
    },

    async wip(silent) {
      const r = await api("/api/wip");
      if (!r.ok) return loadError("wip", r, silent, "Could not scan repos.");
      const list = r.data || [];
      if (!changed("wip", list)) return;
      if (list.length === 0) {
        return show("wip", message("empty", "No dirty repos — everything committed."));
      }
      show("wip", table([col("Repo", "lead"), col("Changed", "status"), col("Last commit", "muted wide")], list.map((d) => [
        el("td", null, [el("span", { class: "proj", text: d.name })]),
        el("td", null, [badge("warn", d.changed + " file" + (d.changed === 1 ? "" : "s"))]),
        d.lastCommit,
      ]), !silent));
    },

    async health(silent) {
      const r = await api("/api/doctor");
      if (!r.ok) return loadError("health", r, silent, "Could not run health checks.");
      const list = r.data || [];
      if (!changed("health", list)) return;
      const counts = { ok: 0, warn: 0, fail: 0 };
      list.forEach((c) => { counts[c.status] = (counts[c.status] || 0) + 1; });
      const summary = el("div", { class: "summary" }, [
        badge("ok", counts.ok + " ok"),
        badge("warn", counts.warn + " warn"),
        badge("fail", counts.fail + " fail"),
      ]);
      const items = list.map((c, i) => {
        const li = el("li", silent ? null : { class: "reveal" }, [
          badge(c.status),
          el("div", { class: "text" }, [
            c.name ? el("span", { class: "name", text: c.name }) : null,
            richText(c.detail),
            c.lines && c.lines.length ? el("pre", { text: c.lines.join("\n") }) : null,
          ]),
        ]);
        if (!silent) li.style.setProperty("--i", String(Math.min(i, 18)));
        return li;
      });
      show("health", el("div", null, [summary, el("ul", { class: "checks" }, items)]));
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

  // --- devices: Syncthing not set up yet (D58) ------------------------------------

  function syncSetupCard(d) {
    return el("div", { class: "setup-card" }, [
      el("h3", { text: "Sync is not set up on this device yet" }),
      el("p", { class: "hint" }, [richText(d.hint || "Syncthing keeps the workspace in sync between devices. It is optional.")]),
      el("p", { class: "hint", text: "From a terminal, on each device (the pair form above works once Syncthing runs):" }),
      commandBox("cwm pair --start"),
      d.error ? el("p", { class: "detail" }, ["details: ", richText(d.error)]) : null,
    ]);
  }

  // --- accounts (D55-D57) --------------------------------------------------------------

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
    $("accounts-dot").hidden = !defaultWithoutLogin(list);
  }

  let accountsPoll = 0;
  // While a login runs in its terminal, follow it every 2 s (the event
  // stream also reports it, but polling keeps the pill honest without SSE).
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

  function accountCard(a, i, animate) {
    const st = a.login_status || "never";
    const head = el("div", { class: "acct-head" }, [
      el("span", { class: "acct-name", text: a.name }),
      a.is_default ? el("span", { class: "tag tag-default", text: "default" }) : null,
      loginPill(st),
    ]);
    const meta = el("div", { class: "acct-meta" }, [
      metaLine("login", a.email ? a.email + (a.plan ? " · " + a.plan : "") : "none recorded"),
      metaLine("last login", relTime(a.last_login), a.last_login),
      metaLine("last used", a.last_used ? relTime(a.last_used) : "not launched yet", a.last_used),
      a.is_default && a.config_dir_ready === false ? metaLine("config dir", "missing — log in again to recreate it") : null,
    ]);
    const children = [head, meta];
    if (st === "failed" || (st === "in_progress" && a.login_error)) {
      children.push(el("div", { class: "msg msg-error acct-error" }, [
        "The last login did not finish.",
        a.login_error ? el("span", { class: "detail", text: a.login_error }) : null,
        el("div", { class: "acct-inline" }, [accountLoginButton(a, "Retry login", "btn-primary")]),
      ]));
    } else if (st === "in_progress") {
      children.push(el("div", { class: "msg msg-info acct-error" }, [
        el("span", { class: "spinner", "aria-hidden": "true" }),
        "Waiting for the login in its terminal window…",
        el("div", { class: "acct-inline" }, [accountLoginButton(a, "Open a new login window")]),
      ]));
    }
    const actions = el("div", { class: "acct-actions" });
    if (st !== "failed" && st !== "in_progress") {
      actions.appendChild(accountLoginButton(a, st === "done" ? "Log in again" : "Log in", st === "never" ? "btn-primary" : ""));
    }
    if (!a.is_default) {
      const def = el("button", { type: "button", class: "btn btn-small", text: "Make default" });
      def.addEventListener("click", () => setDefault(a.name, def));
      actions.appendChild(def);
    }
    const rm = el("button", { type: "button", class: "btn btn-small btn-danger", text: "Remove" });
    rm.setAttribute("aria-label", "Remove account " + a.name);
    rm.addEventListener("click", () => askRemove(a));
    actions.appendChild(rm);
    children.push(actions);
    const card = el("article", { class: "acct-card" + (a.is_default ? " is-default" : "") + (animate ? " reveal" : ""), "aria-label": "Account " + a.name }, children);
    if (animate) card.style.setProperty("--i", String(Math.min(i, 18)));
    return card;
  }

  let accountsCache = [];
  function renderAccounts(list, animate) {
    accountsCache = list;
    renderBanner(list);
    const box = content("accounts");
    if (!list.length) return box.replaceChildren();
    box.replaceChildren(el("div", { class: "acct-grid" }, list.map((a, i) => accountCard(a, i, animate))));
  }

  function validAccountName(name) {
    if (!name) return "Enter a name.";
    if (name.length > 32) return "At most 32 characters.";
    if (/[\\/]/.test(name)) return "No / or \\ in account names.";
    if (["add", "list", "use", "remove", "rm", "login"].includes(name)) return "“" + name + "” is reserved by cwm — pick another name.";
    if (!/^[a-z0-9][a-z0-9_-]*$/.test(name) || /^[0-9]+$/.test(name)) return "Use lowercase letters, digits, dashes or underscores, starting with a letter or digit (not all digits).";
    if (accountsCache.some((a) => a.name === name)) return "There already is an account “" + name + "”.";
    return "";
  }

  // reportLogin explains a login launch inline and as a toast.
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
    if (reportLogin(box, name, r) && r.status !== 424) input.value = "";
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

  async function setDefault(name, btn) {
    btn.disabled = true;
    const r = await post("/api/accounts/set-default", { name: name });
    if (r.ok) toast("ok", "“" + name + "” is the default account now", "cwm open and Resume use it.");
    else toast("error", "Could not change the default account", r.error);
    await load("accounts", true);
    refreshState();
  }

  let removing = null;
  function askRemove(a) {
    const dlg = $("acct-remove-dialog");
    removing = a;
    $("acct-remove-text").textContent = accountsCache.length <= 1
      ? "“" + a.name + "” is the only account. cwm needs one to launch Claude Code, so it cannot be removed — add another account first."
      : "Remove “" + a.name + "” from cwm? Its account dir is backed up to ~/.cwm/backups first." +
        (a.is_default ? " It is the default account; the next remaining account becomes the default." : "");
    $("acct-remove-dir").checked = false;
    $("acct-remove-confirm").disabled = accountsCache.length <= 1;
    dlg.showModal();
  }

  function wireAccounts() {
    $("acct-add").addEventListener("click", addAccount);
    $("acct-name").addEventListener("keydown", (ev) => { if (ev.key === "Enter") addAccount(); });
    const dlg = $("acct-remove-dialog");
    $("acct-remove-cancel").addEventListener("click", () => dlg.close());
    $("acct-remove-confirm").addEventListener("click", async () => {
      const a = removing;
      if (!a) return;
      const btn = $("acct-remove-confirm");
      btn.disabled = true;
      const r = await post("/api/accounts/remove", { name: a.name, also_delete_config_dir: $("acct-remove-dir").checked });
      btn.disabled = false;
      dlg.close();
      if (r.ok) {
        toast("ok", "Removed “" + a.name + "”", (r.data.backup ? "Backup: " + r.data.backup + ". " : "") +
          (r.data.deleted_config_dir ? "Its config dir was deleted. " : "") + "Default account: " + (r.data.default || "none") + ".");
      } else {
        toast("error", "Could not remove “" + a.name + "”", r.error);
      }
      await load("accounts", true);
      refreshState();
    });
  }

  // --- setup state + wizard -----------------------------------------------------

  let state = null;
  async function refreshState() {
    const r = await api("/api/state");
    if (r.ok && r.data) {
      state = r.data;
      for (const id of ["wiz-own-id", "pair-own-id"]) {
        $(id).textContent = state.deviceID || "unavailable — Syncthing is not running on this device";
      }
      renderClaude(state.claude);
    }
    return r;
  }

  // renderClaude shows whether Add account can work (D49): claude missing →
  // the install command; found outside PATH → where, plus the PATH command
  // for the user's own terminals. Nothing when all is well.
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

  function gotoStep(name) {
    const at = STEPS.indexOf(name);
    document.querySelectorAll(".steps li").forEach((li) => {
      const i = STEPS.indexOf(li.dataset.step);
      li.className = i < at ? "done" : i === at ? "current" : "";
      if (i === at) li.setAttribute("aria-current", "step"); else li.removeAttribute("aria-current");
    });
    document.querySelectorAll(".step").forEach((div) => { div.hidden = div.dataset.step !== name; });
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
      const r = await post("/api/init", {});
      initBtn.disabled = false;
      if (!r.ok) {
        toast("error", "Initialization failed", r.error);
        return say(initBox, "error", "Initialization failed.", r.error);
      }
      const checks = r.data.checks || [];
      const fails = checks.filter((c) => c.status === "fail").length;
      const warns = checks.filter((c) => c.status === "warn").length;
      say(initBox, "ok", "Workspace initialized.",
        "health: " + (checks.length - fails - warns) + " ok, " + warns + " warn, " + fails + " fail (details in the Health tab)");
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
      const r = await post("/api/accounts", { name: name });
      addBtn.disabled = false;
      if (r.status === 424) {
        refreshState();
        toastNoClaude(r);
        return say(addBox, "error", "Claude Code is needed first.", r.error, r.command);
      }
      if (r.ok) toastPathHint(r.data);
      if (!r.ok && r.status !== 501) return say(addBox, "error", "Could not add the account.", r.error, r.command);
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
      const ok = await pairWith($("wiz-peer-id"), $("wiz-peer-name"), $("wiz-pair-result"), pairBtn);
      if (ok) setTimeout(() => gotoStep("done"), 1500);
    });
    $("wiz-pair-skip").addEventListener("click", () => gotoStep("done"));
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

  // load shows a skeleton and fetches; silent reloads keep the current view.
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
    if (!TABS.includes(name)) name = TABS[0];
    for (const t of TABS) {
      const selected = t === name;
      const tab = $("tab-" + t);
      const panel = $("panel-" + t);
      tab.setAttribute("aria-selected", String(selected));
      tab.tabIndex = selected ? 0 : -1;
      if (selected && panel.hidden) {
        panel.classList.remove("enter");
        void panel.offsetWidth; // restart the enter animation
        panel.classList.add("enter");
      }
      panel.hidden = !selected;
    }
    if (location.hash !== "#" + name) history.replaceState(null, "", "#" + name);
    if (!loaded[name]) load(name);
    else if (stale[name]) load(name, true);
  }

  function current() {
    const name = location.hash.replace(/^#/, "");
    return TABS.includes(name) ? name : TABS[0];
  }

  function dashboardVisible() {
    return !$("dashboard").hidden;
  }

  async function refreshNow(manual) {
    const btn = $("refresh");
    if (manual) btn.classList.add("spinning");
    if (dashboardVisible()) await load(current(), !manual);
    if (manual) {
      setTimeout(() => btn.classList.remove("spinning"), 300);
    }
  }

  // --- theme + background toggles ---------------------------------------------------

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
    const btn = $("bg-toggle");
    btn.setAttribute("aria-pressed", String(on));
    btn.title = "Animated background (" + (on ? "on" : "off") + (reduceMotion.matches ? " — reduced motion: still image" : "") + ")";
    if (persist) store("cwm-bg", on ? "on" : "off");
  }

  function wireToggles() {
    applyTheme(root.getAttribute("data-theme-pref") || "system", false);
    document.querySelectorAll("[data-theme-set]").forEach((b) => {
      b.addEventListener("click", () => applyTheme(b.dataset.themeSet, true));
    });
    const onSystem = () => {
      if ((root.getAttribute("data-theme-pref") || "system") === "system") applyTheme("system", false);
    };
    if (systemLight.addEventListener) systemLight.addEventListener("change", onSystem);
    applyBg(root.getAttribute("data-bg") !== "off", false);
    $("bg-toggle").addEventListener("click", () => {
      const on = root.getAttribute("data-bg") === "off";
      applyBg(on, true);
      toast("info", "Animated background " + (on ? "on" : "off"));
    });
  }

  // --- typing tagline ----------------------------------------------------------------

  function typeTagline() {
    const node = $("tagline");
    const phrases = ["one workspace · every device", "workspace dashboard"];
    if (reduceMotion.matches) return; // static text already in the DOM
    let p = 0, i = 0, deleting = false;
    const tick = () => {
      if (document.hidden) { setTimeout(tick, 500); return; }
      const word = phrases[p];
      if (!deleting) {
        i++;
        node.textContent = word.slice(0, i);
        if (i === word.length) {
          if (p === phrases.length - 1) return; // settle on the last phrase
          deleting = true;
          setTimeout(tick, 1500);
          return;
        }
        setTimeout(tick, 35 + Math.random() * 40);
      } else {
        i--;
        node.textContent = word.slice(0, i);
        if (i === 0) { deleting = false; p++; setTimeout(tick, 300); return; }
        setTimeout(tick, 16);
      }
    };
    node.textContent = "";
    setTimeout(tick, 250);
  }

  // --- live updates: SSE when the server has /api/events, else polling ----------------

  let pollTimer = 0;
  let liveMode = "init";
  let lastRefresh = Date.now();

  // beat replays the live dot's single ping: one heartbeat per data refresh.
  function beat() {
    const box = $("live");
    box.classList.remove("beat");
    void box.offsetWidth;
    box.classList.add("beat");
  }

  function setLive(mode) {
    liveMode = mode === "offline" ? liveMode : mode;
    const box = $("live");
    box.dataset.mode = mode;
    const label = { init: "connecting", sse: "live", poll: "auto · 15s", offline: "offline" }[mode] || mode;
    $("live-text").textContent = label;
    box.title = {
      sse: "Live updates: server-sent events from cwm",
      poll: "Auto-refresh every 15 seconds (this cwm has no live event stream)",
      offline: "cwm gui is not reachable — retrying",
    }[mode] || "Live updates";
  }

  // sseAvailable probes GET /api/events: 200 + text/event-stream means the
  // agent streams events (v0.6+); anything else falls back to polling.
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

  const TOPIC_TAB = { doctor: "health", health: "health", sessions: "sessions", projects: "projects", devices: "devices", wip: "wip", accounts: "accounts" };
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
        // v0.6 "state" events name what changed: refresh just those tabs.
        if (Array.isArray(d.changed)) parts = d.changed.map((c) => String(c).toLowerCase()).filter((c) => TOPIC_TAB[c]);
      }
    } catch (err) { /* plain-text payloads are fine */ }
    topic = topic.toLowerCase();
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

  // --- keyboard shortcuts ------------------------------------------------------------

  function typing(target) {
    if (!target || !target.tagName) return false;
    const tag = target.tagName;
    return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || target.isContentEditable;
  }

  function wireKeys() {
    const dlg = $("keys");
    $("keys-close").addEventListener("click", () => dlg.close());
    document.addEventListener("keydown", (ev) => {
      if (ev.defaultPrevented || ev.ctrlKey || ev.metaKey || ev.altKey) return;
      if (ev.key === "Escape" && ev.target === $("sessions-filter")) {
        ev.target.blur();
        return;
      }
      if (typing(ev.target) || dlg.open || $("debug-dialog").open || $("acct-remove-dialog").open) return;
      const k = ev.key;
      if (k >= "1" && k <= "6" && dashboardVisible()) {
        const name = TABS[Number(k) - 1];
    // While the shortcut help is open the tab key hints show (D66).
    dlg.addEventListener("close", () => root.removeAttribute("data-keys"));
        activate(name);
        $("tab-" + name).focus({ preventScroll: true });
      } else if (k === "/" && dashboardVisible()) {
        ev.preventDefault();
        activate("sessions");
        $("sessions-filter").focus();
      } else if (k === "t") {
        cycleTheme();
      } else if (k === "b") {
        $("bg-toggle").click();
      } else if (k === "r") {
        refreshNow(true);
      } else if (k === "d") {
        copyDebugLogs();
      } else if (k === "?") {
        dlg.showModal();
      } else {
        return;
      }
      ev.preventDefault();
    });
  }

  // --- boot ------------------------------------------------------------------------------

        root.setAttribute("data-keys", "open");
  document.addEventListener("DOMContentLoaded", async () => {
    wireToggles();
    typeTagline();
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

    const v = await api("/api/version");
    if (v.ok && v.data) {
      $("version").textContent = "cwm " + v.data.version + " (" + v.data.os + "/" + v.data.arch + ", " + v.data.commit + ")";
    }
    startLive();
    const st = await refreshState();
    let dismissed = false;
    try { dismissed = sessionStorage.getItem("cwm-wizard-dismissed") === "1"; } catch (err) { dismissed = false; }
    if (st.ok && state && (!state.initialized || !hasAccount()) && !dismissed) {
      showWizard(true);
      gotoStep(!state.initialized ? "init" : "account");
      return;
    }
    activate(current());
  });
})();
