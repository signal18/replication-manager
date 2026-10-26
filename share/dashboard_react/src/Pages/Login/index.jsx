import React, { useCallback, useEffect, useRef, useState, Suspense } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { useNavigate } from 'react-router-dom'
import { login, logout, setUserData } from '../../redux/authSlice'
import styles from './styles.module.scss'
import { Box, Container, FormControl, FormLabel, FormErrorMessage, Heading, Input, Stack, Text, HStack, Link } from '@chakra-ui/react'
import PageContainer from '../PageContainer'
import { isAuthorized } from '../../utility/common'
import PasswordControl from '../../components/PasswordControl'
import RMButton from '../../components/RMButton'
import Message from '../../components/Message'
import { useTheme } from '../../ThemeProvider'
import { clearCluster } from '../../redux/clusterSlice'
import { clearClusters } from '../../redux/globalClustersSlice'
import { authService } from '../../services/authService'

function Login({ dashboard = false }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [usernameError, setUsernameError] = useState('')
  const [passwordError, setPasswordError] = useState('')
  const [errorMessage, setErrorMessage] = useState('')
  const [cloud18Enabled, setCloud18Enabled] = useState(false)
  const [cloud18Loading, setCloud18Loading] = useState(false)
  // with Sign in with Cloud18 the page asks the name first: an internal account then shows the
  // password field, a Cloud18 user goes to the realm (#2185)
  const [showPassword, setShowPassword] = useState(false)
  const { theme } = useTheme()

  const navigate = useNavigate()
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
          navigate('/')
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
  }, [dispatch, navigate])

  // Sign in with Cloud18 (#2185) on a partner repman: the silent check on opening (already signed in
  // at Cloud18 in this browser: straight in, nothing shown), and the way back from the realm, a
  // one-time code in the address traded once for the session (never a token in the address).
  const searchParams = new URLSearchParams(window.location.search)
  const cloud18Code = searchParams.get('cloud18')
  const cloud18Back = cloud18Code || searchParams.get('cloud18_error') || searchParams.get('cloud18_silent')
  const cloud18Traded = useRef(false) // the one-time code is sent once (StrictMode runs effects twice)
  useEffect(() => {
    if (dashboard) return
    authService.cloud18SSOStatus()
      .then((resp) => {
        const enabled = resp?.status === 200 && resp?.data?.enabled === true
        setCloud18Enabled(enabled)
        // once per tab: after a Logout (which ends the Cloud18 session) or a check that found none,
        // the page shows its form and does not try again
        if (enabled && !cloud18Back && !isAuthorized() && sessionStorage.getItem('cloud18_silent_tried') !== '1') {
          sessionStorage.setItem('cloud18_silent_tried', '1')
          window.location.assign('/api/auth/cloud18/start?silent=1')
          return
        }
        // no silent check: the page's usual autologin (not after a Cloud18 return with a code)
        if (!cloud18Code && !isAuthorized()) doAutologin()
      })
      .catch(() => {
        setCloud18Enabled(false)
        if (!cloud18Code && !isAuthorized()) doAutologin()
      })
    const params = new URLSearchParams(window.location.search)
    if (params.get('cloud18_silent')) {
      window.history.replaceState(null, '', '/login')
    }
    const failed = params.get('cloud18_error')
    if (failed) {
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
    if (!cloud18Code || cloud18Traded.current) return
    cloud18Traded.current = true
    window.history.replaceState(null, '', '/login')
    setCloud18Loading(true)
    authService.cloud18SSOSession(cloud18Code)
      .then((resp) => {
        if (resp?.status === 200 && resp?.data?.token) {
          localStorage.setItem('user_token', resp.data.token)
          localStorage.setItem('username', resp.data.username)
          localStorage.setItem('login_method', 'cloud18')
          dispatch(setUserData())
          navigate('/')
        } else {
          setErrorMessage('Sign in with Cloud18: the sign-in expired, try again')
        }
      })
      .catch(() => setErrorMessage('Sign in with Cloud18: no answer, try again'))
      .finally(() => setCloud18Loading(false))
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
            navigate('/slideshow')
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
    if (sessionStatus !== 'unauthenticated') return
    if (isAuthorized()) return
    doAutologin()
  }, [sessionStatus, autologinRetryCount, doAutologin])

  useEffect(() => {
    if (!loading || !loadingGitLogin) {
      if (isLogged && user) {
        // Navigate to the right destination after manual login regardless of mode.
        navigate(dashboard ? '/slideshow' : '/')
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

    // Sign in with Cloud18 (#2185): the name decides, an internal account asks its password here,
    // a Cloud18 user goes to the realm with the email prefilled (one password, on the realm's page)
    if (cloud18Enabled && !showPassword) {
      setCloud18Loading(true)
      authService.loginMethod(username)
        .then((resp) => {
          if (resp?.status === 200 && resp?.data?.method === 'cloud18') {
            window.location.assign('/api/auth/cloud18/start?login_hint=' + encodeURIComponent(username))
            return
          }
          setCloud18Loading(false)
          setShowPassword(true)
        })
        .catch(() => {
          setCloud18Loading(false)
          setShowPassword(true)
        })
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
              <Stack spacing='5'>
                <FormControl isInvalid={usernameError}>
                  <FormLabel className={theme === 'dark' ? styles.darkLoginText : ""} htmlFor='username'>Username or Email</FormLabel>
                  <Input id='username' type='text' value={username} className={theme === 'dark' ? styles.darkLoginText : ""} onChange={(e) => { setUsername(e.target.value); setShowPassword(false) }} />
                  <FormErrorMessage sx={styles.errorMessage}>{usernameError}</FormErrorMessage>
                </FormControl>
                {(!cloud18Enabled || showPassword) && <PasswordControl
                  passwordError={passwordError}
                  inputClassName={theme === 'dark' ? styles.darkLoginText : ""}
                  labelClassName={theme === 'dark' ? styles.darkLoginText : ""}
                  onChange={(e) => setPassword(e.target.value)}
                  className={`${styles.revealButton} ${styles.errorMessage} ${theme === 'dark' ? styles.darkLoginText : ""}`}
                />}
              </Stack>
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
                <RMButton
                  id='btnGitRegister'
                  type='button'
                  size='medium'
                  onClick={() => (cloud18Enabled ? window.location.assign('/api/auth/cloud18/start?signup=1') : navigate('/signup'))}
                  isLoading={loadingGitLogin}
                  loadingText={'Signing up to Cloud18'}>
                  Sign up
                </RMButton>
              </Stack>
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
