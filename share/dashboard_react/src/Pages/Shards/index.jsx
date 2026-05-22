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
import RMIconButton from '../../components/RMIconButton'
import { HiCog } from 'react-icons/hi'
import { sizeOf } from '../../utility/common'
import SchemaGraph from './SchemaGraph'
import { useTheme } from '../../ThemeProvider'
import { isAutoReloadPaused } from '../../utility/autoReloadPause'

// ─── Sync badge (DataTable Sync column) ───────────────────────────────────────
// Colours: [bg-light, fg-light, border-light, bg-dark, fg-dark, border-dark]
const SYNC_META = {
  OK:  { light: { bg: '#EAF3DE', fg: '#27500A', border: '#C0DD97' }, dark: { bg: '#1e3314', fg: '#7ec85e', border: '#3a6128' }, label: 'OK',    title: 'In sync across all replicas' },
  ER:  { light: { bg: '#FCEBEB', fg: '#A32D2D', border: '#F7C1C1' }, dark: { bg: '#2d1414', fg: '#ef8080', border: '#6b2828' }, label: 'ERROR', title: 'Checksum mismatch detected' },
  NA:  { light: { bg: '#F1EFE8', fg: '#5F5E5A', border: '#D3D1C7' }, dark: { bg: '#252528', fg: '#a0a09a', border: '#444448' }, label: 'N/A',   title: 'Cannot checksum: no unique key or process error' },
  PR:  { light: { bg: '#EBF4FF', fg: '#1A55A3', border: '#90C3F7' }, dark: { bg: '#0d1e38', fg: '#7ab8ef', border: '#1e4a80' }, label: 'IN PROGRESS', title: 'Checksum in progress' },
  '': { light: { bg: '#FAEEDA', fg: '#633806', border: '#FAC775' }, dark: { bg: '#2e2214', fg: '#d4914a', border: '#6b4020' }, label: '—',     title: 'Not yet checksummed' },
}

function SyncBadge({ value, chunksCount, chunksCurrent }) {
  const { theme } = useTheme()
  const key    = (value || '').toUpperCase()
  const meta   = SYNC_META[key] || SYNC_META['']
  const colors = theme === 'dark' ? meta.dark : meta.light
  const pct    = key === 'PR' && chunksCount > 0 ? Math.round((chunksCurrent / chunksCount) * 100) : null

  return (
    <Tooltip label={pct !== null ? `${chunksCurrent} / ${chunksCount} chunks` : meta.title} hasArrow placement="top">
      <span style={{
        display: 'inline-flex', alignItems: 'center', gap: 4,
        padding: '2px 8px', borderRadius: 5, fontSize: 11, fontWeight: 600,
        background: colors.bg, color: colors.fg,
        border: `1px solid ${colors.border}`,
        whiteSpace: 'nowrap', cursor: 'default',
        flexDirection: 'column', minWidth: pct !== null ? 80 : undefined,
      }}>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
          {key === 'OK' && <span style={{ fontSize: 9 }}>✓</span>}
          {key === 'ER' && <span style={{ fontSize: 9 }}>✕</span>}
          {key === 'NA' && <span style={{ fontSize: 9 }}>⊘</span>}
          {key === 'PR' && <span style={{ fontSize: 9 }}>↻</span>}
          {!key         && <span style={{ fontSize: 9 }}>○</span>}
          {pct !== null ? `${meta.label} ${pct}%` : meta.label}
        </span>
        {pct !== null && (
          <span style={{
            width: '100%', height: 4, borderRadius: 2,
            background: theme === 'dark' ? '#1e4a80' : '#c3def9',
            overflow: 'hidden', display: 'block',
          }}>
            <span style={{
              display: 'block', height: '100%', borderRadius: 2,
              width: `${pct}%`,
              background: theme === 'dark' ? '#7ab8ef' : '#1A55A3',
              transition: 'width 0.3s ease',
            }} />
          </span>
        )}
      </span>
    </Tooltip>
  )
}

