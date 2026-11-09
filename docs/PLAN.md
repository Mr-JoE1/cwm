# cwm — decision log

Dated entries, newest first. Public-safe: no private infrastructure or personal
details. Decisions are binding until superseded by a newer entry.

## 2026-11-09 (v0.4.1)

- **D22 — CI platform contract.** windows-latest runs build/vet/platform-logic
  tests; POSIX-stub behavioral tests skip on windows with an explicit message
  (coverage stays on ubuntu, incl. e2e); real-Windows junction/linker
  verification is an opt-in integration test (`CWM_WIN_INTEGRATION=1`) to run
  on a real Windows box, not CI.

## 2026-11-04 (v0.4)

- **D19 — session discovery: raw `projects/*/<uuid>.jsonl` at depth 1 is the
  source of truth.** `sessions-index.json` is enrichment only — a missing,
  stale or malformed index never hides a session; the raw scan still lists
  it. Subagents (`agent-*.jsonl`) and sidechains (`isSidechain` index
  entries) are excluded.
- **D20 — the resume path reuses the launcher passthrough.** `cwm sessions
  --pick [N]` resolves a row and launches `claude --resume <id>` through the
  existing `Open` path — no second launch path; clone-on-demand, name pinning
  and sync hints apply unchanged.
- **D21 — `cwm wip dirty` scans the repos root only.** Repos absent locally
  are clean by definition on that machine — the view answers "what should I
  push before switching devices", not "what is dirty anywhere".

## 2026-11-03 (v0.3)

- **D16 — deletions SYNC (normal semantics); the safety net is staggered
  versioning, 30 days** (`cleanInterval` 3600, `maxAge` 2592000). The v0.1
  bash-kit advice "ignore-delete ON" is withdrawn: with versioning active,
  cascading deletes are recoverable from `.stversions`, while ignore-delete
  causes worse failure modes (orphans accumulate on peers).
- **D17 — cwm never supervises the syncthing daemon.** It discovers a
  running instance (GUI+API read from `config.xml`); `cwm pair --start`
  spawns ONE detached `syncthing serve --no-browser` process when none is
  running — no supervision, no autostart. Users running syncthing as a
  service (the recommended setup) are untouched.
- **D18 — folder ID is the fixed string `cwm-workspace`**, identical on
  every device — it is the folder identity. The local PATH differs per
  device and that is fine.

## 2026-11-03

- **D12 — usage lens v0.2 = identity + login freshness only.** The account
  list shows who you are logged in as and how fresh that login is; live usage
  numbers are deferred — the underlying endpoint is undocumented and needs
  research, and cwm never fabricates data.
- **D13 — MCP = `workspace/mcp.json` + `--mcp-config` at launch.** cwm manages
  server entries in `workspace/mcp.json` (`cwm mcp add|list|remove`) and
  appends `--mcp-config` when launching; the account's `.claude.json` is
  never rewritten by cwm.
- **D14 — link modes symlink/junction/copy, probed at init** and stored in
  `config.toml` (`link_mode = auto|symlink|junction|copy`); launch-dir
  assembly uses the probed mode per item type.
- **D15 — hooks are `cwm hook` subcommands.** The generated `settings.json`
  invokes `<abs cwm binary> hook session-start|session-end`; the bash hooks
  remain for the legacy kit only.

## 2026-11-02 (second session)

- **D10 — cwm is all-in-one, not a ccam companion.** cwm absorbs every
  account-switcher feature (multiple logins, switching, per-account usage,
  import, shortcuts) as a *subset* of its workspace model. No dependency on
  ccam or any other tool; "use ccam alongside cwm" positioning is withdrawn.
  README rewritten accordingly.
- **D11 — workspace-first account architecture.** The workspace (sessions,
  memory, skills) is the primary object; accounts are pluggable logins
  attached to it. Layout: `~/.cwm/workspace/` (synced content) +
  `~/.cwm/accounts/<name>/` (credentials only, never synced by default) +
  `~/.cwm/launch/<account>/` (assembled at launch — workspace items +
  that account's credentials — and set as `CLAUDE_CONFIG_DIR`). All accounts
  attached to a workspace share it fully by default; isolation comes from
  creating separate workspaces, not from per-item sharing matrices.
  Open question for v0.2 implementation: Windows file-symlink constraint for
  shared single files (junctions cover dirs; dev-mode or copy-sync needed for
  `CLAUDE.md` / `history.jsonl` / `settings.json`).

## 2026-11-02

- **D1 — product name `cwm`** (Claude Workspace Manager), repo `Mr-JoE1/cwm`,
  released under the MIT license.
- **D2 — workspace dir stays `~/.claude-me` in v0.1** — stability over
  cosmetics; existing synced folders keep working. v0.2 makes the directory
  configurable, with migration from `~/.claude-me`.
- **D3 — session identity via `CLAUDE_CODE_PROJECT_DIR_NAME` pinning**, not
  OS-specific path slugs — this is the cross-OS core of cwm.
- **D4 — `repos.tsv` TSV manifest** (name, tab, origin URL) — no JSON
  dependency in bash; written by the SessionStart hook.
- **D5 — WIP handoff via `sync/<sanitized-hostname>` branches** — non-force
  pushes only, hooks always exit 0 so a failed push never kills a session.
- **D6 — sync engine is Syncthing P2P**; v0.3 automates pairing via its REST
  API. No cloud service, ever.
- **D7 — GUI is an embedded localhost web UI in a Go binary** (the Syncthing
  model), decided for v0.5. The CLI stays first-class at all times.
- **D8 — `cl` is kept as a legacy alias for `cwm`** for one major version,
  then it is deprecated.
- **D9 — doctor is local-only by default** (`--net` opts into the SSH probe)
  so it stays fast and testable offline.
