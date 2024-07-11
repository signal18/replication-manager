import React, { forwardRef, useEffect, useRef, useState, Suspense } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { useNavigate } from 'react-router-dom'
import { gitLogin, login } from '../redux/authSlice'
import {
  Box,
  Button,
  Container,
  Divider,
  FormControl,
  FormLabel,
  FormErrorMessage,
  HStack,
  Heading,
  IconButton,
  Image,
  Input,
  InputGroup,
  InputRightElement,
  Stack,
  Text,
  background,
  useDisclosure,
  useMergeRefs,
  useTheme
} from '@chakra-ui/react'
import PageContainer from './PageContainer'
import { HiEye, HiEyeOff } from 'react-icons/hi'
import { isAuthorized } from '../utility/common'

function Login(props) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [usernameError, setUsernameError] = useState('')
  const [passwordError, setPasswordError] = useState('')
  const [errorMessage, setErrorMessage] = useState('')
  const theme = useTheme()

  const navigate = useNavigate()
  const dispatch = useDispatch()
  const {
    common: { theme: currentTheme },
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

  const styles = {
    loginContainer: {
      bg: '#fff',
      label: {
        color: theme.colors.primary.dark
      },
      input: {
        color: theme.colors.primary.dark,
        borderColor: 'var(--chakra-colors-gray-200)',
        '&:hover': {
          borderColor: 'var(--chakra-colors-gray-200)'
        }
      }
    },
    errorMessage: {
      color: 'var(--chakra-colors-red-500)'
    },
    revealButton: {
      svg: {
        fill: theme.colors.primary.dark
      }
    }
  }

  const onButtonClick = () => {
    // Set initial error values to empty
    setUsernameError('')
    setPasswordError('')

    // Check if the user has entered both fields correctly
    if ('' === username) {
      setUsernameError('Please enter your username')
      return
    }

    if ('' === password) {
      setPasswordError('Please enter a password')
      return
    }

    logIn()

    // Authentication calls will be made here...
  }

  const logIn = () => {
    dispatch(login({ username, password }))
  }

  const onGitButtonClick = () => {
    dispatch(gitLogin({}))
  }

  return (
    <PageContainer>
      <Suspense fallback={<div>Loading...</div>}>
        <Container maxWidth='lg' py={{ base: '12', md: '24' }} px={{ base: '0', sm: '8' }}>
          <Stack spacing='8'>
            <Stack spacing='6'>
              <Stack spacing={{ base: '2', md: '3' }} textAlign='center'>
                <Heading size='md'>Sign in to your account</Heading>
              </Stack>
            </Stack>
            <Box
              sx={styles.loginContainer}
              py={{ base: '8', sm: '8' }}
              px={{ base: '4', sm: '10' }}
              bg={{ base: 'transparent', sm: 'bg.surface' }}
              boxShadow={{ base: 'none', sm: 'md' }}
              borderRadius={{ base: 'none', sm: 'xl' }}>
              <Stack spacing='6'>
                <Stack spacing='5'>
                  <FormControl isInvalid={usernameError}>
                    <FormLabel htmlFor='username'>Username</FormLabel>
                    <Input id='username' type='text' value={username} onChange={(e) => setUsername(e.target.value)} />
                    <FormErrorMessage sx={styles.errorMessage}>{usernameError}</FormErrorMessage>
                  </FormControl>
                  <PasswordField
                    passwordError={passwordError}
                    onChange={(e) => setPassword(e.target.value)}
                    styles={styles}
                  />
                </Stack>
                {error && <Text color='red.500'>{error}</Text>}

                <Stack spacing='6'>
                  <Button type='button' onClick={onButtonClick} isLoading={loading} loadingText={'Signing in'}>
                    Sign in
                  </Button>
                  <Button type='button' onClick={onGitButtonClick} isLoading={false}>
                    Sign in with cloud18
                  </Button>
                </Stack>
              </Stack>
            </Box>
          </Stack>
        </Container>
      </Suspense>
    </PageContainer>
  )
}

export default Login

const PasswordField = forwardRef((props, ref) => {
  const { isOpen, onToggle } = useDisclosure()
  const inputRef = useRef(null)

  const mergeRef = useMergeRefs(inputRef, ref)
  const onClickReveal = () => {
    onToggle()
    if (inputRef.current) {
      inputRef.current.focus({ preventScroll: true })
    }
  }

  return (
    <FormControl isInvalid={props.passwordError}>
      <FormLabel htmlFor='password'>Password</FormLabel>
      <InputGroup>
        <InputRightElement>
          <IconButton
            sx={props.styles.revealButton}
            variant='text'
            aria-label={isOpen ? 'Mask password' : 'Reveal password'}
            icon={isOpen ? <HiEyeOff /> : <HiEye />}
            onClick={onClickReveal}
          />
        </InputRightElement>
        <Input
          id='password'
          ref={mergeRef}
          name='password'
          onChange={props.onChange}
          type={isOpen ? 'text' : 'password'}
          autoComplete='current-password'
          required
          {...props}
        />
      </InputGroup>
      <FormErrorMessage sx={props.styles.errorMessage}>{props.passwordError}</FormErrorMessage>
    </FormControl>
  )
})
