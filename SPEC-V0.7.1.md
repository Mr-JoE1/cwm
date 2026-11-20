# SPEC v0.7.1: stats & activity

Roadmap row v0.7.1 (`docs/ARCH-V2.md`) and D48: stats are local and
derived. They are computed only from the session JSONL files already in the
workspace, never from an Anthropic API, and they never leave the machine.
Decisions D60–D66 are logged in `docs/PLAN.md`. Compatibility rule as before:
flags, env vars, exit codes and existing `/api/*` shapes only gain fields.
Deliberate exceptions are listed in "Behaviour changes" at the end.

## A: what is counted (D60)

Source: `<workspace>/projects/<project>/<session>.jsonl`, the top level of
each project dir only. `agent-*.jsonl` and the `<session>/subagents/` dirs
are not read (the same discovery as `cwm sessions`, D19). One JSON object
per line. A line that does not parse (malformed, or a file still being
written whose last line has no newline yet) is skipped, never an error. An
unterminated last line is not consumed: the next scan reads it again.

- **Assistant message**: `type == "assistant"` with a non-empty
  `message.id`. One API response is often written as several lines (one
  per streamed content block) sharing `message.id`. Each id counts **once**,
  across all files. Its usage, model and timestamp come from the line with
  the largest `usage.output_tokens` for that id (ties: the earliest
  timestamp, then the first seen in scan order). `model == "<synthetic>"`
  (Claude Code's local error placeholders) is not an API response and is
  not counted.
- **User prompt**: `type == "user"` whose `message.content` is a string, or
  an array with at least one block that is not `tool_result`. Entries with
  `isMeta: true` (injected by Claude Code, not typed by a person) are not
  prompts. Copies of the same entry (same `uuid`, e.g. a transcript copied
  on resume) count once.
- **Messages** = user prompts + distinct assistant message ids.
- **Tokens**, per assistant message: `input` = `input_tokens`, `output` =
  `output_tokens`, `cache_write` = `cache_creation_input_tokens`,
  `cache_read` = `cache_read_input_tokens`. `total` is their sum. The
  5-minute / 1-hour split of cache writes (`usage.cache_creation`) is kept
  for the cost estimate; without it a write counts as 5-minute.
- **Sessions**: distinct `sessionId` (the file name when an entry has
  none) with at least one counted message in the range. A session is
  active on every local day it has a counted message, so one spanning
  midnight counts on both days and once in the total.
- **Project**: the project dir name. **Model**: `message.model`.
- **Days** are local calendar days in a time zone: the machine's
  (`time.Local`) unless `?tz=<IANA name>` / `--tz` says otherwise. The
  dashboard passes the browser's `Intl` zone.
- **Active days**: days in the range with at least one counted message.
- **Streaks** are all-time, independent of the range. Current streak: the
  run of consecutive active days ending today, or ending yesterday while
  today has no activity yet; else 0. Longest streak: the longest such run,
  with its first and last day (the latest one on a tie).
- **First / last activity**: the earliest and latest counted timestamp
  in the range.

### Account attribution (D62)
Claude Code writes a `{"type":"session-owner","accountUuid":…}` entry into
each transcript. An entry belongs to the account of the closest preceding
`session-owner` line in its file. The uuid is mapped to a cwm account name
through `[account.login] account_uuid` in config.toml, or, for an account
that has none recorded, the `oauthAccount.accountUuid` of its account
dir's `.claude.json`. No owner line, or an owner uuid that no cwm account
has: **unattributed**. Nothing is guessed (not from the default account,
not from the device). Raw uuids never appear in the API, the CLI or the
debug bundle.

### Cost estimate (D63)
`internal/stats/prices.go` holds one static table of public list prices
in USD per million tokens (input, output, 5-minute cache write, 1-hour
cache write, cache read) and `PricesUpdated`, the date they were checked.
A model id matches the longest table key that equals it or is followed by
`-<date>` (`claude-sonnet-4-5-20250929` → `claude-sonnet-4-5`). Models
without a price count $0 and are listed in `unpriced`. Everywhere the
number appears it is called an estimate: "est., list prices — your plan
may be flat-rate". Tiered prices (Haiku 5.5 above 100K prompt tokens) use
the base tier.

## B: incremental scan and cache (D61)

`stats.Scan(workspaceDir, opts) (*Snapshot, error)`; `opts` carry the cache
path (`<cwm home>/cache/stats.json`), the account map, the clock, range and
time zone. Aggregation is split from scanning (`stats.Index` holds the
per-file data; `Index.Snapshot(query)` aggregates), so a different range or
time zone is a re-aggregation and never a rescan.

Per file the cache keeps: path (relative to the workspace), size, mtime,
the byte offset parsed so far, a SHA-256 of the first 4 KiB (or of the
whole file when shorter), the parse state needed to resume (the last
`session-owner` uuid), and the file's events. An event is one counted
user prompt or assistant message: kind, 15-minute UTC bucket, session,
model, session-owner uuid (mapped to an account name only when a
snapshot is built, so a renamed or newly logged-in account needs no
rescan; the cache is local, 0600, and never part of the debug bundle),
the four token counts (plus the 1h share of cache writes),
`output_tokens` and the timestamp for the dedupe rule, and a 64-bit hash of
`message.id` / `uuid` for the cross-file dedupe.

15-minute UTC buckets: every current IANA offset is a multiple of 15
minutes, so a bucket never straddles a local midnight, and any time zone
can be applied after the fact.

On each scan, for each `projects/*/*.jsonl`:
- same size and mtime as cached → reuse the cached events (a cache hit);
- larger, and the head hash of the old prefix still matches → resume
  parsing at the cached offset (append path; ids seen earlier in the file
  are updated in place when a later line has more output tokens);
- anything else (smaller, head changed) → reparse from byte 0 (rewrite);
- files that disappeared are dropped from the cache.

The cache is written atomically (temp + rename, 0600) only when something
changed; a version field guards the format (another version → full
rescan). A corrupt or unreadable cache is a full rescan, never an error.
`cache_hit_ratio` = files reused unchanged / files seen.

Targets (benchmark on generated fixtures: 1000 sessions × ~200 lines):
full scan < 2 s, rescan with nothing changed < 200 ms (cache loaded from
disk, as the CLI does).

## C: API, CLI, events (D64)

`GET /api/stats?range=7d|30d|90d|365d|all&tz=<IANA>` (authenticated like
every GET; a bad range or zone is 400; default `30d` and the agent's local
zone). Body:

```json
{
  "range": "365d", "tz": "Europe/Berlin", "from": "2025-11-21", "to": "2026-11-20",
  "totals": {"sessions": 0, "messages": 0, "user_messages": 0, "assistant_messages": 0,
             "tokens": {"input": 0, "output": 0, "cache_write": 0, "cache_read": 0, "total": 0},
             "active_days": 0, "first_activity": "…", "last_activity": "…"},
  "streaks": {"current": 0, "longest": 0, "longest_start": "…", "longest_end": "…"},
  "daily": [{"date": "2026-11-20", "sessions": 0, "messages": 0,
             "tokens": {"input": 0, "output": 0, "cache_write": 0, "cache_read": 0, "total": 0}}],
  "calendar": [{"date": "…", "sessions": 0, "messages": 0, "tokens": 0}],
  "top_projects": [{"name": "…", "sessions": 0, "messages": 0, "tokens": {…}}],
  "top_models":   [{"name": "…", "sessions": 0, "messages": 0, "tokens": {…}, "cost_usd": 0}],
  "accounts":     [{"name": "…|unattributed", "sessions": 0, "messages": 0, "tokens": {…}}],
  "cost_estimate": {"usd": 0, "prices_updated": "2026-11-20", "note": "est., list prices — your plan may be flat-rate"},
  "unpriced": ["…"],
  "scanned_files": 0, "scan_ms": 0, "cache_hit_ratio": 0
}
```

- `daily`: one row per day from `from` to `to` (zeros included). For
  `all`, `from` is the first active day.
- `calendar`: always the last 378 days (54 weeks, enough for a 53-week grid
  in either week start), totals only.
- `top_projects` / `top_models`: by total tokens then messages then name,
  10 at most. `accounts`: all, same order.
- `scanned_files`, `scan_ms`, `cache_hit_ratio` describe the scan the
  answer comes from (a cached answer reports that scan).

`cwm stats [--range R] [--tz Z] [--json]`: `--json` prints exactly the API
body (same encoder; only the three scan fields differ run to run). The
default is a human table: KPIs, top projects, top models, accounts, a
14-day bar column, the estimate with its caveat. It uses only glyphs that
`termout` maps to ASCII (bars are `█`→`#`, `·`→`-`), so legacy consoles show
clean ASCII.

Agent: one `stats.Engine` per server keeps the index in memory. A scan runs
at most once every 60 s, in the background, single-flight. The request
path never scans when it has an index: it aggregates the cached one (a
millisecond job) and, when that index is older than 60 s, kicks a
background refresh. Only the very first request of a process with no
index waits for the first scan (which itself reads the disk cache). The
existing `/api/events` snapshot gains a `stats` fingerprint (totals of the
cached index, all-time); when it changes the `state` event lists
`"stats"` in `changed`. The snapshot poll only reads the cached
fingerprint and nudges the engine; no second loop.

`/api/debug` does not include stats data (it may report the cache file
size only).

## D: dashboard (D65)

Seventh tab **Stats** (`7`); `1`–`7` and the `?` help list all seven.

- Range selector 7d / 30d / 90d / 1y / all (a segmented control; the
  choice is remembered in localStorage).
- KPI cards: sessions, messages, total tokens (compact `1.2M`, exact
  value in `title`), active days, current / longest streak, estimated
  API-equivalent cost labelled "est., list prices — your plan may be
  flat-rate".
- Heatmap: 53 weeks, weeks as columns, Monday first unless the locale's
  week starts on Sunday (`Intl.Locale.weekInfo` when available, default
  Monday). 5 levels: 0 = no activity, 1–4 from the quartiles of the
  non-zero days of the shown metric. Month labels on top, weekday labels
  (Mon/Wed/Fri) on the left. Toggle Sessions / Messages / Tokens. Each
  cell is a `<button>` with an `aria-label` ("1,234 tokens on Tue, 18 Nov
  2026"); one cell is in the tab order (roving tabindex); arrow keys move
  by day (up/down) and week (left/right), Home/End jump to the ends. A
  tooltip shows the same text on hover and focus. Colours: the theme's
  accent ramp (`--heat-0`…`--heat-4`, defined for dark and light).
- Breakdowns: top projects and top models (horizontal bars), token kind
  split (one stacked bar + legend), 30-day token trend (SVG area + line
  built with `createElementNS`).
- Empty state (no counted message at all): "No sessions yet — start one
  and your activity appears here."
- Rules unchanged: DOM nodes and `textContent` only (no innerHTML and
  friends), no external requests, same CSP, all web assets < 150 KB,
  `prefers-reduced-motion` respected, 390 px layout (KPI cards stack, the
  heatmap scrolls horizontally inside its card).

## E: polish (D66)

1. **Favicon 16**: a hand-tuned 16 px mark (bold `>` and a cursor bar on
   the pixel grid, no hexagon) as `favicon-16.svg`; `favicon.svg` (32 px and
   up) keeps the hex mark. The page links both (`sizes="16x16"` and
   `sizes="any"`). docs/brand gets the files and a note.
2. **Tab shortcut hints** no longer read as counts: the digit is a small
   boxed `kbd`-style hint at low opacity, shown only on hover / keyboard
   focus of the tab or while the `?` dialog is open.
3. **Readable panels**: surfaces become opaque enough that the animated
   background never shows through text (dark `--surface` .96, light .97),
   and the canvas is dimmed under the content column.
4. **Installer wrap**: long notes and child-output lines wrap at the
   terminal width (`$COLUMNS`, the console width, fallback 80); continuation
   lines keep the `      ` / `      │ ` prefix. Harness checks at 60 columns.
5. **`cwm account remove`**: `--delete-config` (alias of `--delete-dir`)
   deletes the account's config dir after the backup. Without either flag,
   on a TTY, a second prompt asks "also delete its config folder <dir>?
   [y/N]"; non-interactive runs never delete without the flag. Deletion is
   limited to dirs inside the cwm home (`accounts/<name>`, `launch/<name>`);
   an imported dir elsewhere is kept and the CLI says so.
6. **`cwm mcp add/remove` keep unknown fields**: mcp.json is read as raw
   JSON (top-level keys and every server's fields round-trip, sorted
   keys, 2-space indent, no HTML escaping). `cwm mcp add <name> [--env
   K=V]... -- <cmd> [args...]` and `cwm mcp add <name> --url <url> [--type
   http|sse] [--header 'K: V']...`. `list` shows the url for url servers.
7. **doctor repos-tsv**: a missing repos.tsv is ok when the workspace has
   no project dirs yet ("created on first launch"); the warning stays when
   project dirs exist.

## Behaviour changes
- `stats` becomes a reserved name (a repo called `stats` opens with `cwm
  open stats`).
- The accounts identity reader also accepts Claude Code's camelCase
  `oauthAccount` keys (`accountUuid`, `emailAddress`, `organizationName`),
  so attribution (and the e-mail) work with real `.claude.json` files.

## Tests / gate
- stats: golden fixtures with hand-computed totals (duplicated
  `message.id` across lines and across files, malformed and truncated
  lines, tool_result-only and meta user entries, `<synthetic>`, a session
  across midnight, tz override moving a day, streak edges: today inactive,
  gap, tie), append path (grown file resumes at the offset, partial last
  line), rewrite detection (same size or shorter, changed head), deleted
  files, corrupt cache, attribution, price matching, benchmark.
- gui: `/api/stats` auth, 400s, shape, the `stats` change in the event
  snapshot, 7 tabs.
- cli: `stats --json` equals the API body, table output in ASCII mode,
  account remove `--delete-config` and the prompt, mcp golden round trip
  with extra fields, `--env` / `--url`.
- doctor: fresh workspace without projects has no repos-tsv warning.
- Gate = the v0.6.2 gate plus `go test -run XXX -bench . ./internal/stats/`.
