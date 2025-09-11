import React, { useEffect, useState, lazy } from 'react'
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

  useEffect(() => {
    if (isAuthorized() && (user === null || user.User === undefined)) {
      dispatch(whoami({}))
    }
    handleResize() // Initial setup

    window.addEventListener('resize', handleResize)

    return () => {
      window.removeEventListener('resize', handleResize)
    }
  }, [currentBreakpoint, dispatch])

  useEffect(() => {
    if (!isLogged && user === null && !isAuthorized() && location.pathname !== '/login') {
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
    <Box className={styles.container}>
      <Navbar username={user?.username} />
      <Box className={styles.pageContent}>{children}</Box>
      <Box as='footer' className={styles.footer} textAlign={location.pathname === '/login' ? 'right' : 'left'}>
        {location.pathname === '/login' ? monitor?.config.apiSwaggerEnabled && (<Link href='/api-docs/index.html' target='_blank' rel='noreferrer'>API Swagger</Link>) : (<Text>{`Replication-Manager ${fullVersion} © 2017-${new Date().getFullYear()} SIGNAL18 CLOUD SAS`}</Text>)}
      </Box>
    </Box>
  )
}

export default PageContainer
