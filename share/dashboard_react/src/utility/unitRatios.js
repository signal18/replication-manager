// Unit ratios come from the server ONLY (cluster.unitRatios / globalResources.unitRatios,
// fed by the settings resource-manager-ratio-dbu/-apu/-bku). The dashboard never types a
// ratio, not even as a fallback: with no payload yet, every axis reads 0 and the helpers
// below guard the divisions.
const MiB = 1024 * 1024
const GiB = 1024 * MiB

const EMPTY = { coresPerUnit: 0, memMBPerUnit: 0, diskGBPerUnit: 0, iopsPerUnit: 0 }

// getUnitRatios returns { database, compute, storage } from a cluster JSON or a global
// resources payload.
export const getUnitRatios = (source) => {
  const r = source?.unitRatios || {}
  const pick = (k) => ({ ...EMPTY, ...(r[k] || {}) })
  return { database: pick('database'), compute: pick('compute'), storage: pick('storage') }
}

// Bytes per unit on the memory and disk axes, for the real→unit overlays of the charts.
export const memBytesPerUnit = (ratio) => (ratio?.memMBPerUnit || 0) * MiB
export const diskBytesPerUnit = (ratio) => (ratio?.diskGBPerUnit || 0) * GiB

// A human line for one unit of a profile, e.g. "1 core · 4GB · 20GB disk · 1000 IO/s".
export const describeUnit = (ratio) => {
  const parts = []
  if (ratio?.coresPerUnit) parts.push(`${ratio.coresPerUnit} core${ratio.coresPerUnit > 1 ? 's' : ''}`)
  if (ratio?.memMBPerUnit) parts.push(ratio.memMBPerUnit >= 1024 ? `${ratio.memMBPerUnit / 1024}GB` : `${ratio.memMBPerUnit}MB`)
  if (ratio?.diskGBPerUnit) parts.push(`${ratio.diskGBPerUnit}GB disk`)
  if (ratio?.iopsPerUnit) parts.push(`${ratio.iopsPerUnit} IO/s`)
  return parts.join(' · ')
}

// The four DBU axes of the grouped chart, ratios from the server.
export const dbuAxes = (ratio) => ([
  { key: 'cpu', label: 'CPU', ratio: ratio.coresPerUnit || 1, light: '#3f8fd0', dark: '#5aa8e6' },
  { key: 'mem', label: 'Mem', ratio: memBytesPerUnit(ratio) || 1, light: '#a21caf', dark: '#d946ef' },
  { key: 'io', label: 'IO', ratio: ratio.iopsPerUnit || 1, light: '#e0603a', dark: '#ef7a54' },
  { key: 'disk', label: 'Disk', ratio: diskBytesPerUnit(ratio) || 1, light: '#37a06f', dark: '#4dc088' },
])

// The three APU axes (no IO).
export const apuAxes = (ratio) => ([
  { key: 'cpu', label: 'CPU', ratio: ratio.coresPerUnit || 1, light: '#3f8fd0', dark: '#5aa8e6' },
  { key: 'mem', label: 'Mem', ratio: memBytesPerUnit(ratio) || 1, light: '#a21caf', dark: '#d946ef' },
  { key: 'disk', label: 'Disk', ratio: diskBytesPerUnit(ratio) || 1, light: '#37a06f', dark: '#4dc088' },
])
