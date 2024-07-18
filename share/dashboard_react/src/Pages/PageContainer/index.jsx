import React, { useEffect, useState, lazy } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { useNavigate } from 'react-router-dom'
import { setUserData } from '../../redux/authSlice'
import { Box, useBreakpointValue, Text, background, useColorMode } from '@chakra-ui/react'
import { isAuthorized } from '../../utility/common'
import { setIsMobile, setIsTablet, setIsDesktop } from '../../redux/commonSlice'
//const Navbar = lazy(() => import('../components/Navbar'))
import Navbar from '../../components/Navbar'
import styles from './styles.module.scss'

function PageContainer({ children }) {
  const dispatch = useDispatch()
  const navigate = useNavigate()
  const { colorMode } = useColorMode()

  const [fullVersion, setFullVersion] = useState('')

  const {
    common: { isDesktop },
    auth: { isLogged, user },
    cluster: { clusters }
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
      marginTop: isDesktop ? '54px' : '0'
    },
    footer: {
      px: '4',
      py: '2',
      background: colorMode === 'light' ? 'blue.100' : 'blue.900'
    }
  }
  console.log('fullversion::', fullVersion)

  useEffect(() => {
    if (clusters?.length > 0) {
      setFullVersion(clusters[0].config?.fullVersion)
    }
  }, [clusters])

  useEffect(() => {
    if (clusters?.length > 0) {
      setFullVersion(clusters[0].config?.fullVersion)
    }
  }, [clusters])

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
    <Box className={styles.container}>
      <Navbar username={user?.username} />
      <Box className={styles.pageContent}>{children}</Box>
      {fullVersion && (
        <Box as='footer' className={styles.footer}>
          <Text>{`Replication-Manager ${fullVersion} © 2017-${new Date().getFullYear()} SIGNAL18 CLOUD SAS`}</Text>
        </Box>
      )}
    </Box>
  )
}

export default PageContainer
