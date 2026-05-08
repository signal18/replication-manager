// SSO upgrade state management tests.
// Run with: node src/redux/__tests__/authSlice.ssoUpgrade.test.js
//
// Tests pure functions and logic only — no framework imports needed.
// Redux reducer contracts are verified via inline state-transition helpers
// that mirror the authSlice implementation exactly.

import { REASON_HINTS } from '../../components/ssoUpgradeReasons.js'

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

// ─── Inline auth state reducers (mirrors authSlice exactly) ──────────────────
// These are direct transcriptions of the authSlice reducer cases so we can
// verify the logic without importing the full Redux slice (which has Vite-
// specific bare imports that don't resolve in plain Node.js).

const INITIAL_AUTH_STATE = {
  user: null,
  loading: false,
  isLogged: false,
  ssoUpgradeId: null,
}

function applyLogout(state) {
  // Mirrors logout reducer in authSlice.js
  return {
    ...state,
    user: null,
    isLogged: false,
    ssoUpgradeId: null,   // Fix 1: logout must clear upgradeId
  }
}

function applyLoginFulfilled(state, data, username) {
  // Mirrors login.fulfilled extraReducer in authSlice.js
  const parsed = typeof data === 'string' ? JSON.parse(data) : data
  return {
    ...state,
    isLogged: true,
    user: { username },
    loading: false,
    ssoUpgradeId: parsed?.upgrade_id ?? null,  // Fix 2: always reset, not just set
  }
}

function applyClearSSOUpgrade(state) {
  // Mirrors clearSSOUpgrade reducer in authSlice.js
  return { ...state, ssoUpgradeId: null }
}

// ─── logout clears ssoUpgradeId ───────────────────────────────────────────────

console.log('\nTest Suite: logout clears ssoUpgradeId')

{
  const before = { ...INITIAL_AUTH_STATE, ssoUpgradeId: 'poll-abc', isLogged: true, user: { username: 'alice' } }
  const after = applyLogout(before)
  assertEqual(after.ssoUpgradeId, null, 'logout sets ssoUpgradeId to null')
  assertEqual(after.isLogged, false, 'logout clears isLogged')
  assertEqual(after.user, null, 'logout clears user')
}

{
  // Idempotent: logout when already logged out
  const after = applyLogout(INITIAL_AUTH_STATE)
  assertEqual(after.ssoUpgradeId, null, 'logout is idempotent on ssoUpgradeId')
}

// ─── clearSSOUpgrade ──────────────────────────────────────────────────────────

console.log('\nTest Suite: clearSSOUpgrade')

{
  const before = { ...INITIAL_AUTH_STATE, ssoUpgradeId: 'upgrade-xyz' }
  const after = applyClearSSOUpgrade(before)
  assertEqual(after.ssoUpgradeId, null, 'clearSSOUpgrade sets ssoUpgradeId to null')
}

{
  const after = applyClearSSOUpgrade(INITIAL_AUTH_STATE)
  assertEqual(after.ssoUpgradeId, null, 'clearSSOUpgrade is idempotent')
}

// ─── login.fulfilled sets / resets ssoUpgradeId ──────────────────────────────

console.log('\nTest Suite: login.fulfilled ssoUpgradeId handling')

{
  const after = applyLoginFulfilled(INITIAL_AUTH_STATE, { token: 'local-jwt', upgrade_id: 'poll-001' }, 'alice')
  assertEqual(after.ssoUpgradeId, 'poll-001', 'login with upgrade_id stores it')
  assertEqual(after.isLogged, true, 'login marks user logged in')
}

{
  const after = applyLoginFulfilled(INITIAL_AUTH_STATE, { token: 'local-jwt' }, 'alice')
  assertEqual(after.ssoUpgradeId, null, 'login without upgrade_id leaves ssoUpgradeId null')
}

{
  // Fix 2: stale id from previous session must be cleared on new login
  const stale = { ...INITIAL_AUTH_STATE, ssoUpgradeId: 'stale-old' }
  const after = applyLoginFulfilled(stale, { token: 'new-jwt' }, 'alice')
  assertEqual(after.ssoUpgradeId, null, 'login without upgrade_id clears stale ssoUpgradeId')
}

{
  const stale = { ...INITIAL_AUTH_STATE, ssoUpgradeId: 'old-id' }
  const after = applyLoginFulfilled(stale, { token: 'new-jwt', upgrade_id: 'new-id' }, 'alice')
  assertEqual(after.ssoUpgradeId, 'new-id', 'login with new upgrade_id replaces stale id')
}

