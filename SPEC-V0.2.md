# cwm v0.2 — Go Core + Accounts Spec

Single Go binary implementing the all-in-one toolkit (D10/D11). Module
`github.com/Mr-JoE1/cwm`, Go ≥1.25, one third-party dep allowed: `github.com/BurntSushi/toml`.
The legacy bash kit (bin/, hooks/, templates/, bootstrap.*, tests/test.sh) stays in-tree,
untouched and passing — fallback for one more release, removed in v1.0.

Build in three stages; each ends with `go build ./... && go vet ./... && go test ./...`
green + a commit. Stages are marked **[A]**, **[B]**, **[C]** below. `gofmt -l .` must be empty.

## Layout

```
go.mod, go.sum
cmd/cwm/main.go            thin: os.Exit(cwm.Run(os.Args[1:], os.Stdout, os.Stderr))
internal/cli/              arg parsing, subcommand dispatch, help text
internal/config/           config.toml load/save (atomic), account CRUD  [A]
internal/storage/          layout map, claude resolution, slug logic      [A]
internal/manifest/         repos.tsv read/write/dedupe                   [A]
internal/launcher/         open/list/reserved-names/exec                 [B]
internal/wip/              session-start/end hook logic                  [B]
internal/doctor/           port of bin/doctor.sh (11 checks, --net)      [B]
internal/accounts/         add/list/use/remove, identity read, assembly  [C]
internal/workspace/        init, migrate ~/.claude-me, import, mcp       [C]
testdata/fakeclaude/       fakeclaude stub (shell script, POSIX sh)      [B]
test/                      e2e_test.go (build tag e2e: `go test -tags e2e ./test/`)
.github/workflows/         ci.yml, release.yml                           [C]
.goreleaser.yaml                                                        [C]
docs/PLAN.md               + D12–D15                                     [C]
README.md                  install/quick-start rewrite for Go binary     [C]
```

## Home layout (D11 — implement exactly)

```
$CWM_HOME (default ~/.cwm; env-overridable; config.toml key home_dir NOT allowed — env only)
├── config.toml
├── workspace/            # synced content (items below)
│   ├── projects/ skills/ agents/ commands/ rules/ plugins/ output-styles/
│   ├── scheduled-tasks/  CLAUDE.md  history.jsonl  repos.tsv  mcp.json
│   └── .stignore
├── accounts/<name>/      # login only: .credentials.json, .claude.json
├── launch/<account>/     # ASSEMBLED per launch → becomes CLAUDE_CONFIG_DIR
└── backups/<ts>/         # pre-mutation backups
```

Workspace item list (canonical, code constant): `projects, skills, agents, commands,
rules, plugins, output-styles, scheduled-tasks` (dirs) + `CLAUDE.md, history.jsonl,
repos.tsv` (files). `mcp.json` is managed but NOT linked into launch dirs (see MCP).
Account items: `.credentials.json`, `.claude.json` — never shared, never synced.

## config.toml [A]

```toml
version = 1
default_account = "main"
link_mode = "auto"            # auto|symlink|junction|copy — probed at init, D14
[[account]]
name = "main"
dir  = "~/.cwm/accounts/main" # ~ expanded
```
- Parse with BurntSushi/toml; save via temp file + fsync + rename in the SAME dir.
- `~` expansion helper (shared in storage pkg). Unknown keys tolerated.
- Account CRUD: add/remove/set-default; names `[a-z0-9][a-z0-9_-]{0,31}`, not all-digits.

## internal/storage [A]

- `ClaudeBin() (path, version, error)`: PATH lookup (cache in
  `$CWM_HOME/state/claude-path` + PATH hash, ccam pattern), `claude --version` parse.
