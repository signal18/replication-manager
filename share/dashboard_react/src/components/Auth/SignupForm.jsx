import { useState } from 'react'
import { Box, FormControl, FormErrorMessage, FormLabel, Input, Stack } from '@chakra-ui/react'
import { useTheme } from '../../ThemeProvider'
import PasswordControl from '../PasswordControl'
import RMButton from '../RMButton'
import Message from '../Message'
import loginStyles from '../../Pages/Login/styles.module.scss'

const emailRegex = /^[^@\s]+@[^@\s]+\.[^@\s]+$/

function SignupForm({
  onSubmit,
  onSuccess,
  onCancel,
  submitLabel = 'Create account',
  loadingText = 'Signing up',
  successMessage = 'Signup successful',
  className = ''
}) {
  const { theme } = useTheme()

  const [firstName, setFirstName] = useState('')
  const [lastName, setLastName] = useState('')
  const [username, setUsername] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [errors, setErrors] = useState({})
  const [loading, setLoading] = useState(false)
  const [errorMessage, setErrorMessage] = useState('')
  const [localSuccessMessage, setLocalSuccessMessage] = useState('')

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

  return (
    <Box as='form' className={`${loginStyles.loginForm} ${className}`.trim()} onSubmit={handleSubmit}>
      <Stack spacing='6'>
        <Stack spacing='5'>
          <FormControl isInvalid={errors.firstName}>
            <FormLabel className={theme === 'dark' ? loginStyles.darkLoginText : ''}>First Name</FormLabel>
            <Input value={firstName} className={theme === 'dark' ? loginStyles.darkLoginText : ''} onChange={(e) => setFirstName(e.target.value)} />
            <FormErrorMessage sx={loginStyles.errorMessage}>{errors.firstName}</FormErrorMessage>
          </FormControl>
          <FormControl isInvalid={errors.lastName}>
            <FormLabel className={theme === 'dark' ? loginStyles.darkLoginText : ''}>Last Name</FormLabel>
            <Input value={lastName} className={theme === 'dark' ? loginStyles.darkLoginText : ''} onChange={(e) => setLastName(e.target.value)} />
            <FormErrorMessage sx={loginStyles.errorMessage}>{errors.lastName}</FormErrorMessage>
          </FormControl>
          <FormControl isInvalid={errors.username}>
            <FormLabel className={theme === 'dark' ? loginStyles.darkLoginText : ''}>Username</FormLabel>
            <Input value={username} className={theme === 'dark' ? loginStyles.darkLoginText : ''} onChange={(e) => setUsername(e.target.value)} />
            <FormErrorMessage sx={loginStyles.errorMessage}>{errors.username}</FormErrorMessage>
          </FormControl>
          <FormControl isInvalid={errors.email}>
            <FormLabel className={theme === 'dark' ? loginStyles.darkLoginText : ''}>Email</FormLabel>
            <Input type='email' value={email} className={theme === 'dark' ? loginStyles.darkLoginText : ''} onChange={(e) => setEmail(e.target.value)} />
            <FormErrorMessage sx={loginStyles.errorMessage}>{errors.email}</FormErrorMessage>
          </FormControl>
          <PasswordControl
            passwordError={errors.password}
            inputClassName={theme === 'dark' ? loginStyles.darkLoginText : ''}
            labelClassName={theme === 'dark' ? loginStyles.darkLoginText : ''}
            onChange={(e) => setPassword(e.target.value)}
            className={`${loginStyles.revealButton} ${loginStyles.errorMessage} ${theme === 'dark' ? loginStyles.darkLoginText : ''}`}
          />
        </Stack>

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
