/**
 * SchemaGraph.jsx
 * ─────────────────────────────────────────────────────────────────────────────
 * Graph / ERD view for the Shards page.
 *
 * Props:
 *   tables               Array<Table>   — filtered table list from index.jsx
 *   activeRelationSources Set<string>   — which edge sources to render;
 *                                         e.g. new Set(['foreign_key','workload_query'])
 *                                         'column_name_match' is OFF by default (controlled
 *                                         in index.jsx, passed here so SchemaGraph is
 *                                         stateless w.r.t. source filtering)
 *   onChecksum           Function(schema, table)
 *   onRepair             Function(schema, table)
 *
 * JSON contract (Go patch — fields on every Table):
 *   table_parents   []TableLink  — outgoing edges (this table references another)
 *   table_children  []TableLink  — incoming edges (another references this table)
 *   size_weight_pct float64
 *
 * TableLink:
 *   linked_schema, linked_table, local_columns[], remote_columns[],
 *   relation_name, relation_source, cardinality, join_weight_pct?
 *
 * Theme: useTheme() from src/ThemeProvider.jsx (custom, not Chakra).
 * No external graph library — pure SVG + minimal force simulation.
 * ─────────────────────────────────────────────────────────────────────────────
 */

import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useTheme } from '../../ThemeProvider'

// ─── Layout constants ─────────────────────────────────────────────────────────
const CANVAS_W    = 2400   // wider canvas gives the force layout more room
const CANVAS_H    = 1800   // taller canvas — user pans to explore
const NW_SIMPLE   = 152
const NH_SIMPLE   = 58
const NW_DETAILED = 234
const ROW_H       = 24
const HDR_H       = 42
const FORCE_TICKS = 400    // more ticks → better separation
const REPULSION   = 55000  // stronger repulsion pushes nodes further apart
const SPRING_LEN  = 380    // longer natural spring length
const SPRING_K    = 0.018  // softer spring so repulsion wins near-overlap
const DAMPING     = 0.78

// ─── Theme palette — [lightValue, darkValue] ──────────────────────────────────
const T = {
  canvasBg:   ['#f4f4f1', '#16161c'],
  nodeBg:     ['#ffffff', '#22222c'],
  nodeBorder: ['#e2e0d8', '#38384a'],
  nodeStripe: ['#f8f8f6', '#28283a'],
  textPri:    ['#1a1a18', '#e8e6e0'],
  textSec:    ['#666460', '#9a9890'],
  textMut:    ['#a0a09a', '#58586a'],
  ctrlBg:     ['#ffffff', '#22222c'],
  ctrlBorder: ['#dddbd2', '#38384a'],
  shadow:     ['rgba(0,0,0,0.07)', 'rgba(0,0,0,0.36)'],
  edgeFk:     '#3B8ADD',
  edgeNm:     '#EF9F27',
  edgeWq:     '#1D9E75',
}

// Schema accent palette — [fill-L, text-L, border-L, fill-D, text-D, border-D]
const SCHEMA_PAL = [
  ['#E6F1FB','#0C447C','#B5D4F4','#0d2340','#7ab8ef','#1e4270'],
  ['#E1F5EE','#085041','#9FE1CB','#0a2820','#5ec9a8','#1a5040'],
  ['#FAEEDA','#633806','#FAC775','#2e1e08','#d4914a','#6b4020'],
  ['#FAECE7','#712B13','#F5C4B3','#2e1208','#d4765a','#6b2818'],
  ['#EEEDFE','#3C3489','#CECBF6','#1a1840','#9b94e8','#303078'],
  ['#EAF3DE','#27500A','#C0DD97','#132808','#7ec85e','#285018'],
  ['#FBEAF0','#72243E','#F4C0D1','#2e101e','#d47898','#5a1830'],
  ['#F1EFE8','#444441','#D3D1C7','#222220','#9a9890','#383836'],
]

// Sync colours — [bg-L, fg-L, bd-L, bg-D, fg-D, bd-D]
const SYNC = {
  OK:  ['#EAF3DE','#27500A','#C0DD97','#1e3314','#7ec85e','#3a6128'],
  ER:  ['#FCEBEB','#A32D2D','#F7C1C1','#2d1414','#e07070','#6b2828'],
  NA:  ['#F1EFE8','#5F5E5A','#D3D1C7','#222228','#8a8882','#444448'],
  PR:  ['#EBF4FF','#1A55A3','#90C3F7','#0d1e38','#7ab8ef','#1e4a80'],
  '': ['#FAEEDA','#633806','#FAC775','#2e2214','#d4914a','#6b4020'],
}

// ─── Pure helpers ─────────────────────────────────────────────────────────────
const p = (arr, dark) => dark ? arr[1] : arr[0]

function schemaPal(schema, schemaList, dark) {
  const i = schemaList.indexOf(schema)
  const c = SCHEMA_PAL[i % SCHEMA_PAL.length]
  return dark
    ? { fill: c[3], text: c[4], border: c[5] }
    : { fill: c[0], text: c[1], border: c[2] }
}

function syncMeta(value, dark) {
  const k   = (value || '').toUpperCase()
  const c   = SYNC[k] || SYNC['']
  const off = dark ? 3 : 0
  return {
    bg:    c[0 + off],
    fg:    c[1 + off],
    bd:    c[2 + off],
    icon:  k === 'OK' ? '✓' : k === 'ER' ? '✕' : k === 'NA' ? '⊘' : k === 'PR' ? '↻' : '○',
    label: k === 'OK' ? 'OK' : k === 'ER' ? 'ERROR' : k === 'NA' ? 'N/A' : k === 'PR' ? 'IN PROGRESS' : '—',
  }
}

function edgeColor(src) {
  return src === 'foreign_key' ? T.edgeFk
       : src === 'column_name_match' ? T.edgeNm
       : T.edgeWq
}
function edgeDash(src) {
  return src === 'foreign_key'       ? null
       : src === 'column_name_match' ? '7 4'
       : '3 6'
}
function edgeMid(src) {
  return src === 'foreign_key' ? 'fk' : src === 'column_name_match' ? 'nm' : 'wq'
}
function cardLabel(c) {
  return c === '1-1' ? '1:1' : c === 'N-N' ? 'N:N' : '1:N'
}

function typeBadge(t = '', dark) {
  const l = t.toLowerCase()
  if (/int|decimal|float|double|numeric/.test(l))
    return dark ? { bg:'#0d2340', fg:'#7ab8ef' } : { bg:'#E6F1FB', fg:'#0C447C' }
  if (/varchar|char|text|enum|set/.test(l))
    return dark ? { bg:'#132808', fg:'#7ec85e' } : { bg:'#EAF3DE', fg:'#27500A' }
  if (/date|time|timestamp|year/.test(l))
    return dark ? { bg:'#2e1e08', fg:'#d4914a' } : { bg:'#FAEEDA', fg:'#633806' }
  if (/bool|bit/.test(l))
    return dark ? { bg:'#1a1840', fg:'#9b94e8' } : { bg:'#EEEDFE', fg:'#3C3489' }
  if (/blob|binary|json/.test(l))
    return dark ? { bg:'#2e1208', fg:'#d4765a' } : { bg:'#FAECE7', fg:'#712B13' }
  return dark ? { bg:'#222220', fg:'#9a9890' } : { bg:'#F1EFE8', fg:'#444441' }
}

