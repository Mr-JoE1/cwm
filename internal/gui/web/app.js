// cwm dashboard (v0.5-A, read-only). Vanilla JS, no dependencies.
// Every value from the API is rendered with textContent — never parsed as HTML —
// because session prompts and repo names are user-controlled text.
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

  async function api(path) {
    let res;
    try {
      res = await fetch(path, { credentials: "same-origin", headers: { Accept: "application/json" } });
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
      return { ok: false, status: res.status, error: (body && body.error) || res.statusText };
    }
    return { ok: true, status: res.status, data: body };
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
        el("td", null, [el("code", { text: "cwm sessions --pick " + n })]),
      ]));
    });
    if (sessionsCache.length === 0) {
      show("sessions", message("empty", "No sessions yet — open a repo via cwm open <name> to create one."));
    } else if (rows.length === 0) {
      show("sessions", message("empty", "No session matches the filter."));
    } else {
      show("sessions", table(["N", "Project", "ID", "First prompt", "Msgs", "Branch", "Modified", "Resume"], rows));
    }
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

    const v = await api("/api/version");
    if (v.ok && v.data) {
      document.getElementById("version").textContent =
        "cwm " + v.data.version + " (" + v.data.os + "/" + v.data.arch + ", " + v.data.commit + ")";
    }
    activate(current());
  });
})();
