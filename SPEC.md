# claude-workspace-kit — Specification

Private, local, cross-device Claude Code workspace for a user who shares a Claude Max
account with two friends. Workspace = sessions, MCP connectors, custom skills, memory,
scheduled tasks. Projects live on GitHub (SSH keys). Two device classes: **Windows + Linux**.

## Goals

1. **Privacy**: all sessions/context/skills/MCP config live in a private config dir
   (`CLAUDE_CONFIG_DIR=~/.claude-me`), never in the shared default `~/.claude`.
2. **Account independence**: sessions are keyed to the config dir + project name, not the
   login. Switching Claude accounts must not affect the workspace.
3. **Cross-OS sync (Windows + Linux)**: via Syncthing P2P over the `~/.claude-me` folder.
4. **Plug and play on fresh OS**: bootstrap + Syncthing pairing → full workspace restored.
5. **Repo-on-demand resume**: continuing a session on another device auto-clones the
   project from GitHub (SSH) if missing locally, then resumes the session.
6. **WIP auto-push**: on session end, dirty repos are committed to `sync/<hostname>`
   branches and pushed, so the other device can pick up in-flight work.

## Core mechanism (verified against Claude Code docs)

- `CLAUDE_CONFIG_DIR` relocates EVERYTHING (sessions, memory, skills, MCP, credentials,
  scheduled tasks) into one folder. Officially supported.
- `CLAUDE_CODE_PROJECT_DIR_NAME` (requires Claude Code >= 2.1.234) pins the session
  folder name, overriding the OS-specific path slug. This makes Windows and Linux store
  sessions under the SAME `projects/<name>/` dir — no path rewriting needed.
  Must be exported in the environment `claude` is started from (the `cl` launcher does it).
- Sessions resume via `claude --resume` (picker) or `--continue` (latest) from the repo dir.
- Transcripts are append-only JSONL; one writer at a time (exit before resuming elsewhere).

## Deliverables

```
claude-workspace-kit/
├── README.md                  # architecture, setup, daily use, troubleshooting
├── bootstrap.sh               # Linux installer (idempotent)
├── bootstrap.ps1              # Windows installer (idempotent)
├── bin/cl                     # bash launcher
├── bin/cl.ps1 + bin/cl.cmd    # Windows launcher (cmd → ps1)
├── hooks/session-start.sh     # appends to repos.tsv
├── hooks/session-start.ps1
├── hooks/session-end.sh       # WIP auto-commit + push to sync/<hostname>
├── hooks/session-end.ps1
├── templates/settings.linux.json.tmpl    # {{HOME}} placeholder
├── templates/settings.windows.json.tmpl
├── templates/stignore.tmpl    # → ~/.claude-me/.stignore
└── tests/test.sh              # Linux end-to-end test with stubbed `claude`
```

## Component behavior

### `cl` launcher (bash; `cl.ps1` mirrors it)

- `CLAUDE_CONFIG_DIR` = `$HOME/.claude-me` (exported, hard fails with clear message if
  the dir is missing → tells user to run bootstrap or pair Syncthing first).
- `cl` (no args): list workspace projects (dirs under `.claude-me/projects/`) with session
  counts + last-modified, and print usage.
- `cl <name>`: 
  1. `~/repos/<name>` missing → look up remote URL in `~/.claude-me/repos.tsv`
     (tab-separated: `name<TAB>url`), `git clone <url> ~/repos/<name>` (SSH).
     If unknown name → error listing known names.
  2. `cd ~/repos/<name>`, export `CLAUDE_CODE_PROJECT_DIR_NAME=<name>`, exec `claude --resume`
     (native picker).
- `cl <name> new`: same but exec `claude` (fresh session).
- `cl <name> last`: exec `claude --continue`.
- `cl --wip`: run the WIP push logic in the current directory (same as session-end hook).
- Any other args (`cl --resume <id>` etc.): pass through to `claude` with the env set
  ONLY when cwd is inside `~/repos` (pin = basename of the repo dir); otherwise plain
  pass-through with just CLAUDE_CONFIG_DIR.
- After clone/resume setup, if the repo has a remote branch `sync/<other-hostname>` that
  contains commits not on the current branch, print a one-line hint:
  `git pull origin sync/<hostname>` (do NOT auto-merge).

### SessionStart hook

- If cwd is a git repo with an `origin` remote: append `basename(cwd)<TAB>origin-push-url`
  to `$CLAUDE_CONFIG_DIR/repos.tsv`, dedupe (sort -u style). Never fails the session:
  all errors swallowed to a log line.

### SessionEnd hook (WIP auto-push)

- Preconditions: cwd is a git repo; has origin remote; tree is dirty or has untracked files.
- `git add -A`
- `git -c user.name="claude-sync" -c user.email="claude-sync@localhost" commit -m "wip: <hostname> <iso-date>" --no-verify` (the -c flags only apply if no user config — actually they always apply as overrides; keep them so commits never fail on unconfigured boxes)
- `git push origin HEAD:refs/heads/sync/<hostname>` (non-force; on failure log and exit 0)
- Log everything to `$CLAUDE_CONFIG_DIR/logs/wip.log`. Hook ALWAYS exits 0.
- `<hostname>` = short hostname, sanitized `[A-Za-z0-9-]`.

