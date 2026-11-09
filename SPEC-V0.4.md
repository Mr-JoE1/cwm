# cwm v0.4 — Session Browser + WIP UX Spec

Goal: `cwm sessions` lists every session in the synced workspace — on ANY device,
including a fresh one where the repo isn't cloned yet — and one command resumes it
(clone-on-demand still applies). Plus the "dirty repo" view for WIP handoff.

Build in three stages, each gated on the full verification matrix. Tactics from
v0.2/v0.3: read lists ≤4 files, retry on silent runner death.

## CLI surface

```
cwm sessions [project] [--pick [N]] [--limit K] [--json]
  # list sessions across all workspace projects (or one project), newest first:
  #   N  project   session-id (8 chars)  first prompt (≤60 chars)  msgs  branch  modified (relative)
  # N = row number used by --pick. Rows come from workspace/projects/*/<uuid>.jsonl.
  # --pick (TTY)     : print table, prompt "resume which? [N]", read stdin line,
  #                    resolve row → launcher Open(project, ModeNew, ["--resume", id])
  #                    (clone-on-demand + pin + sync hints still apply)
  # --pick N         : non-interactive — resolve row N directly
  # --json           : machine-readable (one object per session)
  # --limit K        : cap rows (default 20; 0 = unlimited)

cwm wip dirty
  # scan $CWM_REPOS_ROOT/*/ for git repos with uncommitted work:
  #   repo  <n> changed file(s)  last commit subject
  # the "what to push before switching devices" view. Repos that don't exist locally
  # are not listed (they're clean by definition on this machine).
```

`cwm open <repo> --resume <full-session-id>` already works via passthrough — this
release adds discovery on top.

## internal/sessions package — stage V0.4-1

```go
type Session struct {
    Project     string    // projects/<dir> name
    ID          string    // filename stem (uuid)
    Path        string    // absolute jsonl path
    FirstPrompt string    // index entry or extracted from transcript
    Summary     string    // index entry when present
    Messages    int       // index entry (0 when absent)
    Branch      string    // index entry
    Modified    time.Time // file mtime (index fileMtime is advisory only)
    Size        int64
}

List(wsProjectsDir string, project string, limit int) ([]Session, error)
ResolvePick(wsProjectsDir, project string, n int) (Session, error) // 1-based row, same sort
```

- Source of truth = the `<uuid>.jsonl` files at depth 1 of each `projects/<dir>/`.
  Skip: non-`.jsonl` files, `agent-*.jsonl` (subagents), anything in a subdirectory,
  and (when the index says so) `isSidechain: true` entries.
- Enrichment: when `projects/<dir>/sessions-index.json` exists, parse it
  (fields: entries[].{sessionId, firstPrompt, summary, messageCount, modified,
  gitBranch, isSidechain}) and join by sessionId. Index missing/stale/malformed →
  never an error; raw scan still lists the session.
- Fallback extraction (no index entry): read the FIRST 64KB of the transcript; find
  the first line with `"type":"user"` and a string `message.content` → FirstPrompt
  (unquote JSON string, collapse whitespace). Absent → "(no prompt)".
- Sort: Modified desc, then Project asc, then ID. Limit applies AFTER sort; limit 0 = all.
- `ResolvePick` recomputes the same listing (list is cheap) and returns row n
  (1-based; out of range → error "no session row N — run 'cwm sessions' to see rows").

### Unit tests (V0.4-1)

Fixtures under t.TempDir: two projects; index present for one (with a sidechain entry
that must be hidden + an entry whose file is missing → not listed), index absent for
the other (firstPrompt extracted from raw jsonl, including a file whose first user
message is an array-content line → skipped → next string line used; a file with no
user line → "(no prompt)"); an `agent-x.jsonl` and a `<uuid>/subagents/` dir →
excluded. Assert sort order, limit behavior, ResolvePick 1-based + error paths,
malformed index JSON tolerated.

## CLI wiring + e2e — stage V0.4-2

- `internal/cli/sessions_cmd.go`: parse `[project] [--pick [N]] [--limit K] [--json]`;
  workspace guard (same error style as pair/list); render table (aligned columns,
  relative times "3h ago" / "2d ago" / "2026-11-01" beyond 7d) or JSON; `--pick`:
  TTY check on stdin — interactive prompt loop (empty line → abort, non-number →
  re-prompt) vs `--pick N` direct; then call `launcher.Open(project, ModeNew,
  []string{"--resume", id})`. Note: Open prints cloning + hints and execs.
- `internal/cli/wip_cmd.go` gains `dirty`: iterate `$CWM_REPOS_ROOT/*` (skip
  non-dirs, non-git), `git status --porcelain` per repo (2s timeout each), dirty →
  row {name, changed count, last commit subject via `git log -1 --format=%s`};
  sort by name; empty → "(no dirty repos — everything committed)".
- Reserved names: add `sessions`. (Note `wip dirty` is a subcommand of wip, no new
  reserved word.)
- e2e additions (test/e2e_test.go, same harness): fixture workspace with projects/
  alpha/{2 uuid jsonl + sessions-index.json with 3 entries incl 1 sidechain} and
  projects/beta/{1 jsonl, no index}; assert `cwm sessions` lists 3 rows (2 alpha +
  1 beta) sorted newest-first, sidechain hidden, index-enriched fields shown;
  `cwm sessions alpha` filters; `cwm sessions --json` parses; `--pick 1` with no
  repo clone present → clone-on-demand fires and fakeclaude capture contains
  `--resume <expected-id>` + pin env (the fresh-device story, proven); `cwm wip
  dirty` on a seeded dirty clone lists it, clean clone lists nothing.

## Stage V0.4-3 — docs + release

- docs/PLAN.md under `## 2026-11-04 (v0.4)`:
  - D19 — session discovery: raw `projects/*/<uuid>.jsonl` at depth 1 is the source
    of truth; sessions-index.json is enrichment only (missing/stale/malformed index
    never hides a session); subagents and sidechains excluded.
  - D20 — resume path: `cwm sessions --pick [N]` resolves a row and reuses the
    launcher passthrough (`claude --resume <id>`) — no second launch path; clone-on-
    demand, pinning and sync hints apply unchanged.
  - D21 — `cwm wip dirty` scans the repos root only (repos absent locally are clean
    by definition on that machine).
- README: new "Find and resume sessions (v0.4)" section with the fresh-device
  walkthrough (`cwm sessions` → `--pick` → working), `cwm wip dirty` example, add
  `sessions` to command examples; roadmap marks v0.4 shipped, renumber.
- Tag v0.3.0 → v0.4.0 via existing goreleaser pipeline (no pipeline changes).

## Constraints

- Same Go rules: stdlib only, no panics, %w errors, gofmt/vet clean.
- No new deps. JSON parsing: encoding/json (index + transcript lines). The 64KB
  bounded read must not load whole files.
- Existing tests/e2e/legacy must pass (allowed: doctor/registry additions for
  `sessions` reserved name; e2e file gains new tests only).
- Public-safe docs.
