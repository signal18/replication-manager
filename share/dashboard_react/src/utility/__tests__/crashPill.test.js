// Crash-pill signal tests.
// Run with: node src/utility/__tests__/crashPill.test.js
//
// The crash pill reflects LIVE cluster health (open REJOIN states in
// clusterAlerts), NOT the durable crash archive. Reproduces the belair miss:
// a healthy cluster whose crash archive still holds an old failed rejoin must
// NOT alarm, because its live REJOIN state has cleared.

import { crashRejoinNeedsAttention } from '../crashPill.js'

let passed = 0
let failed = 0
function assert(condition, description) {
  if (condition) { passed++ }
  else { failed++; console.log(`  ✗ ${description}`) }
}

// ─── Healthy cluster (belair): no open REJOIN state → calm ───────────────────
{
  const noAlerts = { errors: [], warnings: [], infos: [] }
  assert(crashRejoinNeedsAttention(noAlerts) === false,
    'belair: recovered cluster has no open REJOIN state → no alarm (even with old crashes in the archive)')
}

// ─── Live rejoin problems must alarm ─────────────────────────────────────────
{
  const warn0186 = { warnings: [{ number: 'WARN0186', desc: 'rejoin needs operator', from: 'REJOIN' }], errors: [] }
  assert(crashRejoinNeedsAttention(warn0186) === true, 'open WARN0186 (rejoin needs operator) → alarm')

  const warn0184 = { warnings: [{ number: 'WARN0184', desc: 'diverged, not flashback-able', from: 'REJOIN' }] }
  assert(crashRejoinNeedsAttention(warn0184) === true, 'open WARN0184 (analyzed divergence pending) → alarm')

  const rejoinError = { warnings: [], errors: [{ number: 'ERR00099', desc: 'x', from: 'REJOIN' }] }
  assert(crashRejoinNeedsAttention(rejoinError) === true, 'a REJOIN state in errors also alarms')
}

// ─── Unrelated alerts must NOT alarm the crash pill ──────────────────────────
{
  const other = { warnings: [{ number: 'WARN0055', desc: 'disk', from: 'CHECK' }, { number: 'WARN0070', desc: 'proxy', from: 'PROXY' }], errors: [] }
  assert(crashRejoinNeedsAttention(other) === false, 'non-REJOIN warnings (disk, proxy) do not alarm the crash pill')
}

// ─── Defensive ───────────────────────────────────────────────────────────────
{
  assert(crashRejoinNeedsAttention(null) === false, 'null alerts → no alarm')
  assert(crashRejoinNeedsAttention({}) === false, 'empty alerts object → no alarm')
  assert(crashRejoinNeedsAttention({ warnings: [null], errors: undefined }) === false, 'null entries / missing group → no alarm')
}

// ─── Summary ──────────────────────────────────────────────────────────────────
if (failed > 0) {
  console.log(`\n❌ Failed: ${failed}, Passed: ${passed}`)
  process.exit(1)
}
console.log(`\n✅ All tests passed: ${passed}`)
