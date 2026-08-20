# On-disk log history

## Why

Issue [#1714](https://github.com/signal18/replication-manager/issues/1714)
added level/module filtering to the GUI log viewer
(`share/dashboard_react/src/Pages/Dashboard/components/Logs/index.jsx`). That
filtering originally only applied to the in-memory ring buffer
(`s18log.HttpLog`, `utils/s18log/httplog.go`), which is a fixed-size recent
window. Anything older had already fallen out of the API and only existed in
the rotated on-disk log files.

This implementation adds a bounded, filterable read path over that on-disk
history while reusing the existing log endpoints.

## Scope

The **main log file and its maintenance sibling** are history-backed:

- `cluster.Logrus == repman.Logrus` (`server/server.go`) writes
  `repman.Conf.LogFile`.
- `cluster.MaintenanceLogrus == repman.MaintenanceLogrus` writes
  `maintenanceLogPath(repman.Conf.LogFile)` (the `-maintenance` sibling).
  `LogModuleWithFieldsPrintf` (`cluster/cluster_log.go`) routes
  maintenance-adjacent modules there instead of the main file —
  `ConstLogModMaintenance`, `ConstLogModTask`, `ConstLogModRestic`,
  `ConstLogModSST`, `ConstLogModBackupStream`, `ConstLogModPurge` — this
  routing predates the history feature and is unrelated to it.
- Both files' entries carry `cluster=` and `module=` tags.

Both the `general` and `task` history splits (`config.IsTaskLogModule`)
straddle that file boundary: `task` includes `ConstLogModTask`/`SST`/
`BackupStream`/`Restic` (maintenance file) alongside `ConstLogModDbErrors`/
`DbSqlErrors`/`DbSlowquery`/`DbOptimize`/`DbAudit` (main file), and `general`
includes `ConstLogModMaintenance`/`Purge` (maintenance file) alongside
everything else not in the task list (main file). Scanning only the main
file would silently drop real matches for the maintenance-routed half of
each split — `s18log.ReadHistoryFiles` scans both (`utils/s18log/
history.go`); `readLogHistory` (`server/api_global.go`) is the only caller
and always passes both paths. See "Multi-file scan order" below for how the
two files' candidates are interleaved, which is not simply "finish the main
file, then start the maintenance file."

Those two files back:

- `GET /api/global/http-logs`
- per-cluster `general` and `task` history via
  `GET /api/clusters/{clusterName}/topology/logs/{logType}`

The following are **not** history-backed:

- `security`, `workload`, `schema` (the maintenance file itself is
  history-backed as described above — it's just not independently
  selectable as its own `logType`, since its content is already reachable
  through the `general`/`task` split)
- per-cluster `sql_error.log` and `sql_general.log`

Those loggers write to separate files and do not consistently carry the
`cluster=` / `module=` tags needed to reconstruct filtered `HttpMessage`
entries. Requesting history for unsupported cluster log types returns `400`
instead of a silently empty result.

### Global history is server-only, like the live endpoint

`repman.GlobalLogs` (the live buffer `GET /api/global/http-logs` falls back
to without `since`/`until`) is populated only by `server/server_log.go`'s
`LogModuleWithFieldsPrintf`, which always tags entries `Group: "none"` —
cluster-level log lines never reach it. History mode for the same endpoint
therefore calls `readLogHistory(r, s18log.GroupNone, "")`, not
`readLogHistory(r, "", "")`: an empty `Group` means "no filter" in
`HistoryQuery` and would additionally return every cluster's history rows,
which the live response never does. `group == ""` (true "no filter") is
reachable only through `readLogHistory` itself, not through either HTTP
handler.

## Reader design (`utils/s18log/history.go`)

`ReadHistory(baseLogFile string, q HistoryQuery) (HistoryResult, error)` scans
the active log file plus its rotated backups, including `.gz` backups, under
bounded disk and memory use.

### File discovery and scan order

- `listHistoryFiles` enumerates the active file plus lumberjack-style rotated
  backups.
- Files are scanned **newest first**: active file, then backups from most to
  least recent.
- When a bound cuts the scan short, the dropped data is the **oldest** history,
  not the most recent.

### Multi-file scan order (`ReadHistoryFiles`)

`ReadHistoryFiles(baseLogFiles []string, q HistoryQuery) (HistoryResult,
error)` scans more than one independently-rotated base log file (the main
file plus its `-maintenance` sibling) as **one** recency-ordered sequence,
not one base file fully finished before the next starts:

- Every base's candidate files (active + backups) are pooled together and
  sorted by `historyFile.recency`, newest first, before any scanning begins.
  For a backup, `recency` is the rotation timestamp embedded in its
  lumberjack filename (reliable — immune to a later `chown`/copy touching
  mtime). For an active file there's no such embedded timestamp, so
  `recency` falls back to the directory entry's `ModTime`.
- The pooled, sorted list is then scanned by the same loop `ReadHistory`
  uses for one base (`scanFileList`), so `MaxScanBytes`/`MaxFiles`/`Limit`
  are one shared budget across every file from every base — never a fresh
  full budget per base (which would let scanning N bases open up to N times
  the configured cap).
- This matters specifically because a naive "finish base A entirely, then
  start base B" approach would let a busy, list-earlier base exhaust the
  whole budget before a genuinely more recent file from a list-later base is
  ever opened — silently dropping newer history purely because of argument
  order, not actual recency. Pooling by recency first means a budget cutoff
  always drops the OLDEST-ranked file across all bases, matching the
  single-base newest-first guarantee above instead of contradicting it at
  the multi-file boundary.
- Trade-off: ordering is file-granular, not line-granular. Each file is
  still scanned and merged as a whole unit (like within one base), so one
  base's individual line timestamps are not re-interleaved against another
  base's lines the way a full read-everything-then-globally-sort approach
  would. This keeps the streaming, bounded, one-file-in-memory-at-a-time
  design intact — the alternative would require buffering every base's
  matches before any bound could be applied.

### Per-file streaming model

- Each file is streamed line-by-line.
- Compressed backups are read through `gzip.Reader`.
- Files are not loaded fully into memory.
- Within a single file, lines are read in on-disk ascending order.

### Parsing and reconstruction

- Each line is parsed as generic logfmt (`key=value` / `key="quoted value"`).
- Unparseable lines are skipped rather than treated as fatal errors.
- `level=` is mapped back into the frontend's existing level buckets.
- `module=` is reconstructed through `config.ModuleFromTag`, the inverse of
  `config.GetTagsForLog`.
- Results are returned **newest first**, matching `HttpLog.Buffer`.

### Bounds and truncation

The reader is always bounded per T18:

- `MaxScanBytes`
- `MaxFiles`
- `Limit`

Zero or non-positive values fall back to package defaults; they never mean
"unbounded".

`HistoryResult.Truncated` is set when the result may be incomplete because the
scan stopped due to:

- byte budget exhaustion
- file-count exhaustion
- request `Limit` exhaustion
- `scanner.Err()` mid-file (oversized line, I/O error)
- a backup that can't be opened as gzip at all (`gzip.NewReader` fails on a
  partially-written or corrupted `.gz` file) — treated the same as a
  scanner error, not as "this file legitimately had zero matching lines":
  its content is just as unrecoverable, so it must not look like a clean,
  complete scan. Like `scanner.Err()`, this stops the whole scan at that
  point (files older than the corrupt one are not opened either) rather
  than erroring the request outright (`F8`: less broken beats completely
  broken) — matches, if any, found in files newer than the corrupt one are
  still returned.

