import React, { useEffect,lazy,Suspense } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { useNavigate } from 'react-router-dom'
import { logout } from '../../redux/authSlice'
import { Box } from '@chakra-ui/react'
const Navbar= lazy(()=>import('../Navbar'))
function PageContainer({ children }) {
  const dispatch = useDispatch()
  const navigate = useNavigate()
  const styles = {
    container: {
      display: 'flex',
      bg: '#eff2fe',
      height: '100%',
      width: '100%'
    }
  }

  const {
    auth: { isLogged, user }
  } = useSelector((state) => state)
  useEffect(() => {
    if (!isLogged && user === null) {
      navigate('/login')
    }
  }, [isLogged, user])

  const handleLogout = () => {
    dispatch(logout())
  }
  const isAuthorized = () => {
    return localStorage.getItem('user_token') !== null
  }
  return (
    <Box sx={styles.container}>
      {isAuthorized() &&   <Suspense fallback={<div>Loading...</div>}><Navbar /></Suspense>}
      {children}
    </Box>
  )
}

export default PageContainer
