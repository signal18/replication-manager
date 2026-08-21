import { useState, useMemo, useCallback, useEffect, useRef, memo } from 'react'
import TagPill from '../../../../components/TagPill'
import { Box, Code, Flex, HStack, Input, VStack, Menu, MenuButton, MenuList, MenuItem, Checkbox, Button, Text } from '@chakra-ui/react'
import styles from './styles.module.scss'
import NotFound from '../../../../components/NotFound'
import { useSelector } from 'react-redux'
import { clusterService } from '../../../../services/clusterService'
import { globalClustersService } from '../../../../services/globalClustersService'
import { isAutoReloadPaused } from '../../../../utility/autoReloadPause'

// Buffer Level values aren't limited to the 4 canonical uppercase constants
// (INFO/WARN/ERROR/DEBUG) — real call sites also emit TEST/BENCH/ALERT/ALERTOK/
// START/STATE, and a typo'd WARNING (vs. WARN, in cluster/srv_rejoin.go). Group
// them by the logrus severity the backend actually dispatches each one at —
// NOT config.IsEligibleForPrinting, which only gates verbosity eligibility and
// doesn't classify ALERTOK or WARNING at all:
//   - Errorf: ERROR, ALERT                             (cluster_log.go:263,301)
//   - Warnf:  WARN, START, STATE (open + resolve)       (cluster_log.go:279,319;
//                                                         logPrintStateTo:533,541)
//   - Infof:  INFO, TEST, BENCH, ALERTOK                (cluster_log.go:275,293,
//                                                         297,339 — ALERTOK is a
//                                                         resolved/success event,
//                                                         not a failure)
//   - Debugf: DEBUG                                     (cluster_log.go:277)
// WARNING has no case in that switch (falls to the default Printf branch, which
// logrus treats as Info) — it's grouped here with WARN anyway, since that's the
// evident intent of the typo, not the literal runtime severity.
// TRACE is included in DBG to mirror the backend's historyLevelBuckets
// (utils/s18log/history.go) — on-disk history reconstructs a logrus
// level=trace line as DEBUG's bucket, so the live and history paths must
// bucket it the same way or toggling DBG off/on would behave differently
// depending on which path a given row came from.
const LEVEL_BUCKETS = {
  ERR: ['ERROR', 'ALERT'],
  WARN: ['WARN', 'WARNING', 'START', 'STATE'],
  INFO: ['INFO', 'TEST', 'BENCH', 'ALERTOK'],
  DBG: ['DEBUG', 'TRACE'],
}
const LEVEL_ORDER = ['ERR', 'WARN', 'INFO', 'DBG']
const LEVEL_COLORS = { ERR: 'red', WARN: 'orange', INFO: 'blue', DBG: 'gray', OTHER: 'gray' }
// OTHER catches any level string LEVEL_BUCKETS doesn't recognize (backend
// counterpart: bucketForLevel's fallback in utils/s18log/history.go). It's
// rendered as its own chip below (LEVEL_CHIPS) rather than left toggle-less,
// so "WARN+ERR only" can actually exclude unrecognized levels instead of
// always including them regardless of what's deselected.
const ALL_LEVEL_BUCKETS = new Set([...LEVEL_ORDER, 'OTHER'])
const LEVEL_CHIPS = [...LEVEL_ORDER, 'OTHER']

// Per-click page size for "Load older" — deliberately smaller than the
// server's own default cap (log-history-max-lines, 5000): that default is
// sized for a one-shot "give me everything in this window" query (time
// range), not for a single increment of manual backward paging.
const HISTORY_PAGE_LIMIT = 200
// Hard client-side ceiling on accumulated `history` (paged-in via repeated
// "Load older" clicks, or a wide "Apply range"). logsData/data re-run their
// filtering, and every row re-renders, on EVERY live-buffer poll tick — not
// just on click — so an unbounded history array turns "click Load older a
// few times" into "the whole log view lags on every poll" as it keeps
// re-processing an ever-growing list on an interval it doesn't control.
// Evicts the oldest rows first (history is newest-first overall — see
// handleLoadOlder), matching the same drop-oldest discipline the backend
// scanner already uses for its own bounds.
const MAX_HISTORY_ROWS = 2000

function bucketForLevel(level) {
  const upper = (level || '').toUpperCase()
  for (const bucket of LEVEL_ORDER) {
    if (LEVEL_BUCKETS[bucket].includes(upper)) return bucket
  }
  return 'OTHER'
}

// Mirrors config.GetTagsForLog (config/config.go:3264) — the single canonical
// module-id -> tag vocabulary. Do not invent new labels here; keep in sync with
// the Go switch if module constants ever change (guarded server-side by
// config.TestGetTagsForLog).
const MODULE_TAGS = {
  0: 'general',
  1: 'election',
  2: 'sst',
  3: 'heartbeat',
  4: 'conf',
  5: 'git',
  6: 'backup',
  7: 'orchestrator',
  8: 'vault',
  9: 'topology',
  10: 'proxy',
  11: 'proxysql',
  12: 'haproxy',
  13: 'prxjanitor',
  14: 'maxscale',
  15: 'graphite',
  16: 'purge',
  17: 'job',
  18: 'restic',
  19: 'mailer',
  20: 'support',
  21: 'externalscript',
  22: 'stats',
  23: 'sql',
  24: 'app',
  25: 'errorlog',
  26: 'slowquery',
  27: 'optimize',
  28: 'auditlog',
  29: 'sqlerrorlog',
  30: 'plugin',
  31: 'maintenance',
  32: 'arbitration',
  // -1 = config.ConstLogModUncategorized: the producer has no module concept
  // at all (a hand-rolled buffer write bypassing the module-aware logging
  // path). Deliberately distinct from 0/general — do not fold into it.
  '-1': 'uncategorized',
}

