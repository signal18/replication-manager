export function getReferenceKey(ref = {}) {
  // Use a collision-safe tuple encoding for (appId, mountName)
  // instead of delimiter-joined strings.
  return JSON.stringify([String(ref.appId || ''), String(ref.mountName || '')])
}

export function getTargetsFingerprint(targets = []) {
  return JSON.stringify(
    (targets || [])
      .map((t) => [String(t?.appId || ''), String(t?.mountName || '')])
      .sort((a, b) => {
        if (a[0] !== b[0]) return a[0] < b[0] ? -1 : 1
        if (a[1] !== b[1]) return a[1] < b[1] ? -1 : 1
        return 0
      })
  )
}

export function buildDefaultSelectedTargetsByKey(references = []) {
  return (references || []).reduce((acc, ref) => {
    acc[getReferenceKey(ref)] = true
    return acc
  }, {})
}

export function buildSelectedSyncTargets(references = [], selectedTargetsByKey = {}) {
  const seen = new Set()
  return (references || [])
    .filter((ref) => selectedTargetsByKey[getReferenceKey(ref)])
    .map((ref) => ({
      appId: String(ref?.appId || '').trim(),
      mountName: String(ref?.mountName || '').trim(),
    }))
    .filter((target) => target.appId && target.mountName)
    .filter((target) => {
      const key = JSON.stringify([target.appId, target.mountName])
      if (seen.has(key)) return false
      seen.add(key)
      return true
    })
}

export function getSelectedTargetsCount(references = [], selectedTargetsByKey = {}) {
  return buildSelectedSyncTargets(references, selectedTargetsByKey).length
}
