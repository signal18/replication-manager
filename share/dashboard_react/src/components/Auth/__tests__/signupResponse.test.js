import {
  isPendingSignupResponse,
  isConfirmedSignupResponse,
  resolveSignupErrorMessage,
  pendingSignupMessage,
  existingAccountMessage,
} from '../signupResponse.js'

let passed = 0
let failed = 0

function assertEqual(actual, expected, description) {
  if (actual === expected) {
    passed += 1
    console.log(`  ✓ ${description}`)
    return
  }
  failed += 1
  console.log(`  ✗ ${description}`)
  console.log(`      Expected: ${JSON.stringify(expected)}`)
  console.log(`      Actual:   ${JSON.stringify(actual)}`)
}

function assertFalse(value, description) {
  assertEqual(value, false, description)
}

function assertTrue(value, description) {
  assertEqual(value, true, description)
}

// ─── isPendingSignupResponse ──────────────────────────────────────────────────

console.log('\nTest Suite: isPendingSignupResponse')

{
  // 201 with email_confirmed: false → pending even though status is 201
  const response = { status: 201, data: { state: 'created', identity: { email_confirmed: false } } }
  assertTrue(isPendingSignupResponse(response), '201 + email_confirmed: false → pending')
}

{
  // 201 with no identity field → not pending
  const response = { status: 201, data: { state: 'created' } }
  assertFalse(isPendingSignupResponse(response), '201 + no identity → not pending')
}

{
  // 202 with no body → pending
  const response = { status: 202, data: {} }
  assertTrue(isPendingSignupResponse(response), '202 + no body → pending')
}

{
  // 200 with no pending indicators → not pending
  const response = { status: 200, data: { state: 'ok' } }
  assertFalse(isPendingSignupResponse(response), '200 + no pending indicators → not pending')
}

// ─── isConfirmedSignupResponse ────────────────────────────────────────────────

console.log('\nTest Suite: isConfirmedSignupResponse')

{
  // 201 + confirmed
  const response = { status: 201, data: { state: 'created', identity: { email_confirmed: true } } }
  assertTrue(isConfirmedSignupResponse(response), '201 + email_confirmed: true → confirmed')
}

{
  // 201 + email_confirmed: false → NOT confirmed (pending wins)
  const response = { status: 201, data: { state: 'created', identity: { email_confirmed: false } } }
  assertFalse(isConfirmedSignupResponse(response), '201 + email_confirmed: false → not confirmed')
}

{
  // 202 → not confirmed
  const response = { status: 202, data: {} }
  assertFalse(isConfirmedSignupResponse(response), '202 → not confirmed')
}

{
  // 201 + no identity → confirmed (absence of false is not false)
  const response = { status: 201, data: { state: 'created' } }
  assertTrue(isConfirmedSignupResponse(response), '201 + no identity field → confirmed')
}

// ─── resolveSignupErrorMessage ────────────────────────────────────────────────

console.log('\nTest Suite: resolveSignupErrorMessage')

{
  // 409 with CRM message → use CRM message
  const response = { status: 409, data: { message: 'GitLab account already exists for this email. Please log in.' } }
  assertEqual(
    resolveSignupErrorMessage(response),
    'GitLab account already exists for this email. Please log in.',
    '409 + CRM message → uses CRM message'
  )
}

{
  // 409 with CRM error field → use CRM error
  const response = { status: 409, data: { error: 'account_exists' } }
  assertEqual(
    resolveSignupErrorMessage(response),
    'account_exists',
    '409 + CRM error field → uses CRM error'
  )
}

{
  // 409 with no body → falls back to existingAccountMessage
  const response = { status: 409, data: {} }
  assertEqual(
    resolveSignupErrorMessage(response),
    existingAccountMessage,
    '409 + no body → falls back to existingAccountMessage'
  )
}

{
  // Non-409 with message
  const response = { status: 422, data: { message: 'Invalid username' } }
  assertEqual(
    resolveSignupErrorMessage(response),
    'Invalid username',
    'non-409 structured message → uses message field'
  )
}

{
  // No response → generic fallback
  assertEqual(
    resolveSignupErrorMessage(null),
    'Signup failed',
    'null response → generic fallback'
  )
}

// ─── Result ───────────────────────────────────────────────────────────────────

if (failed > 0) {
  console.log(`\n❌ Failed: ${failed}, Passed: ${passed}`)
  process.exit(1)
}

console.log(`\n✅ All tests passed: ${passed}`)