This means `Truncated=true` is a conservative signal: it is acceptable to
occasionally say "there may be more" when the result happened to exactly fit
the limit, but not acceptable to silently under-report partial history.

### Time bounds

- `Since` is **inclusive**: `ts >= Since`
- `Until` is **exclusive**: `ts < Until`

Exclusive `Until` is required for backward pagination: the GUI re-sends
`until=<oldest row already shown>`, and an inclusive bound would repeat that
row on the next page.

## Timestamp handling

### On-disk format

The main history-backed log file writes timestamps as:

`2006-01-02 15:04:05`

The same zoneless format every other log file this server writes uses
(`security`/`workload`/`schema`/`maintenance`, the graphite API log,
`origin/develop`'s main log before this feature existed at all) and the same
format the live in-memory buffer uses (`cluster/cluster_log.go`,
`time.Now().Format(...)`, no `.UTC()` call — this is the server's real local
time with the zone dropped from the text, not a claim that the server runs in
UTC). No log file this project writes carries a real numeric offset.

An earlier version of this feature (commit `d3e337a01`) had the main log file
write `2006-01-02 15:04:05 -0700` instead, specifically so `since`/`until`
filtering could parse a real offset. That was reverted: it made the main log
file inconsistent with every other log file for no benefit, once the
consequence of *not* having a real offset was understood (see "Why the
zoneless format is still correct" below) — matching `-0700` was solving a
problem the reader design doesn't actually have.

### Reader compatibility

`parseHistoryTimestamp` supports two layouts, for robustness rather than
because either is currently written:

1. offset-aware: `2006-01-02 15:04:05 -0700`
2. zoneless: `2006-01-02 15:04:05`

Only the zoneless layout is written today. The offset-aware layout exists
purely so any file rotated during the (now-reverted) window when
`d3e337a01` was live keeps parsing until it ages out via
`log-rotate-max-age` — it costs nothing to keep and removing it would only
make old files unreadable sooner than they'd otherwise age out on their own.

### Why the zoneless format is still correct for since/until filtering

`time.Parse` on a layout with no offset verb defaults the result's location
to UTC — this is Go's standard behavior, not something this package adds. The
parsed value is therefore not the log line's true real-world instant (that
would require knowing the server's actual offset), but a *consistent* label:
every history line and every `since`/`until` query bound goes through the
exact same zoneless-parses-as-UTC rule (`server/api_global.go`'s
`time.Parse(time.RFC3339, raw)` requires an explicit `Z`/offset by RFC3339's
own syntax, and the frontend's `datetimeLocalToRFC3339` supplies that by
literally appending `"Z"` to the picker's own digits — not by converting
through the browser's real timezone). Because both sides are labeled
identically, `scanHistoryFile`'s `ts.Before(q.Until)` / `ts.Before(q.Since)`
comparisons correctly preserve wall-clock digit ordering regardless of what
the server's real timezone is: picking "17:00" matches rows whose displayed
digits say "17:00", digit for digit. This is what makes the simple, literal
picker conversion correct rather than merely convenient.

The one thing this can't do is tell you the true real-world instant a line
was logged at (that still requires knowing the server's real offset, which no
written log format currently carries) — but nothing in this feature needs
that; it only needs consistent relative ordering between stored lines and
query bounds, which the zoneless convention provides for free.

### Mixed-format transition window

The one remaining case where a file can contain both zoneless and
offset-aware lines is a rotated file left over from the `d3e337a01` window
(now reverted). `parseHistoryTimestamp` reports both whether parsing
succeeded and whether it used the offset-aware layout; `scanHistoryFile`'s
ascending-order early-exit on `Until` only fires on the latter, since an
offset-aware line's parsed instant is a real UTC instant while a zoneless
line's is only a consistent label — the two aren't guaranteed to sort
correctly relative to each other within one mixed file. This has no
practical effect going forward (no new lines are offset-aware) and existing
mixed files age out on their own.

### Frontend conversion

`toHistoryTimestamp` in `Logs/index.jsx` normalizes a displayed timestamp
into the RFC3339 shape `since`/`until` expect. It preserves a real offset
when a row happens to have one (backward compatibility with the `d3e337a01`
window, mirroring the reader's own fallback above) and otherwise labels the
zoneless string `"Z"` — the same consistent-not-real-UTC convention described
above. `datetimeLocalToRFC3339` does the same for the picker's own digits,
with no branching at all: there is nothing to detect, guess, or wait for.

### Display: raw value, no conversion

Every row — live or history — renders `log.timestamp` exactly as received,
matching `origin/develop`'s `Logs` component (which has never parsed,
formatted, or stripped anything from a log timestamp). An earlier version of
this feature stripped a history row's offset before display so it would
visually match zone-less live rows; that's no longer needed since no row
carries an offset to strip, and matches the project's existing "don't process
what the server already formatted for you" convention.

## Privilege-drop file ownership

`server.go` drops privileges from root to `--user` using `Setgid` then `Setuid`.
An already-open file descriptor keeps working across that drop, so live logging
continues even if the log file was created while the process was still root.

`ReadHistory`, however, performs **fresh** `os.Open` calls after the drop. If
the log files are still root-owned, history reads fail with `EACCES`.

To close that gap:

- `misc.ChownR(repman.Conf.WorkingDir, uid, gid)` keeps the existing behavior
  for cluster working files
- `s18log.ChownHistoryFiles(baseLogFile, uid, gid)` now transfers ownership of:
  - the main log file
  - rotated backups
  - the derived `-security`, `-workload`, `-schema`, and `-maintenance` files

This chown is best-effort and runs before the actual privilege drop.

`scanHistoryFile` also wraps permission errors with a hint pointing at the two
common causes:

- pre-existing root-owned files from before the fix
- deployment-side ownership problems (for example a rootless Docker volume the
  process never had permission to chown)

## API behavior

The implementation reuses the existing endpoints instead of adding separate
history routes:

- `GET /api/global/http-logs`
- `GET /api/clusters/{clusterName}/topology/logs/{logType}`

Both switch from the in-memory buffer to on-disk history mode when `since` or
`until` is present.

History mode also supports:

- `level`
- `module`
- `text`
- `limit`

### Why reuse the same endpoints

The live GUI polls those endpoints regularly. Reusing them is safe because the
expensive disk scan is **opt-in** via `since` / `until`; the hot polling path
never sends those parameters.

### Response shape

The JSON shape remains the same as the live path:

- global endpoint returns `globalLogsSnapshot`
- cluster endpoint returns `s18log.HttpLog`

In history mode:

- `buffer` contains history matches
- `truncated` reflects `HistoryResult.Truncated`
- `len` means `len(buffer)` for both endpoints

Per-cluster history is additionally restricted to `logType=general` or
`logType=task`, using `config.IsTaskLogModule` to mirror the live split.

## Configuration

History behavior is controlled through:

| Flag | Default | Meaning |
|---|---|---|
| `log-history-enable` | `true` | Enables the history API path. |
| `log-history-max-scan-bytes` | 50MB | Maximum total post-decompression bytes read per request. |
| `log-history-max-lines` | 5000 | Maximum lines returned per request. |
| `log-history-max-files` | 20 | Maximum rotated files opened per request. |

The bounds are always enforced even if the feature itself is disabled by
configuration.

## GUI behavior

`Logs` (`share/dashboard_react/src/Pages/Dashboard/components/Logs/index.jsx`)
takes an optional:

`onLoadOlder(params) => Promise<{buffer, truncated}>`

When provided, it enables two history entry points.

### Load older

- sends the current level/module/text filters
- sends `until` from the true pagination cursor (`oldestFetchedCursor`)
- appends the returned page to local history state

### Time range

- sends `since` / `until` directly from the date-time inputs
- replaces local history state with the returned window
- hides the live buffer while the range is active

### Applied range banner

The visible banner is driven from `appliedRange`, not the live-editable input
values:

- editing the picker does not relabel the current result before re-fetch
- paging further back clears the lower bound and sets `rangeExtended`
- if capping evicts the newest rows from an active range, the upper bound is no
  longer claimed and `rangeUpperTruncated` explains why

### Error and truncation reporting

`apiHelper.js` resolves `{data, status}` even for non-2xx responses. The GUI
therefore uses `unwrapHistoryResponse` to distinguish:

- real request failures (`historyError`)
- genuine "no more history" (`historyExhausted`)
- bounded / partial results (`historyTruncated`)

### Client-side bounding

History loaded into the GUI is bounded to avoid reintroducing the memory and GC
pressure that motivated this work.

Two capping functions are used intentionally:

- `capHistoryKeepingNewest`
  - used for one-shot range fetches
  - keeps the newest part of the requested window
- `capHistoryKeepingTail`
  - used for repeated `Load older` paging
  - keeps the newest rows the user has not already paged past at the top while
    preserving the newly fetched older page at the bottom

`oldestFetchedCursor` is tracked separately from the displayed oldest row so the
cap cannot stall or skip pagination.

## Level bucketing parity (live vs. history)

`Logs/index.jsx`'s `LEVEL_BUCKETS`/`bucketForLevel` (live in-memory buffer)
and `history.go`'s `historyLevelBuckets`/`bucketForLevel` (on-disk history)
must classify every level string identically, or a "WARN+ERR only" filter
would behave differently depending on whether a row came from the live
buffer or a history fetch:

- `OTHER` (anything neither side's bucket map recognizes) has its own toggle
  chip (`LEVEL_CHIPS`, rendered after `LEVEL_ORDER`) instead of being
  always-selected with no way to exclude it — previously `ALL_LEVEL_BUCKETS`
  included `OTHER` but only `ERR`/`WARN`/`INFO`/`DBG` had chips, so it could
  never actually be deselected.
- `TRACE` buckets into `DBG` on both sides. The backend already did this
  (`historyLevelBuckets`); the frontend's `LEVEL_BUCKETS.DBG` previously
  listed only `DEBUG`.

## Known limitations and deferred work

The following are intentionally left out of this change:

- Non-history-backed log families (`security`, `workload`, `schema`,
  `maintenance`, per-cluster SQL history) still need `cluster=` / `module=`
  tagging before safe filtered history can be added.
- No log file this project writes (main, security, workload, schema,
  maintenance, live buffer) carries a real timezone offset — all of them
  write `time.Now()`/zoneless-formatted wall-clock text, matching
  `origin/develop`'s own behavior everywhere it has ever written a log. This
  means there is no way to recover a log line's true real-world instant from
  the file alone. That's an accepted, pre-existing limitation of the whole
  logging system, not something specific to history browsing — see "Why the
  zoneless format is still correct for since/until filtering" above for why
  `ReadHistory`'s `since`/`until` filtering doesn't actually need one.
- Timestamps render exactly as the backend sends them, for every row, live or
  history — matching `origin/develop`'s `Logs` component, which has never
  parsed, formatted, or stripped anything from `log.timestamp`.
  `datetimeLocalToRFC3339` is equally simple: the picker's own digits, labeled
  `"Z"`, no branching, no state to learn first — always usable, and correct
  by construction per the zoneless-parses-as-UTC convention both sides share.
  Two earlier, more complex designs were tried and reverted during this
  feature's development (offset-stripping before display, then a real
  numeric-offset-based picker requiring the user to fetch history first) —
  both were solving a problem that doesn't exist once the reader's own
  zoneless-comparison behavior is understood; neither is needed.
- User-facing documentation on `docs.signal18.io` is still required separately
  from this implementation note.
