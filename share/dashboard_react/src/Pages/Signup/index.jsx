import { useNavigate } from 'react-router-dom'
import { Container } from '@chakra-ui/react'
import PageContainer from '../PageContainer'
import SignupForm from '../../components/Auth/SignupForm'
import { authService } from '../../services/authService'
import { isConfirmedSignupResponse, isPendingSignupResponse } from '../../components/Auth/signupResponse'

function Signup() {
  const navigate = useNavigate()

  const handleSignupSuccess = (response) => {
    if (isConfirmedSignupResponse(response)) {
      setTimeout(() => navigate('/login'), 1500)
    } else if (isPendingSignupResponse(response)) {
      // Give the user time to read the "check your email" message before redirecting.
      setTimeout(() => navigate('/login'), 3000)
    }
  }

  return (
    <PageContainer>
      <Container maxWidth='6xl' py={{ base: '8', md: '12' }} px={{ base: '0', sm: '8' }}>
        <SignupForm
          splitLayout
          onSubmit={authService.signup}
          onSuccess={handleSignupSuccess}
          successMessage='Email already confirmed. Redirecting to login...'
        />
      </Container>
    </PageContainer>
  )
}

export default Signup
