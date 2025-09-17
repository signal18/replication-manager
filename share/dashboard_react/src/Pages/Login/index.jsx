import React, { useEffect, useState, Suspense } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { useNavigate } from 'react-router-dom'
import { login, logout } from '../../redux/authSlice'
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

function Login(props) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [usernameError, setUsernameError] = useState('')
  const [passwordError, setPasswordError] = useState('')
  const [errorMessage, setErrorMessage] = useState('')
  const { theme } = useTheme()

  const navigate = useNavigate()
  const dispatch = useDispatch()
  const {
    auth: { isLogged, loading, loadingGitLogin, user, error }
  } = useSelector((state) => state)

  useEffect(() => {
    if (isAuthorized()) {
      navigate('/')
    } else {
      // If not authorized, ensure the user is logged out and clear clusters
      dispatch(logout());
      dispatch(clearClusters());
      dispatch(clearCluster());
    }
  }, [])

  useEffect(() => {
    if (!loading || !loadingGitLogin) {
      if (isLogged && user) {
        navigate('/')
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
                  onClick={() => window.open("https://gitlab.signal18.io/users/sign_up", '_blank')}
                  isLoading={loadingGitLogin}
                  loadingText={'Signing up to Cloud18'}>
                  Sign Up To cloud18
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
