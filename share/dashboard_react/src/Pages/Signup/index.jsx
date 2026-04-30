import { useNavigate } from 'react-router-dom'
import { Container, Heading, Stack } from '@chakra-ui/react'
import PageContainer from '../PageContainer'
import SignupForm from '../../components/Auth/SignupForm'
import { authService } from '../../services/authService'

function Signup() {
  const navigate = useNavigate()

  const handleSignupSuccess = () => {
    setTimeout(() => navigate('/login'), 1500)
  }

  return (
    <PageContainer>
      <Container maxWidth='lg' py={{ base: '10', md: '12' }} px={{ base: '0', sm: '8' }}>
        <Stack spacing='6'>
          <Stack spacing='4'>
            <Stack spacing={{ base: '2', md: '3' }} textAlign='center'>
              <Heading size='md'>Sign up with GitLab SSO</Heading>
            </Stack>
          </Stack>
          <SignupForm
            onSubmit={authService.signup}
            onSuccess={handleSignupSuccess}
            successMessage='Signup successful. Redirecting to login...'
          />
        </Stack>
      </Container>
    </PageContainer>
  )
}

export default Signup
