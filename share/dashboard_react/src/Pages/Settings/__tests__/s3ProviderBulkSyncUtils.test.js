// S3 provider bulk sync selection helpers tests
// Run with: node src/Pages/Settings/__tests__/s3ProviderBulkSyncUtils.test.js

import {
  buildDefaultSelectedTargetsByKey,
  buildSelectedSyncTargets,
  getReferenceKey,
  getTargetsFingerprint,
  getSelectedTargetsCount,
} from '../s3ProviderBulkSyncUtils.js'

let passed = 0
let failed = 0

function assert(condition, description) {
  if (condition) {
    passed++
    console.log(`  ✓ ${description}`)
  } else {
    failed++
    console.log(`  ✗ ${description}`)
  }
}

function assertEqual(actual, expected, description) {
  const ok = JSON.stringify(actual) === JSON.stringify(expected)
  if (ok) {
    passed++
    console.log(`  ✓ ${description}`)
  } else {
    failed++
    console.log(`  ✗ ${description}`)
    console.log(`      Expected: ${JSON.stringify(expected)}`)
    console.log(`      Actual:   ${JSON.stringify(actual)}`)
  }
}

const refs = [
  { appId: 'app-a', mountName: 'media' },
  { appId: 'app-b', mountName: 'backup' },
  { appId: 'app-c', mountName: 'archive' },
]

console.log('\nTest Suite: default selection picks all references')
{
  const selected = buildDefaultSelectedTargetsByKey(refs)
  assertEqual(Object.keys(selected).length, 3, 'all references get selection keys')
  assert(selected[getReferenceKey(refs[0])] === true, 'first reference selected by default')
  assert(selected[getReferenceKey(refs[1])] === true, 'second reference selected by default')
  assert(selected[getReferenceKey(refs[2])] === true, 'third reference selected by default')
  assertEqual(getSelectedTargetsCount(refs, selected), 3, 'selected count equals reference count')
}

console.log('\nTest Suite: deselection excludes mount from selected targets payload')
{
  const selected = buildDefaultSelectedTargetsByKey(refs)
  selected[getReferenceKey(refs[1])] = false
  const targets = buildSelectedSyncTargets(refs, selected)
  assertEqual(targets, [
    { appId: 'app-a', mountName: 'media' },
    { appId: 'app-c', mountName: 'archive' },
  ], 'payload contains only currently selected mounts')
  assertEqual(getSelectedTargetsCount(refs, selected), 2, 'selected count tracks deselection')
}

console.log('\nTest Suite: invalid references are filtered from payload')
{
  const dirtyRefs = [
    { appId: 'ok', mountName: 'm1' },
    { appId: '', mountName: 'm2' },
    { appId: 'ok2', mountName: '' },
  ]
  const selected = buildDefaultSelectedTargetsByKey(dirtyRefs)
  const targets = buildSelectedSyncTargets(dirtyRefs, selected)
  assertEqual(targets, [{ appId: 'ok', mountName: 'm1' }], 'payload keeps only valid appId+mountName targets')
}

console.log('\nTest Suite: key generation avoids delimiter collisions')
{
  const refA = { appId: 'a::b', mountName: 'c' }
  const refB = { appId: 'a', mountName: 'b::c' }
  const keyA = getReferenceKey(refA)
  const keyB = getReferenceKey(refB)
  assert(keyA !== keyB, 'distinct (appId,mountName) tuples produce distinct keys even with delimiters')
}

console.log('\nTest Suite: selected target payload is trimmed and deduplicated')
{
  const dirtyRefs = [
    { appId: ' app-a ', mountName: ' media ' },
    { appId: 'app-a', mountName: 'media' },
    { appId: 'app-b', mountName: 'backup' },
  ]
  const selected = buildDefaultSelectedTargetsByKey(dirtyRefs)
  const targets = buildSelectedSyncTargets(dirtyRefs, selected)
  assertEqual(targets, [
    { appId: 'app-a', mountName: 'media' },
    { appId: 'app-b', mountName: 'backup' },
  ], 'selected targets are normalized and deduplicated')
}

console.log('\nTest Suite: target fingerprint is order-insensitive')
{
  const a = [
    { appId: 'app-a', mountName: 'm1' },
    { appId: 'app-b', mountName: 'm2' },
  ]
  const b = [
    { appId: 'app-b', mountName: 'm2' },
    { appId: 'app-a', mountName: 'm1' },
  ]
  assert(getTargetsFingerprint(a) === getTargetsFingerprint(b), 'fingerprint remains stable regardless of target ordering')
}

console.log(`\nSummary: ${passed} passed, ${failed} failed`)
if (failed > 0) {
  process.exit(1)
}
