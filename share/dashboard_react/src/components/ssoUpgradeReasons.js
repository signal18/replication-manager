// Safe, non-technical hints shown for each machine-readable SSO upgrade reason
// code returned by GET /api/login/upgrade (409 Conflict).
// Must never expose raw provider errors or internal details.
export const REASON_HINTS = {
  credential_mismatch: 'SSO could not authenticate your account. Your local session is unaffected.',
  not_registered: 'Your account is not yet registered in SSO — local login remains active.',
  unknown_non_retryable: 'SSO session upgrade could not complete. Your local session is unaffected.',
  retry_exhausted: 'SSO upgrade timed out. Your local session is unaffected.',
  claim_mismatch: 'SSO token could not be validated. Your local session is unaffected.',
}
