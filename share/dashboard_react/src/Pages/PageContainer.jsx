import React, { useEffect, lazy, Suspense } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { useNavigate } from 'react-router-dom'
import { logout, setUserData } from '../redux/authSlice'
import { Box, useBreakpointValue } from '@chakra-ui/react'
import { isAuthorized } from '../utility/common'
import { getClusters, getMonitoredData } from '../redux/clusterSlice'
import { setIsMobile, setIsTablet, setIsDesktop } from '../redux/commonSlice'
import { AppSettings } from '../AppSettings'
const Navbar = lazy(() => import('../components/Navbar'))

function PageContainer({ children }) {
  const dispatch = useDispatch()
  const navigate = useNavigate()
  const styles = {
    container: {
      display: 'flex',
      flexDirection: 'column',
      height: '100%',
      width: '100%'
    }
  }

  const {
    common: { theme },
    auth: { isLogged, user }
  } = useSelector((state) => state)

  const currentBreakpoint = useBreakpointValue({
    base: 'base',
    sm: 'mobile',
    md: 'tablet',
    lg: 'desktop'
  })

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
      {children}
    </Box>
  )
}

export default PageContainer
