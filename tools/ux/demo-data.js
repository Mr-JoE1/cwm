// Demo HOME for cwm screenshots and UX audits: invented data only (capture
// mode; LEGACY=1 = a v0.7 home that needs migration; --fresh = the first-run
// wizard). Used by tools/ux/demo.sh.
// usage: node demo-data.js <HOME> <cwm-binary> [--fresh]
"use strict";
const fs = require("fs");
const path = require("path");
const cp = require("child_process");
const crypto = require("crypto");

const [home, cwm, mode] = process.argv.slice(2);
if (!home || !cwm) { console.error("usage: demo-data.js HOME CWM [--fresh]"); process.exit(2); }
fs.rmSync(home, { recursive: true, force: true });
fs.mkdirSync(home, { recursive: true });
const env = { HOME: home, PATH: "/usr/bin:/bin", TZ: "Europe/Berlin" };
if (mode === "--fresh") process.exit(0); // first-run wizard: nothing at all
cp.execFileSync(cwm, ["init"], { env, stdio: "ignore" });
const C = path.join(home, ".cwm");
const LEGACY = process.env.LEGACY === "1";
const ws = LEGACY ? path.join(C, "workspace") : path.join(home, ".claude");
const cfgTail = LEGACY ? "" : fs.readFileSync(path.join(C, "config.toml"), "utf8").split("\n").filter((l, i, a) => i >= a.findIndex((x) => x.startsWith("mode ="))).join("\n");

// --- accounts: one logged in with a time, one logged in with the time unknown
const acct = (n) => path.join(C, "accounts", n);
for (const n of ["work", "personal"]) fs.mkdirSync(acct(n), { recursive: true });
const credsAt = new Date(Date.now() - 2 * 86400e3 - 3 * 3600e3);
fs.writeFileSync(path.join(acct("work"), ".credentials.json"), "{}");
fs.utimesSync(path.join(acct("work"), ".credentials.json"), credsAt, credsAt);
fs.writeFileSync(path.join(acct("work"), ".claude.json"), JSON.stringify({ oauthAccount: { accountUuid: "demo-uuid-work", emailAddress: "dev@example.com" } }));
fs.writeFileSync(path.join(acct("personal"), ".claude.json"), JSON.stringify({ oauthAccount: { accountUuid: "demo-uuid-personal", emailAddress: "me@example.org" } }));
fs.writeFileSync(path.join(C, "config.toml"), `version = 1
default_account = "work"
link_mode = "auto"
${LEGACY ? "" : 'mode = "capture"'}

[[account]]
name = "work"
dir = "~/.cwm/accounts/work"
[account.login]
email = "dev@example.com"
plan = "max"
account_uuid = "demo-uuid-work"

[[account]]
name = "personal"
dir = "~/.cwm/accounts/personal"
[account.login]
email = "me@example.org"
plan = "pro"
account_uuid = "demo-uuid-personal"
` + (LEGACY ? "" : "\n" + cfgTail.replace(/^mode = .*\n/, "")));
if (!LEGACY) {
  fs.writeFileSync(path.join(ws, ".credentials.json"), JSON.stringify({ claudeAiOauth: { accessToken: "FAKE-demo-token" } }));
  fs.writeFileSync(path.join(home, ".claude.json"), JSON.stringify({ oauthAccount: { accountUuid: "demo-uuid-work", emailAddress: "dev@example.com" } }));
}