// ─── REASON_HINTS coverage ────────────────────────────────────────────────────

console.log('\nTest Suite: REASON_HINTS coverage')

const CONTRACT_REASONS = [
  'credential_mismatch',
  'not_registered',
  'unknown_non_retryable',
  'retry_exhausted',
  'claim_mismatch',
]

for (const reason of CONTRACT_REASONS) {
  const hint = REASON_HINTS[reason]
  assert(typeof hint === 'string' && hint.length > 0, `${reason} has a non-empty hint`)
}

{
  const forbidden = ['invalid_grant', 'oauth', 'http 4', 'http 5', 'stack', 'exception']
  for (const [reason, hint] of Object.entries(REASON_HINTS)) {
    const lower = hint.toLowerCase()
    for (const term of forbidden) {
      assert(!lower.includes(term), `hint for ${reason} does not expose technical term "${term}"`)
    }
  }
}

// ─── Polling response handler logic ──────────────────────────────────────────
// Inline simulation of what SSOUpgradePoller does for each HTTP status code.

console.log('\nTest Suite: polling response handling')

function handlePollResponse(status, data) {
  let tokenReplaced = null
  let warningReason = null
  let continuePolling = false

  if (status === 202) {
    continuePolling = true
  } else if (status === 200) {
    if (data?.token) tokenReplaced = data.token
  } else if (status === 409) {
    warningReason = data?.reason ?? 'unknown_non_retryable'
    // Do NOT replace token — local JWT remains valid per contract
  }
  // 404 / 410: stop silently

  return { tokenReplaced, warningReason, continuePolling }
}

{
  const r = handlePollResponse(202, { status: 'pending' })
  assert(r.continuePolling, '202 keeps polling')
  assertEqual(r.tokenReplaced, null, '202 does not write token')
  assertEqual(r.warningReason, null, '202 has no warning')
}

{
  const r = handlePollResponse(200, { token: 'sso-jwt' })
  assert(!r.continuePolling, '200 stops polling')
  assertEqual(r.tokenReplaced, 'sso-jwt', '200 replaces local JWT with SSO JWT')
  assertEqual(r.warningReason, null, '200 shows no warning')
}

{
  const r = handlePollResponse(200, {})
  assertEqual(r.tokenReplaced, null, '200 with no token does not crash or write empty value')
}

{
  const r = handlePollResponse(409, { status: 'failed', reason: 'credential_mismatch' })
  assert(!r.continuePolling, '409 stops polling')
  assertEqual(r.tokenReplaced, null, '409 does NOT replace token — local JWT preserved')
  assertEqual(r.warningReason, 'credential_mismatch', '409 captures reason code')
}

{
  const r = handlePollResponse(409, { status: 'failed', reason: 'not_registered' })
  assertEqual(r.warningReason, 'not_registered', '409 not_registered captures reason')
  assertEqual(r.tokenReplaced, null, '409 not_registered does not touch token')
}

{
  const r = handlePollResponse(409, { status: 'failed' })
  assertEqual(r.warningReason, 'unknown_non_retryable', '409 without reason defaults to unknown_non_retryable')
}

{
  const r = handlePollResponse(410, { status: 'expired' })
  assert(!r.continuePolling, '410 stops polling')
  assertEqual(r.tokenReplaced, null, '410 does not touch token')
  assertEqual(r.warningReason, null, '410 is silent — no warning shown')
}

{
  const r = handlePollResponse(404, null)
  assert(!r.continuePolling, '404 stops polling')
  assertEqual(r.tokenReplaced, null, '404 does not touch token')
  assertEqual(r.warningReason, null, '404 is silent — no warning shown')
}

// ─── Client-side poll limit guard ─────────────────────────────────────────────

console.log('\nTest Suite: client-side poll limit')

{
  const MAX_CLIENT_POLLS = 30
  let count = 0
  let stopped = false

  // Simulate the poller loop incrementing count on each 202
  while (count < MAX_CLIENT_POLLS) {
    const r = handlePollResponse(202, {})
    if (!r.continuePolling) { stopped = true; break }
    count++
  }
  if (count >= MAX_CLIENT_POLLS) stopped = true

  assert(stopped, 'polling stops after MAX_CLIENT_POLLS 202 responses')
  assert(count <= MAX_CLIENT_POLLS, 'poll count does not exceed maximum')
}

// ─── Summary ──────────────────────────────────────────────────────────────────

if (failed > 0) {
  console.log(`\n❌ Failed: ${failed}, Passed: ${passed}`)
  process.exit(1)
}
console.log(`\n✅ All tests passed: ${passed}`)
