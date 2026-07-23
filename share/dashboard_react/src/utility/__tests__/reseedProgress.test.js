// Reseed-progress signal tests.
// Run with: node src/utility/__tests__/reseedProgress.test.js
//
// Reads the structured per-server field server.reseedProgress (not a parsed alert
// string), so the "in progress" set, the bar-vs-timer decision, and the byte/elapsed
// formatting are all exact.

import {
  getActiveReseeds,
  hasActiveReseed,
  reseedHasBar,
  formatBytes,
  formatElapsed,
} from '../reseedProgress.js'

let passed = 0
let failed = 0
function assert(condition, description) {
  if (condition) {
    passed++
  } else {
    failed++
    console.log(`  ✗ ${description}`)
  }
}

// ─── getActiveReseeds / hasActiveReseed ──────────────────────────────────────
{
  assert(getActiveReseeds(null).length === 0, 'null servers → no active reseeds')
  assert(getActiveReseeds([]).length === 0, 'empty servers → no active reseeds')
  assert(hasActiveReseed(undefined) === false, 'undefined servers → hasActiveReseed false')

  const servers = [
    { url: 'db1:3306', state: 'Slave' }, // no reseedProgress
    { url: 'db2:3306', reseedProgress: { inProgress: false } }, // idle marker
    { url: 'db3:3306', reseedProgress: { inProgress: true, percent: 42, total: 100, bytes: 42 } },
    { url: 'db4:3306', reseedProgress: { inProgress: true, percent: -1, fromRejoin: true } },
    null,
  ]
  const active = getActiveReseeds(servers)
  assert(active.length === 2, 'only servers with reseedProgress.inProgress are active')
  assert(active[0].url === 'db3:3306' && active[1].url === 'db4:3306', 'active rows carry the server url')
  assert(hasActiveReseed(servers) === true, 'a running reseed → hasActiveReseed true')
}

// ─── reseedHasBar (byte-instrumented vs generic timer) ───────────────────────
{
  assert(reseedHasBar({ percent: 42, total: 100 }) === true, 'byte-instrumented (percent>=0, total>0) → bar')
  assert(reseedHasBar({ percent: -1, total: 0, fromRejoin: true }) === false, 'generic timer (percent -1) → no bar')
  assert(reseedHasBar({ percent: 0, total: 100 }) === true, '0% with a known total still shows a bar')
  assert(reseedHasBar(null) === false, 'null → no bar')
}

// ─── formatBytes (mirrors backend humanBytes) ────────────────────────────────
{
  assert(formatBytes(0) === '0B', '0 → 0B')
  assert(formatBytes(512) === '512B', '512 → 512B')
  assert(formatBytes(1024) === '1K', '1024 → 1K')
  assert(formatBytes(100 * 1024 * 1024 * 1024) === '100G', '100 GiB → 100G')
}

// ─── formatElapsed ───────────────────────────────────────────────────────────
{
  assert(formatElapsed(45) === '45s', '45s')
  assert(formatElapsed(192) === '3m12s', '3m12s')
  assert(formatElapsed(3600 + 23 * 60 + 10) === '1h23m', '1h23m (drops seconds at hour scale)')
  assert(formatElapsed(-5) === '0s', 'negative clamps to 0s')
}

console.log(`\nreseedProgress: ${passed} passed, ${failed} failed`)
if (failed > 0) process.exit(1)
