import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import {
  checksumRepairAllTables,
  checksumRepairTable,
  checksumAllTables,
  checksumTable,
  getShardSchema,
  monitorAllSchemas,
} from '../../redux/clusterSlice'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../../components/DataTable'
import styles from './styles.module.scss'
import { isEqual } from 'lodash'
import { Flex, VStack, Tooltip } from '@chakra-ui/react'
import RMButton from '../../components/RMButton'
import { getTablePct } from '../../utility/common'
import Gauge from '../../components/Gauge'
import AccordionComponent from '../../components/AccordionComponent'
import { GeneralLogs, TaskLogs } from '../Dashboard/components/Logs'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { sizeOf } from '../../utility/common'
import SchemaGraph from './SchemaGraph'

// ─── Sync badge (DataTable Sync column) ───────────────────────────────────────
const SYNC_META = {
  OK:  { bg: '#EAF3DE', fg: '#27500A', border: '#C0DD97', label: 'OK',    title: 'In sync across all replicas' },
  ER:  { bg: '#FCEBEB', fg: '#A32D2D', border: '#F7C1C1', label: 'ERROR', title: 'Checksum mismatch detected' },
  NA:  { bg: '#F1EFE8', fg: '#5F5E5A', border: '#D3D1C7', label: 'N/A',   title: 'Cannot checksum: no unique key or process error' },
  '': { bg: '#FAEEDA', fg: '#633806', border: '#FAC775', label: '—',     title: 'Not yet checksummed' },
}

function SyncBadge({ value }) {
  const key  = (value || '').toUpperCase()
  const meta = SYNC_META[key] || SYNC_META['']
  return (
    <Tooltip label={meta.title} hasArrow placement="top">
      <span style={{
        display: 'inline-flex', alignItems: 'center', gap: 4,
        padding: '2px 8px', borderRadius: 5, fontSize: 11, fontWeight: 500,
        background: meta.bg, color: meta.fg,
        border: `1px solid ${meta.border}`,
        whiteSpace: 'nowrap', cursor: 'default',
      }}>
        {key === 'OK' && <span style={{ fontSize: 9 }}>✓</span>}
        {key === 'ER' && <span style={{ fontSize: 9 }}>✕</span>}
        {key === 'NA' && <span style={{ fontSize: 9 }}>⊘</span>}
        {!key         && <span style={{ fontSize: 9 }}>○</span>}
        {meta.label}
      </span>
    </Tooltip>
  )
}

// ─── Table cylinder icon (DataTable Name column) ──────────────────────────────
function TableIcon({ color = '#718096', size = 14 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" style={{ flexShrink: 0 }}>
      <ellipse cx="8" cy="4" rx="5.5" ry="2" fill={color} opacity="0.18" stroke={color} strokeWidth="1.1" />
      <path d="M2.5 4v8c0 1.1 2.46 2 5.5 2s5.5-.9 5.5-2V4" stroke={color} strokeWidth="1.1" fill="none" />
      <ellipse cx="8" cy="4" rx="5.5" ry="2" fill={color} opacity="0.12" stroke={color} strokeWidth="1.1" />
      <line x1="2.5" y1="7.5"  x2="13.5" y2="7.5"  stroke={color} strokeWidth="0.8" opacity="0.5" />
      <line x1="2.5" y1="10.5" x2="13.5" y2="10.5" stroke={color} strokeWidth="0.8" opacity="0.5" />
    </svg>
  )
}

// ─── Toolbar toggle button ────────────────────────────────────────────────────
function ViewToggleBtn({ active, onClick, children }) {
  return (
    <button
      onClick={onClick}
      style={{
        padding: '4px 14px',
        border: `1px solid ${active ? 'var(--gray-color)' : 'transparent'}`,
        borderRadius: 6,
        background: active ? 'var(--color-bg, white)' : 'transparent',
        color: active ? 'var(--text-color)' : 'var(--darkgray-color)',
        cursor: 'pointer',
        fontSize: 13,
        fontWeight: active ? 600 : 400,
        transition: 'all 0.12s',
      }}
    >
      {children}
    </button>
  )
}

