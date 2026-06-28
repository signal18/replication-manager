import React, { useCallback, useEffect, useRef, useState, Suspense } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { useNavigate } from 'react-router-dom'
import { login, logout, setUserData } from '../../redux/authSlice'
import styles from './styles.module.scss'
import { Box, Container, FormControl, FormLabel, FormErrorMessage, Heading, Input, Stack, Text } from '@chakra-ui/react'
import PageContainer from '../PageContainer'
import { isAuthorized } from '../../utility/common'
import PasswordControl from '../../components/PasswordControl'
import RMButton from '../../components/RMButton'
import Message from '../../components/Message'
import { useTheme } from '../../ThemeProvider'
import { clearCluster } from '../../redux/clusterSlice'
import { clearClusters } from '../../redux/globalClustersSlice'

function Login({ dashboard = false }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [usernameError, setUsernameError] = useState('')
  const [passwordError, setPasswordError] = useState('')
  const [errorMessage, setErrorMessage] = useState('')
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

  useEffect(() => {
    if (!dashboard) {
      // If a token exists, SessionGuard (App.jsx) is already validating it via whoami.
      // doAutologin will be called reactively below if whoami clears the stale token.
      if (isAuthorized()) return

      // No token on mount: try autologin immediately.
      doAutologin()
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
                  <Input id='username' type='text' value={username} className={theme === 'dark' ? styles.darkLoginText : ""} onChange={(e) => setUsername(e.target.value)} />
                  <FormErrorMessage sx={styles.errorMessage}>{usernameError}</FormErrorMessage>
                </FormControl>
                <PasswordControl
                  passwordError={passwordError}
                  inputClassName={theme === 'dark' ? styles.darkLoginText : ""}
                  labelClassName={theme === 'dark' ? styles.darkLoginText : ""}
                  onChange={(e) => setPassword(e.target.value)}
                  className={`${styles.revealButton} ${styles.errorMessage} ${theme === 'dark' ? styles.darkLoginText : ""}`}
                />
              </Stack>
              {errorMessage && <Message message={errorMessage} />}

              <Stack spacing='6'>
                <RMButton
                  id='btnLogin'
                  type='submit'
                  size='medium'
                  onClick={onButtonClick}
                  isLoading={loading}
                  loadingText={'Signing in'}>
                  Sign in
                </RMButton>
                <RMButton
                  id='btnGitRegister'
                  type='button'
                  size='medium'
                  onClick={() => navigate('/signup')}
                  isLoading={loadingGitLogin}
                  loadingText={'Signing up to Cloud18'}>
                  Sign up with GitLab SSO
                </RMButton>
              </Stack>
            </Stack>
          </Box>
        </Stack>
      </Container>
      {/* </Suspense> */}
    </PageContainer>
  )
}

export default Login
