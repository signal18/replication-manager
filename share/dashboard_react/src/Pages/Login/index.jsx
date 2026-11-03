import React, { useCallback, useEffect, useRef, useState, Suspense } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { login, logout, setUserData } from '../../redux/authSlice'
import styles from './styles.module.scss'
import { Box, Container, Divider, Heading, Spinner, Stack, Text, HStack, Link } from '@chakra-ui/react'
import PageContainer from '../PageContainer'
import { isAuthorized } from '../../utility/common'
import LoginForm from '../../components/LoginForm'
import RMButton from '../../components/RMButton'
import Message from '../../components/Message'
import { useTheme } from '../../ThemeProvider'
import { clearCluster } from '../../redux/clusterSlice'
import { clearClusters } from '../../redux/globalClustersSlice'
import { authService } from '../../services/authService'

function Login({ dashboard = false }) {
  // The admin zone (/login?admin) is the API (local) login only, with the local admin pre-filled.
  const [adminZone] = useState(() => new URLSearchParams(window.location.search).has('admin'))
  const [username, setUsername] = useState(adminZone ? 'admin' : '')
  const [password, setPassword] = useState('')
  const [usernameError, setUsernameError] = useState('')
  const [passwordError, setPasswordError] = useState('')
  const [errorMessage, setErrorMessage] = useState('')
  const [cloud18Enabled, setCloud18Enabled] = useState(false)
  // Cloud18 logins only (#2262): the login goes straight to Cloud18; the user and password form is
  // the admin zone's, at /login?admin
  const [cloud18Only, setCloud18Only] = useState(false)
  const [cloud18Loading, setCloud18Loading] = useState(false)
  // the Cloud18 status is known and no silent check is under way: autologin may run
  const [cloud18Ready, setCloud18Ready] = useState(false)
  const [cloud18Retrying, setCloud18Retrying] = useState(false)
  const { theme } = useTheme()

  const dispatch = useDispatch()
  const {
    auth: { isLogged, loading, loadingGitLogin, user, error, sessionStatus }
  } = useSelector((state) => state)

  // Guards against concurrent autologin fetches. Reset to false on network failure
  // so a later retry is allowed; stays true after a definitive success or server rejection.
  const autologinCalledRef = useRef(false)
  const [autologinRetryCount, setAutologinRetryCount] = useState(0)

  const doAutologin = useCallback(() => {
    if (autologinCalledRef.current) return
    autologinCalledRef.current = true
    fetch('/api/autologin')
      .then((res) => (res.ok ? res.json() : null))
      .then((data) => {
        if (data && data.token) {
          localStorage.setItem('user_token', data.token)
          localStorage.setItem('username', data.username || 'admin')
          dispatch(setUserData())
          window.location.assign('/')
        } else {
          // Server is up but autologin is not configured — no retry.
          dispatch(logout())
          dispatch(clearClusters())
          dispatch(clearCluster())
        }
      })
      .catch(() => {
        // Network failure — server not up yet.
        // Keep the guard set until the timer fires: dispatch(logout()) below will
        // flip sessionStatus to 'unauthenticated', which would immediately re-trigger
        // the sessionStatus effect. The guard being true prevents that instant retry.
        dispatch(logout())
        dispatch(clearClusters())
        dispatch(clearCluster())
        setTimeout(() => {
          autologinCalledRef.current = false  // allow next attempt only after the delay
          setAutologinRetryCount((c) => c + 1)
        }, 10_000)
      })
  }, [dispatch])

  // Sign in with Cloud18 (#2185) on a partner repman: the silent check on opening (already signed in
  // at Cloud18 in this browser: straight in, nothing shown), and the way back from the realm, a
  // one-time code in the address traded once for the session (never a token in the address).
  const searchParams = new URLSearchParams(window.location.search)
  const cloud18Code = searchParams.get('cloud18')
  const cloud18Back = cloud18Code || searchParams.get('cloud18_error') || searchParams.get('cloud18_silent')
  const cloud18Traded = useRef(false) // the one-time code is sent once (StrictMode runs effects twice)
  useEffect(() => {
    if (dashboard) return
    let statusRetry = null
    let stopped = false
    let statusAttempts = 0
    // The Cloud18 status decides whether to go to Cloud18 or show the local form. If the server is
    // starting/unreachable (restart) the check gets a 5xx or a network error: RETRY on the
    // "connecting" screen instead of concluding "not Cloud18" and dropping to the API form — a
    // marketplace instance must still reach Cloud18 once the server is back. Capped (~1 min) so a
    // genuinely dead backend eventually falls through to the form rather than spinning forever.
    function fallBackToForm() {
      setCloud18Retrying(false)
      setCloud18Enabled(false)
      setCloud18Ready(true)
      if (!cloud18Code && !isAuthorized() && !adminZone) doAutologin()
    }
    function scheduleStatusRetry() {
      if (stopped) return
      if (statusAttempts++ >= 24) { fallBackToForm(); return }
      setCloud18Retrying(true) // show the "retrying" screen (not a blank) while the server is down
      statusRetry = setTimeout(checkCloud18Status, 2500)
    }
    function checkCloud18Status() {
      authService.cloud18SSOStatus()
        .then((resp) => {
          if (stopped) return
          if (!resp?.status || resp.status >= 500) { scheduleStatusRetry(); return }
          const enabled = resp.status === 200 && resp?.data?.enabled === true
          setCloud18Enabled(enabled)
          setCloud18Only(enabled && resp?.data?.cloud18Only === true)
          // Cloud18 logins only: straight to the Cloud18 sign-in (its page offers Sign up; an open
          // Cloud18 session comes straight back), except the admin zone and a return from Cloud18
          if (enabled && resp?.data?.cloud18Only === true && !cloud18Back && !adminZone && !isAuthorized()) {
            window.location.assign('/api/auth/cloud18/start')
            return
          }
          // once per tab: after a Logout (which ends the Cloud18 session) or a check that found none,
          // the page shows its form and does not try again
          if (enabled && !cloud18Back && !adminZone && !isAuthorized() && sessionStorage.getItem('cloud18_silent_tried') !== '1') {
            sessionStorage.setItem('cloud18_silent_tried', '1')
            window.location.assign('/api/auth/cloud18/start?silent=1')
            return
          }
          // no silent check: the page's usual autologin (not after a Cloud18 return with a code)
          setCloud18Retrying(false)
          setCloud18Ready(true)
          if (!cloud18Code && !isAuthorized() && !adminZone) doAutologin()
        })
        .catch(() => { if (!stopped) scheduleStatusRetry() }) // network failure: retry, not the API form
    }
    checkCloud18Status()
    const params = new URLSearchParams(window.location.search)
    if (params.get('cloud18_silent')) {
      window.history.replaceState(null, '', '/login')
    }
    const failed = params.get('cloud18_error')
    if (failed) {
      // 'expired' = the sign-in's one-time state was lost (most often a server restart wiped the
      // in-memory state, or a stale attempt) — a fresh sign-in just works, so retry once instead of
      // dead-ending on the error. The session guard prevents a redirect loop if it keeps failing;
      // it is cleared on a successful sign-in below.
      if (failed === 'expired' && sessionStorage.getItem('cloud18_expired_retried') !== '1') {
        sessionStorage.setItem('cloud18_expired_retried', '1')
        window.location.assign('/api/auth/cloud18/start')
        return
      }
      // only the server's fixed reasons are shown, never text from the address
      const reasons = {
        expired: 'the sign-in expired or was not started in this browser, try again',
        refused: 'Cloud18 did not sign you in',
        token: 'Cloud18 sent a token this repman refuses',
        busy: 'too many sign-ins in progress, try again in a few minutes',
        failed: 'no session could be opened'
      }
      setErrorMessage('Sign in with Cloud18: ' + (reasons[failed] || 'failed, try again'))
      window.history.replaceState(null, '', '/login')
    }
    if (cloud18Code && !cloud18Traded.current) {
      cloud18Traded.current = true
      window.history.replaceState(null, '', '/login')
      setCloud18Loading(true)
      authService.cloud18SSOSession(cloud18Code)
        .then((resp) => {
          if (resp?.status === 200 && resp?.data?.token) {
            localStorage.setItem('user_token', resp.data.token)
            localStorage.setItem('username', resp.data.username)
            localStorage.setItem('login_method', 'cloud18')
            sessionStorage.removeItem('cloud18_expired_retried')
            dispatch(setUserData())
            window.location.assign('/')
          } else {
            setErrorMessage('Sign in with Cloud18: the sign-in expired, try again')
          }
        })
        .catch(() => setErrorMessage('Sign in with Cloud18: no answer, try again'))
        .finally(() => setCloud18Loading(false))
    }
    return () => { stopped = true; if (statusRetry) clearTimeout(statusRetry) }
  }, [])

  useEffect(() => {
    if (!dashboard) {
      // On mount, autologin starts once the page knows whether a silent Cloud18 check runs
      // (the Cloud18 status above, #2185); a token, if any, is validated by SessionGuard (App.jsx).
      return
    }

    // Dashboard (slideshow) mode: retry until the server is ready after restart.
    let retryTimer = null
    let cancelled = false

    const tryDashboardToken = () => {
      fetch('/api/dashboard-token')
        .then((res) => (res.ok ? res.json() : null))
        .then((data) => {
          if (cancelled) return
          if (data && data.token) {
            localStorage.setItem('user_token', data.token)
            localStorage.setItem('username', data.username || 'viewer')
            dispatch(setUserData())
            window.location.assign('/slideshow')
          } else {
            // Server not ready yet or no dashboard token configured — retry.
            retryTimer = setTimeout(tryDashboardToken, 3000)
          }
        })
        .catch(() => {
          if (!cancelled) retryTimer = setTimeout(tryDashboardToken, 3000)
        })
    }

    tryDashboardToken()
    return () => {
      cancelled = true
      clearTimeout(retryTimer)
    }
  }, [])

  // Fires when:
  //  - sessionStatus changes to 'unauthenticated' (stale token cleared by SessionGuard), or
  //  - autologinRetryCount increments (previous attempt hit a network failure during restart).
  useEffect(() => {
    if (dashboard) return
    // not before the Cloud18 status is known (a silent check may be under way), never on a
    // Cloud18 return with a code (#2185)
    if (!cloud18Ready || cloud18Code) return
    if (adminZone) return // the admin zone always shows the local-admin form, never autologins
    if (sessionStatus !== 'unauthenticated') return
    if (isAuthorized()) return
    doAutologin()
  }, [sessionStatus, autologinRetryCount, doAutologin, cloud18Ready])

  useEffect(() => {
    if (!loading || !loadingGitLogin) {
      if (isLogged && user) {
        // Navigate to the right destination after manual login regardless of mode.
        try { sessionStorage.removeItem('cloud18_expired_retried') } catch (e) {} // any login clears the one-shot guard
        window.location.assign(dashboard ? '/slideshow' : '/')
      }
      if (error) {
        setErrorMessage(error)
      }
    }
  }, [loading, loadingGitLogin, isLogged, error])

  const onButtonClick = (e) => {
    e.preventDefault()
    setUsernameError('')
    setPasswordError('')

    if ('' === username) {
      setUsernameError('Please enter your username')
      return
    }

    if ('' === password) {
      setPasswordError('Please enter a password')
      return
    }

    logIn()
  }

  const logIn = () => {
    dispatch(login({ username, password }))
  }

  // While deciding whether to redirect to Cloud18 (or exchanging a returned code), show an empty
  // background rather than flashing the login form that is about to disappear. cloud18Ready flips
  // true only once we know we are staying on this page (the admin zone sets it and shows the form).
  if (!dashboard && (!cloud18Ready || cloud18Loading)) {
    // The backend is unreachable (e.g. a restart): say so instead of a silent blank. Show the
    // "retrying" spinner while the Cloud18 status check is retrying (cloud18Retrying) — even for a
    // visitor with no token, where whoami never runs — or when whoami reported the server
    // unavailable. It recovers on its own once the status check resolves (form or Cloud18) or whoami
    // succeeds. Otherwise (the brief decide/redirect window) keep an empty background so the login
    // form does not flash before the Cloud18 redirect.
    if (cloud18Retrying || sessionStatus === 'unavailable') {
      return (
        <PageContainer>
          <Container maxWidth='lg' py={{ base: '24', md: '24' }}>
            <Stack spacing='4' align='center'>
              <Spinner size='lg' />
              <Text fontSize='sm' color='gray.500'>Waiting for the server — it is unavailable, retrying…</Text>
            </Stack>
          </Container>
        </PageContainer>
      )
    }
    return <PageContainer />
  }

  return (
    <PageContainer>
      {/* <Suspense fallback={<div>Loading...</div>}> */}
      <Container maxWidth='lg' py={{ base: '24', md: '24' }} px={{ base: '0', sm: '8' }}>
        <Stack spacing='8'>
          <Stack spacing='6'>
            <Stack spacing={{ base: '2', md: '3' }} textAlign='center'>
              <Heading size='md'>Sign in to your account</Heading>
            </Stack>
          </Stack>
          <Box as='form' className={styles.loginForm} onSubmit={onButtonClick}>
            <Stack spacing='6'>
              {cloud18Only && !adminZone ? (
                <>
                  {errorMessage && <Message message={errorMessage} />}
                  <RMButton
                    id='btnCloud18Login'
                    type='button'
                    size='medium'
                    isLoading={cloud18Loading}
                    onClick={() => window.location.assign('/api/auth/cloud18/start')}>
                    Sign in with Cloud18
                  </RMButton>
                  <Text fontSize='sm' textAlign='center'>
                    <Link id='lnkAdminZone' color='teal.500' href='/login?admin'>
                      Admin zone
                    </Link>
                  </Text>
                </>
              ) : (
                <>
                {cloud18Only && (
                  <Text fontSize='sm'>Admin zone: the local admin account of this replication-manager. Other accounts sign in with Cloud18.</Text>
                )}
                <LoginForm
                  username={username}
                  password={password}
                  onUsernameChange={(e) => setUsername(e.target.value)}
                  onPasswordChange={(e) => setPassword(e.target.value)}
                  usernameError={usernameError}
                  passwordError={passwordError}
                  onSubmit={onButtonClick}
                  inputClassName={theme === 'dark' ? styles.darkLoginText : ""}
                  labelClassName={theme === 'dark' ? styles.darkLoginText : ""}
                  errorClassName={styles.errorMessage}
                  revealClassName={`${styles.revealButton} ${styles.errorMessage} ${theme === 'dark' ? styles.darkLoginText : ""}`}
                />
                {errorMessage && <Message message={errorMessage} />}

                <Stack spacing='6'>
                  <RMButton
                    id='btnLogin'
                    type='submit'
                    size='medium'
                    onClick={onButtonClick}
                    isLoading={loading || cloud18Loading}
                    loadingText={'Signing in'}>
                    Sign in
                  </RMButton>
                  {cloud18Enabled && !cloud18Only && (
                    <>
                      {/* the fields above serve Sign in only: the Cloud18 password is typed on the
                          Cloud18 page (#2185); an email already typed is prefilled there */}
                      <HStack spacing='3'>
                        <Divider />
                        <Text fontSize='sm' color='gray.500' whiteSpace='nowrap'>or</Text>
                        <Divider />
                      </HStack>
                      <RMButton
                        id='btnCloud18Login'
                        type='button'
                        size='medium'
                        onClick={() => window.location.assign('/api/auth/cloud18/start' + (username.includes('@') ? '?login_hint=' + encodeURIComponent(username.trim()) : ''))}>
                        Sign in with Cloud18
                      </RMButton>
                      <RMButton
                        id='btnCloud18Register'
                        type='button'
                        size='medium'
                        onClick={() => window.location.assign('/api/auth/cloud18/start?signup=1')}>
                        Sign up with Cloud18
                      </RMButton>
                    </>
                  )}
                  {cloud18Only && !adminZone && (
                    <Text fontSize='sm' textAlign='center'>
                      <Link id='lnkCloud18Back' color='teal.500' href='/api/auth/cloud18/start'>
                        Back to Sign in with Cloud18
                      </Link>
                    </Text>
                  )}
                </Stack>
                </>
              )}
            </Stack>
          </Box>
          <HStack justify='center' spacing='6' pt='2' fontSize='sm'>
            <Link href='https://www.signal18.io' isExternal color='teal.500'>signal18.io</Link>
            <Link href='https://docs.signal18.io' isExternal color='teal.500'>Documentation</Link>
          </HStack>
        </Stack>
      </Container>
      {/* </Suspense> */}
    </PageContainer>
  )
}

export default Login
