import React, { useEffect, lazy } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { useNavigate } from 'react-router-dom'
import { setUserData } from '../redux/authSlice'
import { Box, useBreakpointValue } from '@chakra-ui/react'
import { isAuthorized } from '../utility/common'
import { setIsMobile, setIsTablet, setIsDesktop } from '../redux/commonSlice'
const Navbar = lazy(() => import('../components/Navbar'))

function PageContainer({ children }) {
  const dispatch = useDispatch()
  const navigate = useNavigate()

  const {
    common: { theme, isDesktop },
    auth: { isLogged, user }
  } = useSelector((state) => state)

  const currentBreakpoint = useBreakpointValue({
    base: 'base',
    sm: 'mobile',
    md: 'tablet',
    lg: 'desktop'
  })

  const styles = {
    container: {
      display: 'flex',
      flexDirection: 'column',
      height: '100%',
      width: '100%'
    },
    pageContent: {
      zIndex: 1,
      marginTop: isDesktop ? '74px' : '0'
    }
  }

  useEffect(() => {
    if (isAuthorized() && user === null) {
      dispatch(setUserData())
    }
    handleResize() // Initial setup

    window.addEventListener('resize', handleResize)

    return () => {
      window.removeEventListener('resize', handleResize)
    }
  }, [currentBreakpoint, dispatch])

  useEffect(() => {
    if (!isLogged && user === null && !isAuthorized()) {
      navigate('/login')
    }
  }, [isLogged, user])

  const handleResize = () => {
    const isMobile = currentBreakpoint === 'mobile' || currentBreakpoint === 'base'
    const isTablet = currentBreakpoint === 'tablet'
    const isDesktop = currentBreakpoint === 'desktop'
    dispatch(setIsMobile(isMobile))
    dispatch(setIsTablet(isTablet))
    dispatch(setIsDesktop(isDesktop))
  }

  return (
    <Box sx={styles.container}>
      <Navbar username={user?.username} theme={theme} />
      <Box sx={styles.pageContent}>{children}</Box>
    </Box>
  )
}

export default PageContainer
