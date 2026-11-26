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

- Build: `go build ./...` (and `GOOS=windows go build ./...` before commits)
- Test: `go test ./...` — must pass, exit 0
- E2E: `go test -tags e2e ./test/` (fakeclaude + local git only, no network)
- Lint: `gofmt -l .` empty, `go vet ./...` clean
- Legacy bash kit: `bash tests/test.sh` must stay green (untouched files)

## Go rules

- Module `github.com/Mr-JoE1/cwm`, Go ≥1.25; only approved dep: BurntSushi/toml
- `SPEC-V0.2.md` is authoritative for v0.2; stage markers [A]/[B]/[C]
- Library code: no panics, wrapped errors (`%w`), testable seams (e.g. Linker interface)
- Unit/e2e tests never touch network or real `claude` (fakeclaude stub)

## Architecture

```
~/.claude-me/            CLAUDE_CONFIG_DIR — Syncthing-synced workspace
  projects/<name>/       sessions + memory (name pinned by launcher, OS-independent)
  repos.tsv              name<TAB>origin-url manifest (hook-written)
  settings.json          machine-specific (NOT synced; bootstrap-generated from tmpl)
~/repos/<name>/          canonical clone root (not synced; cloned on demand)
~/bin/cwm (+ cl alias)   launcher installed by bootstrap; `cwm doctor` health checks
```

Data flow: SessionStart hook records repo → repos.tsv; `cwm <name>` reads repos.tsv,
clones if missing, exports CLAUDE_CONFIG_DIR + CLAUDE_CODE_PROJECT_DIR_NAME, execs
`claude --resume`; SessionEnd hook pushes dirty trees to `sync/<hostname>` on origin.

`bin/cwm` is the canonical launcher; `bin/cl` is a legacy-alias wrapper. `bin/doctor.sh` /
`bin/doctor.ps1` implement `cwm doctor` (local checks only; `--net` adds SSH probe).

## Code Style

- bash: `#!/usr/bin/env bash`, `set -euo pipefail` in non-interactive scripts,
  4-space indent, quote all variable expansions.
- PowerShell: 5.1-compatible, `Set-StrictMode -Version Latest` where safe, CRLF endings,
  UTF-8 no BOM, camelCase params.
- No jq / python / node dependencies anywhere (dev-only exception: `tools/ux/`, Node ≥ 22, never shipped).
- Every hook exits 0 unconditionally (errors → log file).
- Comments explain WHY, not what.

## Hard rules

- Never write outside the repo except what tests/test.sh does inside its mktemp sandbox.
- Do not touch ~/.claude or ~/.claude-me on this machine (tests use sandbox HOME).
- tests must not require network or a real `claude` binary — stub everything.

## Web dashboard (internal/gui/web)

- Vanilla JS, one CSS file, the `el()` helper; no framework, no build step.
- No inline scripts or styles, no HTML-parsing sinks, no external hosts (CSP).
- Every colour is a token in both themes; `node scripts/contrast-check.js` must pass.
- Budget (D106): code (html + css + js + svg) ≤ 260 KB raw and ≤ 75 KB gzip;
  self-hosted fonts ≤ 60 KB total. `go test ./internal/gui/` enforces it.
- UX checks: `tools/ux/demo.sh` + `node tools/ux/capture.mjs <out> --compare <before>/audit.json`
  (demo data only; never capture a real workspace).
