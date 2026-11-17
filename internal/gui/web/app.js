// cwm dashboard (v0.5) — read-only views plus the setup wizard and actions.
// Vanilla JS, no dependencies. Every value from the API is rendered with
// textContent — never parsed as HTML — because session prompts and repo names
// are user-controlled text. Actions POST JSON with the X-CWM-CSRF header; the
// value comes from /api/csrf (a derived value — the launch token itself stays
// in the HttpOnly cookie).
"use strict";

(function () {
  const TABS = ["sessions", "projects", "devices", "wip", "health"];
  const loaded = {};
  let sessionsCache = [];

  function el(tag, attrs, children) {
    const node = document.createElement(tag);
    if (attrs) {
      for (const [k, v] of Object.entries(attrs)) {
        if (k === "class") node.className = v;
        else if (k === "text") node.textContent = v;
        else node.setAttribute(k, v);
      }
    }
    for (const child of children || []) {
      if (child === null || child === undefined) continue;
      node.appendChild(typeof child === "string" ? document.createTextNode(child) : child);
    }
    return node;
  }

  function content(name) {
    return document.querySelector('[data-content="' + name + '"]');
  }

  function show(name, node) {
    const box = content(name);
    box.replaceChildren(node);
  }

  function message(cls, text, detail) {
    return el("div", { class: cls }, [text, detail ? el("span", { class: "detail", text: detail }) : null]);
  }

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
      document.getElementById("auth-banner").hidden = false;
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

  // --- inline feedback --------------------------------------------------

  function copyText(text, btn) {
    const done = (ok) => {
      const old = btn.textContent;
      btn.textContent = ok ? "Copied" : "Select and copy manually";
      setTimeout(() => { btn.textContent = old; }, 1600);
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

  // say renders one inline outcome: kind is ok | error | info | wait.
  function say(box, kind, text, detail, cmd) {
    const cls = kind === "wait" ? "info" : kind;
    box.replaceChildren(el("div", { class: "msg msg-" + cls }, [
      kind === "wait" ? el("span", { class: "spinner", "aria-hidden": "true" }) : null,
      text,
      detail ? el("span", { class: "detail", text: detail }) : null,
      cmd ? commandBox(cmd) : null,
    ]));
  }

  // sayLaunch reports a terminal-launch action (D27): success, the 501
  // copy-the-command fallback, or an error.
  function sayLaunch(box, r, okText) {
    if (r.ok) return say(box, "info", okText, "running: " + r.data.command);
    if (r.status === 501) {
      return say(box, "error", "No terminal window could be opened on this machine. Run this command in a terminal instead:", "", r.command);
    }
    return say(box, "error", "Could not start it.", r.error, r.command);
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
      return false;
    }
    const d = r.data;
    const what = d.changed
      ? "Paired with " + d.peerID.slice(0, 7) + (d.peerName ? " (" + d.peerName + ")" : "") + "."
      : "Already paired with " + d.peerID.slice(0, 7) + " — nothing to do.";
    say(box, "ok", what, "Now pair from the other device with this device's ID: " + d.deviceID);
    return true;
  }

  function table(headers, rows) {
    const thead = el("thead", null, [el("tr", null, headers.map((h) => el("th", { text: h })))]);
    const tbody = el("tbody", null, rows);
    return el("table", null, [thead, tbody]);
  }

  function td(text, cls) {
    return el("td", cls ? { class: cls, text: String(text) } : { text: String(text) });
  }

  function relTime(iso) {
    if (!iso) return "never";
    const t = new Date(iso);
    if (isNaN(t.getTime())) return iso;
    const sec = Math.max(0, (Date.now() - t.getTime()) / 1000);
    if (sec < 3600) return Math.floor(sec / 60) + "m ago";
    if (sec < 86400) return Math.floor(sec / 3600) + "h ago";
    if (sec <= 7 * 86400) return Math.floor(sec / 86400) + "d ago";
    return t.toISOString().slice(0, 10);
  }

  function bytes(n) {
    if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + " GiB";
    if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MiB";
    if (n >= 1 << 10) return (n / (1 << 10)).toFixed(1) + " KiB";
    return n + " B";
  }

  function badge(statusName) {
    const label = statusName === "fail" ? "FAIL" : statusName;
    return el("span", { class: "badge badge-" + statusName, text: label });
  }

  // --- tabs -------------------------------------------------------------

  function renderSessions() {
    const q = document.getElementById("sessions-filter").value.trim().toLowerCase();
    const rows = [];
    sessionsCache.forEach((s, i) => {
      const n = i + 1; // row number matches `cwm sessions --pick N` (unfiltered order)
      const hay = (s.project + " " + s.firstPrompt + " " + s.summary).toLowerCase();
      if (q && !hay.includes(q)) return;
      const prompt = el("td", { class: "prompt" }, [s.firstPrompt || "(no prompt)"]);
      if (s.summary) prompt.title = s.summary;
      rows.push(el("tr", null, [
        td(n, "num"),
        td(s.project),
        td(s.id.slice(0, 8), "mono"),
        prompt,
        td(s.messages, "num"),
        td(s.branch || "", "muted"),
        el("td", { title: s.modified, text: relTime(s.modified) }),
        resumeCell(s),
      ]));
    });
    if (sessionsCache.length === 0) {
      show("sessions", message("empty", "No sessions yet — open a repo via cwm open <name> to create one."));
    } else if (rows.length === 0) {
      show("sessions", message("empty", "No session matches the filter."));
    } else {
      show("sessions", table(["N", "Project", "ID", "First prompt", "Msgs", "Branch", "Modified", ""], rows));
    }
  }

  function resumeCell(s) {
    const box = el("div", { class: "result", "aria-live": "polite" });
    const btn = el("button", { type: "button", class: "btn btn-small", text: "Resume" });
    btn.title = "Open a terminal window resuming this session";
    btn.addEventListener("click", async () => {
      btn.disabled = true;
      say(box, "wait", "Opening a terminal…");
      const r = await post("/api/sessions/resume", { project: s.project, id: s.id });
      btn.disabled = false;
      sayLaunch(box, r, "Opened a terminal window for this session.");
    });
    return el("td", { class: "actions" }, [btn, box]);
  }

  const loaders = {
    async sessions() {
      const r = await api("/api/sessions");
      if (!r.ok) return show("sessions", message("error", "Could not load sessions.", r.error));
      sessionsCache = r.data || [];
      renderSessions();
    },

    async projects() {
      const r = await api("/api/projects");
      if (!r.ok) return show("projects", message("error", "Could not load projects.", r.error));
      const list = r.data || [];
      if (list.length === 0) {
        return show("projects", message("empty", "No projects yet — open a repo via cwm open <name> new to create one."));
      }
      show("projects", table(["Project", "Sessions", "Last session"], list.map((p) => el("tr", null, [
        td(p.name),
        td(p.sessions, "num"),
        el("td", { title: p.modified || "", text: relTime(p.modified) }),
      ]))));
    },

    async devices() {
      const r = await api("/api/devices");
      if (!r.ok) {
        const text = r.status === 503
          ? "Syncthing is not reachable on this machine (optional — run cwm pair to set up sync)."
          : "Could not load devices.";
        return show("devices", message("error", text, r.error));
      }
      const list = r.data || [];
      if (list.length === 0) {
        return show("devices", message("empty", "No paired devices yet — run cwm pair on both devices."));
      }
      show("devices", table(["Device", "Name", "Connected", "Address", "In", "Out", "Completion"], list.map((d) => {
        const fill = el("span");
        fill.style.width = Math.max(0, Math.min(100, d.completion)) + "%";
        return el("tr", null, [
          el("td", { class: "mono", title: d.id, text: d.id.slice(0, 7) }),
          td(d.name || ""),
          el("td", null, [el("span", { class: "badge badge-" + (d.connected ? "ok" : "warn"), text: d.connected ? "yes" : "no" })]),
          td(d.address || "", "mono"),
          td(bytes(d.inBytes), "num"),
          td(bytes(d.outBytes), "num"),
          el("td", null, [el("span", { class: "bar" }, [fill]), d.completion.toFixed(1) + "%"]),
        ]);
      })));
    },

    async wip() {
      const r = await api("/api/wip");
      if (!r.ok) return show("wip", message("error", "Could not scan repos.", r.error));
      const list = r.data || [];
      if (list.length === 0) {
        return show("wip", message("empty", "No dirty repos — everything committed."));
      }
      show("wip", table(["Repo", "Changed", "Last commit"], list.map((d) => el("tr", null, [
        td(d.name),
        td(d.changed + " changed file(s)"),
        td(d.lastCommit, "muted"),
      ]))));
    },

    async health() {
      const r = await api("/api/doctor");
      if (!r.ok) return show("health", message("error", "Could not run health checks.", r.error));
      const list = r.data || [];
      const counts = { ok: 0, warn: 0, fail: 0 };
      list.forEach((c) => { counts[c.status] = (counts[c.status] || 0) + 1; });
      const summary = el("div", { class: "summary" }, [
        el("span", { class: "badge badge-ok", text: counts.ok + " ok" }),
        el("span", { class: "badge badge-warn", text: counts.warn + " warn" }),
        el("span", { class: "badge badge-fail", text: counts.fail + " fail" }),
      ]);
      const items = list.map((c) => el("li", null, [
        badge(c.status),
        el("div", { class: "text" }, [
          c.detail,
          c.lines && c.lines.length ? el("pre", { text: c.lines.join("\n") }) : null,
        ]),
      ]));
      show("health", el("div", null, [summary, el("ul", { class: "checks" }, items)]));
    },
  };

  // --- setup state + wizard ----------------------------------------------

  let state = null;
  async function refreshState() {
    const r = await api("/api/state");
    if (r.ok && r.data) {
      state = r.data;
      for (const id of ["wiz-own-id", "pair-own-id"]) {
        document.getElementById(id).textContent = state.deviceID ||
          "unavailable — Syncthing is not running on this device";
      }
    }
    return r;
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
    document.getElementById("wizard").hidden = !on;
    document.getElementById("dashboard").hidden = on;
  }

  function leaveWizard() {
    if (accountPoll) clearInterval(accountPoll);
    accountPoll = null;
    showWizard(false);
    for (const k of Object.keys(loaded)) delete loaded[k]; // reload with the new state
    activate(current());
  }

  function hasAccount(name) {
    return !!(state && (state.accounts || []).some((a) => !name || a.name === name));
  }

  function afterAccountStep() {
    gotoStep("pair");
  }

  function wireWizard() {
    const initBtn = document.getElementById("wiz-init");
    const initBox = document.getElementById("wiz-init-result");
    initBtn.addEventListener("click", async () => {
      initBtn.disabled = true;
      say(initBox, "wait", "Initializing the workspace…");
      const r = await post("/api/init", {});
      initBtn.disabled = false;
      if (!r.ok) return say(initBox, "error", "Initialization failed.", r.error);
      const checks = r.data.checks || [];
      const fails = checks.filter((c) => c.status === "fail").length;
      const warns = checks.filter((c) => c.status === "warn").length;
      say(initBox, "ok", "Workspace initialized.",
        "health: " + (checks.length - fails - warns) + " ok, " + warns + " warn, " + fails + " fail (details in the Health tab)");
      await refreshState();
      setTimeout(() => gotoStep(hasAccount() ? "pair" : "account"), 700);
    });

    const nameIn = document.getElementById("wiz-account-name");
    const addBtn = document.getElementById("wiz-account-add");
    const addBox = document.getElementById("wiz-account-result");
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

    const pairBtn = document.getElementById("wiz-pair");
    pairBtn.addEventListener("click", async () => {
      const ok = await pairWith(document.getElementById("wiz-peer-id"), document.getElementById("wiz-peer-name"),
        document.getElementById("wiz-pair-result"), pairBtn);
      if (ok) setTimeout(() => gotoStep("done"), 1500);
    });
    document.getElementById("wiz-pair-skip").addEventListener("click", () => gotoStep("done"));
    document.getElementById("wiz-done").addEventListener("click", leaveWizard);
    document.getElementById("wiz-dismiss").addEventListener("click", () => {
      try { sessionStorage.setItem("cwm-wizard-dismissed", "1"); } catch (err) { /* private mode */ }
      leaveWizard();
    });
    document.getElementById("wiz-own-id-copy").addEventListener("click", (ev) => {
      if (state && state.deviceID) copyText(state.deviceID, ev.currentTarget);
    });
  }

  function wirePairForm() {
    const btn = document.getElementById("pair-submit");
    btn.addEventListener("click", async () => {
      const ok = await pairWith(document.getElementById("pair-device-id"), document.getElementById("pair-name"),
        document.getElementById("pair-result"), btn);
      if (ok) load("devices");
    });
    document.getElementById("pair-own-id-copy").addEventListener("click", (ev) => {
      if (state && state.deviceID) copyText(state.deviceID, ev.currentTarget);
    });
  }

  function load(name) {
    show(name, message("loading", "Loading…"));
    loaded[name] = true;
    return loaders[name]();
  }

  function activate(name) {
    if (!TABS.includes(name)) name = TABS[0];
    for (const t of TABS) {
      const selected = t === name;
      document.getElementById("tab-" + t).setAttribute("aria-selected", String(selected));
      document.getElementById("tab-" + t).tabIndex = selected ? 0 : -1;
      document.getElementById("panel-" + t).hidden = !selected;
    }
    if (location.hash !== "#" + name) history.replaceState(null, "", "#" + name);
    if (!loaded[name]) load(name);
  }

  function current() {
    const name = location.hash.replace(/^#/, "");
    return TABS.includes(name) ? name : TABS[0];
  }

  document.addEventListener("DOMContentLoaded", async () => {
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
          document.getElementById("tab-" + next).focus();
        }
      });
    });
    document.getElementById("refresh").addEventListener("click", () => load(current()));
    document.getElementById("sessions-filter").addEventListener("input", renderSessions);
    window.addEventListener("hashchange", () => activate(current()));
    wireWizard();
    wirePairForm();

    const v = await api("/api/version");
    if (v.ok && v.data) {
      document.getElementById("version").textContent =
        "cwm " + v.data.version + " (" + v.data.os + "/" + v.data.arch + ", " + v.data.commit + ")";
    }
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
