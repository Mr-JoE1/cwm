# cwm — Claude Workspace Manager

Carry your Claude Code workspace with you: sessions, memory, skills and MCP
config in one private folder — synced across your machines, decoupled from any
login. **Your workspace stays yours: local, portable, account-independent.**

> **ccam switches accounts; cwm carries the workspace.**

## Why cwm

- **Privacy, local-first** — sessions, memory, skills and MCP config live in
  your own `~/.claude-me` folder, not the default shared `~/.claude`. Nothing
  is uploaded anywhere; sync is peer-to-peer.
- **Account-independent** — sessions are keyed to the workspace folder + a
  pinned project name, not to the login. Switch, share or rotate Claude
  accounts without touching your workspace.
- **Cross-OS sync** — Linux and Windows read and write the *same* session
  folders via Syncthing P2P. Resume on the other machine exactly where you
  left off.
- **Repos on demand** — every repo you open is remembered in a small manifest;
  on another device `cwm <repo>` clones it and drops you into the resume
  picker.

## The gap

| Need | ccam | dotsync + ccsync + session-roam | raw Syncthing | **cwm** |
|---|---|---|---|---|
| Switch Claude accounts | yes | no | no | n/a (works alongside) |
| Workspace private + relocatable | no | partial (dotfiles only) | no | **yes** (`CLAUDE_CONFIG_DIR`) |
| Same session folder on every OS | no | partial (path rewriting) | no — slugs differ per OS | **yes** (name pinning) |
| Repo clone-on-demand + resume | no | partial | no | **yes** (`repos.tsv` + `cwm <repo>`) |
| WIP handoff between devices | no | manual branches | no | **yes** (`sync/<host>` auto-push) |
| Cloud service required | no | mixed | no | **no — P2P only** |

## Install

```sh
git clone git@github.com:Mr-JoE1/cwm.git ~/cwm && cd ~/cwm
./bootstrap.sh          # idempotent — safe to re-run
exec bash               # pick up new PATH + CLAUDE_CONFIG_DIR
```

Windows (PowerShell 5.1):

```powershell
git clone git@github.com:Mr-JoE1/cwm.git $env:USERPROFILE\cwm
cd $env:USERPROFILE\cwm
powershell -ExecutionPolicy Bypass -File .\bootstrap.ps1
# open a NEW terminal so PATH + CLAUDE_CONFIG_DIR take effect
```

**Prerequisites:** Claude Code ≥ 2.1.234, git, an SSH key with access to your
GitHub repos. [Syncthing](https://syncthing.net) on every device for sync.
Bootstrap on both machines, then pair Syncthing on the `~/.claude-me` /
`%USERPROFILE%\.claude-me` folder.

## Quick start

```sh
cwm                    # list workspace projects: session counts + last-modified
cwm myproject          # clone from GitHub (SSH) if missing, then resume picker
cwm myproject new      # fresh session in that repo
cwm myproject last     # continue the latest session
cwm --wip              # push dirty work to sync/<hostname> right now
cwm doctor             # local health checks (--net adds an SSH probe)
```

The full first-run flow: run `bootstrap` → pair Syncthing → `cwm <repo>`.
Everything else (clone-on-demand, session resume, WIP branches) is automatic.

## How it works

```
+--------------+                        +----------------+
|   Linux      |   Syncthing P2P        |   Windows      |
| ~/.claude-me/|<---------------------->| %USERPROFILE%\ |
|  projects/   |   (no cloud, ever)     |  .claude-me\   |
|  skills/     |                        |  projects\     |
|  repos.tsv   |                        |  repos.tsv     |
+------+-------+                        +--------+-------+
       |                                         |
       |  cwm <repo> pins                        |
       |  CLAUDE_CODE_PROJECT_DIR_NAME=<repo>     |
+------v-----------------------+     +-----------v-----------------+
| ~/repos/<repo>               |<--->| ~\repos\<repo>              |
| cloned on demand from GitHub | SSH | cloned on demand from GitHub |
+------------------------------+     +-----------------------------+
```

Claude Code names session folders after the *absolute path* of the project,
slugified per OS — so the same repo gets `projects/-home-me-repos-alpha/` on
Linux and `projects/C--Users-me-repos-alpha/` on Windows, and cross-device
resume silently breaks. cwm pins `CLAUDE_CODE_PROJECT_DIR_NAME=<repo>` (Claude
Code ≥ 2.1.234), so every OS writes the same `projects/<repo>/` folder — no
path rewriting, ever.

## What syncs / what stays local

| Synced (your workspace) | Local (per machine) |
|---|---|
| `projects/` — sessions + memory | `settings.json` (absolute hook paths) |
| `skills/`, `agents/`, `commands/`, `rules/`, `plugins/` | `logs/` |
| `scheduled-tasks/`, `history.jsonl` | caches, `shell-snapshots/`, `todos/` |
| `repos.tsv`, `.claude.json`, `.credentials.json` | `debug/`, `statsig/`, OS junk |

The split is enforced by the `.stignore` shipped in the repo and installed by
bootstrap.

## Roadmap

- **v0.1** — cwm rebrand, `cwm doctor`, MIT license, product README *(this release)*
- **v0.2** — configurable workspace dir + migration from `~/.claude-me`
- **v0.3** — Syncthing pairing automated via its REST API
- **v0.4** — session hygiene: merge/dedup legacy path-slug folders
- **v0.5** — embedded localhost web UI (Go binary, Syncthing model); CLI stays first-class
- **v1.0** — stable CLI + GUI, complete docs, tagged long-term support

Decisions and their rationale: [docs/PLAN.md](docs/PLAN.md).

## ccam and cwm

They complement each other: **ccam switches accounts; cwm carries the
workspace.** cwm makes the workspace independent of whichever account is
active, so the two tools compose cleanly. Interop documentation is planned for
a later release.

## License

MIT — see [LICENSE](LICENSE). cwm is an unofficial community tool and is not
affiliated with or endorsed by Anthropic.
