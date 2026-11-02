# cwm v0.1 — Brand & Foundation Spec

Rebrand the working kit into **cwm — Claude Workspace Manager** and add the first
product surface: `cwm doctor`. Ship as tag `v0.1.0`.

Positioning (public): *"ccam switches accounts; cwm carries the workspace."* cwm makes a
Claude Code workspace — sessions, memory, skills, MCP config — private, account-independent,
and synced across devices (Windows + Linux first). Privacy framing: "your workspace stays
yours — local, portable, decoupled from any login." Do NOT mention personal details, shared
accounts with specific people, or any private infrastructure anywhere in public files.

## Deliverables

| File | Action |
|---|---|
| `LICENSE` | NEW — MIT, `Copyright (c) 2026 Maher` |
| `README.md` | REWRITE — product README (below) |
| `docs/PLAN.md` | NEW — decision log (below) |
| `SPEC-V0.1.md` | this file |
| `bin/cwm` | NEW — canonical launcher (current `bin/cl` logic, rebranded) |
| `bin/cl` | REPLACE — thin legacy-alias wrapper: `exec "$(dirname "$0")/cwm" "$@"` |
| `bin/doctor.sh` | NEW — local health checks (below) |
| `bin/cwm.ps1` | NEW — canonical Windows launcher (current `bin/cl.ps1` logic, rebranded) |
| `bin/cwm.cmd` | NEW — cmd shim → `cwm.ps1` (like current cl.cmd) |
| `bin/cl.ps1`, `bin/cl.cmd` | REPLACE — legacy-alias wrappers calling cwm.ps1 |
| `bin/doctor.ps1` | NEW — Windows port of doctor.sh |
| `bootstrap.sh`, `bootstrap.ps1` | UPDATE — install `cwm`, `cl` (alias), `cwm-doctor`; rebranded output text |
| `tests/test.sh` | UPDATE — keep all existing assertions (via `bin/cl` wrapper), add new (below) |
| `AGENT.md` | UPDATE — file map + new rules |

Keep: `hooks/`, `templates/`, `SPEC.md` (historical v0 spec) unchanged.

## Launcher changes (`bin/cwm`)

- All user-facing text says `cwm`; usage block adds: "`cl` is a legacy alias for `cwm`."
- New subcommand intercepted before pass-through: `cwm doctor [--net]` → runs the doctor
  script installed next to the launcher (`$HOME/bin/cwm-doctor` on Linux; Windows: doctor
  invoked from the same dir as the installed `cwm.ps1`). Unknown/missing doctor script →
  clear error, exit 1.
- Everything else identical to current `bin/cl` behavior (env guard, list, clone-on-demand,
  pin, modes new/last, `--wip`, pass-through). Workspace dir stays `~/.claude-me` (decision
  logged in PLAN.md; v0.2 will make it configurable).

## `cwm doctor` checks (local only; `--net` adds SSH probe)

Output lines prefixed `ok:` / `warn:` / `FAIL:`. Exit 1 only on FAIL, else 0.

1. workspace dir exists (`~/.claude-me`) — FAIL if missing
2. `settings.json` present, valid JSON (python-free check: grep for balanced braces is NOT
   enough — use `grep -q` for `"hooks"` and `"SessionStart"` keys), hooks entries reference
   existing executable files — FAIL if hooks wired but missing
3. hook scripts present in `~/.claude-me/hooks/` — warn if absent
4. `.stignore` present — warn if missing (sync not configured)
5. `claude` on PATH — warn if missing; version < 2.1.234 → warn (pinning unsupported)
6. `CLAUDE_CONFIG_DIR` exported in `~/.bashrc` or `~/.profile` (Linux) / user env var
   (Windows) — warn if not
7. `repos.tsv` readable, every line matches `^[^\t]+\t[^\t]+$` — warn on malformed lines
8. session-folder hygiene: any dir under `projects/` starting with `-` (path-encoded slug
   from unpinned `claude` launches) → warn listing them ("resume via cwm to keep sessions
   cross-OS; these were created by plain claude")
9. WIP log: last 3 lines of `logs/wip.log` shown if present
10. `--net` only: `ssh -o BatchMode=yes -o ConnectTimeout=5 -T git@github.com` —
    ok/warn on "successfully authenticated" / anything else. Never in default mode.
11. End with summary line: `doctor: N ok, M warn, K fail`.

Windows `doctor.ps1`: same checks (6 = `[Environment]::GetEnvironmentVariable('CLAUDE_CONFIG_DIR','User')`).

## README.md outline (public-safe)

1. Title + one-liner + positioning line (ccam vs cwm)
2. Why: 3-4 bullets (privacy/local-first, account-independent, cross-OS sync, repo-on-demand)
3. The gap table: ccam / dotsync+ccsync+session-roam / raw Syncthing / **cwm**
4. Install (clone + bootstrap.sh / bootstrap.ps1) + Prereqs (Claude Code ≥ 2.1.234, git, SSH key; Syncthing for sync)
5. Quick start: `cwm init-flow` = bootstrap → pair Syncthing → `cwm <repo>` (clone+resume); `cwm <repo> new|last`; `cwm --wip`; `cwm doctor`
6. How it works: ASCII architecture diagram (workspace folder ↔ Syncthing ↔ devices; repos on GitHub; launcher pins project name) + the path-slug problem in 3 sentences
7. What syncs / what stays local (table)
8. Roadmap summary (v0.1 … v1.0, one line each) + link to docs/PLAN.md
9. Comparison note: complements ccam (account switching) — interop documented later
10. MIT license; "unofficial, not affiliated with Anthropic"

## docs/PLAN.md (decision log, public-safe)

Dated entries, newest first. Seed with:
- 2026-11-02 D1: product name `cwm`, repo Mr-JoE1/cwm, MIT
- D2: workspace dir stays `~/.claude-me` in v0.1 (stability over cosmetics); v0.2 makes it configurable with migration
- D3: session identity via `CLAUDE_CODE_PROJECT_DIR_NAME` pinning (not path slugs) — the cross-OS core
- D4: `repos.tsv` TSV manifest (no JSON dep in bash); hook-written
- D5: WIP handoff via `sync/<sanitized-hostname>` branches, non-force, always-exit-0 hooks
- D6: sync engine = Syncthing P2P (v0.3 automates via REST); no cloud service ever
- D7: GUI = embedded localhost web UI in Go binary (Syncthing model), decided for v0.5; CLI-first always
- D8: `cl` kept as legacy alias one major version, then deprecated
- D9: doctor is local-only by default (`--net` opt-in) so it stays fast + offline-testable

## Test additions (tests/test.sh, all offline)

- `bin/cl` (wrapper) still passes the existing clone/resume assertions
- `bin/cwm` no-args lists projects
- `bin/cwm alpha` → clone-on-demand + stub capture shows `--resume` + pin env
- `cwm doctor` with missing workspace → exit 1, FAIL line
- after sandbox bootstrap: `bin/cwm doctor` → exit 0, `ok:` summary, no FAIL
- doctor warns: create `projects/-Users-x-legacy/` with one `.jsonl` → doctor output mentions it
- doctor warns on old version: stub `claude --version` → `2.1.100` → warn line about 2.1.234
- doctor never touches network in default mode (no ssh invocation — assert via stubbed `ssh` on PATH that FAILS the test if called)

Constraints: same as AGENT.md (POSIX-ish bash, PS 5.1 + CRLF, no jq/python/network, hooks
always exit 0). Version-stub technique: test stub `claude` prints old version when called
with `--version`, else captures args.