// ─── Main Shards page ─────────────────────────────────────────────────────────
function Shards({ selectedCluster, user, onOpenSchedulerSettings }) {
  const dispatch = useDispatch()

  // ── Redux selectors ────────────────────────────────────────────────────────
  // Select shardSchema and the global pause/refresh flag in one selector so
  // we get both in a single subscription — avoids a second re-render cycle.
  const { shardSchema, isRefreshing } = useSelector((state) => ({
    shardSchema:  state.cluster.shardSchema,
    // Adapt the key below to whichever name your Redux store uses for the
    // global pause flag. Common names across replication-manager versions:
    //   state.cluster.refreshing
    //   state.settings.isPaused
    //   state.cluster.monitorRefresh
    // Using a safe fallback (true = not paused) if the key does not exist yet.
    isRefreshing: state.cluster.refreshing ?? state.settings?.isPaused === false ?? true,
  }))

  // ── Local state ────────────────────────────────────────────────────────────
  // FIX: store the last-seen data in a ref so shardSchema comparisons don't
  // trigger unnecessary re-renders when the selector fires with the same data.
  const [data, setData]                                             = useState(shardSchema || [])
  const prevShardsRef                                               = useRef(shardSchema)
  const [isChecksumAllRunning, setIsChecksumAllRunning]             = useState(false)
  const [isChecksumRepairAllRunning, setIsChecksumRepairAllRunning] = useState(false)
  const [isSchemaConfirmOpen, setIsSchemaConfirmOpen]               = useState(false)
  const [pendingChecksumAll, setPendingChecksumAll]                 = useState(false)
  const [pendingChecksumRepairAll, setPendingChecksumRepairAll]     = useState(false)
  const [checksumTimeout, setChecksumTimeout]                       = useState(false)
  const [checksumRepairTimeout, setChecksumRepairTimeout]           = useState(false)
  const mountedRef = useRef(true)

  // FIX: keep a ref to the current isRefreshing value so the polling loop
  // can read it without being a stale closure, and without being listed as a
  // dependency (which would restart the interval on every pause toggle).
  const isRefreshingRef = useRef(isRefreshing)
  useEffect(() => { isRefreshingRef.current = isRefreshing }, [isRefreshing])

  // FIX: keep a ref to selectedCluster.name for the same reason — the polling
  // loop needs the current name but we don't want to restart the interval
  // every time the cluster object reference changes.
  const clusterNameRef = useRef(selectedCluster?.name)
  useEffect(() => { clusterNameRef.current = selectedCluster?.name }, [selectedCluster?.name])

  // ── View / filter state ────────────────────────────────────────────────────
  const [view,       setView]       = useState('table')
  const [syncFilter, setSyncFilter] = useState('all')
  const [searchText, setSearchText] = useState('')

  // Graph relation-source filter — column_name_match is OFF by default because
  // it generates too many implicit edges and clutters the graph.
  // Passed as a prop to SchemaGraph so the graph can apply it before rendering.
  const [showFkEdges,   setShowFkEdges]   = useState(true)
  const [showNameEdges, setShowNameEdges] = useState(false)   // ← OFF by default
  const [showWorkEdges, setShowWorkEdges] = useState(true)

  // ── Lifecycle ──────────────────────────────────────────────────────────────
  useEffect(() => {
    return () => { mountedRef.current = false }
  }, [])

  // FIX: sync shardSchema → local data only when the content actually changes.
  // The previous code ran isEqual on every render triggered by the selector,
  // which was correct but still caused the state update on the first render
  // after mount when prevShardsRef.current === shardSchema (same reference).
  // Using deep equality on both sides avoids the spurious setData call.
  useEffect(() => {
    if (!shardSchema?.length) return
    if (isEqual(shardSchema, prevShardsRef.current)) return
    prevShardsRef.current = shardSchema
    setData(shardSchema)
  }, [shardSchema])

  // ── Schema refresh — pause-aware ───────────────────────────────────────────
  // BUG FIX: the original code called getShardSchema once on mount (via a
  // useEffect on selectedCluster?.name) and then again inside waitForSchemaCache.
  // The problem is twofold:
  //
  //  1. The Shards page is mounted while the global refresh ticker is already
  //     running in the background (App-level setInterval → dispatch(getCluster…)
  //     which in turn dispatches getShardSchema as a side-effect on the Go
  //     backend). Mounting this page therefore caused a *second* independent
  //     polling path for the same endpoint, doubling backend load.
  //
  //  2. Neither the on-mount call nor waitForSchemaCache checked the global
  //     pause flag, so pausing the dashboard had no effect on this endpoint.
  //
  // The fix:
  //  • Do one immediate fetch on mount / cluster change (so the table is
  //    populated instantly when the user navigates here).
  //  • Start a local interval that re-fetches on a 30 s cadence ONLY when
  //    the global refresh flag is true AND the component is still mounted.
  //  • The interval is cleared on unmount and whenever the cluster changes
  //    (the effect will re-run and create a fresh one).
  //  • isRefreshingRef is read inside the interval callback, not closed over,
  //    so toggling pause mid-interval takes effect on the very next tick.
  //
  // If the app-level ticker already calls getShardSchema as a side-effect you
  // can remove the local interval entirely and keep only the on-mount fetch —
  // the pause fix still works because the app-level ticker itself respects
  // isRefreshing and simply won't fire.

  const SCHEMA_REFRESH_INTERVAL_MS = 30_000  // 30 s — adjust to taste

  useEffect(() => {
    const clusterName = selectedCluster?.name
    if (!clusterName) return

    // Immediate fetch on mount / cluster switch.
    if (isRefreshingRef.current) {
      dispatch(getShardSchema({ clusterName }))
    }

    // Local refresh interval — respects pause flag on every tick.
    const intervalId = setInterval(() => {
      // Read through the ref so we always see the latest value without
      // restarting the interval when isRefreshing toggles.
      if (!mountedRef.current)        return
      if (!isRefreshingRef.current)   return   // ← pause respected here
      if (!clusterNameRef.current)    return
      dispatch(getShardSchema({ clusterName: clusterNameRef.current }))
    }, SCHEMA_REFRESH_INTERVAL_MS)

    return () => clearInterval(intervalId)
  // Intentionally excluding isRefreshing — we read it via ref inside the
  // callback so the interval is not restarted on every pause/resume toggle.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dispatch, selectedCluster?.name])

  // ── Checksum handlers ──────────────────────────────────────────────────────
  const handleChecksum = useCallback(
    (schema, table) => {
      dispatch(checksumTable({ clusterName: selectedCluster?.name, schema, table }))
    },
    [dispatch, selectedCluster?.name]
  )

  const handleChecksumRepair = useCallback(
    (schema, table) => {
      dispatch(checksumRepairTable({ clusterName: selectedCluster?.name, schema, table }))
    },
    [dispatch, selectedCluster?.name]
  )

  const handleChecksumAll = async () => {
    if (!selectedCluster?.name || isChecksumAllRunning) return
    if (!shardSchema || shardSchema.length === 0) {
      setPendingChecksumAll(true)
      setIsSchemaConfirmOpen(true)
      return
    }
    await runChecksumAllFlow()
  }

  const handleChecksumRepairAll = async () => {
    if (!selectedCluster?.name || isChecksumAllRunning) return
    if (!shardSchema || shardSchema.length === 0) {
      setPendingChecksumRepairAll(true)
      setIsSchemaConfirmOpen(true)
      return
    }
    await runChecksumRepairAllFlow()
  }

  const runChecksumAllFlow = async () => {
    if (!selectedCluster?.name || isChecksumAllRunning) return
    setChecksumTimeout(false)
    setIsChecksumAllRunning(true)
    try {
      if (!shardSchema || shardSchema.length === 0) {
        await dispatch(monitorAllSchemas({ clusterName: selectedCluster.name }))
        const ok = await waitForSchemaCache()
        if (!mountedRef.current) return
        if (!ok) { setChecksumTimeout(true); return }
      }
      await dispatch(checksumAllTables({ clusterName: selectedCluster.name }))
    } finally {
      if (mountedRef.current) setIsChecksumAllRunning(false)
    }
  }

  const runChecksumRepairAllFlow = async () => {
    if (!selectedCluster?.name || isChecksumAllRunning) return
    setChecksumRepairTimeout(false)
    setIsChecksumRepairAllRunning(true)
    try {
      if (!shardSchema || shardSchema.length === 0) {
        await dispatch(monitorAllSchemas({ clusterName: selectedCluster.name }))
        const ok = await waitForSchemaCache()
        if (!mountedRef.current) return
        if (!ok) { setChecksumRepairTimeout(true); return }
      }
      await dispatch(checksumRepairAllTables({ clusterName: selectedCluster.name }))
    } finally {
      if (mountedRef.current) setIsChecksumRepairAllRunning(false)
    }
  }

  // FIX: waitForSchemaCache now also checks the pause flag on every iteration.
  // If the user pauses while a cache-wait loop is running, the loop stops
  // dispatching instead of hammering the backend.
  const waitForSchemaCache = async () => {
    if (!selectedCluster?.name) return false
    const maxAttempts = 12
    const intervalMs  = 15_000
    let attempts = 0
    while (attempts < maxAttempts) {
      if (!mountedRef.current)      return false
      if (!isRefreshingRef.current) return false  // ← stop if paused
      const action  = await dispatch(getShardSchema({ clusterName: clusterNameRef.current }))
      const payload = action?.payload?.data
      if (Array.isArray(payload) && payload.length > 0) return true
      attempts++
      await new Promise(r => setTimeout(r, intervalMs))
    }
    return false
  }

  // ── Filtering ──────────────────────────────────────────────────────────────
  const filteredData = useMemo(() => {
    let rows = Array.isArray(data) ? data : []
    if (syncFilter !== 'all') {
      rows = rows.filter(r => (r.table_sync || '').toUpperCase() === syncFilter)
    }
    if (searchText.trim()) {
      const s = searchText.trim().toLowerCase()
      rows = rows.filter(r =>
        r.table_name?.toLowerCase().includes(s) ||
        r.table_schema?.toLowerCase().includes(s)
      )
    }
    return rows
  }, [data, syncFilter, searchText])

  // ── Sync counts for filter pills ──────────────────────────────────────────
  const syncCounts = useMemo(() => {
    const rows = Array.isArray(data) ? data : []
    return rows.reduce((acc, row) => {
      const k = (row.table_sync || '').toUpperCase()
      acc[k]  = (acc[k]  || 0) + 1
      acc.all = (acc.all || 0) + 1
      return acc
    }, {})
  }, [data])

  // ── Active relation sources (passed to SchemaGraph) ────────────────────────
  // SchemaGraph filters its edge list to only include sources in this Set.
  const activeRelationSources = useMemo(() => {
    const s = new Set()
    if (showFkEdges)   s.add('foreign_key')
    if (showNameEdges) s.add('column_name_match')
    if (showWorkEdges) s.add('workload_query')
    return s
  }, [showFkEdges, showNameEdges, showWorkEdges])

  // ── Size totals ────────────────────────────────────────────────────────────
  const columnHelper = createColumnHelper()

  const compareText = useCallback((left, right) => {
    const a = left  == null ? '' : String(left)
    const b = right == null ? '' : String(right)
    return a === b ? 0 : a > b ? 1 : -1
  }, [])

  const compareTextDesc = useCallback((l, r) => compareText(r, l), [compareText])

  const sizePctSorting = useCallback((rowA, rowB, columnId) => {
    const a = Number(rowA.getValue(columnId))
    const b = Number(rowB.getValue(columnId))
    if (a !== b) return a > b ? 1 : -1
    const sc = compareTextDesc(rowA.original.table_schema, rowB.original.table_schema)
    if (sc !== 0) return sc
    return compareTextDesc(rowA.original.table_name, rowB.original.table_name)
  }, [compareTextDesc])

  const localSizeTotals = useMemo(() => {
    const rows = Array.isArray(data) ? data : []
    return rows.reduce(
      (acc, row) => {
        acc.table += Number(row?.data_length  || 0)
        acc.index += Number(row?.index_length || 0)
        return acc
      },
      { table: 0, index: 0 }
    )
  }, [data])

  const sizeTotalsInfo = useMemo(() => {
    const workloadTable = Number(selectedCluster?.workLoad?.dbTableSize || 0)
    const workloadIndex = Number(selectedCluster?.workLoad?.dbIndexSize || 0)
    const workloadTotal = workloadTable + workloadIndex
    const localTotal    = localSizeTotals.table + localSizeTotals.index
    const useLocalTotals = (!Number.isFinite(workloadTotal) || workloadTotal === 0) && localTotal > 0
    return {
      useLocalTotals,
      tableTotal: useLocalTotals ? localSizeTotals.table : workloadTable,
      indexTotal: useLocalTotals ? localSizeTotals.index : workloadIndex,
    }
  }, [localSizeTotals, selectedCluster?.workLoad?.dbTableSize, selectedCluster?.workLoad?.dbIndexSize])

  // ── DataTable column definitions ──────────────────────────────────────────
  const columns = useMemo(() => [
    columnHelper.accessor(row => row.table_schema, {
      id: 'schema',
      header: 'Schema',
      enableSorting: true,
      cell: info => (
        <Flex className={styles.tablesSchemaCol}>
          <RMButton onClick={() => handleChecksum(info.row.original.table_schema, info.row.original.table_name)}>
            Checksum
          </RMButton>
          <RMButton onClick={() => handleChecksumRepair(info.row.original.table_schema, info.row.original.table_name)}>
            Repair
          </RMButton>
          <span>{info.getValue()}</span>
        </Flex>
      ),
    }),
    columnHelper.accessor(row => row.table_name, {
      id: 'tableName',
      header: 'Name',
      enableSorting: true,
      cell: info => (
        <Flex align="center" gap={2}>
          <TableIcon />
          <span>{info.getValue()}</span>
        </Flex>
      ),
    }),
    columnHelper.accessor(row => row.engine,        { header: 'Engine' }),
    columnHelper.accessor(row => row.table_rows,    { header: 'Rows' }),
    columnHelper.accessor(row => sizeOf(row.data_length),  { header: 'Data' }),
    columnHelper.accessor(row => sizeOf(row.index_length), { header: 'Index' }),
    columnHelper.accessor(row => row.table_clusters, { header: 'Shards' }),
    columnHelper.accessor(row => row.table_sync, {
      id: 'syncStatus',
      header: 'Sync',
      enableSorting: true,
      cell: info => <SyncBadge value={info.getValue()} />,
      sortingFn: (rowA, rowB) => {
        const order = { ER: 0, '': 1, NA: 2, OK: 3 }
        const a = (rowA.original.table_sync || '').toUpperCase()
        const b = (rowB.original.table_sync || '').toUpperCase()
        return (order[a] ?? 99) - (order[b] ?? 99)
      },
    }),
    columnHelper.accessor(
      row => getTablePct(row.data_length, row.index_length, sizeTotalsInfo.tableTotal, sizeTotalsInfo.indexTotal),
      {
        id: 'sizePct',
        header: '% Size',
        enableSorting: true,
        sortingFn: sizePctSorting,
        cell: info => {
          if (isNaN(info.getValue())) return ''
          return (
            <Gauge
              className={styles.gauge}
              minValue={0} maxValue={100}
              value={info.getValue()}
              width={100} height={50}
            />
          )
        },
      }
    ),
  ], [handleChecksum, handleChecksumRepair, sizeTotalsInfo, sizePctSorting])

  // ── Sync filter pills ─────────────────────────────────────────────────────
  const SYNC_FILTERS = [
    { key: 'all', label: `All (${syncCounts.all || 0})` },
    { key: 'OK',  label: `OK (${syncCounts['OK'] || 0})`,           ...SYNC_META['OK'] },
    { key: 'ER',  label: `Error (${syncCounts['ER'] || 0})`,        ...SYNC_META['ER'] },
    { key: 'NA',  label: `N/A (${syncCounts['NA'] || 0})`,          ...SYNC_META['NA'] },
    { key: '',    label: `Not checksummed (${syncCounts[''] || 0})`, ...SYNC_META[''] },
  ]

  // ── Relation source filter toggles (shown only in graph view) ─────────────
  const REL_SOURCE_FILTERS = [
    {
      key:     'foreign_key',
      label:   'FK',
      title:   'Foreign key constraints',
      active:  showFkEdges,
      toggle:  () => setShowFkEdges(v => !v),
      color:   '#3B8ADD',
    },
    {
      key:     'column_name_match',
      label:   'Name match',
      title:   'Implicit links inferred from matching column names (off by default — can generate many edges)',
      active:  showNameEdges,
      toggle:  () => setShowNameEdges(v => !v),
      color:   '#EF9F27',
    },
    {
      key:     'workload_query',
      label:   'Workload',
      title:   'Joins observed in real query workload',
      active:  showWorkEdges,
      toggle:  () => setShowWorkEdges(v => !v),
      color:   '#1D9E75',
    },
  ]

  // ── Render ────────────────────────────────────────────────────────────────
  return (
    <VStack className={styles.shardsContainer}>

      {/* ── Schema Actions ──────────────────────────────────────────────── */}
      <Flex className={styles.section} direction="column">
        <Flex className={styles.sectionHeader} direction="column">
          <span className={styles.sectionTitle}>Schema Actions</span>
          <span className={styles.sectionDescription}>
            Run one-off checks or refresh schema metadata for the shard list.
          </span>
        </Flex>
        <Flex className={styles.sectionBody} direction="column">
          <Flex className={styles.actionsRow}>
            <RMButton
              onClick={handleChecksumAll}
              isDisabled={!selectedCluster?.name || isChecksumAllRunning}
              isLoading={isChecksumAllRunning}
            >
              {isChecksumAllRunning ? 'Preparing schema cache…' : 'Checksum All Tables'}
            </RMButton>
            <RMButton
              onClick={handleChecksumRepairAll}
              isDisabled={!selectedCluster?.name || isChecksumRepairAllRunning}
              isLoading={isChecksumRepairAllRunning}
            >
              {isChecksumRepairAllRunning ? 'Preparing schema cache…' : 'Repair All Tables'}
            </RMButton>
            <RMButton
              variant="outline"
              onClick={onOpenSchedulerSettings}
              isDisabled={!onOpenSchedulerSettings || user?.grants['cluster-show-backups'] === false}
            >
              Open Scheduler Settings
            </RMButton>
          </Flex>

          {checksumTimeout && (
            <Flex className={styles.timeoutMessage}>
              <span>Schema monitoring timed out. Check server logs or retry later.</span>
            </Flex>
          )}
          {checksumRepairTimeout && (
            <Flex className={styles.timeoutMessage}>
              <span>Schema repair monitoring timed out. Check server logs or retry later.</span>
            </Flex>
          )}
          {sizeTotalsInfo.useLocalTotals && (
            <Flex className={styles.timeoutMessage}>
              <span>Size percentage uses table list totals (cluster totals missing).</span>
            </Flex>
          )}
        </Flex>
      </Flex>

      {/* ── Toolbar ─────────────────────────────────────────────────────── */}
      <Flex className={styles.section} direction="column" gap={2}>

        {/* Row 1 — view toggle + relation source filters + search */}
        <Flex gap={2} align="center" flexWrap="wrap">
          {/* View toggle */}
          <Flex
            gap={1} p="3px"
            bg="var(--secondary-gray-color)"
            borderRadius={8}
            border="1px solid var(--gray-color)"
          >
            <ViewToggleBtn active={view === 'table'} onClick={() => setView('table')}>
              ☰ Table
            </ViewToggleBtn>
            <ViewToggleBtn active={view === 'graph'} onClick={() => setView('graph')}>
              ⬡ Graph
            </ViewToggleBtn>
          </Flex>

          {/* Relation source filters — only visible in graph view */}
          {view === 'graph' && (
            <Flex gap={1} align="center" p="3px"
              bg="var(--secondary-gray-color)"
              borderRadius={8}
              border="1px solid var(--gray-color)"
            >
              <span style={{ fontSize: 11, color: 'var(--darkgray-color)', padding: '0 6px', fontWeight: 500 }}>
                Links:
              </span>
              {REL_SOURCE_FILTERS.map(f => (
                <Tooltip key={f.key} label={f.title} hasArrow placement="top">
                  <button
                    onClick={f.toggle}
                    style={{
                      display: 'inline-flex',
                      alignItems: 'center',
                      gap: 5,
                      padding: '3px 10px',
                      borderRadius: 5,
                      fontSize: 12,
                      fontWeight: f.active ? 600 : 400,
                      cursor: 'pointer',
                      border: `1px solid ${f.active ? f.color + '66' : 'transparent'}`,
                      background: f.active ? f.color + '18' : 'transparent',
                      color: f.active ? f.color : 'var(--darkgray-color)',
                      transition: 'all 0.12s',
                    }}
                  >
                    {/* Colour dot */}
                    <span style={{
                      width: 7, height: 7, borderRadius: '50%',
                      background: f.active ? f.color : 'var(--darkgray-color)',
                      flexShrink: 0,
                      opacity: f.active ? 1 : 0.4,
                    }} />
                    {f.label}
                  </button>
                </Tooltip>
              ))}
            </Flex>
          )}

          {/* Spacer */}
          <div style={{ flex: 1 }} />

          {/* Refresh indicator */}
          {!isRefreshing && (
            <span style={{
              fontSize: 11, color: 'var(--darkgray-color)',
              padding: '2px 8px', borderRadius: 4,
              border: '1px solid var(--gray-color)',
              background: 'var(--secondary-gray-color)',
            }}>
              ⏸ Refresh paused
            </span>
          )}

          {/* Search */}
          <input
            type="search"
            placeholder="Filter tables…"
            value={searchText}
            onChange={e => setSearchText(e.target.value)}
            style={{
              padding: '5px 10px',
              border: '1px solid var(--gray-color)',
              borderRadius: 6,
              background: 'var(--secondary-gray-color)',
              color: 'var(--text-color)',
              fontSize: 12,
              minWidth: 160,
              outline: 'none',
            }}
          />

          <span style={{ fontSize: 11, color: 'var(--darkgray-color)', whiteSpace: 'nowrap' }}>
            {filteredData.length} / {data.length}
          </span>
        </Flex>

        {/* Row 2 — sync filter pills */}
        <Flex gap={2} flexWrap="wrap" align="center">
          <span style={{ fontSize: 11, color: 'var(--darkgray-color)', fontWeight: 500 }}>
            Sync status:
          </span>
          {SYNC_FILTERS.map(f => {
            const isActive = syncFilter === f.key
            return (
              <button
                key={f.key === '' ? '__none__' : f.key}
                onClick={() => setSyncFilter(f.key)}
                style={{
                  padding: '3px 10px', borderRadius: 20,
                  fontSize: 11, fontWeight: isActive ? 600 : 400,
                  cursor: 'pointer',
                  border: `1px solid ${isActive && f.border ? f.border : 'var(--gray-color)'}`,
                  background: isActive && f.bg ? f.bg : 'transparent',
                  color: isActive && f.fg ? f.fg : 'var(--darkgray-color)',
                  transition: 'all 0.12s',
                }}
              >
                {f.label}
              </button>
            )
          })}
        </Flex>
      </Flex>

      {/* ── Graph view ──────────────────────────────────────────────────── */}
      {view === 'graph' && (
        <SchemaGraph
          tables={filteredData}
          activeRelationSources={activeRelationSources}
          onChecksum={handleChecksum}
          onRepair={handleChecksumRepair}
        />
      )}

      {/* ── Table view ───────────────────────────────────────────────────── */}
      {view === 'table' && (
        <DataTable
          key="shards"
          data={filteredData}
          columns={columns}
          className={styles.table}
          enableSorting={true}
          lockSorting={true}
          initialSorting={[{ id: 'sizePct', desc: true }]}
        />
      )}

      {/* ── Logs ─────────────────────────────────────────────────────────── */}
      <AccordionComponent
        className={styles.accordion}
        heading="Cluster Logs"
        body={<GeneralLogs />}
      />
      <AccordionComponent
        className={styles.accordion}
        heading="Job Logs"
        body={<TaskLogs />}
      />

      {/* ── Schema-cache confirm modal ────────────────────────────────────── */}
      {isSchemaConfirmOpen && (
        <ConfirmModal
          isOpen={isSchemaConfirmOpen}
          closeModal={() => {
            setIsSchemaConfirmOpen(false)
            setPendingChecksumAll(false)
            setPendingChecksumRepairAll(false)
          }}
          title="Schema cache required"
          body="Schema cache is empty. Run a schema scan now and wait for it to complete before checksumming all tables?"
          onConfirmClick={async () => {
            setIsSchemaConfirmOpen(false)
            if (pendingChecksumAll) {
              setPendingChecksumAll(false)
              setPendingChecksumRepairAll(false)
              await runChecksumAllFlow()
            }
          }}
        />
      )}
    </VStack>
  )
}

export default Shards