### settings.json templates

- Generated at bootstrap from `templates/settings.*.json.tmpl` with `{{HOME}}` replaced by
  the ABSOLUTE home path (hooks need absolute paths; Windows won't expand env vars in
  hook commands reliably).
- Content: `env` block with `DISABLE_TELEMETRY=1`, `DISABLE_ERROR_REPORTING=1`,
  `DISABLE_BUG_COMMAND=1`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`;
  `hooks.SessionStart` and `hooks.SessionEnd` entries (correct shape: array of
  `{matcher, hooks:[{type:"command", command}]}` — SessionStart/End take no matcher or "");
  include a minimal `permissions.allow` for git read commands used by hooks.
- **settings.json is machine-specific → excluded from sync** (in .stignore).

### .stignore template

Sync (implicitly, not listed): `.claude.json`, `.credentials.json`, `CLAUDE.md`,
`projects/`, `skills/`, `agents/`, `commands/`, `rules/`, `plugins/`, `history.jsonl`,
`scheduled-tasks/`, `scheduled_tasks.json`, `repos.tsv`, `.stignore`.
Exclude:
```
(?d)cache
(?d)debug
(?d)telemetry
(?d)statsig
(?d)shell-snapshots
(?d)session-env
(?d)paste-cache
(?d)file-history
(?d)backups
(?d)downloads
(?d)ide
(?d)chrome
(?d)todos
(?d)tasks
(?d)sessions
(?d)worktrees
(?d)logs
settings.json
settings.local.json
.DS_Store
Thumbs.db
```

### bootstrap.sh (Linux) / bootstrap.ps1 (Windows)

Idempotent, safe to re-run. Steps:
1. Check `claude` on PATH; parse version; warn (do not abort) if < 2.1.234.
2. `mkdir -p ~/.claude-me/{hooks,logs} ~/repos ~/bin`.
3. Generate `~/.claude-me/settings.json` from the OS template ONLY if not present
   (never clobber; print diff hint if present).
4. Copy hooks → `~/.claude-me/hooks/` (overwrite: they're ours).
5. Copy `.stignore` template → `~/.claude-me/.stignore` (overwrite).
6. Install launcher: `bin/cl` → `~/bin/cl` (chmod +x); Windows: `cl.ps1`+`cl.cmd` →
   `%USERPROFILE%\bin\`.
7. Persist `CLAUDE_CONFIG_DIR`:
   - Linux: append `export CLAUDE_CONFIG_DIR="$HOME/.claude-me"` to `~/.bashrc` and
     `~/.profile` (guarded, no duplicates). Also ensure `~/bin` on PATH the same way.
   - Windows: `[Environment]::SetEnvironmentVariable('CLAUDE_CONFIG_DIR', "<abs>", 'User')`
     and add `%USERPROFILE%\bin` to the User PATH if missing.
8. Print next steps: Syncthing pairing instructions + `cl` usage examples.

### tests/test.sh (Linux, runs on this box)

Uses a sandbox HOME (`/tmp/cw-test-home`... actually use `$(mktemp -d)`) and STUBS:
- Stub `claude` on PATH: prints its args + relevant env (`CLAUDE_CONFIG_DIR`,
  `CLAUDE_CODE_PROJECT_DIR_NAME`) to a capture file, exits 0.
- Run `bootstrap.sh` with HOME=sandbox → assert: `.claude-me` exists, settings.json
  contains the sandbox absolute path, hooks + cl installed, bashrc has the export.
- Create a local git repo `alpha` + a BARE repo as its origin (file:// style path).
  Make it dirty, run `hooks/session-start.sh` with cwd=alpha → assert repos.tsv entry.
- Run `hooks/session-end.sh` → assert bare repo now has branch `sync/<this-hostname>`
  containing the dirty file.
- Run `bin/cl alpha` (with stub claude) → assert: repo cloned into sandbox `~/repos/alpha`
  (delete it first to prove clone-on-demand), capture file shows `--resume` +
  `CLAUDE_CODE_PROJECT_DIR_NAME=alpha`.
- Run `bin/cl` (no args) → lists alpha.
- Run `bin/cl unknown` → non-zero exit + helpful message.
- Print PASS/FAIL summary; exit non-zero on any failure.

## Constraints

- bash scripts: POSIX-ish bash, `#!/usr/bin/env bash`, `set -euo pipefail` where it
  doesn't break interactive sourcing.
- No dependencies beyond git + coreutils (+ PowerShell on Windows). NO jq, NO python.
- Windows scripts: PowerShell 5.1 compatible (no PS7-only syntax), CRLF line endings
  for .ps1/.cmd, UTF-8 without BOM.
- All hooks exit 0 always; errors → log file only.
- Never touch the default `~/.claude` anywhere.
- README documents: the shared-account privacy model, "always launch via cl" (plain
  `claude` still lands in .claude-me but without the pinned project name → fragmentation),
  one-writer rule (exit session before resuming on the other device), .claude.json sync
  conflict trade-off, trust prompts re-appearing per machine, and the mixed-OS notes.
