import { Box, Flex, Image, Button, Spacer, Text, HStack, useColorMode, IconButton, background } from '@chakra-ui/react'
import React, { useEffect } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { logout } from '../redux/authSlice'
import ThemeIcon from './ThemeIcon'
import RefreshCounter from './RefreshCounter'
import { isAuthorized } from '../utility/common'
import { Link } from 'react-router-dom'
import { useTheme } from '@emotion/react'
import { clearCluster } from '../redux/clusterSlice'

function Navbar({ username }) {
  const dispatch = useDispatch()
  const { colorMode } = useColorMode()
  const {
    common: { isMobile, isTablet, isDesktop }
  } = useSelector((state) => state)

  const currentTheme = useTheme()

  const styles = {
    navbarContainer: {
      boxShadow: colorMode === 'dark' ? 'none' : '0px -1px 8px #BFC1CB',
      position: 'fixed',
      zIndex: 2,
      width: '100%',
      padding: '4px',
      background: colorMode === 'light' ? currentTheme.colors.primary.light : currentTheme.colors.primary.dark
    },
    logo: {
      bg: '#eff2fe',
      borderRadius: '4px'
    }
  }

  const handleLogout = () => {
    dispatch(logout())
    dispatch(clearCluster())
  }
  return (
    <>
      <Flex as='nav' sx={styles.navbarContainer} gap='2' align='center'>
        <Link to='/'>
          <Image
            loading='lazy'
            height='50px'
            width={isMobile ? '180px' : 'fit-content'}
            sx={styles.logo}
            objectFit='contain'
            src='/images/logo.png'
            alt='Replication
           Manager'
          />
        </Link>
        <Spacer />

        {isAuthorized() && isDesktop && <RefreshCounter />}

        <Spacer />
        <HStack spacing='4'>
          {isAuthorized() && (
            <>
              {username && isDesktop && <Text>{`Welcome, ${username}`}</Text>}{' '}
              <Button type='button' size={{ base: 'sm' }} onClick={handleLogout}>
                Logout
              </Button>
            </>
          )}

          <ThemeIcon />
        </HStack>
      </Flex>
      {isAuthorized() && !isDesktop && (
        <Box mx='auto' p='16px' marginTop='80px'>
          <RefreshCounter />
        </Box>
      )}
    </>
  )
}

export default Navbar
