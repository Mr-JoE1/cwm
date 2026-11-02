# cwm — Claude Workspace Manager

The all-in-one toolkit for your Claude Code setup: **accounts, workspace and devices
in one tool.** Sessions, memory, skills and MCP config in a private workspace you own —
switch Claude accounts freely, sync across your machines, resume anywhere.

> One tool: your accounts, your workspace, your devices.

## Why cwm

- **All-in-one** — everything account switchers do (multiple logins, switching,
  per-account usage, import) *plus* the layer they all lack: a portable, account-independent
  workspace with cross-device sync. No second tool needed.
- **Workspace-first, not account-first** — account switchers make *accounts* the primary
  object and bolt sharing on top. cwm inverts it: your **workspace** (sessions, memory,
  skills) is the primary object, and accounts are pluggable logins attached to it.
  Switch, share or rotate accounts — the workspace never notices.
- **Privacy, local-first** — your workspace lives in your own folder, not the default
  shared `~/.claude`. Nothing is uploaded anywhere; sync is peer-to-peer.
- **Cross-OS sync** — Linux and Windows read and write the *same* session folders via
  Syncthing P2P. Resume on the other machine exactly where you left off.
- **Repos on demand** — every repo you open is remembered in a small manifest; on another
  device `cwm <repo>` clones it from GitHub and drops you into the resume picker.

## The gap

| Need | ccam & other switchers | dotsync + ccsync + session-roam | raw Syncthing | **cwm** |
|---|---|---|---|---|
| Switch Claude accounts | yes | no | no | **yes** — accounts attached to your workspace |
| Same workspace on every account | partial (per-item sharing) | no | no | **yes** — workspace-first |
| Workspace private + relocatable | no | partial (dotfiles only) | no | **yes** |
| Sync across devices | no | partial (mac-first, path-fragile) | manual | **yes** (P2P, automated) |
| Same session folder on every OS | no | partial (path rewriting) | no — slugs differ per OS | **yes** (name pinning) |
| Repo clone-on-demand + resume | no | partial | no | **yes** (`repos.tsv` + `cwm <repo>`) |
| WIP handoff between devices | no | manual branches | no | **yes** (`sync/<host>` auto-push) |
| GUI for non-terminal users | no | no | web (sync only) | **yes** (planned v0.5) |
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

Coming with the account system (v0.2+):

```sh
cwm account add work        # attach a login to your workspace
cwm account list            # per-account usage, plan, status
cwm --account work myproject  # launch a repo under a specific account
cwm import                  # migrate from ~/.claude — or from ccam profiles
```

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

### The account model (v0.2 target)

```
~/.cwm/
├── workspace/          # your content: projects/, skills/, memory, CLAUDE.md
│                       #   → synced P2P between your devices
├── accounts/<name>/    # logins only: .credentials.json + .claude.json
│                       #   → per machine, never synced by default
└── launch/<account>/   # assembled at launch: workspace items + that
                        #   account's credentials → set as CLAUDE_CONFIG_DIR
```

Every account attached to a workspace sees the *same* sessions, memory and
skills — that is the point of account independence. Credentials stay separate
per account, exactly as Claude Code expects. Multiple workspaces (e.g. `work`
vs `personal`) are supported by creating more than one.

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
- **v0.2** — Go core: single binary, configurable workspace dir, **account system**
  (add/switch/usage, launch-dir assembly), migration from `~/.claude` and ccam profiles
- **v0.3** — Syncthing pairing automated via its REST API
- **v0.4** — session browser + WIP UX (dirty-repo view, pull hints)
- **v0.5** — embedded localhost web UI (wizard, dashboard, sessions, accounts); CLI stays first-class
- **v1.0** — stable CLI + GUI, complete docs, tagged long-term support

Decisions and their rationale: [docs/PLAN.md](docs/PLAN.md).

## Relationship to ccam

cwm is **not built on and does not depend on ccam** — it is an independent,
all-in-one toolkit. It borrows engineering patterns ccam proved (plan → backup →
mutate, atomic writes, doctor, fake-binary E2E tests) and covers everything
ccam does — multiple accounts, switching, per-account usage, import — as a
subset of its workspace model. ccam users can move over with `cwm import`
(v0.2). Credit where due: ccam's account-switching design is excellent; cwm
started from the question "what if the workspace, not the account, came first?"

> **Terms note:** whether using more than one Claude account is allowed depends
> on Anthropic's terms for each account. cwm never merges or hides account
> limits. Your call, your accounts.

## License

MIT — see [LICENSE](LICENSE). cwm is an unofficial community tool and is not
affiliated with or endorsed by Anthropic.