function fmtBytes(b) {
  if (!b) return '0 B'
  const u = ['B','KB','MB','GB','TB']
  let i = 0, v = b
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(1)} ${u[i]}`
}
function fmtRows(n) {
  if (!n) return '0'
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`
  return String(n)
}
function clamp(v, mn, mx) { return Math.max(mn, Math.min(mx, v)) }

// ─── Build flat de-duplicated edge list from table_parents ────────────────────
// activeRelationSources: Set<string> — only sources in this set are included.
function buildEdges(tables, activeRelationSources) {
  const seen = new Set()
  const out  = []
  for (const t of tables) {
    for (const lk of (t.table_parents || [])) {
      // ← Filter by active relation sources before adding to the list
      if (activeRelationSources && !activeRelationSources.has(lk.relation_source)) continue
      const key = `${t.table_name}|${lk.linked_table}|${lk.relation_name}`
      if (seen.has(key)) continue
      seen.add(key)
      out.push({
        childTable:   t.table_name,
        childSchema:  t.table_schema,
        parentTable:  lk.linked_table,
        parentSchema: lk.linked_schema,
        source:       lk.relation_source,
        cardinality:  lk.cardinality,
        joinWeight:   lk.join_weight_pct || 0,
        childCols:    lk.local_columns  || [],
        parentCols:   lk.remote_columns || [],
        name:         lk.relation_name,
      })
    }
  }
  return out
}

// ─── Force simulation ─────────────────────────────────────────────────────────
function nodeH(t, mode) {
  return mode === 'detailed' ? HDR_H + (t.table_columns?.length || 0) * ROW_H + 8 : NH_SIMPLE
}
function nodeW(mode) { return mode === 'detailed' ? NW_DETAILED : NW_SIMPLE }

// Gap maintained between any two node bounding boxes (px in canvas space).
const NODE_GAP = 60

// runLayout:
//   tables  — current table list
//   edges   — edge list for spring attraction
//   mode    — 'simple' | 'detailed'
//   prevPos — positions from the previous layout run (keyed by table_name).
//             Tables already present keep their positions; only new arrivals
//             are placed on the initial circle. Pass {} or null on first run.
//
// Stability guarantee: when only data changes (table_sync, size_weight_pct,
// etc.) but the set of table names is unchanged, the caller passes the same
// prevPos and returns immediately — no simulation, no jitter.
function runLayout(tables, edges, mode, prevPos = {}) {
  const nw = nodeW(mode)
  const cx = CANVAS_W / 2, cy = CANVAS_H / 2
  const r  = Math.min(CANVAS_W, CANVAS_H) * 0.28

  // Separate known tables (keep position) from new ones (place on circle).
  const newTables = tables.filter(t => !prevPos[t.table_name])

  // If no table is new and mode hasn't changed, return prevPos unchanged.
  // This is the key stability guarantee: data refreshes don't move nodes.
  if (newTables.length === 0 && Object.keys(prevPos).length >= tables.length) {
    // Return a copy containing only the tables in the current list
    // (handles tables that were removed by a filter).
    const filtered = {}
    tables.forEach(t => { if (prevPos[t.table_name]) filtered[t.table_name] = { ...prevPos[t.table_name] } })
    return filtered
  }

  // Seed positions: known tables keep their place, new ones go on the circle.
  const pos = {}
  let newIdx = 0
  tables.forEach(t => {
    if (prevPos[t.table_name]) {
      pos[t.table_name] = { ...prevPos[t.table_name], vx: 0, vy: 0 }
    } else {
      // Spread new arrivals evenly among the new-table slots.
      const a = (2 * Math.PI * newIdx) / Math.max(newTables.length, 1)
      pos[t.table_name] = { x: cx + r * Math.cos(a), y: cy + r * Math.sin(a), vx: 0, vy: 0 }
      newIdx++
    }
  })

  // Run simulation. If all tables are known we still run a short settling
  // pass so newly-filtered sets reach equilibrium without jitter.
  const ticks = newTables.length > 0 ? FORCE_TICKS : 60

  for (let tick = 0; tick < ticks; tick++) {
    const alpha = 1 - tick / ticks

    // Box-aware repulsion — push nodes apart until their bounding boxes
    // are separated by at least NODE_GAP pixels in both axes.
    for (let i = 0; i < tables.length; i++) {
      for (let j = i + 1; j < tables.length; j++) {
        const ta = tables[i], tb = tables[j]
        const a  = pos[ta.table_name], b = pos[tb.table_name]

        const halfWA = nw / 2, halfHA = nodeH(ta, mode) / 2
        const halfWB = nw / 2, halfHB = nodeH(tb, mode) / 2

        const dx = b.x - a.x, dy = b.y - a.y
        const dist = Math.sqrt(dx * dx + dy * dy) || 1

        // Minimum centre-to-centre distance that keeps boxes NODE_GAP apart.
        // Use the axis-dominant direction for the clearance estimate.
        const minDist = halfWA + halfWB + NODE_GAP +
          (Math.abs(dy) > Math.abs(dx) ? halfHA + halfHB - halfWA - halfWB : 0)

        if (dist < minDist) {
          // Overlap or too close — apply a strong separating impulse.
          const overlap = (minDist - dist) / dist
          const fx = dx * overlap * 0.5 * alpha
          const fy = dy * overlap * 0.5 * alpha
          a.vx -= fx; a.vy -= fy
          b.vx += fx; b.vy += fy
        } else {
          // Normal inverse-square repulsion for spacing at distance.
          const f = REPULSION / (dist * dist) * alpha
          a.vx -= dx / dist * f; a.vy -= dy / dist * f
          b.vx += dx / dist * f; b.vy += dy / dist * f
        }
      }
    }

    // Spring attraction along edges.
    for (const e of edges) {
      const a = pos[e.childTable], b = pos[e.parentTable]
      if (!a || !b) continue
      const dx = b.x - a.x, dy = b.y - a.y
      const dist = Math.sqrt(dx * dx + dy * dy) || 1
      const f = SPRING_K * (dist - SPRING_LEN) * alpha
      a.vx += dx / dist * f; a.vy += dy / dist * f
      b.vx -= dx / dist * f; b.vy -= dy / dist * f
    }

    // Integrate velocities, damp, clamp to canvas bounds.
    tables.forEach(t => {
      const q  = pos[t.table_name]
      const nh = nodeH(t, mode) / 2
      const hw = nw / 2
      q.vx *= DAMPING; q.vy *= DAMPING
      q.x = clamp(q.x + q.vx, hw + NODE_GAP, CANVAS_W - hw - NODE_GAP)
      q.y = clamp(q.y + q.vy, nh + NODE_GAP, CANVAS_H - nh - NODE_GAP)
    })
  }

  return pos
}

