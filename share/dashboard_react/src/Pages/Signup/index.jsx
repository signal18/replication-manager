import { useState, useEffect, useCallback, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { useDispatch } from 'react-redux'
import { Box, Container, Flex, Heading, Text, VStack, Spinner } from '@chakra-ui/react'
import { HiMail } from 'react-icons/hi'
import PageContainer from '../PageContainer'
import SignupForm from '../../components/Auth/SignupForm'
import RMButton from '../../components/RMButton'
import { authService } from '../../services/authService'
import { login } from '../../redux/authSlice'

const COUNTDOWN_SECONDS = 180 // 3 minutes
const STATUS_POLL_INTERVAL = 5000 // 5 seconds

function EmailVerificationWait({ email, password, onGoToLogin }) {
  const [secondsLeft, setSecondsLeft] = useState(COUNTDOWN_SECONDS)
  const [confirmed, setConfirmed] = useState(false)
  const dispatch = useDispatch()
  const navigate = useNavigate()
  const pollRef = useRef(null)

  // Countdown timer
  useEffect(() => {
    if (secondsLeft <= 0) return
    const timer = setInterval(() => setSecondsLeft((s) => s - 1), 1000)
    return () => clearInterval(timer)
  }, [secondsLeft <= 0])

  // Poll signup/status every 5 seconds via CRM (no login attempt, no rate limit)
  useEffect(() => {
    if (confirmed || secondsLeft <= 0) return

    const checkStatus = async () => {
      try {
        const response = await authService.getSignupStatus(email)
        const data = typeof response.data === 'string' ? JSON.parse(response.data) : response.data
        if (data?.state === 'confirmed') {
          clearInterval(pollRef.current)
          setConfirmed(true)
          // Email confirmed — now do the real login to get a JWT
          dispatch(login({ username: email, password })).then((action) => {
            if (action.meta?.requestStatus === 'fulfilled') {
              navigate('/')
            }
          })
        }
      } catch {
        // CRM unreachable — ignore, retry next tick
      }
    }

    pollRef.current = setInterval(checkStatus, STATUS_POLL_INTERVAL)
    return () => clearInterval(pollRef.current)
  }, [confirmed, secondsLeft <= 0, email, password, dispatch, navigate])

  const minutes = Math.floor(secondsLeft / 60)
  const seconds = secondsLeft % 60
  const timeDisplay = `${minutes}:${seconds.toString().padStart(2, '0')}`

  return (
    <VStack spacing={6} py={10} textAlign='center'>
      <Flex bg='blue.50' borderRadius='full' boxSize='80px' alignItems='center' justifyContent='center'>
        <Box as={HiMail} boxSize='40px' color='blue.500' />
      </Flex>

      <Heading size='lg'>Check your email</Heading>

      <Text fontSize='md' color='gray.600' maxW='md'>
        We sent a verification link to <strong>{email}</strong>.
        Please click the link in the email to activate your account.
      </Text>

      {confirmed ? (
        <Box bg='green.50' borderRadius='lg' px={6} py={4} borderWidth='1px' borderColor='green.200'>
          <Text fontSize='md' color='green.600' fontWeight='semibold'>
            Email verified! Logging you in...
          </Text>
          <Spinner size='sm' color='green.500' mt={2} />
        </Box>
      ) : secondsLeft > 0 ? (
        <Box bg='gray.50' borderRadius='lg' px={6} py={4} borderWidth='1px' borderColor='gray.200'>
          <Flex align='center' gap={3} justify='center'>
            <Spinner size='sm' color='blue.500' />
            <Text fontSize='sm' color='gray.500'>
              Waiting for email verification
            </Text>
          </Flex>
          <Text fontSize='3xl' fontWeight='bold' fontFamily='mono' color='blue.600' mt={2}>
            {timeDisplay}
          </Text>
        </Box>
      ) : (
        <Text fontSize='sm' color='orange.500' fontWeight='semibold'>
          Verification link may have expired. Please check your spam folder or try signing up again.
        </Text>
      )}

      <VStack spacing={2}>
        <RMButton onClick={onGoToLogin} size='medium'>
          Go to Login
        </RMButton>
        <Text fontSize='xs' color='gray.400'>
          You can log in once your email is verified.
        </Text>
      </VStack>
    </VStack>
  )
}

function Signup() {
  const navigate = useNavigate()
  const [verificationData, setVerificationData] = useState(null)

  const handleSignupSuccess = useCallback((response, payload) => {
    setVerificationData({ email: payload.email, password: payload.password })
  }, [])

  if (verificationData) {
    return (
      <PageContainer>
        <Container maxWidth='lg' py={{ base: '8', md: '12' }} px={{ base: '0', sm: '8' }}>
          <EmailVerificationWait
            email={verificationData.email}
            password={verificationData.password}
            onGoToLogin={() => navigate('/login')}
          />
        </Container>
      </PageContainer>
    )
  }

  return (
    <PageContainer>
      <Container maxWidth='6xl' py={{ base: '8', md: '12' }} px={{ base: '0', sm: '8' }}>
        <SignupForm
          splitLayout
          onSubmit={authService.signup}
          onSuccess={handleSignupSuccess}
          successMessage='Account created! Preparing verification...'
        />
      </Container>
    </PageContainer>
  )
}

export default Signup