// --- projects and sessions
const PROJECTS = {
  "api-gateway": ["main", "feat/rate-limit-per-tenant", "fix/jwt-clock-skew"],
  "web-dashboard": ["main", "feat/dark-mode-tokens"],
  "infra-terraform": ["main", "chore/upgrade-provider-aws-5"],
  "mobile-app": ["develop", "feat/offline-sync-queue-with-a-really-long-branch-name"],
  "data-pipeline": ["main"],
  "docs-site": ["main"],
};
const PROMPTS = [
  ["api-gateway", "add per-tenant rate limiting to the token bucket middleware"],
  ["api-gateway", "why does the jwt validation fail when the clock is 30s off?"],
  ["web-dashboard", "refactor the theme tokens so light mode gets its own palette"],
  ["infra-terraform", "plan the aws provider 5.x upgrade and list breaking changes"],
  ["mobile-app", "design an offline queue for sync that survives app restarts"],
  ["data-pipeline", "the nightly job duplicates rows after a retry, find out why"],
  ["docs-site", "write a getting started page for the cli"],
  ["api-gateway", "add tracing spans around the upstream proxy calls"],
  ["web-dashboard", "make the sessions table keyboard navigable"],
  ["mobile-app", "fix the flaky login test on android emulators"],
  ["data-pipeline", "port the csv importer to streaming so it stops using 4 GB of memory"],
  ["infra-terraform", "split the network module into vpc and endpoints"],
  ["api-gateway", "review this diff for race conditions"],
  ["docs-site", "generate the changelog from the merged PRs since v2.3"],
  ["web-dashboard", "the heatmap tooltip is clipped at the right edge"],
  ["mobile-app", "add push notification opt-in screen"],
  ["api-gateway", "write table tests for the retry policy"],
  ["data-pipeline", "add a dead letter queue for malformed events"],
  ["web-dashboard", "explain why this useEffect runs twice"],
  ["infra-terraform", "set up remote state locking with dynamodb"],
  ["api-gateway", "bump go to 1.25 and fix the vet warnings"],
  ["mobile-app", "profile the startup time on low-end devices"],
  ["docs-site", "add search to the docs with a static index"],
  ["data-pipeline", "backfill last month after the schema change"],
  ["web-dashboard", "small: rename the WIP tab"],
  ["api-gateway", "draft the incident summary for tuesday's outage"],
];
const MODELS = ["claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-4-5"];
let seed = 7;
const rnd = () => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed / 0x7fffffff; };
const uuid = () => crypto.randomUUID();
const now = Date.now();
// session age in hours: today x5, yesterday x4, then spread over ~10 days, last few older
const AGES = [0.3, 1.2, 2.5, 4, 6.5, 26, 29, 31, 35, 50, 53, 74, 77, 98, 101, 122, 146, 170, 194, 218, 242, 380, 610, 900, 1400, 2300];
const index = {};
PROMPTS.forEach(([proj, prompt], i) => {
  const dir = path.join(ws, "projects", proj);
  fs.mkdirSync(dir, { recursive: true });
  const id = uuid();
  const branches = PROJECTS[proj];
  const branch = branches[i % branches.length];
  const owner = i % 4 === 3 ? "demo-uuid-personal" : "demo-uuid-work";
  const end = now - AGES[i] * 3600e3;
  const lines = [JSON.stringify({ type: "session-owner", accountUuid: owner })];
  let ts = end - 2 * 3600e3;
  const turns = 3 + Math.floor(rnd() * 20);
  // history: earlier days of the same long-lived session, to fill 12 months of stats
  const hist = [];
  if (i < 20) {
    for (let d = 0; d < 26; d++) hist.push(now - Math.floor(rnd() * 360) * 86400e3 - rnd() * 8 * 3600e3);
    hist.sort((a, b) => a - b);
  }
  const push = (t, first) => {
    const iso = new Date(t).toISOString();
    lines.push(JSON.stringify({ type: "user", uuid: uuid(), sessionId: id, timestamp: iso, gitBranch: branch, message: { role: "user", content: first ? prompt : "continue" } }));
    const inTok = 200 + Math.floor(rnd() * 3000);
    lines.push(JSON.stringify({ type: "assistant", uuid: uuid(), sessionId: id, timestamp: new Date(t + 20e3).toISOString(),
      message: { id: "msg_" + uuid().replace(/-/g, ""), model: MODELS[Math.floor(rnd() * (i % 3 === 0 ? 3 : 2))], role: "assistant", content: [{ type: "text", text: "ok" }],
        usage: { input_tokens: inTok, output_tokens: 300 + Math.floor(rnd() * 4000), cache_creation_input_tokens: Math.floor(rnd() * 20000), cache_read_input_tokens: Math.floor(rnd() * 120000) } } }));
  };
  let first = true;
  for (const t of hist) { push(t, first); first = false; }
  for (let k = 0; k < turns; k++) { push(ts, first); first = false; ts += (2 * 3600e3) / turns; }
  const file = path.join(dir, id + ".jsonl");
  fs.writeFileSync(file, lines.join("\n") + "\n");
  const m = new Date(end);
  fs.utimesSync(file, m, m);
  (index[proj] = index[proj] || []).push({ sessionId: id, firstPrompt: prompt, summary: "", messageCount: (hist.length + turns) * 2, gitBranch: branch, isSidechain: false });
});
for (const [proj, entries] of Object.entries(index)) {
  fs.writeFileSync(path.join(ws, "projects", proj, "sessions-index.json"), JSON.stringify({ version: 1, entries }));
}
// --- sessions that exercise the prompt and project labels (v0.9 B1/B2):
// content blocks instead of a string, a slash-command envelope, nothing but
// a tool result, a summary only; Windows (OneDrive) and WSL folder names.
const EXTRA = [
  ["c--Users-demo-OneDrive---Acme-Corp-2026-billing-svc", 0.6, [{ type: "text", text: "migrate the invoices table to the new schema" }], null, "C:\\Users\\demo\\OneDrive - Acme Corp 2026\\billing-svc"],
  ["--wsl-localhost-Ubuntu-home-demo-projects-edge-proxy", 3.1, "<command-message>workflow-authoring</command-message>\n<command-name>/workflow-authoring</command-name>\n<skill-format>true</skill-format>", null, "/home/demo/projects/edge-proxy"],
  ["-home-demo-src-cli-tools", 27, [{ type: "tool_result", tool_use_id: "toolu_demo", content: "ok" }], null, "/home/demo/src/cli-tools"],
  ["c--Users-demo-OneDrive---Acme-Corp-2026-billing-svc", 52, [{ type: "tool_result", tool_use_id: "toolu_demo2", content: "ok" }], "Fix the flaky CI job on macOS runners", "C:\\Users\\demo\\OneDrive - Acme Corp 2026\\billing-svc"],
];
// every transcript line records the cwd; lists show its basename (v0.9 polish)
for (const [proj, age, content, summary, cwd] of EXTRA) {
  const dir = path.join(ws, "projects", proj);
  fs.mkdirSync(dir, { recursive: true });
  const id = uuid();
  const t = now - age * 3600e3;
  const lines = [JSON.stringify({ type: "user", uuid: uuid(), sessionId: id, cwd, timestamp: new Date(t - 600e3).toISOString(), message: { role: "user", content } }),
    JSON.stringify({ type: "assistant", uuid: uuid(), sessionId: id, cwd, timestamp: new Date(t - 590e3).toISOString(),
      message: { id: "msg_" + uuid().replace(/-/g, ""), model: MODELS[0], role: "assistant", content: [{ type: "text", text: "ok" }], usage: { input_tokens: 900, output_tokens: 1200, cache_creation_input_tokens: 4000, cache_read_input_tokens: 30000 } } })];
  const file = path.join(dir, id + ".jsonl");
  fs.writeFileSync(file, lines.join("\n") + "\n");
  fs.utimesSync(file, new Date(t), new Date(t));
  if (summary) fs.writeFileSync(path.join(dir, "sessions-index.json"), JSON.stringify({ version: 1, entries: [{ sessionId: id, firstPrompt: "", summary, messageCount: 0, gitBranch: "", isSidechain: false }] }));
}
fs.writeFileSync(path.join(ws, "repos.tsv"), Object.keys(PROJECTS).map((p) => p + "\thttps://git.example.com/acme/" + p + ".git").join("\n") + "\n");

// --- handoff: two clones with work that is not pushed
const g = (cwd, ...a) => cp.execFileSync("git", a, { cwd, env: Object.assign({}, env, { GIT_AUTHOR_NAME: "demo", GIT_AUTHOR_EMAIL: "demo@example.com", GIT_COMMITTER_NAME: "demo", GIT_COMMITTER_EMAIL: "demo@example.com" }), stdio: "ignore" });
for (const [name, files, msg] of [["api-gateway", 3, "wip: tenant limits"], ["mobile-app", 1, "queue persistence"]]) {
  const r = path.join(home, "repos", name);
  fs.mkdirSync(r, { recursive: true });
  g(r, "init", "-q", "-b", "main");
  fs.writeFileSync(path.join(r, "README.md"), "# " + name + "\n");
  g(r, "add", ".");
  g(r, "commit", "-q", "-m", msg);
  for (let k = 0; k < files; k++) fs.writeFileSync(path.join(r, "change" + k + ".go"), "package x\n");
}
console.log("demo home ready: " + home);