// ─── Edge geometry ────────────────────────────────────────────────────────────
function rectPt(cx, cy, w, h, tx, ty) {
  const dx = tx - cx, dy = ty - cy
  const s  = Math.min(dx ? w / 2 / Math.abs(dx) : 1e9, dy ? h / 2 / Math.abs(dy) : 1e9)
  return [cx + dx * s, cy + dy * s]
}
function mkEdgePath(ax, ay, aw, ah, bx, by, bw, bh) {
  const c1x = ax + aw / 2, c1y = ay + ah / 2
  const c2x = bx + bw / 2, c2y = by + bh / 2
  const [x1, y1] = rectPt(c1x, c1y, aw, ah, c2x, c2y)
  const [x2, y2] = rectPt(c2x, c2y, bw, bh, c1x, c1y)
  const mx = (x1 + x2) / 2 - (c2y - c1y) * 0.06
  const my = (y1 + y2) / 2 + (c2x - c1x) * 0.06
  return {
    d: `M${x1.toFixed(1)},${y1.toFixed(1)} Q${mx.toFixed(1)},${my.toFixed(1)} ${x2.toFixed(1)},${y2.toFixed(1)}`,
    mx, my,
  }
}

// ─── Tiny DB cylinder icon ────────────────────────────────────────────────────
function DbIcon({ x, y, color, size = 14 }) {
  const s = size / 14
  return (
    <g transform={`translate(${x},${y}) scale(${s})`}>
      <ellipse cx="7" cy="3.5" rx="5.5" ry="2" fill={color} opacity="0.18" stroke={color} strokeWidth="0.9" />
      <path d="M1.5 3.5v8c0 1.1 2.46 2 5.5 2s5.5-.9 5.5-2v-8" stroke={color} strokeWidth="0.9" fill="none" />
      <ellipse cx="7" cy="3.5" rx="5.5" ry="2" fill={color} opacity="0.10" stroke={color} strokeWidth="0.9" />
      <line x1="1.5" y1="7.2"  x2="12.5" y2="7.2"  stroke={color} strokeWidth="0.7" opacity="0.45" />
      <line x1="1.5" y1="10.2" x2="12.5" y2="10.2" stroke={color} strokeWidth="0.7" opacity="0.45" />
    </g>
  )
}

// ─── Small atoms ─────────────────────────────────────────────────────────────
function SyncPill({ value, dark }) {
  const m = syncMeta(value, dark)
  return (
    <span title={m.tip} style={{
      display: 'inline-flex', alignItems: 'center', gap: 3,
      padding: '2px 7px', borderRadius: 5, fontSize: 10, fontWeight: 700,
      background: m.bg, color: m.fg, border: `1px solid ${m.bd}`,
      whiteSpace: 'nowrap',
    }}>
      {m.icon} {m.label}
    </span>
  )
}

function TBtn({ active, onClick, children }) {
  return (
    <button onClick={onClick} style={{
      padding: '4px 12px',
      border: `1px solid ${active ? 'var(--gray-color)' : 'transparent'}`,
      borderRadius: 6,
      background: active ? 'var(--body-bg-color)' : 'transparent',
      color: 'var(--text-color)',
      cursor: 'pointer', fontSize: 12, fontWeight: active ? 600 : 400,
      transition: 'all .12s',
    }}>
      {children}
    </button>
  )
}

function TBtnGroup({ children }) {
  return (
    <div style={{
      display: 'flex', gap: 2, padding: 3,
      background: 'var(--secondary-gray-color)',
      border: '1px solid var(--gray-color)',
      borderRadius: 8,
    }}>
      {children}
    </div>
  )
}

function SectionLabel({ label }) {
  return (
    <div style={{
      padding: '8px 13px 3px', fontSize: 10, fontWeight: 700,
      letterSpacing: '0.05em', textTransform: 'uppercase',
      color: 'var(--darkgray-color)',
    }}>
      {label}
    </div>
  )
}

function ActionBtn({ label, onClick, bgL, fgL, bgD, fgD, dark }) {
  return (
    <button onClick={onClick} style={{
      flex: 1, padding: '4px 0',
      border: `1px solid ${(dark ? fgD : fgL)}44`,
      borderRadius: 5,
      background: dark ? bgD : bgL,
      color: dark ? fgD : fgL,
      fontSize: 11, fontWeight: 600, cursor: 'pointer',
    }}>
      {label}
    </button>
  )
}

