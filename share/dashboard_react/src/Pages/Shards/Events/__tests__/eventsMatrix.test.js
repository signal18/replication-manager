/* global process */
// Events matrix helper tests
// Run with: node src/Pages/Shards/Events/__tests__/eventsMatrix.test.js

import { EVENT_STATUS, buildEventMatrix, cellNote, eventOnCompletion, eventSchedule, eventSchemas, eventStatusLabel, filterEventRows, isMySQLFamily } from '../eventsMatrix.js'

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

console.log('eventStatusLabel')
assert(eventStatusLabel(1, 'MariaDB') === 'ENABLED', 'status 1 is ENABLED')
assert(eventStatusLabel(2, 'Percona') === 'DISABLED', 'status 2 is DISABLED')
assert(eventStatusLabel(3, 'MariaDB') === 'SLAVESIDE_DISABLED', 'status 3 on MariaDB is SLAVESIDE_DISABLED')
assert(eventStatusLabel(3, 'MySQL') === 'REPLICA_SIDE_DISABLED', 'status 3 on MySQL is REPLICA_SIDE_DISABLED')
assert(eventStatusLabel(3, 'Percona') === 'REPLICA_SIDE_DISABLED', 'status 3 on Percona is REPLICA_SIDE_DISABLED')
assert(eventStatusLabel(0, 'MySQL') === 'UNKNOWN (0)', 'an unexpected value is shown, not hidden')
assert(isMySQLFamily('Percona') && isMySQLFamily('MySQL') && !isMySQLFamily('MariaDB') && !isMySQLFamily(undefined), 'flavor families')

console.log('cellNote')
const replicaOn = { isMaster: false, scheduler: true }
const replicaOff = { isMaster: false, scheduler: false }
const master = { isMaster: true, scheduler: true }
assert(cellNote(undefined, replicaOff) === 'missing', 'an absent event is missing')
assert(cellNote({ status: EVENT_STATUS.ENABLED }, replicaOn) === 'running-on-replica', 'ENABLED on a replica with the scheduler ON runs there')
assert(cellNote({ status: EVENT_STATUS.ENABLED }, replicaOff) === 'enabled-on-replica', 'ENABLED on a replica with the scheduler OFF is reported')
assert(cellNote({ status: EVENT_STATUS.REPLICA_SIDE_DISABLED }, replicaOn) === '', 'replica-side disabled on a replica is the expected state')
assert(cellNote({ status: EVENT_STATUS.DISABLED }, replicaOn) === '', 'DISABLED on a replica is not flagged')
assert(cellNote({ status: EVENT_STATUS.ENABLED }, master) === '', 'ENABLED on the master is not flagged')

console.log('buildEventMatrix')
const servers = [
  { id: 'r1', host: 'db2', port: '3306', dbVersion: { flavor: 'MariaDB' }, eventScheduler: false,
    eventStatus: [{ db: 'app', name: 'purge', definer: 'root@%', status: 3 }, { db: 'app', name: 'stats', definer: 'root@%', status: 1 }] },
  { id: 'm1', host: 'db1', port: '3306', dbVersion: { flavor: 'MariaDB' }, eventScheduler: true,
    eventStatus: [{ db: 'app', name: 'purge', definer: 'root@%', status: 1 }, { db: 'app', name: 'stats', definer: 'root@%', status: 1 }, { db: 'crm', name: 'nightly', definer: 'app@%', status: 2 }] },
  { id: 'r2', host: 'db3', port: '3306', dbVersion: { flavor: 'MariaDB' }, eventScheduler: true, eventStatus: null }
]
const m = buildEventMatrix(servers, 'm1')
assert(m.servers[0].id === 'm1' && m.servers[0].isMaster, 'the master is the first column')
assert(m.servers.length === 3 && m.servers[2].events.length === 0, 'a server without eventStatus has no events')
assert(m.rows.map((r) => r.key).join(',') === 'app.purge,app.stats,crm.nightly', 'one row per event, sorted by schema and name')
const purge = m.rows[0]
assert(purge.cells.m1.label === 'ENABLED' && purge.cells.r1.label === 'SLAVESIDE_DISABLED', 'each cell carries its label')
assert(purge.notes.length === 1 && purge.notes[0].serverId === 'r2' && purge.notes[0].note === 'missing', 'purge is only missing on db3')
const stats = m.rows[1]
assert(stats.cells.r1.note === 'enabled-on-replica', 'stats ENABLED on db2 (scheduler OFF) is reported')
assert(stats.notes.some((n) => n.serverId === 'r2' && n.note === 'missing'), 'stats is missing on db3')
const nightly = m.rows[2]
assert(nightly.notes.filter((n) => n.note === 'missing').length === 2, 'an event only on the master is missing on both replicas')
assert(buildEventMatrix([], 'm1').rows.length === 0 && buildEventMatrix(undefined).servers.length === 0, 'no server gives an empty matrix')

