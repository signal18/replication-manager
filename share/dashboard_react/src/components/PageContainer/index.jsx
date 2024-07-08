import React, { useEffect, lazy, Suspense } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { useNavigate } from 'react-router-dom'
import { logout, setUserData } from '../../redux/authSlice'
import { Box, useBreakpointValue } from '@chakra-ui/react'
import { isAuthorized, getRefreshInterval } from '../../utility/common'
import { setRefreshInterval } from '../../redux/clusterSlice'
import { setIsMobile, setIsTablet, setIsDesktop } from '../../redux/commonSlice'
import ThemeIcon from '../ThemeIcon'
const Navbar = lazy(() => import('../Navbar'))

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
    auth: { isLogged, user },
    cluster: { refreshInterval }
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
    const interval = parseInt(getRefreshInterval())
    if (interval !== null && refreshInterval === 0) {
      dispatch(setRefreshInterval({ interval }))
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

  const handleLogout = () => {
    dispatch(logout())
  }

  const handleResize = () => {
    // console.log('breakpointValues::', breakpointValues)
    // const isMobile = window.innerWidth <= parseInt(breakpointValues.base)
    // const isTablet =
    //   window.innerWidth > parseInt(breakpointValues.base) && window.innerWidth <= parseInt(breakpointValues.md)
    // const isDesktop = window.innerWidth > parseInt(breakpointValues.md)
    const isMobile = currentBreakpoint === 'mobile'
    const isTablet = currentBreakpoint === 'tablet'
    const isDesktop = currentBreakpoint === 'desktop'
    dispatch(setIsMobile(isMobile))
    dispatch(setIsTablet(isTablet))
    dispatch(setIsDesktop(isDesktop))
  }

  return (
    <Box sx={styles.container}>
      <Suspense fallback={<div>Loading...</div>}>
        <Navbar username={user?.username} theme={theme} />
      </Suspense>
      {children}
    </Box>
  )
}

export default PageContainer