function SizeBar({ pct }) {
  const { theme } = useTheme()
  const dark = theme === 'dark'
  if (isNaN(pct) || pct == null) return null

  const trackBg  = dark ? '#2a2a2e' : '#e9e7e0'
  const fillBg   = dark ? '#7ab8ef' : '#1A55A3'
  const textColor = dark ? '#c0bfba' : '#444441'

  return (
    <Tooltip label={`${pct.toFixed(2)}% of total size`} hasArrow placement='top'>
      <span style={{
        display:       'inline-flex',
        flexDirection: 'column',
        alignItems:    'flex-start',
        gap:           3,
        minWidth:      80,
        cursor:        'default',
      }}>
        <span style={{ fontSize: 11, fontWeight: 600, color: textColor, lineHeight: 1 }}>
          {pct.toFixed(2)}%
        </span>
        <span style={{
          width:        '100%',
          height:       5,
          borderRadius: 3,
          background:   trackBg,
          overflow:     'hidden',
          display:      'block',
        }}>
          <span style={{
            display:      'block',
            height:       '100%',
            borderRadius: 3,
            width:        `${Math.min(pct, 100)}%`,
            background:   fillBg,
            transition:   'width 0.3s ease',
          }} />
        </span>
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
function Shards({ selectedCluster, user, onOpenSchedulerSettings, onOpenLogsSettings, onOpenMonitoringSettings }) {
  const dispatch = useDispatch()

  // ── Redux selectors ────────────────────────────────────────────────────────
  const shardSchema = useSelector((state) => state.cluster.shardSchema)
  const baseURL     = useSelector((state) => state.auth?.baseURL || '')

  // pauseAutoReload (clusterSlice) writes ONLY to localStorage, never to Redux
  // state. There is no state.cluster.refreshing field. The correct check —
  // identical to Home/index.jsx::callServices() — is:
  const isPaused = isAutoReloadPaused

  // ── Local state ────────────────────────────────────────────────────────────
  // FIX: store the last-seen data in a ref so shardSchema comparisons don't
  // trigger unnecessary re-renders when the selector fires with the same data.
  const [data, setData]                                             = useState(shardSchema || [])
  const prevShardsRef                                               = useRef(null)
  const [isChecksumAllRunning, setIsChecksumAllRunning]             = useState(false)
  const [isChecksumRepairAllRunning, setIsChecksumRepairAllRunning] = useState(false)
  const [isSchemaConfirmOpen, setIsSchemaConfirmOpen]               = useState(false)
  const [pendingChecksumAll, setPendingChecksumAll]                 = useState(false)
  const [pendingChecksumRepairAll, setPendingChecksumRepairAll]     = useState(false)
  const [checksumTimeout, setChecksumTimeout]                       = useState(false)
  const [checksumRepairTimeout, setChecksumRepairTimeout]           = useState(false)
  const mountedRef = useRef(true)

  // ── Master server name — needed for time-machine PFS API calls ────────────
  // The cluster object carries master info in different shapes depending on
  // the backend version; we try the most-specific field first.
  const masterServerName = useMemo(() => {
    if (!selectedCluster) return ''
    // Prefer explicit master object.
    if (selectedCluster.master?.name) return selectedCluster.master.name
    // Fall back to first server marked as master in the servers list.
    const masterSrv = (selectedCluster.servers || []).find(
      s => s.isMaster || s.state === 'Master' || s.state === 'master'
    )
    if (masterSrv?.name) return masterSrv.name
    // Last resort: use host:port of the master.
    return selectedCluster.masterHost || ''
  }, [selectedCluster])

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

  // ── Schema refresh — pause-aware ──────────────────────────────────────────
  // Home/index.jsx already calls getShardSchema on every global ticker tick
  // whenever the Shards tab is active (Home/index.jsx line ~208). Running a
  // second independent setInterval here creates a duplicate polling path that
  // bypasses the pause flag entirely.
  //
  // The correct approach:
  //   • One immediate fetch on mount / cluster switch so the table is not
  //     empty while the user waits for the next global tick. Guard it with
  //     the same localStorage check that Home uses.
  //   • No local interval — let Home drive all subsequent refreshes.
  //   • waitForSchemaCache (used by checksum flows) also checks isPaused()
  //     on every iteration so it stops if the user pauses mid-wait.
  useEffect(() => {
    const clusterName = selectedCluster?.name
    if (!clusterName) return
    // Only fetch immediately if refresh is not paused.
    if (!isPaused()) {
      dispatch(getShardSchema({ clusterName }))
    }
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
      if (!mountedRef.current) return false
      if (isPaused())          return false  // stop polling if user paused
      const action  = await dispatch(getShardSchema({ clusterName: selectedCluster.name }))
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
        acc.free  += Number(row?.data_free    || 0)
        return acc
      },
      { table: 0, index: 0, free: 0 }
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
    columnHelper.accessor(row => sizeOf(row.data_free),    { header: 'Free' }),
    columnHelper.accessor(
      row => {
        const total = (row.data_length || 0) + (row.index_length || 0)
        if (total === 0) return 0
        // Max of data_free ratio and row-based estimate
        const freeRatio = (row.data_free || 0) / total * 100
        const rowBased = row.data_length > 0 && row.table_rows > 0 && row.avg_row_length > 0
          ? Math.max(0, (row.data_length - row.table_rows * row.avg_row_length) / row.data_length * 100)
          : 0
        return Math.round(Math.max(freeRatio, rowBased) * 10) / 10
      },
      {
        id: 'fragPct',
        header: 'Frag %',
        enableSorting: true,
        cell: info => {
          const v = info.getValue()
          if (v === 0) return <span style={{ color: 'var(--darkgray-color)' }}>—</span>
          const color = v > 30 ? '#e53e3e' : v > 10 ? '#dd6b20' : 'inherit'
          return <span style={{ color, fontWeight: v > 10 ? 600 : 400 }}>{v}%</span>
        },
      }
    ),
    columnHelper.accessor(row => row.table_clusters, { header: 'Shards' }),
    columnHelper.accessor(row => row.table_sync, {
      id: 'syncStatus',
      header: 'Sync',
      enableSorting: true,
      cell: info => (
        <SyncBadge
          value={info.getValue()}
          chunksCount={info.row.original.table_chunks_count}
          chunksCurrent={info.row.original.table_chunks_current}
        />
      ),
      sortingFn: (rowA, rowB) => {
      const order = { PR: 0, ER: 1, '': 2, NA: 3, OK: 4 }
      const a = (rowA.original.table_sync || '').toUpperCase()
      const b = (rowB.original.table_sync || '').toUpperCase()
      const diff = (order[a] ?? 99) - (order[b] ?? 99)
      if (diff !== 0) return diff
      // Both PR: sort by progress percentage descending (most advanced first)
      if (a === 'PR' && b === 'PR') {
      const countA = rowA.original.table_chunks_count   || 0
      const currA  = rowA.original.table_chunks_current || 0
      const countB = rowB.original.table_chunks_count   || 0
      const currB  = rowB.original.table_chunks_current || 0
      const pctA = countA > 0 ? currA / countA : 0
      const pctB = countB > 0 ? currB / countB : 0
      return pctB - pctA
      }
      return 0
      },
    }),
    columnHelper.accessor(
      row => getTablePct(row.data_length, row.index_length, sizeTotalsInfo.tableTotal, sizeTotalsInfo.indexTotal),
      {
        id: 'sizePct',
        header: '% Size',
        enableSorting: true,
        sortingFn: sizePctSorting,
        cell: info => <SizeBar pct={info.getValue()} />,
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
              isDisabled={!selectedCluster?.name || isChecksumAllRunning || !user?.grants['cluster-checksum']}
              isLoading={isChecksumAllRunning}
            >
              {isChecksumAllRunning ? 'Preparing schema cache…' : 'Checksum All Tables'}
            </RMButton>
            <RMButton
              onClick={handleChecksumRepairAll}
              isDisabled={!selectedCluster?.name || isChecksumRepairAllRunning || !user?.grants['cluster-checksum-repair']}
              isLoading={isChecksumRepairAllRunning}
            >
              {isChecksumRepairAllRunning ? 'Preparing schema cache…' : 'Repair All Tables'}
            </RMButton>
            <RMButton
              variant="outline"
              onClick={onOpenSchedulerSettings}
              isDisabled={!onOpenSchedulerSettings || !user?.grants['cluster-show-backups']}
            >
              Open Scheduler Settings
            </RMButton>
            {onOpenMonitoringSettings && (
              <RMIconButton icon={HiCog} tooltip='Monitoring Settings' onClick={onOpenMonitoringSettings} size='sm' variant='ghost' />
            )}
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
        </Flex>
      </Flex>

      {/* ── Size summary ──────────────────────────────────────────────── */}
      {(sizeTotalsInfo.tableTotal > 0 || sizeTotalsInfo.indexTotal > 0) && (
        <Flex gap={4} px={3} py={2} fontSize='sm' color='gray.600'>
          <span><strong>Total Data:</strong> {sizeOf(sizeTotalsInfo.tableTotal)}</span>
          <span><strong>Total Index:</strong> {sizeOf(sizeTotalsInfo.indexTotal)}</span>
          <span><strong>Total:</strong> {sizeOf(sizeTotalsInfo.tableTotal + sizeTotalsInfo.indexTotal)}</span>
          {localSizeTotals.free > 0 && (
            <span><strong>Free (reclaimable):</strong> {sizeOf(localSizeTotals.free)}</span>
          )}
        </Flex>
      )}

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

          {/* Refresh indicator — reads the same localStorage flag as Home */}
          {isPaused() && (
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
          clusterName={selectedCluster?.name || ''}
          serverName={masterServerName}
          baseURL={baseURL}
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
          initialSorting={[{ id: 'syncStatus', desc: false }]}
        />
      )}

      {/* ── Logs ─────────────────────────────────────────────────────────── */}
      <AccordionComponent
        className={styles.accordion}
        heading="Cluster Logs"
        headerActions={onOpenLogsSettings ? <RMIconButton icon={HiCog} tooltip='Log Settings' onClick={onOpenLogsSettings} size='xs' variant='ghost' /> : null}
        body={<GeneralLogs />}
      />
      <AccordionComponent
        className={styles.accordion}
        heading="Job Logs"
        headerActions={onOpenLogsSettings ? <RMIconButton icon={HiCog} tooltip='Log Settings' onClick={onOpenLogsSettings} size='xs' variant='ghost' /> : null}
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
