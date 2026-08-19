// Reseed / rejoin progress signal — zero dependencies (unit-testable with `node`).
//
// The dashboard shows a live "reseed in progress" panel driven by the per-server
// structured progress the backend attaches to /api/clusters/{name}/topology/servers
// as `server.reseedProgress` (absent when idle). Shape:
//   { inProgress, fromRejoin, task, backup, tool, bytes, total, percent,
//     startedUnix, elapsedSecs, rateBytesSec, recentRateBytesSec, recentRateReady, line,
//     phase }
// `phase` is set only for physical reseed/flashback (the SST send/receive path) --
// one of 'waiting_receiver' / 'sending_sst' / 'applying_backup', "" for reseed paths
// that don't track it (logical, splitdump, direct-stream). It has no byte/rate
// numbers behind it yet (see formatReseedPhase) -- physical reseed doesn't wire
// through the byte counter the other methods use, so this is a text-only signal for
// now, not a substitute for the bar/bytes shown when reseedHasBar/reseedHasBytes.
// rateBytesSec is a lifetime average (stable, but lags a real slowdown/speedup);
// recentRateBytesSec is a windowed rate over the last few ticks (noisier, reflects
// current throughput), valid only once recentRateReady is true (enough ticks sampled).
// `percent` is -1 when the reseed method has no byte instrumentation — those show a
// generic "rejoin reseed in progress, started T" timer instead of a filled bar.
//
// This is a state-machine signal, NOT a parsed alert string: it reads the structured
// per-server field, so the URL and the numbers are exact (the WARN0189 alert only
// carries a human string with the url baked in).

// getActiveReseeds returns one row per server currently restoring, each shaped
// { url, ...reseedProgress }.
export const getActiveReseeds = (clusterServers) => {
  if (!Array.isArray(clusterServers)) return []
  return clusterServers
    .filter((s) => s && s.reseedProgress && s.reseedProgress.inProgress)
    .map((s) => ({ url: s.url, ...s.reseedProgress }))
}

// hasActiveReseed reports whether any server is currently being reseeded.
export const hasActiveReseed = (clusterServers) => getActiveReseeds(clusterServers).length > 0

// reseedHasBar reports whether a row can show a real progress bar (byte-instrumented)
// rather than only an indeterminate timer.
export const reseedHasBar = (rp) =>
  !!rp && typeof rp.percent === 'number' && rp.percent >= 0 && rp.total > 0

// reseedHasBytes reports whether a row has streamed-bytes/rate to show even though
// the total is unknown (percent -1) — e.g. a direct-stream reseed, which counts real
// bytes forwarded but has no fixed size to divide by. Distinct from reseedHasBar: a
// row can have bytes without a bar (unknown total) or a bar without bytes (n/a here,
// reseedHasBar already implies total>0 which implies byte instrumentation).
export const reseedHasBytes = (rp) => !!rp && typeof rp.bytes === 'number' && rp.bytes > 0

// formatBytes → "223M" / "100G" (mirror of the backend humanBytes).
export const formatBytes = (b) => {
  if (!b || b < 1024) return `${b || 0}B`
  const units = ['K', 'M', 'G', 'T', 'P', 'E']
  let n = b / 1024
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${Math.round(n)}${units[i]}`
}

// formatRate → "<rate>/s", or a "measuring…" placeholder for the sub-1s window right
// after a reseed starts (backend elapsedSecs is truncated to whole seconds, so
// rateBytesSec reads 0 for that window regardless of how fast bytes are actually
// flowing). Once elapsedSecs > 0 this shows the real average rate as-is, including a
// genuine 0B/s if the reseed truly is that slow -- this is NOT a staleness detector
// (rateBytesSec is a cumulative average, so it decays slowly rather than snapping to 0
// on a stall; real staleness is caught server-side by the reseed stall watchdog, which
// aborts the task instead of leaving a misleading rate on screen).
export const formatRate = (rateBytesSec, elapsedSecs) =>
  elapsedSecs > 0 ? `${formatBytes(rateBytesSec)}/s` : 'measuring…'

// formatRateLine combines the windowed "recent" rate (current throughput) with the
// lifetime "average" rate into one display string, e.g. "3M/s now · 18M/s avg". The
// average alone is known to lag a real slowdown/speedup (it barely moves once a
// restore has been running a while), so once the backend has sampled enough ticks to
// report a recent rate (rp.recentRateReady), lead with that. Before then -- the first
// few ticks of a fresh reseed -- there's nothing windowed to show yet, so this falls
// back to exactly what formatRate alone would show (including "measuring…").
export const formatRateLine = (rp) => {
  const avg = formatRate(rp && rp.rateBytesSec, rp && rp.elapsedSecs)
  if (!rp || !rp.recentRateReady) return avg
  const recent = `${formatBytes(rp.recentRateBytesSec)}/s now`
  return avg === 'measuring…' ? `${recent} · ${avg}` : `${recent} · ${avg} avg`
}

// reseedPhaseLabels maps the backend's ReseedPhase* constants (cluster/restore_progress.go)
// to a display label. Keep in sync with that file's ReseedPhaseWaitingReceiver /
// ReseedPhaseSendingSST / ReseedPhaseApplyingBackup string values.
const reseedPhaseLabels = {
  waiting_receiver: 'Waiting for destination receiver',
  sending_sst: 'Sending backup to destination',
  applying_backup: 'Applying physical backup on destination',
}

// formatReseedPhase(phase) → a display label, or "" for an unset/unrecognized phase
// (idle, or a reseed path — logical/splitdump/direct-stream — that doesn't track
// phase). Unrecognized values fall back to "" rather than the raw string so an older
// dashboard build never renders a raw enum value if the backend adds a phase later.
export const formatReseedPhase = (phase) => reseedPhaseLabels[phase] || ''

// formatElapsed(seconds) → "1h23m" / "3m12s" / "45s".
export const formatElapsed = (secs) => {
  secs = Math.max(0, Math.floor(secs || 0))
  const h = Math.floor(secs / 3600)
  const m = Math.floor((secs % 3600) / 60)
  const s = secs % 60
  if (h > 0) return `${h}h${m}m`
  if (m > 0) return `${m}m${s}s`
  return `${s}s`
}