console.log('filterEventRows')
const keys = (rows) => rows.map((r) => r.key).join(',')
assert(eventSchemas(m.rows).join(',') === 'app,crm', 'schemas holding events, sorted, once each')
assert(keys(filterEventRows(m.rows, m.servers)) === 'app.purge,app.stats,crm.nightly', 'no filter keeps every row')
assert(keys(filterEventRows(m.rows, m.servers, { schema: 'crm' })) === 'crm.nightly', 'schema filter is exact')
assert(keys(filterEventRows(m.rows, m.servers, { search: 'STA' })) === 'app.stats', 'search is case-insensitive on the event name')
assert(keys(filterEventRows(m.rows, m.servers, { search: 'cr' })) === 'crm.nightly', 'search matches the schema too')
assert(keys(filterEventRows(m.rows, m.servers, { status: 'replica-side-disabled' })) === 'app.purge', 'status filter: replica-side disabled on any server')
assert(keys(filterEventRows(m.rows, m.servers, { status: 'disabled' })) === 'crm.nightly', 'status filter: DISABLED')
assert(keys(filterEventRows(m.rows, m.servers, { status: 'missing' })) === 'app.purge,app.stats,crm.nightly', 'status filter: missing on any server')
assert(keys(filterEventRows(m.rows, m.servers, { status: 'enabled', schema: 'app', search: 'purge' })) === 'app.purge', 'filters combine')
assert(filterEventRows(m.rows, m.servers, { schema: 'none' }).length === 0, 'no match gives no row')
const many = buildEventMatrix([{ id: 'm', host: 'h', port: '1', eventScheduler: true, eventStatus: Array.from({ length: 5000 }, (_, i) => ({ db: `s${i % 50}`, name: `e${i}`, definer: 'd@%', status: 1 })) }], 'm')
assert(many.rows.length === 5000 && eventSchemas(many.rows).length === 50 && filterEventRows(many.rows, many.servers, { schema: 's7' }).length === 100, 'a large cluster: 5000 events in 50 schemas filter to one schema')

console.log('eventSchedule')
assert(eventSchedule({ eventType: 'ONE TIME', executeAt: '2030-06-01 00:00:00' }) === 'Once, at 2030-06-01 00:00:00', 'one-time event')
assert(eventSchedule({ eventType: 'RECURRING', intervalValue: '1', intervalField: 'DAY', starts: '2030-01-01 00:00:00' }) === 'Every 1 DAY, starting 2030-01-01 00:00:00', 'recurring event without end')
assert(eventSchedule({ eventType: 'RECURRING', intervalValue: '30', intervalField: 'MINUTE', starts: '2030-01-01 00:00:00', ends: '2031-01-01 00:00:00' }) === 'Every 30 MINUTE, starting 2030-01-01 00:00:00, until 2031-01-01 00:00:00', 'recurring event with an end')
assert(eventSchedule({ eventType: 'RECURRING', intervalValue: '1:30', intervalField: 'HOUR_MINUTE' }) === 'Every 1:30 HOUR MINUTE', 'composite interval unit')
assert(eventSchedule(undefined) === '' && eventSchedule({}) === '', 'no schedule gives no text')
assert(eventOnCompletion('PRESERVE').startsWith('kept') && eventOnCompletion('NOT PRESERVE').startsWith('dropped') && eventOnCompletion('') === '', 'on completion')

console.log(`\n${passed} passed, ${failed} failed`)
if (failed > 0) process.exit(1)
