/* global process */
// Scheduled Database Events display helper tests
// Run with: node src/Pages/Shards/Events/__tests__/eventsMatrix.test.js

import { buildEventRows, driftKinds, eventSchemas, filterEventRows, shortCrc } from '../eventsMatrix.js'

let passed = 0
let failed = 0

function assert(condition, description) {
  if (condition) {
    passed++
    console.log(`  PASS ${description}`)
  } else {
    failed++
    console.log(`  FAIL ${description}`)
  }
}

// an answer of GET .../schema/events: db3 unavailable, cleanup_history missing
// on db2, refresh_stats with a different definition on db2
const view = {
  enabled: true,
  servers: [
    { id: 'm', url: 'db1:3306', isMaster: true, collection: 'checked' },
    { id: 'r1', url: 'db2:3306', isMaster: false, collection: 'checked', comparison: 'different' },
    { id: 'r2', url: 'db3:3306', isMaster: false, collection: 'unavailable', comparison: 'not-checked' }
  ],
  events: [
    {
      db: 'app',
      name: 'cleanup_history',
      nodes: { m: { present: true, status: 'active', definitionCrc64: '18446744073709551615' }, r1: { present: false } },
      drifts: [{ serverId: 'r1', drift: 'missing' }]
    },
    {
      db: 'crm',
      name: 'refresh_stats',
      nodes: { m: { present: true, status: 'active', definitionCrc64: '1' }, r1: { present: true, status: 'active', definitionCrc64: '2' } },
      drifts: [{ serverId: 'r1', drift: 'definition' }, { serverId: 'r1', drift: 'status' }]
    },
    { db: 'crm', name: 'same', nodes: { m: { present: true, status: 'disabled', definitionCrc64: '3' }, r1: { present: true, status: 'disabled', definitionCrc64: '3' } }, drifts: [] }
  ]
}

console.log('shortCrc')
assert(shortCrc('18446744073709551615') === 'ffffffff', 'a uint64 beyond Number.MAX_SAFE_INTEGER is read exactly')
assert(shortCrc('1') === '00000000', 'leading zeros are kept')
assert(shortCrc('') === '' && shortCrc(undefined) === '' && shortCrc('x') === '', 'nothing or garbage shows nothing')

console.log('buildEventRows')
const { servers, rows } = buildEventRows(view)
assert(servers.length === 3 && servers[0].isMaster, 'servers as given, master first')
assert(rows.length === 3 && rows[0].key === 'app.cleanup_history', 'one row per event')
const cleanup = rows[0].cells
assert(cleanup.m.state === 'present' && cleanup.m.status === 'active' && cleanup.m.crc === 'ffffffff', 'present on the master')
assert(cleanup.r1.state === 'absent' && cleanup.r1.drifts.join() === 'missing', 'checked and absent on db2, with the drift repman reported')
assert(cleanup.r2.state === 'not-checked' && cleanup.r2.drifts.length === 0, 'an unavailable server is not checked, never missing')
assert(rows[1].cells.r1.drifts.join() === 'definition,status', 'drifts are per server, as reported')
assert(rows[2].drifts.length === 0 && rows[2].cells.r1.state === 'present', 'a consistent event has no drift')
assert(buildEventRows(null).rows.length === 0 && buildEventRows({ enabled: false, servers: [], events: [] }).servers.length === 0, 'an empty or disabled answer')

console.log('driftKinds / eventSchemas / filterEventRows')
assert(driftKinds(rows[1]).join() === 'definition,status' && driftKinds(rows[2]).length === 0, 'drift kinds of a row')
assert(eventSchemas(rows).join() === 'app,crm', 'schemas, once, sorted')
assert(filterEventRows(rows, { drift: 'any' }).length === 2, 'any drift')
assert(filterEventRows(rows, { drift: 'missing' }).map((r) => r.name).join() === 'cleanup_history', 'one drift kind')
assert(filterEventRows(rows, { schema: 'crm' }).length === 2, 'schema')
assert(filterEventRows(rows, { search: 'REFRESH' }).length === 1, 'search, case-insensitive')
assert(filterEventRows(rows, { schema: 'crm', drift: 'any' }).map((r) => r.name).join() === 'refresh_stats', 'filters combine')

console.log(`\n${passed} passed, ${failed} failed`)
process.exit(failed > 0 ? 1 : 0)
