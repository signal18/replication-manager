import React, { useCallback, useEffect, useState, lazy } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { useLocation, useNavigate } from 'react-router-dom'
import { whoami } from '../../redux/authSlice'
import { Box, useBreakpointValue, Text, HStack, Link } from '@chakra-ui/react'
import { isAuthorized } from '../../utility/common'
import { setIsMobile, setIsTablet, setIsDesktop } from '../../redux/commonSlice'
import Navbar from '../../components/Navbar'
import styles from './styles.module.scss'
import { getMonitoredData } from '../../redux/globalClustersSlice'

function PageContainer({ children }) {
  const dispatch = useDispatch()
  const location = useLocation()
  const navigate = useNavigate()
  const [fullVersion, setFullVersion] = useState('')

  const isLogged = useSelector((state) => state.auth.isLogged)
  const user = useSelector((state) => state.auth.user)
  const monitor = useSelector((state) => state.globalClusters.monitor)
  const { isMobile, isTablet, isDesktop } = useSelector((state) => state.common)

  const currentBreakpoint = useBreakpointValue({
    base: 'base',
    sm: 'mobile',
    md: 'tablet',
    lg: 'desktop'
  })

  useEffect(() => {
    if (monitor === null) {
      dispatch(getMonitoredData({}))
    }

    if (monitor?.fullVersion) {
      setFullVersion(monitor?.fullVersion)
    }
  }, [monitor])

  const handleResize = useCallback(() => {
    const nextIsMobile = currentBreakpoint === 'mobile' || currentBreakpoint === 'base'
    const nextIsTablet = currentBreakpoint === 'tablet'
    const nextIsDesktop = currentBreakpoint === 'desktop'
    if (nextIsMobile !== isMobile) {
      dispatch(setIsMobile(nextIsMobile))
    }
    if (nextIsTablet !== isTablet) {
      dispatch(setIsTablet(nextIsTablet))
    }
    if (nextIsDesktop !== isDesktop) {
      dispatch(setIsDesktop(nextIsDesktop))
    }
  }, [currentBreakpoint, dispatch, isDesktop, isMobile, isTablet])

  useEffect(() => {
    if (isAuthorized() && (user === null || user.User === undefined)) {
      dispatch(whoami({}))
    }
    handleResize() // Initial setup

    window.addEventListener('resize', handleResize)

    return () => {
      window.removeEventListener('resize', handleResize)
    }
  }, [dispatch, handleResize, user])

  useEffect(() => {
    const isPublicAuthPage = location.pathname === '/login' || location.pathname === '/signup'
    if (!isLogged && user === null && !isAuthorized() && !isPublicAuthPage) {
      // Slideshow auth goes through /dashboard (viewer token) not /login (regular autologin → /)
      navigate(location.pathname === '/slideshow' ? '/dashboard' : '/login')
    }
  }, [isLogged, location.pathname, navigate, user])

  return (
    <Box className={styles.container}>
      <Navbar username={user?.username} />
      <Box className={styles.pageContent}>{children}</Box>
      <Box as='footer' className={styles.footer} textAlign={(location.pathname === '/login' || location.pathname === '/signup') ? 'right' : 'left'}>
        {(location.pathname === '/login' || location.pathname === '/signup')
          ? monitor?.config?.apiSwaggerEnabled && (<Link href='/api-docs/index.html' target='_blank' rel='noreferrer'>API Swagger</Link>)
          : (<Text>{`Replication-Manager ${fullVersion} © 2017-${new Date().getFullYear()} SIGNAL18 CLOUD SAS`}</Text>)}
      </Box>
    </Box>
  )
}

export default PageContainer
