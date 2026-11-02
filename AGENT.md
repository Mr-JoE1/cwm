# AGENT.md

## Model

Pin: `zai/glm-5.2`

## Project Overview

`claude-workspace-kit` makes a Claude Code workspace private, local, account-independent,
and synced across Windows + Linux. It ships: bootstrap installers (sh + ps1), a `cl`
launcher that pins `CLAUDE_CODE_PROJECT_DIR_NAME` so both OSes share one session folder
and auto-clones GitHub repos on demand, SessionStart/SessionEnd hooks (repo manifest +
WIP auto-push to `sync/<hostname>` branches), settings templates, a Syncthing `.stignore`,
and a Linux end-to-end test with stubbed binaries.

`SPEC.md` in the repo root is the authoritative spec. Follow it exactly.

## Key Commands

- Build: none (plain scripts).
- Test: `bash tests/test.sh` — must pass, exit 0, all assertions green.
- Syntax: `bash -n <file>` for every .sh.

## Architecture

```
~/.claude-me/            CLAUDE_CONFIG_DIR — Syncthing-synced workspace
  projects/<name>/       sessions + memory (name pinned by launcher, OS-independent)
  repos.tsv              name<TAB>origin-url manifest (hook-written)
  settings.json          machine-specific (NOT synced; bootstrap-generated from tmpl)
~/repos/<name>/          canonical clone root (not synced; cloned on demand)
~/bin/cl (+ cl.ps1/cmd)  launcher installed by bootstrap
```

Data flow: SessionStart hook records repo → repos.tsv; `cl <name>` reads repos.tsv,
clones if missing, exports CLAUDE_CONFIG_DIR + CLAUDE_CODE_PROJECT_DIR_NAME, execs
`claude --resume`; SessionEnd hook pushes dirty trees to `sync/<hostname>` on origin.

## Code Style

- bash: `#!/usr/bin/env bash`, `set -euo pipefail` in non-interactive scripts,
  4-space indent, quote all variable expansions.
- PowerShell: 5.1-compatible, `Set-StrictMode -Version Latest` where safe, CRLF endings,
  UTF-8 no BOM, camelCase params.
- No jq / python / node dependencies anywhere.
- Every hook exits 0 unconditionally (errors → log file).
- Comments explain WHY, not what.

## Hard rules

- Never write outside the repo except what tests/test.sh does inside its mktemp sandbox.
- Do not touch ~/.claude or ~/.claude-me on this machine (tests use sandbox HOME).
- tests must not require network or a real `claude` binary — stub everything.