- `MinVersion = 2.1.234`, compare helper.
- `Slugify(path)`: non-`[A-Za-z0-9-]` → `-` (used by doctor's legacy-folder warning only).
- `HostName()`: sanitized `[A-Za-z0-9-]` (used by WIP branches).

## internal/manifest [A]

- Read/write `repos.tsv`: `name\turl` lines, dedupe on write, atomic write.
- `Lookup(name) (url, ok)`. Compatible with the bash kit's file format (same columns).

## CLI surface (dispatch in internal/cli) [A: skeleton; B/C: commands]

```
cwm version
cwm help [cmd]
cwm init [--migrate] [--account <name>]            [C]
cwm import [--from <dir>] [--ccam]                 [C]
cwm open <repo> [new|last] | cwm <repo> [new|last] [B]
cwm list                                            [B]
cwm wip [status]                                    [B]
cwm doctor [--net]                                  [B]
cwm account add|list|use|remove                     [C]
cwm mcp add|list|remove                             [C]
cwm hook session-start|session-end                  [B]  # used by settings.json hooks
cwm --account <name> <...>                          # global flag, any launch mode
```
Reserved names (repo can't be called these; error suggests `cwm open <name>`):
`init import open list wip doctor account mcp hook version help statusline gui`.
`--dry-run` accepted globally by mutating commands (init, import, account add/remove,
mcp add/remove): print plan, change nothing. Exit code 0.

## Launcher [B]

`cwm open <repo> [mode]` (+ direct form `cwm <repo>`):
1. Reserved-name check → error.
2. Workspace must exist (`~/.cwm/workspace`), else FAIL with "run `cwm init`".
3. Default account must exist (or `--account`); ensure assembly (stage C — in B,
   if no account system yet, fall back: CLAUDE_CONFIG_DIR=$CWM_HOME/workspace directly).
4. Repo `$CWM_REPOS_ROOT` (default `~/repos`, env-overridable) + name: if missing,
   manifest lookup → `git clone <url>` (stdio inherited, non-interactive); unknown →
   error + known names. If `~/repos/<name>` exists but is not a git repo → warn, continue.
5. chdir repo; export `CLAUDE_CONFIG_DIR` (assembled), `CLAUDE_CODE_PROJECT_DIR_NAME=<repo>`.
6. Sync-branch hint (port from bin/cwm: fetch origin, rev-list HEAD..origin/sync/<other>).
7. exec: mode new → `claude`; last → `claude --continue`; default → `claude --resume`;
   extra args after mode pass through verbatim. `syscall.Exec` on Unix; on Windows
   spawn+wait+exit-code (ccam pattern).
8. MCP: if workspace/mcp.json exists and non-empty → append `--mcp-config <abs path>`.

`cwm list`: workspace projects/ dirs, session count (`*.jsonl` at depth 1), last mtime.
`cwm wip`: run session-end logic now; `cwm wip status`: list `origin/sync/*` ahead counts.

## Hooks + doctor [B]

`cwm hook session-start` (cwd = repo): manifest update (name → push URL). Always exit 0.
`cwm hook session-end`: port hooks/session-end.sh exactly — dirty check, `git add -A`,
`commit -m "wip: <host> <UTC>"` with `-c user.name=claude-sync -c user.email=claude-sync@localhost
--no-verify`, `push origin HEAD:refs/heads/sync/<host>` non-force, log to
`$CWM_HOME/logs/wip.log`, ALWAYS exit 0.

`cwm doctor [--net]`: port bin/doctor.sh's 11 checks, same `ok:/warn:/FAIL:` prefixes,
summary line `doctor: N ok, M warn, K fail`, exit 1 only on FAIL. `--net`: SSH probe
`ssh -o BatchMode=yes -o ConnectTimeout=5 -T git@github.com`. Default mode must never
exec `ssh` (E2E asserts with PATH stub that fails the test if invoked).

## testdata/fakeclaude [B]

POSIX sh script `testdata/fakeclaude/fakeclaude.sh` (Windows E2E not required locally;
CI linux-only for e2e tag):
- `--version` → echo `${FAKECLAUDE_VERSION:-2.1.400}`
- first arg `auth` + second `login` → write `$CLAUDE_CONFIG_DIR/.credentials.json`
  `{"token":"fake"}` and `.claude.json` with `oauthAccount.email =
  ${FAKECLAUDE_EMAIL:-fake@example.com}`, `plan = "claude_max"`; echo done; exit 0
- otherwise → append one JSON line `{"args":[...],"cwd":...,"claude_config_dir":...,
  "project_dir_name":...}` to `$FAKECLAUDE_CAPTURE` (path must be set, else exit 5);
  exit 0

## E2E (test/e2e_test.go, build tag `e2e`) [B/C]

Temp HOME (t.TempDir + env swap). PATH: fakeclaude as `claude`. Real git.
- **B**: init-less open fails cleanly → init-lite (mkdir workspace) → create bare origin +
  seed repo with dirty file → `cwm hook session-start` records manifest → `cwm open alpha`
  (deleting ~/repos/alpha first to prove clone) → fakeclaude capture asserts `--resume`,
  pin env, CLAUDE_CONFIG_DIR under launch/ or workspace → `cwm hook session-end` → bare
  origin has `sync/<hostname>` with the dirty file → doctor: green summary, ssh stub NOT
  called → doctor with legacy `-Users-x` folder → warn line → old fakeclaude version 2.1.100
  → version warn → reserved name `cwm doctor-open` style: repo literally named `list` →
  reserved error.
- **C**: `cwm init --account main` (fakeclaude auth login) → accounts/main has creds →
  assembly: launch/main contains links to every workspace item + REAL (copied/linked)
  `.credentials.json` + `.claude.json` → `cwm account list` shows email/plan → `--account`
  flag → `cwm import --from <fake ~/.claude>` copies items + imports login as account
  `imported` → ccam import fixture (fake profiles dir with two profiles + groups/default)
  → both accounts + shared items land in workspace → `--dry-run` on import: no fs changes.

## Accounts [C]

- `cwm account add <name>`: validate name → mkdir accounts/<name> → run
  `claude auth login` with `CLAUDE_CONFIG_DIR=<account dir>` (stdio inherited) → read
  `.claude.json` → extract `oauthAccount` (email, account_uuid, organization_name? —
  tolerate missing keys) → save identity into config.toml `[[account.login]]` → print
  summary. Non-TTY without creds after login → warn, keep account.
- `cwm account list`: table `#, name, email, plan, default*, last login` — last login from
  `.credentials.json` mtime. `--json` output.
- `cwm account use <name>` / `remove <name>` (remove refuses if running? v0.2: refuse if
  launch dir has live lock — skip lock check, just remove + backup account dir to backups/).
- **Assembly** `EnsureLaunch(account)`: launch/<name>/ gets: dirs → symlink (unix) or
  junction (windows, via `cmd /c mklink /J`) to workspace items; files → symlink (unix) /
  symlink-or-copy (windows: probe `os.Symlink`; if fails and link_mode=copy → copy now +
  register copy-back in hook session-end for `history.jsonl`); credentials → copy (NOT
  link) from accounts/<name>/ every assembly (cheap, always fresh, avoids rotation
  invalidating links). Probed mode stored in config `link_mode` (D14). Assembly is
  idempotent: correct link → skip; wrong target → replace (backup first); unexpected
  real file → FAIL with doctor pointer.
- Windows note: implement junction + copy paths, unit-test the planner with interface
  seam (`Linker` interface: Symlink/Junction/Copy), real-Windows behavior verified in CI
  only (local runs are Linux).

## Workspace init / migrate / import / mcp [C]

- `cwm init`: create skeleton; write `workspace/settings.json` NO — settings.json is
  machine-local: write it to launch template? DECISION: settings.json lives in
  `accounts/<name>/` is wrong too... **settings.json is generated per launch-dir at
  assembly time** (hooks → `<abs cwm binary> hook session-start|session-end`, env block
  DISABLE_TELEMETRY etc. — port templates/settings.*.json.tmpl semantics). Probe claude
  version; probe link mode; detect `~/.claude-me` (bash-kit workspace) → prompt
  `--migrate` flow: plan (move items to workspace/, import `.credentials.json`+
  `.claude.json` as account `main`, keep `~/.claude-me` untouched as backup — COPY not
  move, safer) → confirm → execute → backup receipt in backups/.
- `cwm import --from <dir>`: same content rules as migrate, source = any claude config
  dir (default `~/.claude`); `--ccam`: scan `~/.local/share/ccam/profiles/*/` for
  account dirs (each → account with creds) and `~/.local/share/ccam/groups/<g>/` for
  shared items (→ workspace items, first-content-wins, conflicts logged not merged).
  Plan → confirm → backup → execute. Never deletes source. `--dry-run`.
- `cwm mcp add <name> -- <command...> [args]` / `list` / `remove <name>`: manage
  `workspace/mcp.json` `{"mcpServers":{name:{command,args}}}` (also accept full JSON via
  `--json <file>`); launcher appends `--mcp-config` (D13).

## CI + release [C]

- `.github/workflows/ci.yml`: push/PR → matrix [ubuntu-latest, windows-latest],
  go-version 1.25.x: `gofmt -l` (empty), `go vet`, `go test ./...`, ubuntu extra:
  `go test -tags e2e ./test/`, `go build` for GOOS=windows/amd64 + linux/amd64+arm64.
- `.github/workflows/release.yml`: on tag `v*`: goreleaser release (uses GITHUB_TOKEN).
- `.goreleaser.yaml`: build ldflags `-s -w -X main.version={{.Version}}`, binaries
  `cwm` (linux amd64/arm64, windows amd64), archives + checksums, skip brew/scoop for now.
- `cwm version` prints `cwm <semver> (<goos>/<goarch>, <commit>)`; dev build = `dev`.

## PLAN.md additions [C]

- D12: usage lens v0.2 = identity + login freshness only; live usage numbers deferred
  (undocumented endpoint; needs research; never fabricate).
- D13: MCP = workspace/mcp.json + `--mcp-config` at launch; account .claude.json never
  rewritten by cwm.
- D14: link modes symlink/junction/copy probed at init, stored in config.
- D15: hooks are `cwm hook` subcommands (binary path in generated settings.json);
  bash hooks remain for legacy kit only.

## README [C]

Rewrite install section: release binary (once CI publishes) / `go install
github.com/Mr-JoE1/cwm@latest` / from source; account quick start (`cwm init`,
`cwm account add main`, `cwm open <repo>`); migration section (bash kit `--migrate`,
ccam `import --ccam`); legacy bash mode kept one release (collapsed section at bottom).

## Constraints

- Errors: `fmt.Errorf` wrapped `%w`; no panics in library code.
- No network in unit/e2e tests except local git file:// and fakeclaude (no github.com).
- All mutating ops: plan → confirm (TTY) / `--yes` (non-TTY) → backup → atomic execute.
- Public text: no personal context (same privacy rule as v0.1).
- Windows code paths must compile on Linux (GOOS=windows build in CI and locally:
  `GOOS=windows go build ./...` green before every stage commit).
