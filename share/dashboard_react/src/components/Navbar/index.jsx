import { Box, Flex, Image, Button, Spacer, Text, HStack, useColorMode, IconButton } from '@chakra-ui/react'
import React, { useEffect } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { logout } from '../../redux/authSlice'
import ThemeIcon from '../ThemeIcon'
import RefreshCounter from '../RefreshCounter'
import { HiMoon, HiSun } from 'react-icons/hi'
import { isAuthorized } from '../../utility/common'

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
        <Image
          loading='lazy'
          height='50px'
          width={isMobile ? '180px' : 'fit-content'}
          sx={styles.logo}
          objectFit='contain'
          src='/images/logo.png'
          alt='Replication Manager'
        />
        <Spacer />

        {isAuthorized() && !isMobile && <RefreshCounter />}

        <Spacer />
        <HStack spacing='4'>
          {isAuthorized() && (
            <>
              {username && !isMobile && <Text>{`Welcome, ${username}`}</Text>}{' '}
              <Button type='button' size={{ base: 'sm' }} colorScheme='blue' onClick={handleLogout}>
                Logout
              </Button>
            </>
          )}

          <ThemeIcon />
        </HStack>
      </Flex>
      {isAuthorized() && isMobile && (
        <Box mx='auto' p='16px'>
          <RefreshCounter />
        </Box>
      )}
    </>
  )
}

export default Navbar
