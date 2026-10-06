// Pure helpers of the Events tab: they turn the monitored eventStatus and
// eventScheduler of every server (GET /api/clusters/{cluster}/topology/servers)
// into a matrix event x server. The page is read-only: it reports what the
// servers hold, it changes nothing.

// Event status values, as repman reads them (the ordinal of the status enum:
// mysql.event on MariaDB, the data dictionary behind information_schema.EVENTS
// on MySQL 8).
export const EVENT_STATUS = {
  ENABLED: 1,
  DISABLED: 2,
  REPLICA_SIDE_DISABLED: 3
}

// isMySQLFamily tells MySQL and Percona Server from MariaDB by the server's
// dbVersion.flavor.
export const isMySQLFamily = (flavor) => /mysql|percona/i.test(flavor || '')

// eventStatusLabel is the name the server itself gives the status: state 3 is
// SLAVESIDE_DISABLED on MariaDB and REPLICA_SIDE_DISABLED on MySQL 8.
export const eventStatusLabel = (status, flavor) => {
  switch (status) {
    case EVENT_STATUS.ENABLED:
      return 'ENABLED'
    case EVENT_STATUS.DISABLED:
      return 'DISABLED'
    case EVENT_STATUS.REPLICA_SIDE_DISABLED:
      return isMySQLFamily(flavor) ? 'REPLICA_SIDE_DISABLED' : 'SLAVESIDE_DISABLED'
    default:
      return `UNKNOWN (${status})`
  }
}

export const eventKey = (db, name) => `${db}.${name}`

// cellNote returns the observation for one event on one server, or '':
// - missing: other servers have the event, this one does not;
// - running-on-replica: ENABLED on a replica whose event scheduler is ON, so it
//   runs there;
// - enabled-on-replica: ENABLED on a replica whose scheduler is OFF, so it runs
//   as soon as the scheduler is turned on.
export const cellNote = (cell, server) => {
  if (!cell) return 'missing'
  if (!server.isMaster && cell.status === EVENT_STATUS.ENABLED) {
    return server.scheduler ? 'running-on-replica' : 'enabled-on-replica'
  }
  return ''
}

// buildEventMatrix returns { servers, rows }: servers in the order given, the
// master first, and one row per event found on any server, sorted by schema
// and name, with each server's cell (status, label, note) and the notes found
// on the row.
export const buildEventMatrix = (clusterServers, masterId) => {
  const servers = (clusterServers || [])
    .map((s) => ({
      id: s.id,
      name: `${s.host}:${s.port}`,
      isMaster: s.id === masterId,
      flavor: s.dbVersion?.flavor || '',
      scheduler: !!s.eventScheduler,
      state: s.state,
      events: Array.isArray(s.eventStatus) ? s.eventStatus : []
    }))
    .sort((a, b) => (a.isMaster === b.isMaster ? 0 : a.isMaster ? -1 : 1))

  const rowsByKey = new Map()
  servers.forEach((server) => {
    server.events.forEach((ev) => {
      const key = eventKey(ev.db, ev.name)
      if (!rowsByKey.has(key)) {
        rowsByKey.set(key, { key, db: ev.db, name: ev.name, definer: ev.definer, cells: {} })
      }
      rowsByKey.get(key).cells[server.id] = { status: ev.status, label: eventStatusLabel(ev.status, server.flavor) }
    })
  })

  const rows = [...rowsByKey.values()].sort((a, b) => a.key.localeCompare(b.key))
  rows.forEach((row) => {
    row.notes = []
    servers.forEach((server) => {
      const note = cellNote(row.cells[server.id], server)
      if (row.cells[server.id]) row.cells[server.id].note = note
      if (note) row.notes.push({ serverId: server.id, serverName: server.name, note })
    })
  })
  return { servers, rows }
}

// Status filter values of the matrix; a row matches when the event has that
// status on at least one server ('missing': absent from at least one server).
export const STATUS_FILTERS = [
  { value: '', label: 'Any status' },
  { value: 'enabled', label: 'ENABLED' },
  { value: 'disabled', label: 'DISABLED' },
  { value: 'replica-side-disabled', label: 'Replica-side disabled' },
  { value: 'missing', label: 'Missing' }
]

const statusOfFilter = {
  enabled: EVENT_STATUS.ENABLED,
  disabled: EVENT_STATUS.DISABLED,
  'replica-side-disabled': EVENT_STATUS.REPLICA_SIDE_DISABLED
}

// eventSchemas returns the schemas that hold events, sorted, for the schema
// filter.
export const eventSchemas = (rows) => [...new Set((rows || []).map((r) => r.db))].sort((a, b) => a.localeCompare(b))

// filterEventRows keeps the rows that match every given filter: search (schema
// or event name, case-insensitive), schema (exact), status (see STATUS_FILTERS)
// and observationsOnly.
export const filterEventRows = (rows, servers, { search = '', schema = '', status = '', observationsOnly = false } = {}) => {
  const term = search.trim().toLowerCase()
  return (rows || []).filter((r) => {
    if (observationsOnly && r.notes.length === 0) return false
    if (schema && r.db !== schema) return false
    if (term && !r.db.toLowerCase().includes(term) && !r.name.toLowerCase().includes(term)) return false
    if (status === 'missing') return (servers || []).some((s) => !r.cells[s.id])
    if (status) return Object.values(r.cells).some((c) => c.status === statusOfFilter[status])
    return true
  })
}

// eventSchedule describes when an event runs, from the schedule fields of its
// definition (GET .../events): "Once, at <time>" or "Every <n> <unit>, starting
// <time>[, until <time>]". Unknown or empty fields are left out.
export const eventSchedule = (ev) => {
  if (!ev) return ''
  if (ev.eventType === 'ONE TIME') {
    return ev.executeAt ? `Once, at ${ev.executeAt}` : 'Once'
  }
  if (ev.eventType === 'RECURRING') {
    const unit = (ev.intervalField || '').replace(/_/g, ' ')
    let text = ev.intervalValue ? `Every ${ev.intervalValue} ${unit}`.trim() : 'Recurring'
    if (ev.starts) text += `, starting ${ev.starts}`
    if (ev.ends) text += `, until ${ev.ends}`
    return text
  }
  return ev.eventType || ''
}

// eventOnCompletion explains ON COMPLETION: what happens to the event after its
// last run.
export const eventOnCompletion = (onCompletion) => {
  switch (onCompletion) {
    case 'PRESERVE':
      return 'kept after its last run (ON COMPLETION PRESERVE)'
    case 'NOT PRESERVE':
      return 'dropped after its last run (ON COMPLETION NOT PRESERVE)'
    default:
      return onCompletion || ''
  }
}