// Falls back to 'uncategorized' (not 'general') for any module id this map
// doesn't recognize — e.g. a gap in the backend's own mapping, or a value
// that predates this frontend's copy of it — same user-facing meaning as the
// explicit -1 sentinel above: "no real module info for this entry."
// Silently relabeling it as "general" would misrepresent it.
function moduleTag(moduleId) {
  return MODULE_TAGS[moduleId] ?? 'uncategorized'
}

// rowKeyFor gives each row a content-based identity instead of its array
// index. `logsData`/`data` get a brand-new array (and brand-new row objects,
// freshly parsed from the poll response) on every live-buffer poll tick even
// when the underlying lines haven't changed, and the live buffer prepends
// new lines — so an index key would relabel every existing row on every
// poll, defeating LogRow's memoization below (React treats "row at index 5"
// as a different element once a new line shifts everything down by one,
// even though the content is identical).
function rowKeyFor(log) {
  return `${log.group}|${log.timestamp}|${log.module}|${log.text}`
}

// keyedRows disambiguates exact-duplicate rows before handing keys to React.
// rowKeyFor's content signature isn't guaranteed unique on its own —
// timestamps are second-granularity, so the same module logging the exact
// same message twice within a second (a repeated heartbeat/retry line) is
// plausible, not contrived. Duplicate sibling keys are a real React
// reconciliation bug (silently mis-binding which DOM node backs which row),
// not just a console warning. Appending a per-content occurrence index fixes
// that while staying stable across polls: `data` is always append-ordered
// (never reshuffled), so duplicates keep the same relative order across
// renders — the only way a *new* duplicate can appear is via the live
// buffer's prepend, which correctly becomes occurrence #1 and pushes the
// existing ones to #2, #3, ...; that costs one extra remount for that
// specific group of duplicates, not a reintroduction of "everything
// remounts every poll" (which is what plain index keys caused).
function keyedRows(data) {
  const seen = new Map()
  return data.map((log) => {
    const base = rowKeyFor(log)
    const n = (seen.get(base) || 0) + 1
    seen.set(base, n)
    return { key: n === 1 ? base : `${base}#${n}`, log }
  })
}

// LogRow is memoized with a content comparator (not reference equality,
// which would never match — poll responses always produce fresh objects)
// so a poll tick that only adds a couple of new lines doesn't force React to
// re-render every already-on-screen row, which is what makes a "load a lot
// of history, then let the interval poll keep running" view expensive.
//
// log.timestamp is rendered exactly as received, same as develop's Logs
// component does for every row — no stripping, no per-row conversion. All
// log files (main/security/workload/schema/maintenance, live buffer) write
// the same zoneless wall-clock format (server/server.go, cluster/
// cluster_log.go), so there's nothing to strip or reconcile between rows.
const LogRow = memo(
  function LogRow({ log }) {
    const levelColor = LEVEL_COLORS[bucketForLevel(log.level)]
    return (
      <tr className={styles.tr}>
        <td className={`${styles.td} ${styles.timestamp}`}>
          <Code bg='transparent'>{log.timestamp}</Code>{' '}
        </td>
        <td className={styles.td}>
          <TagPill text={log.level} colorScheme={levelColor} />
        </td>
        <td className={`${styles.td} ${styles.text}`}>
          <Code bg='transparent'>{log.text.replace(/,(?!\s)/g, ', ')}</Code>
        </td>
      </tr>
    )
  },
  (prev, next) =>
    prev.log.timestamp === next.log.timestamp &&
    prev.log.level === next.log.level &&
    prev.log.module === next.log.module &&
    prev.log.text === next.log.text
)

