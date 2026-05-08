import { useEffect, useRef } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { authService } from '../services/authService'
import { clearSSOUpgrade } from '../redux/authSlice'
import { showWarningToast } from '../redux/toastSlice'

import { REASON_HINTS } from './ssoUpgradeReasons.js'

const POLL_INTERVAL_MS = 2000
// Client-side guard: stop after ~60 s even if server keeps returning 202.
const MAX_CLIENT_POLLS = 30

// SSOUpgradePoller is a renderless component mounted at the App level so it
// survives navigation. When ssoUpgradeId is set in Redux state (by loginHandler
// after a non-admin Cloud18 local-auth success), it polls GET /api/login/upgrade
// every 2 s until a terminal response is received:
//
//   202 → keep polling
//   200 → replace user_token in localStorage with the upgraded SSO JWT
//   409 → show a non-blocking warning toast with a safe hint; keep local JWT
//   410 → stop silently (expired or already consumed)
//   404 → stop silently (unknown id)
//   network error → retry like 202 (transient)
function SSOUpgradePoller() {
  const dispatch = useDispatch()
  const ssoUpgradeId = useSelector((state) => state.auth.ssoUpgradeId)

  const timerRef = useRef(null)
  const activeRef = useRef(false)
  const pollCountRef = useRef(0)

  useEffect(() => {
    if (!ssoUpgradeId) return

    activeRef.current = true
    pollCountRef.current = 0

    const doPoll = async () => {
      if (!activeRef.current) return

      if (pollCountRef.current >= MAX_CLIENT_POLLS) {
        dispatch(clearSSOUpgrade())
        return
      }

      try {
        const response = await authService.pollUpgrade(ssoUpgradeId)
        if (!activeRef.current) return

        if (response.status === 202) {
          // Server-side retries still in progress — keep polling.
          pollCountRef.current++
          timerRef.current = setTimeout(doPoll, POLL_INTERVAL_MS)
          return
        }

        // Terminal states — all paths below must dispatch clearSSOUpgrade.
        if (response.status === 200) {
          const token = response.data?.token
          if (token) {
            localStorage.setItem('user_token', token)
          }
        } else if (response.status === 409) {
          const reason = response.data?.reason || 'unknown_non_retryable'
          const hint = REASON_HINTS[reason] ?? REASON_HINTS.unknown_non_retryable
          dispatch(showWarningToast({ title: 'SSO upgrade', description: hint }))
          // Do NOT log out — per contract, local JWT remains valid on 409.
        }
        // 404, 410: stop silently; local JWT is still valid.

        dispatch(clearSSOUpgrade())
      } catch {
        // Network error — treat as transient and retry.
        if (activeRef.current) {
          pollCountRef.current++
          timerRef.current = setTimeout(doPoll, POLL_INTERVAL_MS)
        }
      }
    }

    timerRef.current = setTimeout(doPoll, POLL_INTERVAL_MS)

    return () => {
      activeRef.current = false
      clearTimeout(timerRef.current)
    }
  }, [ssoUpgradeId, dispatch])

  return null
}

export default SSOUpgradePoller
