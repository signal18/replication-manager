import { Box, Flex, Image, Button, Spacer, Text, HStack, useColorMode, IconButton } from '@chakra-ui/react'
import React from 'react'
import { useDispatch } from 'react-redux'
import { logout } from '../../redux/authSlice'
import RefreshCounter from '../RefreshCounter'
import { HiMoon } from 'react-icons/hi'

function Navbar({ username }) {
  const dispatch = useDispatch()
  const { toggleColorMode } = useColorMode()
  const styles = {
    navbarContainer: {
      boxShadow: '0px -1px 8px #BFC1CB'
    }
  }

  const handleLogout = () => {
    dispatch(logout())
  }
  return (
    <Flex as='nav' p='10px' sx={styles.navbarContainer} gap='2' align='center'>
      <Image
        loading='lazy'
        height='50px'
        width='300px'
        objectFit='contain'
        src='/images/logo.png'
        alt='Replication Manager'
      />
      <Spacer />

      <RefreshCounter />

      <Spacer />
      <HStack spacing='4'>
        {username && <Text>{`Welcome, ${username}`}</Text>}
        <Button type='button' colorScheme='blue' onClick={handleLogout}>
          Logout
        </Button>
        <IconButton
          onClick={toggleColorMode}
          icon={<HiMoon fontSize='1.5rem' />}
          size='sm'
          variant='unstyled'
          colorScheme='blue'
        />
      </HStack>
    </Flex>
  )
}

export default Navbar
