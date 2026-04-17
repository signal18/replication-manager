export const PREVIEW_STATUSES = {
  WILL_CHANGE: 'will_change',
  NO_CHANGE: 'no_change',
  PROVIDER_MISSING: 'provider_missing',
  ERROR: 'error',
}

export const APPLY_STATUSES = {
  CHANGED: 'changed',
  UNCHANGED: 'unchanged',
  PROVIDER_MISSING: 'provider_missing',
  STALE_STATE: 'stale_state',
  ERROR: 'error',
}

const toTargetKey = (t) => JSON.stringify([String(t?.appId || '').trim(), String(t?.mountName || '').trim()])

export const formatSyncValue = (value) => {
  if (value === null || value === undefined) return '(empty)'
  const normalized = String(value)
  return normalized === '' ? '(empty)' : normalized
}

export const hasPendingPreviewChanges = (previewData) => {
  if (!previewData || typeof previewData !== 'object') return false
  const summaryWillChange = Number(previewData?.summary?.willChange || 0)
  if (summaryWillChange > 0) return true
  if (!Array.isArray(previewData?.results)) return false
  return previewData.results.some((r) => r?.status === PREVIEW_STATUSES.WILL_CHANGE)
}

export const buildEligibleApplyTargetsFromPreview = (selectedTargets = [], previewData = null) => {
  if (!Array.isArray(selectedTargets) || selectedTargets.length === 0) return []
  if (!previewData || !Array.isArray(previewData?.results)) return selectedTargets

  const statusByTarget = new Map()
  previewData.results.forEach((r) => {
    statusByTarget.set(toTargetKey(r?.target), r?.status)
  })

  return selectedTargets.filter((t) => {
    const status = statusByTarget.get(toTargetKey(t))
    if (!status) return true
    return status !== PREVIEW_STATUSES.PROVIDER_MISSING && status !== PREVIEW_STATUSES.ERROR
  })
}

export const buildBulkApplyDisplaySummary = ({ selectedTotal = 0, applySummary = {}, excludedFromApply = 0 }) => {
  const changed = Number(applySummary?.changed || 0)
  const unchanged = Number(applySummary?.unchanged || 0)
  const failedFromApply = Number(applySummary?.failed || 0)
  const excluded = Number(excludedFromApply || 0)
  return {
    total: Number(selectedTotal || 0),
    changed,
    unchanged,
    failed: failedFromApply + excluded,
  }
}
