import { useNavigate } from 'react-router-dom'
import { Container } from '@chakra-ui/react'
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
      <Container maxWidth='6xl' py={{ base: '8', md: '12' }} px={{ base: '0', sm: '8' }}>
        <SignupForm
          splitLayout
          onSubmit={authService.signup}
          onSuccess={handleSignupSuccess}
          successMessage='Signup successful. Redirecting to login...'
        />
      </Container>
    </PageContainer>
  )
}

export default Signup
