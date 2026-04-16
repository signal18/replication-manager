// Sync diff helper tests
// Run with: node src/components/__tests__/syncDiffUtils.test.js

import {
  buildBulkApplyDisplaySummary,
  buildEligibleApplyTargetsFromPreview,
  formatSyncValue,
  hasPendingPreviewChanges,
} from '../syncDiffUtils.js'

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

console.log('\nTest Suite: formatSyncValue renders empty sentinel')
{
  assertEqual(formatSyncValue(''), '(empty)', 'empty string is rendered as (empty)')
  assertEqual(formatSyncValue(undefined), '(empty)', 'undefined is rendered as (empty)')
  assertEqual(formatSyncValue('***'), '***', 'masked values are preserved')
}

console.log('\nTest Suite: hasPendingPreviewChanges detects will_change rows')
{
  const none = { summary: { willChange: 0 }, results: [{ status: 'no_change' }] }
  const some = { summary: { willChange: 1 }, results: [{ status: 'will_change' }] }
  assert(hasPendingPreviewChanges(none) === false, 'no_change-only preview has no pending changes')
  assert(hasPendingPreviewChanges(some) === true, 'will_change preview has pending changes')
}

console.log('\nTest Suite: buildEligibleApplyTargetsFromPreview excludes hard preview failures')
{
  const selected = [
    { appId: 'app-a', mountName: 'm1' },
    { appId: 'app-b', mountName: 'm2' },
    { appId: 'app-c', mountName: 'm3' },
  ]
  const preview = {
    results: [
      { target: { appId: 'app-a', mountName: 'm1' }, status: 'will_change' },
      { target: { appId: 'app-b', mountName: 'm2' }, status: 'provider_missing' },
      { target: { appId: 'app-c', mountName: 'm3' }, status: 'error' },
    ],
  }
  const eligible = buildEligibleApplyTargetsFromPreview(selected, preview)
  assertEqual(eligible, [{ appId: 'app-a', mountName: 'm1' }], 'provider_missing and error rows are excluded from apply')
}

console.log('\nTest Suite: buildBulkApplyDisplaySummary includes skipped rows in failed count')
{
  const summary = buildBulkApplyDisplaySummary({
    selectedTotal: 4,
    applySummary: { changed: 2, unchanged: 1, failed: 0 },
    excludedFromApply: 1,
  })
  assertEqual(summary, { total: 4, changed: 2, unchanged: 1, failed: 1 }, 'failed count includes rows skipped before apply')
}

console.log(`\nSummary: ${passed} passed, ${failed} failed`)
if (failed > 0) {
  process.exit(1)
}
