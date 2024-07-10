import { Box, Flex, Image, Button, Spacer, Text, HStack, useColorMode, IconButton } from '@chakra-ui/react'
import React, { useEffect } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { logout } from '../redux/authSlice'
import ThemeIcon from './ThemeIcon'
import RefreshCounter from './RefreshCounter'
import { isAuthorized } from '../utility/common'
import { Link } from 'react-router-dom'

function Navbar({ username, theme }) {
  const dispatch = useDispatch()
  const {
    common: { isMobile, isTablet, isDesktop }
  } = useSelector((state) => state)

  const styles = {
    navbarContainer: {
      boxShadow: theme === 'dark' ? 'none' : '0px -1px 8px #BFC1CB'
    },
    logo: {
      bg: '#eff2fe',
      borderRadius: '4px'
    }
  }

  const handleLogout = () => {
    dispatch(logout())
  }
  return (
    <>
      <Flex as='nav' p='10px' sx={styles.navbarContainer} gap='2' align='center'>
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

          <ThemeIcon theme={theme} />
        </HStack>
      </Flex>
      {isAuthorized() && !isDesktop && (
        <Box mx='auto' p='16px'>
          <RefreshCounter />
        </Box>
      )}
    </>
  )
}

export default Navbar
