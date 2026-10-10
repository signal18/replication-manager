// Display helpers of the Scheduled Database Events section: they lay out the
// answer of GET /api/clusters/{cluster}/schema/events as a matrix event x
// server. The comparison is done by repman (the drifts and the per-server
// comparison come with the answer); nothing here decides whether two events
// differ.

// Collection state of a server, as the answer names it.
export const COLLECTION_LABELS = {
  checked: 'checked',
  unavailable: 'not readable at the last schema scan',
  unsupported: 'not supported for PostgreSQL',
  'not-checked': 'not checked yet'
}

// Drift kinds, as the answer names them, in the order they are shown.
export const DRIFT_LABELS = {
  missing: 'missing on the replica',
  extra: 'only on the replica',
  definition: 'definition differs',
  definer: 'definer differs',
  status: 'enabled on one side, disabled on the other'
}

export const DRIFT_ORDER = Object.keys(DRIFT_LABELS)

// shortCrc shows a definition CRC64 (a decimal string: it does not fit a JS
// number) as its first 8 hex digits, enough to tell two definitions apart by eye.
export const shortCrc = (crc) => {
  if (!crc) return ''
  try {
    return BigInt(crc).toString(16).padStart(16, '0').slice(0, 8)
  } catch {
    return ''
  }
}

// buildEventRows returns { servers, rows } from the answer: the servers in the
// order given (the master first), and one row per event with, per server, a
// cell: 'present' (status class, short CRC), 'absent' (the server was checked
// and does not hold it) or 'not-checked' (the server's events were not read:
// nothing is known), and the drift kinds the answer reports on that server.
export const buildEventRows = (view) => {
  const servers = Array.isArray(view?.servers) ? view.servers : []
  const rows = (Array.isArray(view?.events) ? view.events : []).map((ev) => {
    const drifts = Array.isArray(ev.drifts) ? ev.drifts : []
    const cells = {}
    servers.forEach((srv) => {
      const node = ev.nodes?.[srv.id]
      const cellDrifts = drifts.filter((d) => d.serverId === srv.id).map((d) => d.drift)
      if (!node) {
        cells[srv.id] = { state: 'not-checked', drifts: cellDrifts }
      } else if (!node.present) {
        cells[srv.id] = { state: 'absent', drifts: cellDrifts }
      } else {
        cells[srv.id] = { state: 'present', status: node.status, crc: shortCrc(node.definitionCrc64), drifts: cellDrifts }
      }
    })
    return { key: `${ev.db}.${ev.name}`, db: ev.db, name: ev.name, cells, drifts }
  })
  return { servers, rows }
}

// driftKinds returns the drift kinds of a row, once each, in DRIFT_ORDER.
export const driftKinds = (row) => DRIFT_ORDER.filter((k) => (row?.drifts || []).some((d) => d.drift === k))

// eventSchemas returns the schemas that hold events, sorted, for the schema filter.
export const eventSchemas = (rows) => [...new Set((rows || []).map((r) => r.db))].sort((a, b) => a.localeCompare(b))

// filterEventRows keeps the rows that match every given filter: search (schema
// or event name, case-insensitive), schema (exact), drift (a drift kind, or
// 'any' for a row with at least one drift).
export const filterEventRows = (rows, { search = '', schema = '', drift = '' } = {}) => {
  const term = search.trim().toLowerCase()
  return (rows || []).filter((r) => {
    if (schema && r.db !== schema) return false
    if (term && !r.db.toLowerCase().includes(term) && !r.name.toLowerCase().includes(term)) return false
    if (drift === 'any') return r.drifts.length > 0
    if (drift) return r.drifts.some((d) => d.drift === drift)
    return true
  })
}
