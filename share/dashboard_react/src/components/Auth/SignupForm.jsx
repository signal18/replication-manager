import { useState, useEffect } from 'react'
import { Box, Flex, FormControl, FormErrorMessage, FormLabel, Heading, HStack, Input, SimpleGrid, Stack, Text, VStack } from '@chakra-ui/react'
import { useTheme } from '../../ThemeProvider'
import PasswordControl from '../PasswordControl'
import RMButton from '../RMButton'
import Message from '../Message'
import loginStyles from '../../Pages/Login/styles.module.scss'
import { authService } from '../../services/authService'

const emailRegex = /^[^@\s]+@[^@\s]+\.[^@\s]+$/

function SignupForm({
  onSubmit,
  onSuccess,
  onCancel,
  submitLabel = 'Create account',
  loadingText = 'Signing up',
  successMessage = 'Signup successful',
  className = '',
  splitLayout = false
}) {
  const { theme } = useTheme()

  const [firstName, setFirstName] = useState('')
  const [lastName, setLastName] = useState('')
  const [username, setUsername] = useState('')
  const [email, setEmail] = useState('')
  const [telephone, setTelephone] = useState('')
  const [company, setCompany] = useState('')
  const [password, setPassword] = useState('')
  const [errors, setErrors] = useState({})
  const [loading, setLoading] = useState(false)
  const [errorMessage, setErrorMessage] = useState('')
  const [localSuccessMessage, setLocalSuccessMessage] = useState('')
  const [promo, setPromo] = useState(null) // { enabled, free_credit_offer, ... }

  useEffect(() => {
    let cancelled = false
    authService.getSignupPromo()
      .then((res) => {
        if (cancelled) return
        if (res.status === 200 && res.data?.promo?.enabled) {
          setPromo(res.data.promo)
        }
      })
      .catch(() => { /* promo is optional; fail silently */ })
    return () => { cancelled = true }
  }, [])

  const validate = () => {
    const nextErrors = {}
    if (!firstName.trim()) nextErrors.firstName = 'Please enter your first name'
    if (!lastName.trim()) nextErrors.lastName = 'Please enter your last name'
    if (!username.trim()) nextErrors.username = 'Please enter your username'
    if (!email.trim()) nextErrors.email = 'Please enter your email'
    else if (!emailRegex.test(email.trim())) nextErrors.email = 'Please enter a valid email'
    if (!password) nextErrors.password = 'Please enter a password'
    setErrors(nextErrors)
    return Object.keys(nextErrors).length === 0
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    setErrorMessage('')
    setLocalSuccessMessage('')

    if (!validate()) return

    setLoading(true)
    try {
      const payload = {
        first_name: firstName.trim(),
        last_name: lastName.trim(),
        username: username.trim(),
        email: email.trim().toLowerCase(),
        telephone: telephone.trim(),
        company: company.trim(),
        password
      }

      const response = await onSubmit(payload)
      if (response.status >= 200 && response.status < 300) {
        setLocalSuccessMessage(successMessage)
        if (onSuccess) {
          await Promise.resolve(onSuccess(response, payload))
        }
      } else {
        const message = typeof response.data === 'object' && response.data?.error
          ? response.data.error
          : 'Signup failed'
        setErrorMessage(message)
      }
    } catch (error) {
      setErrorMessage(error.message || 'Signup failed')
    } finally {
      setLoading(false)
    }
  }

  const fieldLabelClass = theme === 'dark' ? loginStyles.darkLoginText : ''
  const fieldInputClass = theme === 'dark' ? loginStyles.darkLoginText : ''
  const revealBtnClass = `${loginStyles.revealButton} ${loginStyles.errorMessage} ${theme === 'dark' ? loginStyles.darkLoginText : ''}`

  const formFields = (
    <Stack spacing='3'>
      <SimpleGrid columns={{ base: 1, md: 2 }} spacing='3'>
        <FormControl isInvalid={!!errors.firstName}>
          <FormLabel className={fieldLabelClass} fontSize='sm' fontWeight='semibold'>First Name</FormLabel>
          <Input value={firstName} size='sm' className={fieldInputClass} onChange={(e) => setFirstName(e.target.value)} />
          <FormErrorMessage>{errors.firstName}</FormErrorMessage>
        </FormControl>
        <FormControl isInvalid={!!errors.lastName}>
          <FormLabel className={fieldLabelClass} fontSize='sm' fontWeight='semibold'>Last Name</FormLabel>
          <Input value={lastName} size='sm' className={fieldInputClass} onChange={(e) => setLastName(e.target.value)} />
          <FormErrorMessage>{errors.lastName}</FormErrorMessage>
        </FormControl>
      </SimpleGrid>
      <FormControl isInvalid={!!errors.username}>
        <FormLabel className={fieldLabelClass} fontSize='sm' fontWeight='semibold'>Username</FormLabel>
        <Input value={username} size='sm' className={fieldInputClass} onChange={(e) => setUsername(e.target.value)} />
        <FormErrorMessage>{errors.username}</FormErrorMessage>
      </FormControl>
      <FormControl isInvalid={!!errors.email}>
        <FormLabel className={fieldLabelClass} fontSize='sm' fontWeight='semibold'>Email</FormLabel>
        <Input type='email' value={email} size='sm' className={fieldInputClass} onChange={(e) => setEmail(e.target.value)} />
        <FormErrorMessage>{errors.email}</FormErrorMessage>
      </FormControl>
      <FormControl>
        <FormLabel className={fieldLabelClass} fontSize='sm' fontWeight='semibold'>Telephone <Text as='span' fontWeight='normal' color='gray.400'>(optional)</Text></FormLabel>
        <Input type='tel' value={telephone} size='sm' className={fieldInputClass} onChange={(e) => setTelephone(e.target.value)} />
      </FormControl>
      <FormControl>
        <FormLabel className={fieldLabelClass} fontSize='sm' fontWeight='semibold'>Company <Text as='span' fontWeight='normal' color='gray.400'>(optional)</Text></FormLabel>
        <Input value={company} size='sm' className={fieldInputClass} onChange={(e) => setCompany(e.target.value)} />
      </FormControl>
      <PasswordControl
        passwordError={errors.password}
        inputClassName={fieldInputClass}
        labelClassName={fieldLabelClass}
        onChange={(e) => setPassword(e.target.value)}
        className={revealBtnClass}
        size='sm'
      />
    </Stack>
  )

  if (splitLayout) {
    return (
      <Box as='form' className={`${loginStyles.loginForm} ${className}`.trim()} onSubmit={handleSubmit}>
        <SimpleGrid columns={{ base: 1, md: 2 }} spacing={8} alignItems='start'>
          {/* Left column — promo + branding */}
          <VStack spacing={5} align='stretch'>
            {promo?.free_credit_offer > 0 && (
              <Box
                bg='linear-gradient(135deg, #059669 0%, #10b981 100%)'
                borderRadius='lg'
                p={5}
                boxShadow='md'
              >
                <Flex align='center' gap={3}>
                  <Box
                    bg='white'
                    borderRadius='full'
                    boxSize='52px'
                    display='flex'
                    alignItems='center'
                    justifyContent='center'
                    flexShrink={0}
                  >
                    <Text fontSize='2xl' role='img' aria-label='gift'>🎁</Text>
                  </Box>
                  <Box>
                    <Text fontSize='xl' fontWeight='bold' color='white'>
                      Receive {promo.free_credit_offer} free credits
                    </Text>
                    <Text fontSize='sm' color='green.100'>
                      Applied after account verification.
                    </Text>
                  </Box>
                </Flex>
              </Box>
            )}

            <Box>
              <Heading size='lg' mb={2}>Create your GitLab SSO account</Heading>
              <Text fontSize='sm' color='gray.500'>
                Get started with Cloud18 — one account, full access.
              </Text>
            </Box>

            <Text fontSize='sm' color='gray.600' lineHeight='taller'>
              With your Cloud18 account you get:
            </Text>
            <VStack spacing={3} align='stretch'>
              {[
                'Access to community via meet.signal18.io',
                'Backup encrypted configs to cloud repository',
                'Extra alerting on MariaDB blocker issues',
              ].map((item, i) => (
                <HStack key={i} spacing={3}>
                  <Box bg='green.100' borderRadius='full' boxSize='24px' display='flex' alignItems='center' justifyContent='center' flexShrink={0}>
                    <Text fontSize='xs' color='green.700'>✓</Text>
                  </Box>
                  <Text fontSize='sm' color='gray.600'>{item}</Text>
                </HStack>
              ))}
            </VStack>
          </VStack>

          {/* Right column — form card */}
          <VStack spacing={4} align='stretch'>
            <Box
              borderWidth='1px'
              borderRadius='lg'
              borderColor={theme === 'dark' ? 'gray.600' : 'gray.200'}
              bg={theme === 'dark' ? 'gray.700' : 'white'}
              p={{ base: 3, md: 4 }}
            >
              {formFields}
            </Box>

            {errorMessage && <Message message={errorMessage} />}
            {localSuccessMessage && <Message message={localSuccessMessage} type='success' />}

            <Stack spacing='4' direction={{ base: 'column', sm: 'row' }}>
              <RMButton type='submit' size='medium' isLoading={loading} loadingText={loadingText}>
                {submitLabel}
              </RMButton>
              {onCancel && (
                <RMButton type='button' size='medium' variant='ghost' onClick={onCancel} isDisabled={loading}>
                  Cancel
                </RMButton>
              )}
            </Stack>
          </VStack>
        </SimpleGrid>
      </Box>
    )
  }

  return (
    <Box as='form' className={`${loginStyles.loginForm} ${className}`.trim()} onSubmit={handleSubmit}>
      <Stack spacing='4'>
        {/* Promotional hero banner — top of form for maximum visibility */}
        {promo?.free_credit_offer > 0 && (
          <Box
            bg='linear-gradient(135deg, #059669 0%, #10b981 100%)'
            borderRadius='lg'
            p={4}
            boxShadow='md'
          >
            <HStack spacing={3}>
              <Box
                bg='white'
                borderRadius='full'
                boxSize='48px'
                display='flex'
                alignItems='center'
                justifyContent='center'
                flexShrink={0}
              >
                <Text fontSize='2xl' role='img' aria-label='gift'>
                  🎁
                </Text>
              </Box>
              <Box>
                <Text fontSize='lg' fontWeight='bold' color='white'>
                  Receive {promo.free_credit_offer} free credits
                </Text>
                <Text fontSize='sm' color='green.100' lineHeight='short'>
                  Applied after account verification.
                </Text>
              </Box>
            </HStack>
          </Box>
        )}

        {/* Account fields card */}
        <Box
          borderWidth='1px'
          borderRadius='lg'
          borderColor={theme === 'dark' ? 'gray.600' : 'gray.200'}
          bg={theme === 'dark' ? 'gray.700' : 'white'}
          p={{ base: 3, md: 4 }}
        >
          {formFields}
        </Box>

        {errorMessage && <Message message={errorMessage} />}
        {localSuccessMessage && <Message message={localSuccessMessage} type='success' />}

        <Stack spacing='6' direction={{ base: 'column', sm: 'row' }}>
          <RMButton type='submit' size='medium' isLoading={loading} loadingText={loadingText}>
            {submitLabel}
          </RMButton>
          {onCancel && (
            <RMButton type='button' size='medium' variant='ghost' onClick={onCancel} isDisabled={loading}>
              Cancel
            </RMButton>
          )}
        </Stack>
      </Stack>
    </Box>
  )
}

export default SignupForm
