import {
  Badge,
  Box,
  FormControl,
  FormErrorMessage,
  FormLabel,
  HStack,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Stack,
  Text
} from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import { adminMode } from '../../services/adminMode'
import { authService } from '../../services/authService'
import parentStyles from './styles.module.scss'

// AdminModeModal asks for the local admin login when the admin zone needs it (#2262), and shows the
// "Admin mode" badge while that session lasts, with a way to leave it.
function AdminModeModal() {
  const { theme } = useTheme()
  const [isOpen, setIsOpen] = useState(false)
  const [active, setActive] = useState(adminMode.isActive())
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    const open = () => {
      setError('')
      setPassword('')
      setIsOpen(true)
    }
    const refresh = () => setActive(adminMode.isActive())
    window.addEventListener(adminMode.REQUIRED, open)
    window.addEventListener(adminMode.CHANGED, refresh)
    return () => {
      window.removeEventListener(adminMode.REQUIRED, open)
      window.removeEventListener(adminMode.CHANGED, refresh)
    }
  }, [])

  const close = () => {
    setIsOpen(false)
    setPassword('')
    adminMode.cancel()
  }

  const enter = async () => {
    setError('')
    if (!username || !password) {
      setError('The local admin user and password are required')
      return
    }
    setLoading(true)
    try {
      const { data, status } = await authService.login(username, password)
      if (status === 200 && data?.token) {
        setPassword('')
        setIsOpen(false)
        adminMode.enter(data.token, username)
      } else {
        setError(typeof data === 'string' && data ? data : 'The local admin login was refused')
      }
    } catch (e) {
      setError('The local admin login failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <>
      {active && (
        <Box position='fixed' bottom='16px' right='16px' zIndex='toast'>
          <HStack spacing='2'>
            <Badge variant={theme === 'dark' ? 'solid' : 'subtle'} colorScheme='orange'>
              Admin mode ({adminMode.user()})
            </Badge>
            <RMButton size='small' variant='outline' onClick={adminMode.leave}>
              Leave
            </RMButton>
          </HStack>
        </Box>
      )}
      <Modal isOpen={isOpen} onClose={close}>
        <ModalOverlay />
        <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
          <ModalHeader>Admin mode</ModalHeader>
          <ModalCloseButton />
          <ModalBody>
            <Stack spacing='5'>
              <Text>Global settings and the host terminal are changed with the local admin account of this replication-manager.</Text>
              <FormControl isInvalid={!!error}>
                <FormLabel htmlFor='admin-mode-user'>User</FormLabel>
                <Input id='admin-mode-user' type='text' autoComplete='username' value={username} onChange={(e) => setUsername(e.target.value)} />
                <FormLabel htmlFor='admin-mode-password' mt='3'>Password</FormLabel>
                <Input
                  id='admin-mode-password'
                  type='password'
                  autoComplete='current-password'
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && enter()}
                />
                <FormErrorMessage>{error}</FormErrorMessage>
              </FormControl>
            </Stack>
          </ModalBody>
          <ModalFooter gap={3} margin='auto'>
            <RMButton colorScheme='blue' size='medium' variant='outline' onClick={close}>
              Cancel
            </RMButton>
            <RMButton onClick={enter} size='medium' isLoading={loading}>
              Enter admin mode
            </RMButton>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </>
  )
}

export default AdminModeModal
