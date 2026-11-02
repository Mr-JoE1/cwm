# cwm — decision log

Dated entries, newest first. Public-safe: no private infrastructure or personal
details. Decisions are binding until superseded by a newer entry.

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