// toHistoryTimestamp converts a displayed log Timestamp into the RFC3339
// shape the since/until query params expect (server/api_global.go parses
// them with time.Parse(time.RFC3339, ...)). Every log file this server
// writes now (main/security/workload/schema/maintenance, live buffer) uses
// the same zoneless wall-clock format, either "YYYY/MM/DD HH:MM:SS" or
// "YYYY-MM-DD HH:MM:SS" (call sites disagree on separator) — normalized here
// and labeled "Z" for a valid RFC3339 string. Since the backend parses that
// same zoneless shape as a (fictional but consistent) UTC instant on both the
// stored side and the query side — see datetimeLocalToRFC3339 below — the
// "Z" label is not asserting anything about a real timezone, just satisfying
// RFC3339's syntax; relative ordering/filtering is correct regardless of what
// the server's actual zone is. The offset branch below exists only for
// backward compatibility with already-rotated files written before
// server/server.go's main-log formatter briefly carried a real "+HHMM"
// offset — those age out on their own via log-rotate-max-age.
function toHistoryTimestamp(ts) {
  if (!ts) return null
  const normalized = ts.replace(/\//g, '-').replace(' ', 'T')
  const offset = normalized.match(/^(.*T\d{2}:\d{2}:\d{2})\s*([+-]\d{2})(\d{2})$/)
  if (offset) {
    const [, base, offsetHours, offsetMinutes] = offset
    return `${base}${offsetHours}:${offsetMinutes}`
  }
  return normalized + 'Z'
}

// datetimeLocalToRFC3339 converts an <input type="datetime-local"> value
// ("YYYY-MM-DDTHH:MM" or "...:SS", no timezone) into the RFC3339 shape the
// since/until query params expect, by literally labeling the picker's own
// digits "Z" — the same "not really UTC, just a consistent label" convention
// toHistoryTimestamp above uses for the stored side. Because both sides of
// the comparison go through the identical zoneless-parses-as-(fictional)-UTC
// rule, picking "17:00" matches rows whose displayed digits say "17:00",
// digit for digit — true picker/table parity, with no need to know or guess
// at the server's real offset.
function datetimeLocalToRFC3339(v) {
  if (!v) return null
  return (v.length === 16 ? v + ':00' : v) + 'Z'
}

// unwrapHistoryResponse turns apiHelper's always-resolves { data, status }
// shape (it never throws on a non-2xx — see performRequest/handleResponse in
// services/apiHelper.js) into either a resolved { buffer, truncated } or a
// thrown Error, so callers can tell "the server legitimately found no more
// matching history" (empty buffer) apart from "the request failed" (403
// disabled, 400 bad range, 500 read failure) — collapsing both into "no
// history found" would misreport a real error as an empty result.
// `pick` extracts the {buffer,truncated}-shaped node from the endpoint's
// response body: the cluster endpoint returns it directly, the global one
// wraps it under `.general`.
export function unwrapHistoryResponse({ data, status }, pick) {
  if (status < 200 || status >= 300) {
    throw new Error(typeof data === 'string' && data ? data : `Request failed with status ${status}`)
  }
  const node = pick(data) || {}
  return { buffer: node.buffer || [], truncated: !!node.truncated }
}

function Logs({ logs, className, searchable = false, isScrollable = true, onLoadOlder }) {
  const [search, setSearch] = useState('')
  const [levelFilter, setLevelFilter] = useState(ALL_LEVEL_BUCKETS)
  const [moduleFilter, setModuleFilter] = useState(null) // null = show all modules
  const [history, setHistory] = useState([]) // rows fetched via "Load older" or a time-range query
  const [loadingHistory, setLoadingHistory] = useState(false)
  const [historyExhausted, setHistoryExhausted] = useState(false)
  // Set when the bounded scan hit a byte/file cap before exhausting
  // available history (s18log.HistoryResult.Truncated) — the fetched rows
  // are real, but there may be more between them and the next boundary.
  const [historyTruncated, setHistoryTruncated] = useState(false)
  // A real request failure (403 disabled, 400 bad range, 500 read error) —
  // kept distinct from historyExhausted so a failure isn't misreported as
  // "no history found" (see unwrapHistoryResponse above).
  const [historyError, setHistoryError] = useState(null)
  const [sinceInput, setSinceInput] = useState('')
  const [untilInput, setUntilInput] = useState('')
  // true once a time-range query has been applied: the table then shows only
  // that historical window (not mixed with the live buffer, which is always
  // "now" and would otherwise dangle outside the requested range).
  const [timeRangeActive, setTimeRangeActive] = useState(false)
  // What was actually fetched, for the "Showing on-disk history from X to Y"
  // banner — deliberately NOT sinceInput/untilInput. Those are live-editable
  // the moment the user touches the pickers, before "Apply range" re-fetches
  // anything, so the banner would otherwise claim a range that doesn't match
  // what's on screen. `since: null` after "Load older" extends past the
  // originally-applied lower bound (see handleLoadOlder) — that bound is no
  // longer accurate, so the banner drops it rather than keep repeating it.
  const [appliedRange, setAppliedRange] = useState({ since: null, until: null })
  // Distinguishes "no lower bound was ever requested" (appliedRange.since is
  // null because the user only set/applied an upper bound) from "there WAS a
  // lower bound but 'Load older' has since paged past it" — the banner needs
  // different wording for the two, not just "since is missing" either way.
  const [rangeExtended, setRangeExtended] = useState(false)
  // The symmetric case at the OTHER end: capHistoryKeepingTail evicts from
  // the front of `history` (the newest rows) once MAX_HISTORY_ROWS is
  // reached, which happens while paging via "Load older" (see
  // handleLoadOlder). In timeRangeActive mode, logsData is `history` alone
  // (no live buffer mixed in — see the useMemo below), so once this fires,
  // the visible table no longer reaches appliedRange.until even though
  // that's still what the query originally asked for. Sets true the first
  // time an eviction actually removes range rows; reset alongside
  // appliedRange on a fresh "Apply range" / "Back to live" / filter change.
  const [rangeUpperTruncated, setRangeUpperTruncated] = useState(false)
  // The true pagination boundary: the oldest timestamp actually returned by
  // the last successful fetch, BEFORE the cap (see capHistoryKeepingTail
  // below) trims `history` for display. Deliberately separate from
  // `history`/`logsData` — once history hits MAX_HISTORY_ROWS, the display
  // only ever shows the most recent window of what's been fetched, so the
  // oldest *displayed* row doesn't track how far back we've actually already
  // asked the server for. Computing the next "Load older" `until` from the
  // display instead of this cursor would re-request the same
  // already-fetched page forever once the cap is hit. null = nothing
  // fetched yet; handleLoadOlder falls back to the live buffer's oldest row
  // for the very first click.
  const [oldestFetchedCursor, setOldestFetchedCursor] = useState(null)
  // The scrollable table area (not the whole component — see the sticky
  // filter-bar split below). "Back to live" resets scroll position here so
  // the user lands back at the newest row instead of staying scrolled deep
  // into history that's no longer even in the DOM.
  const scrollRef = useRef(null)

  const logsData = useMemo(() => {
    if (timeRangeActive) return history
    const live = logs?.length > 0 ? logs.filter((log) => log.timestamp) : []
    return history.length > 0 ? [...live, ...history] : live
  }, [logs, history, timeRangeActive])

  const presentModules = useMemo(
    () => Array.from(new Set(logsData.map((l) => l.module ?? -1))).sort((a, b) => a - b),
    [logsData]
  )

  const data = useMemo(
    () =>
      logsData.filter((x) => {
        if (!x.text.toLowerCase().includes(search.toLowerCase())) return false
        if (!levelFilter.has(bucketForLevel(x.level))) return false
        if (moduleFilter && !moduleFilter.has(x.module ?? -1)) return false
        return true
      }),
    [logsData, search, levelFilter, moduleFilter]
  )

  const rows = useMemo(() => keyedRows(data), [data])

  // Previously-fetched history was scoped to whatever level/module/text
  // filters were active at fetch time (the server applies them, not just the
  // client). If the user then changes those filters, that history no longer
  // represents "everything matching the current filters for that time
  // window" — mixing it in would silently under-represent the older rows
  // (e.g. a module=sql-scoped page, then clearing the module filter, would
  // make it look like nothing but SQL happened back then). Drop it and fall
  // back to the live view; the user re-triggers "Load older"/"Apply range"
  // to refill under the new scope. Does not touch sinceInput/untilInput so
  // "Apply range" can be re-clicked with the same window under new filters.
  useEffect(() => {
    setHistory([])
    setHistoryExhausted(false)
    setHistoryTruncated(false)
    setHistoryError(null)
    setTimeRangeActive(false)
    setAppliedRange({ since: null, until: null })
    setRangeExtended(false)
    setRangeUpperTruncated(false)
    setOldestFetchedCursor(null)
  }, [search, levelFilter, moduleFilter])

  // Shared level/module/text filter params for both "Load older" and a
  // time-range query, so a history fetch always respects whatever the user
  // currently has narrowed the live view down to.
  const buildFilterParams = useCallback(() => {
    const params = {}
    if (levelFilter.size < LEVEL_ORDER.length + 1) params.level = [...levelFilter].join(',')
    if (moduleFilter) params.module = [...moduleFilter].map((m) => moduleTag(m)).join(',')
    if (search) params.text = search
    return params
  }, [levelFilter, moduleFilter, search])

  // Two capping directions for two different situations — using the wrong
  // one for either is a real bug, not a style choice:
  //
  // capHistoryKeepingNewest: for a one-shot fetch (Apply range) — keeps the
  // front (newest) of `list` and drops the tail. Correct there because the
  // whole point of a wide range query is "show me the newest part of this
  // window if it doesn't all fit."
  //
  // capHistoryKeepingTail: for incremental accumulation (Load older) —
  // keeps the BACK of `list` and drops the front instead. `history` grows
  // by appending each newly-fetched (older) page to the end, so once it's
  // already at MAX_HISTORY_ROWS, capping from the front (like
  // capHistoryKeepingNewest does) would reproduce the exact same `prev`
  // every time — the whole newly-fetched page gets discarded in full.
  // Requests would keep going out and oldestFetchedCursor would keep
  // advancing correctly, but nothing new would ever actually appear:
  // "Load older" turns into a dead button once the cap is hit. Dropping
  // from the front instead evicts rows the user has already scrolled past
  // (they clicked "Load older", so their attention is at the tail), which
  // keeps every newly-fetched page visible.
  const capHistoryKeepingNewest = useCallback((list) => {
    if (list.length <= MAX_HISTORY_ROWS) return { list, trimmed: false }
    return { list: list.slice(0, MAX_HISTORY_ROWS), trimmed: true }
  }, [])
  const capHistoryKeepingTail = useCallback((list) => {
    if (list.length <= MAX_HISTORY_ROWS) return { list, trimmed: false }
    return { list: list.slice(list.length - MAX_HISTORY_ROWS), trimmed: true }
  }, [])

  const handleLoadOlder = useCallback(async () => {
    if (!onLoadOlder || loadingHistory) return
    // Prefer the true fetch cursor over the displayed oldest row: once
    // capHistoryKeepingTail has trimmed `history` for display, the last
    // visible row no longer reflects how far back we've actually already
    // fetched (see oldestFetchedCursor's declaration). Only the very first
    // "Load older" click (cursor still null) falls back to the live
    // buffer's oldest row.
    const oldest = oldestFetchedCursor ?? logsData[logsData.length - 1]?.timestamp
    if (!oldest) return

    setLoadingHistory(true)
    setHistoryError(null)
    try {
      const params = { ...buildFilterParams(), until: toHistoryTimestamp(oldest), limit: HISTORY_PAGE_LIMIT }
      const { buffer, truncated } = await onLoadOlder(params)
      if (buffer.length === 0) {
        setHistoryExhausted(true)
        setHistoryTruncated(truncated)
      } else {
        // buffer is newest-first (server contract), so its last entry is the
        // oldest row THIS fetch reached — advance the cursor from the full
        // page as fetched, not from whatever capHistoryKeepingTail ends up
        // keeping (it always preserves this page in full anyway — see that
        // function's comment — but the cursor logic shouldn't depend on it).
        setOldestFetchedCursor(buffer[buffer.length - 1].timestamp)
        // Compute synchronously via plain function calls (not inside the
        // setHistory updater below): a setState updater function isn't
        // invoked synchronously here — setLoadingHistory/setHistoryError
        // above already gave this fiber pending work, which rules out
        // React's "eager state" fast path, so the updater only actually
        // runs later, during the render phase. Assigning `capTrimmed` from
        // inside it and reading that variable on the next lines would
        // almost always still see its initial `false` — silently breaking
        // both historyTruncated below and rangeUpperTruncated further down.
        // `history` here is safe to read directly from closure: this branch
        // only runs when handleLoadOlder's own `loadingHistory` guard has
        // already prevented a second concurrent call from racing it.
        const { list, trimmed: capTrimmed } = capHistoryKeepingTail([...history, ...buffer])
        setHistory(list)
        // Paging further into the past than the range originally applied
        // (if any) — the banner's "from" bound no longer describes what's
        // on screen, so drop it (and flag rangeExtended) rather than keep
        // repeating a stale value.
        if (appliedRange.since) {
          setAppliedRange((prev) => ({ ...prev, since: null }))
          setRangeExtended(true)
        }
        // The "to" bound is NOT automatically safe the way it might look:
        // capHistoryKeepingTail evicts from the FRONT of `history` once
        // MAX_HISTORY_ROWS is hit, and in timeRangeActive mode `history` IS
        // the visible table (no live buffer mixed in — see the logsData
        // useMemo). So once capTrimmed fires while a range is active, the
        // newest rows of that range — the ones nearest appliedRange.until —
        // are exactly what just got evicted. Flag it so the banner stops
        // claiming a "to" bound the table no longer actually reaches.
        if (capTrimmed && timeRangeActive) {
          setRangeUpperTruncated(true)
        }
        setHistoryTruncated(truncated || capTrimmed)
      }
    } catch (err) {
      setHistoryError(err?.message || 'Failed to load older logs')
    } finally {
      setLoadingHistory(false)
    }
  }, [
    onLoadOlder,
    loadingHistory,
    logsData,
    history,
    buildFilterParams,
    appliedRange,
    capHistoryKeepingTail,
    oldestFetchedCursor,
    timeRangeActive
  ])

  const handleApplyRange = useCallback(async () => {
    if (!onLoadOlder || loadingHistory || (!sinceInput && !untilInput)) return

    setLoadingHistory(true)
    setHistoryError(null)
    try {
      // Bound the request itself to what can actually be displayed: without
      // this, a wide range can come back with (server default) up to 5000
      // rows in one response — capHistoryKeepingNewest then has to trim
      // mid-response rather than at a page boundary, which is exactly the
      // case that makes the raw-response cursor (below) unsafe.
      const params = { ...buildFilterParams(), limit: MAX_HISTORY_ROWS }
      if (sinceInput) params.since = datetimeLocalToRFC3339(sinceInput)
      if (untilInput) params.until = datetimeLocalToRFC3339(untilInput)

      const { buffer: rawBuffer, truncated: serverTruncated } = await onLoadOlder(params)
      const { list: buffer, trimmed: capTrimmed } = capHistoryKeepingNewest(rawBuffer)
      const truncated = serverTruncated || capTrimmed
      setHistory(buffer)
      // Cursor from the DISPLAYED `buffer`, not the raw response: capping
      // the request above should make them equal, but if the server ever
      // returns more than requested, deriving the cursor from the trimmed-
      // off raw response would silently skip exactly the rows that got
      // trimmed — they'd never be displayed AND never be reachable by a
      // later "Load older" either. Keeping the cursor tied to what's
      // actually on screen means "Load older" always continues from there,
      // no gap, at worst a re-fetch of a window that was already covered
      // (unlike handleLoadOlder's per-click pages, which are small enough
      // to never be split mid-page, so using their raw result is safe there).
      setOldestFetchedCursor(buffer.length > 0 ? buffer[buffer.length - 1].timestamp : null)
      setTimeRangeActive(true)
      setHistoryExhausted(buffer.length === 0)
      setHistoryTruncated(truncated)
      // Snapshot what was actually fetched, not the (still-editable) inputs:
      // the banner must describe the applied query, so it doesn't silently
      // relabel itself if the user edits the pickers afterward without
      // re-clicking "Apply range".
      setAppliedRange({ since: sinceInput || null, until: untilInput || null })
      setRangeExtended(false)
      setRangeUpperTruncated(false)
    } catch (err) {
      setHistoryError(err?.message || 'Failed to load history for that range')
    } finally {
      setLoadingHistory(false)
    }
  }, [onLoadOlder, loadingHistory, sinceInput, untilInput, buildFilterParams, capHistoryKeepingNewest])

  const handleClearRange = useCallback(() => {
    setSinceInput('')
    setUntilInput('')
    setHistoryTruncated(false)
    setHistoryError(null)
    setTimeRangeActive(false)
    setHistory([])
    setHistoryExhausted(false)
    setAppliedRange({ since: null, until: null })
    setRangeExtended(false)
    setRangeUpperTruncated(false)
    setOldestFetchedCursor(null)
    if (scrollRef.current) scrollRef.current.scrollTop = 0
  }, [])

  const handleSearch = (e) => {
    setSearch(e.target.value)
  }

  const toggleLevel = (bucket) => {
    setLevelFilter((prev) => {
      const next = new Set(prev)
      if (next.has(bucket)) next.delete(bucket)
      else next.add(bucket)
      return next
    })
  }

  const toggleModule = (mod) => {
    setModuleFilter((prev) => {
      const base = prev ?? new Set(presentModules)
      const next = new Set(base)
      if (next.has(mod)) next.delete(mod)
      else next.add(mod)
      return next
    })
  }

  const isModuleChecked = (mod) => moduleFilter === null || moduleFilter.has(mod)

  return (
    <Box
      className={`${styles.logContainer} ${className}`}
      display='flex'
      flexDirection='column'
      overflow='hidden'>
      <VStack spacing={4} flexShrink={0} align='stretch'>
        {searchable && (
          <Flex direction={'row'} w={'100%'} p={4} wrap='wrap'>
            <HStack gap='4' wrap='wrap'>
              <HStack className={styles.search}>
                <label htmlFor='logSearch'>Search</label>
                <Input id='logSearch' type='search' size='sm' onChange={handleSearch} />
              </HStack>
              <HStack gap='1'>
                {LEVEL_CHIPS.map((bucket) => (
                  <TagPill
                    key={bucket}
                    text={bucket}
                    colorScheme={LEVEL_COLORS[bucket]}
                    variant={levelFilter.has(bucket) ? 'solid' : 'outline'}
                    onClick={() => toggleLevel(bucket)}
                  />
                ))}
              </HStack>
              {presentModules.length > 1 && (
                <Menu closeOnSelect={false}>
                  <MenuButton as={Button} size='sm'>
                    Module
                  </MenuButton>
                  <MenuList maxH='300px' overflowY='auto'>
                    {presentModules.map((mod) => (
                      <MenuItem key={mod} onClick={() => toggleModule(mod)}>
                        <Checkbox isChecked={isModuleChecked(mod)} pointerEvents='none' mr={2} />
                        {moduleTag(mod)}
                      </MenuItem>
                    ))}
                  </MenuList>
                </Menu>
              )}
              {onLoadOlder && (
                <HStack gap='3' wrap='wrap' rowGap={2}>
                  <HStack gap='2'>
                    {/* Picker digits are matched against the table's digits directly — see
                        datetimeLocalToRFC3339 — so there's nothing to know or wait for
                        before these are usable. */}
                    <label htmlFor='logSince'>Since</label>
                    <Input
                      id='logSince'
                      type='datetime-local'
                      size='sm'
                      w='auto'
                      minW='190px'
                      value={sinceInput}
                      onChange={(e) => setSinceInput(e.target.value)}
                    />
                  </HStack>
                  <HStack gap='2'>
                    <label htmlFor='logUntil'>Until</label>
                    <Input
                      id='logUntil'
                      type='datetime-local'
                      size='sm'
                      w='auto'
                      minW='190px'
                      value={untilInput}
                      onChange={(e) => setUntilInput(e.target.value)}
                    />
                  </HStack>
                  <HStack gap='2'>
                    <Button
                      size='sm'
                      colorScheme='blue'
                      onClick={handleApplyRange}
                      isLoading={loadingHistory && !!(sinceInput || untilInput)}
                      isDisabled={!sinceInput && !untilInput}>
                      Apply range
                    </Button>
                    {/* Shown whenever there's accumulated history to discard — not just
                        timeRangeActive: plain "Load older" clicks (no time range ever
                        applied) also pile rows into `history`, and until now there was no
                        way back to the live-only view for that case short of a page reload. */}
                    {(timeRangeActive || history.length > 0) && (
                      <Button size='sm' variant='outline' onClick={handleClearRange}>
                        Back to live
                      </Button>
                    )}
                  </HStack>
                </HStack>
              )}
            </HStack>
          </Flex>
        )}
        {timeRangeActive && (
          <Text fontSize='sm' color='gray.500' w='100%' px={4}>
            Showing on-disk history{appliedRange.since ? ` from ${appliedRange.since.replace('T', ' ')}` : ''}
            {/* Suppressed once rangeUpperTruncated: capHistoryKeepingTail evicts
                the newest range rows first once MAX_HISTORY_ROWS is hit while
                paging via "Load older", so the table may no longer actually
                reach this bound — see handleLoadOlder. Claiming it anyway would
                misdescribe what's on screen. */}
            {appliedRange.until && !rangeUpperTruncated ? ` to ${appliedRange.until.replace('T', ' ')}` : ''} — live
            tail is hidden while a time range is applied
            {rangeExtended ? ' (extended further back via "Load older")' : ''}
            {rangeUpperTruncated
              ? ' — the newest rows of this range were evicted to bound memory; "Back to live" and a narrower range will show them'
              : ''}
            .
          </Text>
        )}
        {historyError && (
          <Text fontSize='sm' color='red.500' w='100%' px={4}>
            Failed to load history: {historyError}
          </Text>
        )}
        {historyTruncated && !historyError && (
          <Text fontSize='sm' color='orange.500' w='100%' px={4}>
            History scan stopped at a bound before reaching the requested range/limit — narrow your filters or
            time range for a complete result.
          </Text>
        )}
      </VStack>
      {/* Only this part scrolls — the filter bar and banners above stay put
          (flexShrink={0} on the VStack) so they're reachable without
          scrolling back up, e.g. to change filters while deep in a long log. */}
      <Box ref={scrollRef} flex='1' minH={0} overflow={isScrollable ? 'auto' : 'hidden'}>
        <table className={styles.table}>
          <tbody>
            {data?.length > 0 ? (
              rows.map(({ key, log }) => <LogRow key={key} log={log} />)
            ) : (
              <tr>
                <td>
                  <NotFound text={'No logs found'} className={styles.notfound} />
                </td>
              </tr>
            )}
          </tbody>
        </table>
        {onLoadOlder && (
          <HStack w='100%' justify='center' p={2}>
            {historyExhausted ? (
              <Text fontSize='sm' color='gray.500'>
                {timeRangeActive ? 'No logs found in that time range' : 'No older history found'}
              </Text>
            ) : (
              <Button size='sm' onClick={handleLoadOlder} isLoading={loadingHistory}>
                Load older
              </Button>
            )}
          </HStack>
        )}
      </Box>
    </Box>
  )
}

// Server-scoped (config.LogHistoryEnable has no per-cluster override — see
// doc/implementation/utils/s18log/LOG_HISTORY_READER.md), so the source of
// truth is a monitor config, not anything cluster-specific. But history
// requests for a peer go to that peer's API (baseURL, see getClusterLogHistory/
// getGlobalLogHistory below) while state.globalClusters.monitor is always
// fetched from the LOCAL server (getMonitoredData is called with no baseURL —
// see globalClustersSlice.js) — reusing it here regardless of baseURL would
// gate the peer's history controls on the wrong server's setting. So: when
// baseURL is empty (viewing local), reuse the already-fetched local monitor
// state; when it points at a peer, fetch that peer's own /monitor directly
// (same ad hoc getApi(baseURL) pattern PeerClusterList uses for other
// peer-scoped data) rather than trusting the local value. Defaults to
// disabled (hidden controls) while the peer fetch is in flight or if it
// fails — a control that's briefly missing is much better than one that's
// shown and then 403s.
export function useLogHistoryEnabled(baseURL) {
  const localEnabled = useSelector((state) => state.globalClusters.monitor?.config?.logHistoryEnable)
  // Same knob Pages/Home/index.jsx's own polling effect reads (state.cluster.
  // refreshInterval, in seconds; <= 0 means the user turned auto-refresh
  // off). Reusing it — instead of a cadence of our own — means "refresh
  // off" actually stops this poll too, and a cadence the user changes is
  // picked up here as well.
  const refreshInterval = useSelector((state) => state.cluster.refreshInterval)
  const [peerEnabled, setPeerEnabled] = useState(false)

  useEffect(() => {
    if (!baseURL) return
    let cancelled = false

    const fetchPeerHistoryEnabled = () => {
      globalClustersService
        .getMonitoredData(baseURL)
        .then(({ data }) => {
          if (!cancelled) setPeerEnabled(!!data?.config?.logHistoryEnable)
        })
        .catch(() => {
          if (!cancelled) setPeerEnabled(false)
        })
    }

    // Reset before the first fetch for THIS baseURL resolves — otherwise a
    // switch from a peer with history enabled to one without it would keep
    // returning the old peer's `true` (stale state.peerEnabled) for as long
    // as the new fetch is in flight, briefly showing controls the new peer
    // will 403 on.
    setPeerEnabled(false)
    fetchPeerHistoryEnabled()

    // localEnabled (state.globalClusters.monitor) is kept fresh by the
    // app-wide poll in Pages/Home/index.jsx's callServices (dispatches
    // getMonitoredData every 10 ticks of refreshInterval — see its comment
    // there). That poll always omits baseURL, i.e. local only, so it never
    // reaches a peer's monitor data — without a poll of our own here, a
    // log-history-enable flip on the peer mid-session would never be picked
    // up while this component stays mounted. Mirror that same "10x less
    // often" cadence rather than invent a different one — and skip setting
    // up the interval at all when refreshInterval <= 0, matching how
    // callServices' own effect (Pages/Home/index.jsx) treats that value as
    // "auto-refresh is off", not "poll anyway on some default cadence".
    //
    // Respect the same pause/auto-reload gate callServices' own polling
    // honors (isAutoReloadPaused — user-paused, or a menu/modal lock held).
    // Logs is keyed on historyEnabled (see GeneralLogs/TaskLogs/GlobalLogs
    // below), so a value flip remounts it and drops local history/filter
    // state — exactly what "paused"/"refresh off" promise won't happen.
    // Only the recurring poll is gated, not the initial fetch above: that
    // one establishes whether to show the controls at all for this peer in
    // the first place (equivalent to any other "load on mount" fetch
    // elsewhere in the app, which callServices' gates never covered either).
    let intervalId
    if (refreshInterval > 0) {
      intervalId = setInterval(() => {
        if (isAutoReloadPaused()) return
        fetchPeerHistoryEnabled()
      }, refreshInterval * 10 * 1000)
    }

    return () => {
      cancelled = true
      clearInterval(intervalId)
    }
  }, [baseURL, refreshInterval])

  return baseURL ? peerEnabled : !!localEnabled
}

export const GeneralLogs = ({ className }) => {
  const logs = useSelector((state) => state.cluster.clusterLogs.general)
  const clusterName = useSelector((state) => state.cluster.clusterData?.name)
  const baseURL = useSelector((state) => state?.auth?.baseURL)
  const historyEnabled = useLogHistoryEnabled(baseURL)
  const onLoadOlder = useCallback(
    async (params) => {
      const res = await clusterService.getClusterLogHistory(clusterName, 'general', params, baseURL)
      return unwrapHistoryResponse(res, (d) => d)
    },
    [clusterName, baseURL]
  )
  // Keyed on source (baseURL + clusterName) AND historyEnabled, not a bare
  // "general": Logs holds local history/timeRangeActive state that must not
  // survive a cluster or peer switch onto the same mounted component
  // instance (or fetched history from the old source leaks into the new
  // source's live view), and must not survive log-history-enable flipping
  // off mid-session either (or previously fetched on-disk history rows the
  // backend would now reject stay visible after the controls that fetched
  // them disappear).
  return (
    <Logs
      key={`general-${baseURL || 'local'}-${clusterName || ''}-${historyEnabled ? 'h1' : 'h0'}`}
      logs={logs?.buffer}
      className={className}
      searchable={true}
      onLoadOlder={clusterName && historyEnabled ? onLoadOlder : undefined}
    />
  )
}

export const TaskLogs = ({ className }) => {
  const taskLogs = useSelector((state) => state.cluster.clusterLogs.task)
  const clusterName = useSelector((state) => state.cluster.clusterData?.name)
  const baseURL = useSelector((state) => state?.auth?.baseURL)
  const historyEnabled = useLogHistoryEnabled(baseURL)
  const onLoadOlder = useCallback(
    async (params) => {
      const res = await clusterService.getClusterLogHistory(clusterName, 'task', params, baseURL)
      return unwrapHistoryResponse(res, (d) => d)
    },
    [clusterName, baseURL]
  )
  return (
    <Logs
      key={`task-${baseURL || 'local'}-${clusterName || ''}-${historyEnabled ? 'h1' : 'h0'}`}
      logs={taskLogs?.buffer}
      className={className}
      searchable={true}
      onLoadOlder={clusterName && historyEnabled ? onLoadOlder : undefined}
    />
  )
}

export const SecurityLogs = ({ className }) => {
  const securityLogs = useSelector((state) => state.cluster.clusterLogs.security)
  return <Logs key={"security"} logs={securityLogs?.buffer} className={className} searchable={true} />
}

export const WorkloadLogs = ({ className }) => {
  const workloadLogs = useSelector((state) => state.cluster.clusterLogs.workload)
  return <Logs key={"workload"} logs={workloadLogs?.buffer} className={className} searchable={true} />
}

export const DDLLogs = ({ className }) => {
  const ddlLogs = useSelector((state) => state.cluster.clusterLogs.ddl)
  return <Logs key={"ddl"} logs={ddlLogs?.buffer} className={className} searchable={true} />
}

export const VariableChangeLogs = ({ className }) => {
  const varChangeLogs = useSelector((state) => state.cluster.clusterLogs['variable-change'])
  return <Logs key={"variable-change"} logs={varChangeLogs?.buffer} className={className} searchable={true} />
}

export const SchemaLogs = ({ className }) => {
  const schemaLogs = useSelector((state) => state.cluster.clusterLogs.schema)
  return <Logs key={"schema"} logs={schemaLogs?.buffer} className={className} searchable={true} />
}

export default Logs
