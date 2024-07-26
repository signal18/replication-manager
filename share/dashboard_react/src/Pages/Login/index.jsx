import React, { useEffect, useState, Suspense } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { useNavigate } from 'react-router-dom'
import { gitLogin, login } from '../../redux/authSlice'
import styles from './styles.module.scss'
import { Box, Container, FormControl, FormLabel, FormErrorMessage, Heading, Input, Stack, Text } from '@chakra-ui/react'
import PageContainer from '../PageContainer'
import { isAuthorized } from '../../utility/common'
import PasswordControl from '../../components/PasswordControl'
import Button from '../../components/Button'

function Login(props) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [usernameError, setUsernameError] = useState('')
  const [passwordError, setPasswordError] = useState('')
  const [errorMessage, setErrorMessage] = useState('')

  const navigate = useNavigate()
  const dispatch = useDispatch()
  const {
    auth: { isLogged, loading, user, error }
  } = useSelector((state) => state)

  useEffect(() => {
    if (isAuthorized()) {
      navigate('/')
    }
  }, [])

  useEffect(() => {
    if (!loading) {
      if (isLogged && user) {
        navigate('/')
      }
      if (error) {
        setErrorMessage(error)
      }
    }
  }, [loading, isLogged])

  const onButtonClick = () => {
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

  const onGitButtonClick = () => {
    dispatch(gitLogin({}))
  }

  return (
    <PageContainer>
      {/* <Suspense fallback={<div>Loading...</div>}> */}
      <Container maxWidth='lg' py={{ base: '12', md: '24' }} px={{ base: '0', sm: '8' }}>
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
                  <FormLabel htmlFor='username'>Username</FormLabel>
                  <Input id='username' type='text' value={username} onChange={(e) => setUsername(e.target.value)} />
                  <FormErrorMessage sx={styles.errorMessage}>{usernameError}</FormErrorMessage>
                </FormControl>
                <PasswordControl
                  passwordError={passwordError}
                  onChange={(e) => setPassword(e.target.value)}
                  className={`${styles.revealButton} ${styles.errorMessage}`}
                />
              </Stack>
              {error && <Text color='red.500'>{error}</Text>}

              <Stack spacing='6'>
                <Button
                  type='submit'
                  size='medium'
                  onClick={onButtonClick}
                  isLoading={loading}
                  loadingText={'Signing in'}>
                  Sign in
                </Button>
                <Button size='medium' onClick={onGitButtonClick} isLoading={false}>
                  Sign in with cloud18
                </Button>
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