// ─── Main export ─────────────────────────────────────────────────────────────
export default function SchemaGraph({
  tables = [],
  activeRelationSources,   // Set<string> — controlled by index.jsx
  onChecksum,
  onRepair,
}) {
  const { theme } = useTheme()
  const dark = theme === 'dark'

  const [graphMode,  setGraphMode]  = useState('simple')
  const [weightMode, setWeightMode] = useState('size')

  const schemas = useMemo(() => [...new Set(tables.map(t => t.table_schema))], [tables])

  // Build edge list, filtered by activeRelationSources from the parent.
  // Re-computed whenever tables or the active source set changes.
  const edges = useMemo(
    () => buildEdges(tables, activeRelationSources),
    [tables, activeRelationSources]
  )

  // Edges filtered to visible tables only (tables is already filtered by
  // index.jsx's syncFilter + searchText — no need to filter again here).
  const visNames = useMemo(() => new Set(tables.map(t => t.table_name)), [tables])
  const visEdges = useMemo(
    () => edges.filter(e => visNames.has(e.childTable) && visNames.has(e.parentTable)),
    [edges, visNames]
  )

  const totalBytes = useMemo(
    () => tables.reduce((s, t) => s + (t.data_length || 0) + (t.index_length || 0), 0),
    [tables]
  )
  const fkCount   = useMemo(() => edges.filter(e => e.source === 'foreign_key').length, [edges])
  const implCount = useMemo(() => edges.filter(e => e.source !== 'foreign_key').length, [edges])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10, width: '100%' }}>

      {/* ── Stats bar ──────────────────────────────────────────────────── */}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        {[
          ['Tables',     tables.length],
          ['Total size', fmtBytes(totalBytes)],
          ['FK edges',   fkCount],
          ['Inferred',   implCount],
          ['Schemas',    schemas.length],
        ].map(([l, v]) => (
          <div key={l} style={{
            flex: '1 1 0', minWidth: 88,
            padding: '9px 12px',
            background: 'var(--secondary-gray-color)',
            border: '1px solid var(--gray-color)',
            borderRadius: 10,
          }}>
            <div style={{ fontSize: 10, color: 'var(--darkgray-color)', fontWeight: 500 }}>{l}</div>
            <div style={{ fontSize: 18, fontWeight: 700, color: 'var(--text-color)', marginTop: 2 }}>{v}</div>
          </div>
        ))}
      </div>

      {/* ── Graph mode toolbar ─────────────────────────────────────────── */}
      <div style={{
        display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap',
        padding: '8px 12px',
        background: 'var(--secondary-gray-color)',
        border: '1px solid var(--gray-color)',
        borderRadius: 10,
      }}>
        <TBtnGroup>
          <TBtn active={graphMode === 'simple'}   onClick={() => setGraphMode('simple')}>Simple</TBtn>
          <TBtn active={graphMode === 'detailed'} onClick={() => setGraphMode('detailed')}>Detailed</TBtn>
        </TBtnGroup>

        {graphMode === 'simple' && (
          <TBtnGroup>
            <TBtn active={weightMode === 'size'}  onClick={() => setWeightMode('size')}>Size %</TBtn>
            <TBtn active={weightMode === 'usage'} onClick={() => setWeightMode('usage')}>Usage %</TBtn>
          </TBtnGroup>
        )}

        <div style={{ flex: 1 }} />

        {/* Schema colour key */}
        {schemas.length > 1 && (
          <div style={{ display: 'flex', gap: 5, flexWrap: 'wrap' }}>
            {schemas.map(s => {
              const pal = schemaPal(s, schemas, dark)
              return (
                <span key={s} style={{
                  padding: '2px 8px', borderRadius: 4, fontSize: 11, fontWeight: 600,
                  background: pal.fill, color: pal.text, border: `1px solid ${pal.border}`,
                }}>{s}</span>
              )
            })}
          </div>
        )}

        {/* Edge legend — reflects only the active sources */}
        {[
          { src: 'foreign_key',       col: T.edgeFk, dash: null,  label: 'Foreign key' },
          { src: 'column_name_match', col: T.edgeNm, dash: '7 4', label: 'Name match'  },
          { src: 'workload_query',    col: T.edgeWq, dash: '3 6', label: 'Workload'     },
        ]
          .filter(it => !activeRelationSources || activeRelationSources.has(it.src))
          .map(it => (
            <div key={it.src} style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
              <svg width="24" height="10" style={{ overflow: 'visible' }}>
                <line x1="0" y1="5" x2="24" y2="5" stroke={it.col} strokeWidth="1.5"
                  strokeDasharray={it.dash || undefined} />
                <polygon points="20,2.5 24,5 20,7.5" fill={it.col} />
              </svg>
              <span style={{ fontSize: 11, color: 'var(--darkgray-color)' }}>{it.label}</span>
            </div>
          ))}

        <div style={{ width: 1, height: 14, background: 'var(--gray-color)' }} />
        {[{l:'1:1',c:'#1D9E75'},{l:'1:N',c:'#3B8ADD'},{l:'N:N',c:'#D85A30'}].map(b => (
          <span key={b.l} style={{
            fontSize: 9, fontWeight: 700, padding: '2px 6px', borderRadius: 4,
            background: b.c + '22', color: b.c, border: `1px solid ${b.c}44`,
          }}>{b.l}</span>
        ))}
      </div>

      {/* ── Graph canvas ───────────────────────────────────────────────── */}
      <GraphCanvas
        tables={tables}
        edges={visEdges}
        graphMode={graphMode}
        weightMode={weightMode}
        schemas={schemas}
        dark={dark}
        onChecksum={onChecksum}
        onRepair={onRepair}
      />

      {/* ── Attribute list ─────────────────────────────────────────────── */}
      <AttributeList
        tables={tables}
        schemas={schemas}
        dark={dark}
        onChecksum={onChecksum}
        onRepair={onRepair}
      />
    </div>
  )
}

