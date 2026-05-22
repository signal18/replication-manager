// AUTO_RELOAD_PAUSE_KEY is written directly by clusterSlice (user-controlled pause).
// The menu/modal lock mechanism uses a separate LOCKS_KEY so the two concerns
// never interfere with each other.
export const AUTO_RELOAD_PAUSE_KEY = 'pause_auto_reload'

const LOCKS_KEY = 'pause_auto_reload_locks'
const TAB_ID_KEY = 'pause_auto_reload_tab_id'

// Locks older than STALE_MS are treated as belonging to a crashed tab.
// No heartbeat is implemented, so a legitimately open lock held for longer
// than this window is also expired. 10 minutes covers realistic menu/modal use.
const STALE_MS = 10 * 60 * 1000

// Stable per-tab identifier. Survives reloads (sessionStorage) but is wiped
// automatically on tab close or crash, so a fresh tab never inherits a
// prior session's lock.
const TAB_ID = (() => {
  let id = sessionStorage.getItem(TAB_ID_KEY)
  if (!id) {
    id = Math.random().toString(36).slice(2)
    sessionStorage.setItem(TAB_ID_KEY, id)
  }
  return id
})()

// Lock entry shape: { count: number, ts: number }

const readLocks = () => {
  try {
    return JSON.parse(localStorage.getItem(LOCKS_KEY) || '{}')
  } catch {
    return {}
  }
}

const writeLocks = (locks) => {
  // Prune stale entries on every write so the map doesn't grow unboundedly.
  const now = Date.now()
  const pruned = Object.fromEntries(
    Object.entries(locks).filter(([, { ts }]) => now - ts <= STALE_MS)
  )
  if (Object.keys(pruned).length === 0) {
    localStorage.removeItem(LOCKS_KEY)
  } else {
    localStorage.setItem(LOCKS_KEY, JSON.stringify(pruned))
  }
  return pruned
}

const hasActiveLock = (locks) => {
  const now = Date.now()
  return Object.values(locks).some(({ count, ts }) => count > 0 && now - ts <= STALE_MS)
}

// Call this instead of reading localStorage directly. Returns true when either
// the user has manually paused auto-reload OR any tab has a menu/modal open.
export const isAutoReloadPaused = () =>
  !!localStorage.getItem(AUTO_RELOAD_PAUSE_KEY) || hasActiveLock(readLocks())

export const acquireAutoReloadPause = () => {
  const locks = readLocks()
  locks[TAB_ID] = { count: (locks[TAB_ID]?.count ?? 0) + 1, ts: Date.now() }
  writeLocks(locks)
}

export const releaseAutoReloadPause = () => {
  const locks = readLocks()
  const current = locks[TAB_ID]?.count ?? 0
  if (current <= 0) return

  const next = current - 1
  if (next === 0) {
    delete locks[TAB_ID]
  } else {
    locks[TAB_ID] = { count: next, ts: Date.now() }
  }
  writeLocks(locks)
}

// Remove this tab's lock on normal close. Hard crashes rely on STALE_MS.
window.addEventListener('beforeunload', () => {
  const locks = readLocks()
  if (!locks[TAB_ID]) return
  delete locks[TAB_ID]
  writeLocks(locks)
})
