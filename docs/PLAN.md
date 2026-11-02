# cwm — decision log

Dated entries, newest first. Public-safe: no private infrastructure or personal
details. Decisions are binding until superseded by a newer entry.

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