// ─── Graph canvas ─────────────────────────────────────────────────────────────
function GraphCanvas({ tables, edges, graphMode, weightMode, schemas, dark, onChecksum, onRepair }) {
  // Initial zoom 1.4 so nodes appear at readable text size (same as other
  // components) and the user sees only part of the canvas, inviting exploration.
  const INIT_ZOOM = 1.4
  // Centre the viewport on the middle of the canvas at startup.
  const initPan = () => ({
    x: -(CANVAS_W / 2 - (typeof window !== 'undefined' ? window.innerWidth * 0.45 : 600) / INIT_ZOOM),
    y: -(CANVAS_H / 2 - 300 / INIT_ZOOM),
  })

  const [pos,      setPos]      = useState({})
  const [selected, setSelected] = useState(null)
  const [hoveredE, setHoveredE] = useState(null)
  const [pan,      setPan]      = useState(initPan)
  const [zoom,     setZoom]     = useState(INIT_ZOOM)
  const [panning,  setPanning]  = useState(false)
  const panStart = useRef(null)
  const svgRef   = useRef(null)
  const wrapRef  = useRef(null)
  // Keep a stable ref to the latest positions so runLayout can read prevPos
  // without causing the layout effect to re-run on every render.
  const posRef   = useRef({})
  const nw = nodeW(graphMode)

  // Structural identity key: sorted table names + mode.
  // This changes ONLY when the set of tables changes or the mode switches.
  // Data refreshes (sync status, row counts, size_weight_pct) do NOT change
  // table_name values, so this key stays the same and the layout is skipped.
  const layoutKey = useMemo(
    () => tables.map(t => t.table_name).sort().join('|') + '::' + graphMode,
    [tables, graphMode]
  )

  useEffect(() => {
    if (!tables.length) return
    // Pass the current positions so runLayout can preserve known nodes.
    const newPos = runLayout(tables, edges, graphMode, posRef.current)
    posRef.current = newPos
    setPos(newPos)
    setSelected(null)
  // edges is intentionally NOT a dependency: spring attraction during layout
  // uses the edges array, but changing which edges are shown (filter toggles)
  // should not re-scramble the layout — only a structural table-set change
  // (captured by layoutKey) or a mode switch should trigger a new layout.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [layoutKey])

  const hlSet = useMemo(() => {
    if (!selected) return null
    const s = new Set([selected])
    edges.forEach(e => {
      if (e.childTable  === selected) s.add(e.parentTable)
      if (e.parentTable === selected) s.add(e.childTable)
    })
    return s
  }, [selected, edges])

  // ── Zoom (wheel anywhere on the canvas wrapper) ───────────────────────────
  const onWheel = useCallback(e => {
    e.preventDefault()
    const nz = clamp(zoom * (e.deltaY < 0 ? 1.11 : 0.90), 0.15, 3.5)
    const rect = (wrapRef.current || svgRef.current)?.getBoundingClientRect()
    if (!rect) return
    const mx = (e.clientX - rect.left) / zoom - pan.x
    const my = (e.clientY - rect.top)  / zoom - pan.y
    setPan(prev => ({ x: prev.x - mx * (nz - zoom) / nz, y: prev.y - my * (nz - zoom) / nz }))
    setZoom(nz)
  }, [zoom, pan])

  // ── Pan — active on ANY mousedown inside the wrapper div (not just svg bg)
  //    Nodes set e.stopPropagation() only for their own click; mousemove on
  //    the wrapper always pans. This gives the "slide canvas anywhere" feel.
  const onWrapMouseDown = useCallback(e => {
    // Don't start a pan if the user clicked a button, input, or the panel.
    if (e.target.closest('button, input, [data-nopan]')) return
    setPanning(true)
    panStart.current = { x: e.clientX - pan.x * zoom, y: e.clientY - pan.y * zoom }
  }, [pan, zoom])

  const onWrapMouseMove = useCallback(e => {
    if (!panning) return
    setPan({ x: (e.clientX - panStart.current.x) / zoom, y: (e.clientY - panStart.current.y) / zoom })
  }, [panning, zoom])

  const stopPan = useCallback(() => setPanning(false), [])

  // Click on bare canvas (not a node) → deselect
  const onWrapClick = useCallback(e => {
    if (e.target === wrapRef.current || e.target === svgRef.current || e.target.tagName === 'svg') {
      setSelected(null)
    }
  }, [])

  const selTable = selected ? tables.find(t => t.table_name === selected) : null

  const resetView = useCallback(() => {
    setZoom(INIT_ZOOM)
    setPan(initPan())
  }, [])

  return (
    <div
      ref={wrapRef}
      style={{
        position: 'relative', width: '100%', height: 620,
        borderRadius: 10, overflow: 'hidden',
        border: `1px solid var(--gray-color)`,
        // Canvas background matches the app body background exactly
        background: 'var(--body-bg-color)',
        cursor: panning ? 'grabbing' : 'grab',
        userSelect: 'none',
      }}
      onMouseDown={onWrapMouseDown}
      onMouseMove={onWrapMouseMove}
      onMouseUp={stopPan}
      onMouseLeave={stopPan}
      onClick={onWrapClick}
      onWheel={onWheel}
    >
      {/* Zoom controls */}
      <div data-nopan style={{ position: 'absolute', top: 10, right: 10, zIndex: 20, display: 'flex', flexDirection: 'column', gap: 5 }}>
        {[
          { l: '+', f: () => setZoom(z => clamp(z * 1.2,  0.15, 3.5)) },
          { l: '−', f: () => setZoom(z => clamp(z * 0.83, 0.15, 3.5)) },
          { l: '⊡', f: resetView },
        ].map(b => (
          <button key={b.l} onClick={b.f} style={{
            width: 28, height: 28,
            border: `1px solid var(--gray-color)`,
            background: 'var(--secondary-gray-color)', color: 'var(--text-color)',
            borderRadius: 6, cursor: 'pointer', fontSize: 14,
            display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>{b.l}</button>
        ))}
      </div>

      <div data-nopan style={{
        position: 'absolute', bottom: 8, right: 10, zIndex: 20,
        fontSize: 10, color: 'var(--darkgray-color)',
        background: 'var(--secondary-gray-color)', padding: '2px 7px',
        border: `1px solid var(--gray-color)`, borderRadius: 4,
      }}>
        {Math.round(zoom * 100)}%
      </div>

      {/* Selection panel */}
      {selTable && (
        <SelectionPanel
          table={selTable}
          pal={schemaPal(selTable.table_schema, schemas, dark)}
          edges={edges}
          dark={dark}
          onChecksum={onChecksum}
          onRepair={onRepair}
          onDismiss={() => setSelected(null)}
        />
      )}

      {/* SVG canvas — transparent so the wrapper bg shows through */}
      <svg
        ref={svgRef}
        width="100%" height="100%"
        style={{ display: 'block', background: 'transparent' }}
      >
        <defs>
          {[['fk', T.edgeFk], ['nm', T.edgeNm], ['wq', T.edgeWq]].map(([id, c]) => (
            <marker key={id} id={`arr-${id}`}
              viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
              <path d="M2 2L8 5L2 8" fill="none" stroke={c}
                strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
            </marker>
          ))}
        </defs>

        <g transform={`translate(${pan.x * zoom},${pan.y * zoom}) scale(${zoom})`}>
          {/* Edges */}
          {edges.map((e, i) => {
            const ap = pos[e.childTable], bp = pos[e.parentTable]
            if (!ap || !bp) return null
            const ct = tables.find(t => t.table_name === e.childTable)
            const pt = tables.find(t => t.table_name === e.parentTable)
            if (!ct || !pt) return null
            const ah = nodeH(ct, graphMode), bh = nodeH(pt, graphMode)
            const { d, mx, my } = mkEdgePath(
              ap.x - nw / 2, ap.y - ah / 2, nw, ah,
              bp.x - nw / 2, bp.y - bh / 2, nw, bh
            )
            const isSel = selected && (e.childTable === selected || e.parentTable === selected)
            const isHov = hoveredE === i
            const op    = selected ? (isSel ? 0.9 : 0.07) : (isHov ? 1 : 0.38)
            const col   = edgeColor(e.source)
            // SVG fill/stroke can't use CSS vars — resolve to concrete colour
            const nodeBg  = dark ? '#2a3048' : '#f7f8fe'
            const textSec = dark ? '#778899' : '#666460'
            const textMut = dark ? '#778899' : '#a0a09a'

            return (
              <g key={i}
                onMouseEnter={() => setHoveredE(i)}
                onMouseLeave={() => setHoveredE(null)}
                style={{ cursor: 'pointer' }}
              >
                <path d={d} fill="none" stroke="transparent" strokeWidth={14} />
                <path d={d} fill="none" stroke={col}
                  strokeWidth={isHov ? 2.2 : 1.2}
                  strokeDasharray={edgeDash(e.source) || undefined}
                  opacity={op}
                  markerEnd={`url(#arr-${edgeMid(e.source)})`}
                  style={{ transition: 'opacity .15s' }}
                />
                {(isHov || isSel) && (
                  <g>
                    <rect x={mx - 14} y={my - 9} width={28} height={17} rx={4}
                      fill={nodeBg} stroke={col} strokeWidth={0.6} opacity={0.96} />
                    <text x={mx} y={my + 4.5} textAnchor="middle"
                      fontSize={9} fontWeight={700} fill={col} fontFamily="system-ui">
                      {cardLabel(e.cardinality)}
                    </text>
                  </g>
                )}
                {isHov && e.joinWeight > 0 && (
                  <g>
                    <rect x={mx - 28} y={my + 11} width={56} height={14} rx={3} fill={nodeBg} opacity={0.90} />
                    <text x={mx} y={my + 22} textAnchor="middle"
                      fontSize={9} fill={textSec} fontFamily="system-ui">
                      {e.joinWeight.toFixed(1)}% joins
                    </text>
                  </g>
                )}
                {isHov && e.childCols.length > 0 && (
                  <g>
                    <rect x={mx - 62} y={my + 28} width={124} height={14} rx={3} fill={nodeBg} opacity={0.88} />
                    <text x={mx} y={my + 39} textAnchor="middle"
                      fontSize={8} fill={textMut} fontFamily="monospace">
                      {e.childCols.join(', ').slice(0, 24)}{e.childCols.join(', ').length > 24 ? '…' : ''}
                    </text>
                  </g>
                )}
              </g>
            )
          })}

          {/* Nodes */}
          {tables.map(t => {
            const pp = pos[t.table_name]
            if (!pp) return null
            const pal  = schemaPal(t.table_schema, schemas, dark)
            const nh   = nodeH(t, graphMode)
            const nx   = pp.x - nw / 2
            const ny   = pp.y - nh / 2
            const isSel = selected === t.table_name
            const isDim = hlSet && !hlSet.has(t.table_name)
            const pct   = ((weightMode === 'size' ? t.size_weight_pct : t.usage_weight_pct) || 0)
            const barW  = Math.max(4, Math.round((pct / 100) * (nw - 16)))
            const sm    = syncMeta(t.table_sync, dark)
            const pkSet = new Set(
              (t.table_indexes || [])
                .filter(ix => ix.name === 'PRIMARY')
                .flatMap(ix => ix.columns?.map(c => c.name) || [])
            )
            const cols   = t.table_columns || []
            // SVG can't use CSS variables — resolve theme colours to literals
            const nodeBg    = dark ? '#2a3048' : '#f7f8fe'
            const nodeBd    = dark ? '#2d3748' : '#e2e8f0'
            const textPri   = dark ? '#e7e9ef' : '#333333'
            const textMut   = dark ? '#778899' : '#a0a09a'
            const nodeStripe = dark ? '#131a34' : '#eff2fe'

            return (
              <g key={t.table_name}
                onClick={e => { e.stopPropagation(); setSelected(s => s === t.table_name ? null : t.table_name) }}
                style={{ cursor: 'pointer', opacity: isDim ? 0.13 : 1 }}
              >
                <rect x={nx} y={ny} width={nw} height={nh} rx={9}
                  fill={nodeBg}
                  stroke={isSel ? pal.text : (isDim ? nodeBd : pal.border)}
                  strokeWidth={isSel ? 2 : 0.8}
                />
                {graphMode === 'simple' ? (
                  <>
                    <rect x={nx} y={ny} width={nw} height={20} rx={7} fill={pal.fill} />
                    <rect x={nx} y={ny + 12} width={nw} height={8} fill={pal.fill} />
                    <DbIcon x={nx + 4} y={ny + 20} color={pal.text} size={13} />
                    <text x={nx + 22} y={ny + 32} fontSize={12} fontWeight={600}
                      fill={textPri} fontFamily="system-ui">
                      {t.table_name.length > 16 ? t.table_name.slice(0, 15) + '…' : t.table_name}
                    </text>
                    <rect x={nx + nw - 42} y={ny + 19} width={36} height={13} rx={3} fill={sm.bg} />
                    <text x={nx + nw - 24} y={ny + 29} textAnchor="middle"
                      fontSize={8} fontWeight={700} fill={sm.fg} fontFamily="system-ui">
                      {sm.icon} {sm.label}
                    </text>
                    <rect x={nx + 8} y={ny + 43} width={nw - 16} height={4} rx={2}
                      fill={dark ? '#2d3748' : '#e2e8f0'} />
                    <rect x={nx + 8} y={ny + 43} width={barW} height={4} rx={2}
                      fill={pal.text} opacity={0.65} />
                    <text x={nx + nw - 7} y={ny + 43} textAnchor="end"
                      fontSize={8} fill={textMut} fontFamily="system-ui">
                      {pct.toFixed(1)}%
                    </text>
                  </>
                ) : (
                  <>
                    <rect x={nx} y={ny} width={nw} height={HDR_H} rx={8} fill={pal.fill} />
                    <rect x={nx} y={ny + HDR_H - 6} width={nw} height={6} fill={pal.fill} />
                    <DbIcon x={nx + 4} y={ny + 7} color={pal.text} size={13} />
                    <text x={nx + 22} y={ny + 13} fontSize={9} fill={pal.text} opacity={0.65} fontFamily="system-ui">
                      {t.table_schema}
                    </text>
                    <text x={nx + 22} y={ny + 29} fontSize={13} fontWeight={700} fill={pal.text} fontFamily="system-ui">
                      {t.table_name.length > 18 ? t.table_name.slice(0, 17) + '…' : t.table_name}
                    </text>
                    <text x={nx + nw - 8} y={ny + 14} textAnchor="end"
                      fontSize={9} fill={pal.text} opacity={0.55} fontFamily="system-ui">
                      {fmtRows(t.table_rows)} rows
                    </text>
                    <rect x={nx + nw - 44} y={ny + 20} width={38} height={13} rx={3} fill={sm.bg} />
                    <text x={nx + nw - 25} y={ny + 30} textAnchor="middle"
                      fontSize={8} fontWeight={700} fill={sm.fg} fontFamily="system-ui">
                      {sm.icon} {sm.label}
                    </text>
                    <line x1={nx} y1={ny + HDR_H} x2={nx + nw} y2={ny + HDR_H}
                      stroke={pal.border} strokeWidth={0.8} />
                    {cols.map((col, ci) => {
                      const ry   = ny + HDR_H + ci * ROW_H
                      const tb   = typeBadge(col.type, dark)
                      const isPk = pkSet.has(col.name)
                      const tl   = Math.min((col.type || '').length, 12)
                      const tw   = tl * 6 + 8
                      return (
                        <g key={col.name}>
                          {ci % 2 === 1 && (
                            <rect x={nx} y={ry} width={nw} height={ROW_H} fill={nodeStripe} />
                          )}
                          {isPk && (
                            <>
                              <rect x={nx + 5} y={ry + 5} width={16} height={13} rx={2}
                                fill={dark ? '#2e2214' : '#FAEEDA'} />
                              <text x={nx + 13} y={ry + ROW_H / 2 + 4} textAnchor="middle"
                                fontSize={7} fontWeight={800}
                                fill={dark ? '#d4914a' : '#B07500'} fontFamily="system-ui">
                                PK
                              </text>
                            </>
                          )}
                          <text
                            x={nx + (isPk ? 26 : 9)} y={ry + ROW_H / 2 + 4}
                            fontSize={11} fontWeight={isPk ? 600 : 400}
                            fill={textPri} fontFamily="system-ui"
                          >
                            {col.name.length > 18 ? col.name.slice(0, 17) + '…' : col.name}
                          </text>
                          <rect x={nx + nw - tw - 7} y={ry + 4} width={tw} height={15} rx={3} fill={tb.bg} />
                          <text x={nx + nw - tw / 2 - 7} y={ry + ROW_H / 2 + 4}
                            textAnchor="middle" fontSize={9} fill={tb.fg} fontFamily="monospace">
                            {(col.type || '').length > 12 ? col.type.slice(0, 10) + '…' : col.type}
                          </text>
                          {col.nullable && (
                            <circle cx={nx + nw - 4} cy={ry + ROW_H / 2} r={2.5}
                              fill="transparent" stroke={textMut} strokeWidth={0.8} />
                          )}
                        </g>
                      )
                    })}
                  </>
                )}
              </g>
            )
          })}
        </g>
      </svg>
    </div>
  )
}

// ─── Selection detail panel ───────────────────────────────────────────────────
function SelectionPanel({ table: t, pal, edges, dark, onChecksum, onRepair, onDismiss }) {
  const parents  = edges.filter(e => e.childTable  === t.table_name)
  const children = edges.filter(e => e.parentTable === t.table_name)
  const sm       = syncMeta(t.table_sync, dark)
  const bd       = 'var(--gray-color)'
  const panelBg  = 'var(--secondary-gray-color)'

  return (
    <div data-nopan style={{
      position: 'absolute', left: 10, top: 10, zIndex: 30, width: 214,
      background: panelBg, border: `1px solid ${bd}`,
      borderRadius: 10, overflow: 'hidden',
      boxShadow: `0 4px 18px rgba(0,0,0,0.18)`,
      fontSize: 12,
    }}>
      <div style={{ background: pal.fill, padding: '8px 11px', borderBottom: `1px solid ${pal.border}` }}>
        <div style={{ fontSize: 10, color: pal.text, opacity: 0.65 }}>{t.table_schema}</div>
        <div style={{ fontSize: 14, fontWeight: 700, color: pal.text, display: 'flex', alignItems: 'center', gap: 6 }}>
          <DbIcon x={0} y={-2} color={pal.text} size={13} />
          <span style={{ marginLeft: 18 }}>{t.table_name}</span>
        </div>
      </div>

      <div style={{ padding: '7px 11px 2px' }}>
        {[
          ['Rows',    fmtRows(t.table_rows)],
          ['Data',    fmtBytes(t.data_length)],
          ['Indexes', fmtBytes(t.index_length)],
          ['Engine',  t.engine || '—'],
          ['Cols',    t.table_columns?.length || '—'],
          ['Size %',  (t.size_weight_pct || 0).toFixed(2) + '%'],
        ].map(([k, v]) => (
          <div key={k} style={{ display: 'flex', justifyContent: 'space-between', padding: '2px 0' }}>
            <span style={{ color: 'var(--darkgray-color)' }}>{k}</span>
            <span style={{ fontWeight: 600, color: 'var(--text-color)' }}>{v}</span>
          </div>
        ))}
        <div style={{ display: 'flex', justifyContent: 'space-between', padding: '3px 0', alignItems: 'center' }}>
          <span style={{ color: 'var(--darkgray-color)' }}>Sync</span>
          <SyncPill value={t.table_sync} dark={dark} />
        </div>
      </div>

      {(parents.length > 0 || children.length > 0) && (
        <div style={{ padding: '5px 11px 5px', borderTop: `1px solid ${bd}`, marginTop: 2 }}>
          <div style={{ fontSize: 10, color: 'var(--darkgray-color)', marginBottom: 4, fontWeight: 700 }}>
            Relations ({parents.length + children.length})
          </div>
          {parents.slice(0, 4).map((e, i) => (
            <div key={`p${i}`} style={{ display: 'flex', gap: 4, padding: '1.5px 0', alignItems: 'center' }}>
              <span style={{ color: edgeColor(e.source), fontSize: 11, fontWeight: 700 }}>→</span>
              <span style={{ flex: 1, color: 'var(--darkgray-color)', fontSize: 11, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {e.parentTable}
              </span>
              <span style={{ fontSize: 9, color: 'var(--darkgray-color)' }}>{cardLabel(e.cardinality)}</span>
            </div>
          ))}
          {children.slice(0, 4).map((e, i) => (
            <div key={`c${i}`} style={{ display: 'flex', gap: 4, padding: '1.5px 0', alignItems: 'center' }}>
              <span style={{ color: edgeColor(e.source), fontSize: 11, fontWeight: 700 }}>←</span>
              <span style={{ flex: 1, color: 'var(--darkgray-color)', fontSize: 11, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {e.childTable}
              </span>
              <span style={{ fontSize: 9, color: 'var(--darkgray-color)' }}>{cardLabel(e.cardinality)}</span>
            </div>
          ))}
          {(parents.length + children.length) > 8 && (
            <div style={{ fontSize: 10, color: 'var(--darkgray-color)', marginTop: 2 }}>
              +{parents.length + children.length - 8} more
            </div>
          )}
        </div>
      )}

      <div style={{ display: 'flex', gap: 6, padding: '7px 9px', borderTop: `1px solid ${bd}` }}>
        <ActionBtn label="Checksum" onClick={() => onChecksum?.(t.table_schema, t.table_name)}
          bgL="#E6F1FB" fgL="#0C447C" bgD="#0d2340" fgD="#7ab8ef" dark={dark} />
        <ActionBtn label="Repair" onClick={() => onRepair?.(t.table_schema, t.table_name)}
          bgL="#EAF3DE" fgL="#27500A" bgD="#132808" fgD="#7ec85e" dark={dark} />
      </div>
      <button onClick={onDismiss} style={{
        width: '100%', padding: '5px 0', border: 'none',
        borderTop: `1px solid ${bd}`, background: 'transparent',
        cursor: 'pointer', fontSize: 11, color: 'var(--darkgray-color)',
      }}>Dismiss</button>
    </div>
  )
}

// ─── Attribute list ───────────────────────────────────────────────────────────
function AttributeList({ tables, schemas, dark, onChecksum, onRepair }) {
  const [expanded, setExpanded] = useState(new Set())
  const toggle = name => setExpanded(prev => {
    const n = new Set(prev)
    n.has(name) ? n.delete(name) : n.add(name)
    return n
  })

  if (!tables.length) return null

  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(340px,1fr))', gap: 10 }}>
      {tables.map(t => {
        const pal   = schemaPal(t.table_schema, schemas, dark)
        const isExp = expanded.has(t.table_name)
        const pkSet = new Set(
          (t.table_indexes || [])
            .filter(ix => ix.name === 'PRIMARY')
            .flatMap(ix => ix.columns?.map(c => c.name) || [])
        )
        const parents  = t.table_parents  || []
        const children = t.table_children || []

        return (
          <div key={t.table_name} style={{
            border: '1px solid var(--gray-color)', borderRadius: 10,
            background: 'var(--secondary-gray-color)', overflow: 'hidden', fontSize: 12,
          }}>
            <div onClick={() => toggle(t.table_name)} style={{
              background: pal.fill, padding: '10px 13px', cursor: 'pointer', userSelect: 'none',
            }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 0 }}>
                  <DbIcon x={0} y={0} color={pal.text} size={14} />
                  <span style={{ marginLeft: 20 }}>
                    <span style={{ fontSize: 9, color: pal.text, opacity: 0.65 }}>{t.table_schema} · </span>
                    <span style={{ fontSize: 13, fontWeight: 700, color: pal.text }}>{t.table_name}</span>
                  </span>
                </div>
                <div style={{ display: 'flex', gap: 7, alignItems: 'center' }}>
                  <SyncPill value={t.table_sync} dark={dark} />
                  <span style={{ fontSize: 10, color: pal.text, opacity: 0.7 }}>{fmtRows(t.table_rows)}</span>
                  <span style={{ fontSize: 12, color: pal.text }}>{isExp ? '▲' : '▼'}</span>
                </div>
              </div>
              <div style={{ display: 'flex', gap: 8, marginTop: 7 }}>
                {[
                  { label: 'Size',  val: t.size_weight_pct  || 0 },
                  { label: 'Usage', val: t.usage_weight_pct || 0 },
                ].map(({ label, val }) => (
                  <div key={label} style={{ flex: 1 }}>
                    <div style={{ fontSize: 8, color: pal.text, opacity: 0.6, marginBottom: 2 }}>
                      {label} {val.toFixed(1)}%
                    </div>
                    <div style={{ height: 3, background: `${pal.text}22`, borderRadius: 2 }}>
                      <div style={{ height: 3, width: `${Math.min(val, 100)}%`, background: pal.text, opacity: 0.5, borderRadius: 2 }} />
                    </div>
                  </div>
                ))}
              </div>
            </div>

            {isExp && (
              <div style={{ paddingBottom: 8 }}>
                <div style={{ display: 'flex', gap: 8, padding: '8px 12px 4px' }}>
                  <ActionBtn label="Checksum" onClick={() => onChecksum?.(t.table_schema, t.table_name)}
                    bgL="#E6F1FB" fgL="#0C447C" bgD="#0d2340" fgD="#7ab8ef" dark={dark} />
                  <ActionBtn label="Repair" onClick={() => onRepair?.(t.table_schema, t.table_name)}
                    bgL="#EAF3DE" fgL="#27500A" bgD="#132808" fgD="#7ec85e" dark={dark} />
                </div>

                <SectionLabel label="Columns" />
                {(t.table_columns || []).map((col, ci) => {
                  const tb   = typeBadge(col.type, dark)
                  const isPk = pkSet.has(col.name)
                  return (
                    <div key={col.name} style={{
                      display: 'flex', alignItems: 'center', gap: 7,
                      padding: '4px 13px',
                      background: ci % 2 ? 'var(--body-bg-color)' : 'transparent',
                    }}>
                      {isPk && (
                        <span style={{
                          fontSize: 8, fontWeight: 800, padding: '1px 4px', borderRadius: 3,
                          background: dark ? '#2e2214' : '#FAEEDA',
                          color: dark ? '#d4914a' : '#B07500',
                        }}>PK</span>
                      )}
                      <span style={{ flex: 1, fontWeight: isPk ? 600 : 400, color: 'var(--text-color)' }}>
                        {col.name}
                      </span>
                      <span style={{
                        fontSize: 10, padding: '1px 5px', borderRadius: 3,
                        background: tb.bg, color: tb.fg, fontFamily: 'monospace',
                      }}>{col.type}</span>
                      {!col.nullable && (
                        <span style={{
                          fontSize: 9, color: 'var(--darkgray-color)',
                          border: '0.5px solid var(--gray-color)',
                          padding: '0 3px', borderRadius: 2,
                        }}>NN</span>
                      )}
                    </div>
                  )
                })}

                {(t.table_indexes || []).length > 0 && (
                  <>
                    <SectionLabel label="Indexes" />
                    {(t.table_indexes || []).map(ix => (
                      <div key={ix.name} style={{ display: 'flex', gap: 7, alignItems: 'center', padding: '3px 13px' }}>
                        {ix.unique && (
                          <span style={{
                            fontSize: 8, fontWeight: 800, padding: '1px 4px', borderRadius: 3,
                            background: dark ? '#0d2340' : '#E6F1FB',
                            color: dark ? '#7ab8ef' : '#0C447C',
                          }}>UQ</span>
                        )}
                        <span style={{ color: 'var(--darkgray-color)' }}>{ix.name}</span>
                        <span style={{ color: 'var(--darkgray-color)', fontSize: 11 }}>
                          ({(ix.columns || []).map(c => c.name).join(', ')})
                        </span>
                      </div>
                    ))}
                  </>
                )}

                {/* Relations — read directly from table_parents / table_children */}
                {(parents.length > 0 || children.length > 0) && (
                  <>
                    <SectionLabel label="Relations" />
                    {parents.map((lk, i) => {
                      const cl = (lk.local_columns || []).join(', ')
                      return (
                        <div key={`p${i}`} style={{ display: 'flex', gap: 6, alignItems: 'center', padding: '3px 13px' }}>
                          <span style={{ color: edgeColor(lk.relation_source), fontSize: 12, fontWeight: 700 }}>→</span>
                          <span style={{ flex: 1, color: 'var(--darkgray-color)' }}>{lk.linked_table}</span>
                          <span style={{ fontSize: 9, color: 'var(--darkgray-color)' }}>{cardLabel(lk.cardinality)}</span>
                          {cl && (
                            <span style={{ fontSize: 9, color: 'var(--darkgray-color)', fontFamily: 'monospace' }}>
                              ({cl.slice(0, 18)}{cl.length > 18 ? '…' : ''})
                            </span>
                          )}
                        </div>
                      )
                    })}
                    {children.map((lk, i) => {
                      const cl = (lk.remote_columns || []).join(', ')
                      return (
                        <div key={`c${i}`} style={{ display: 'flex', gap: 6, alignItems: 'center', padding: '3px 13px' }}>
                          <span style={{ color: edgeColor(lk.relation_source), fontSize: 12, fontWeight: 700 }}>←</span>
                          <span style={{ flex: 1, color: 'var(--darkgray-color)' }}>{lk.linked_table}</span>
                          <span style={{ fontSize: 9, color: 'var(--darkgray-color)' }}>{cardLabel(lk.cardinality)}</span>
                          {cl && (
                            <span style={{ fontSize: 9, color: 'var(--darkgray-color)', fontFamily: 'monospace' }}>
                              ({cl.slice(0, 18)}{cl.length > 18 ? '…' : ''})
                            </span>
                          )}
                        </div>
                      )
                    })}
                  </>
                )}

                <div style={{
                  display: 'flex', gap: 14, padding: '7px 13px 0', marginTop: 4,
                  borderTop: '1px solid var(--gray-color)',
                  fontSize: 10, color: 'var(--darkgray-color)',
                }}>
                  <span>{fmtBytes((t.data_length || 0) + (t.index_length || 0))} total</span>
                  <span>{t.engine}</span>
                  <span style={{ marginLeft: 'auto' }}>{t.table_collation}</span>
                </div>
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
