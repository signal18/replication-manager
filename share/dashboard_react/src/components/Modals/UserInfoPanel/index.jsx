import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton,
  Box, VStack, HStack, Text, Badge, Divider, Table, Thead, Tbody, Tr, Th, Td
} from '@chakra-ui/react'
import React from 'react'
import { HiMoon, HiSun } from 'react-icons/hi'
import RMButton from '../../RMButton'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'

function UserInfoPanel({ isOpen, closeModal, user, onLogout }) {
  const { theme, toggleTheme } = useTheme()

  const grants = user?.grants || {}
  const roles = user?.roles || {}
  const clusterNames = Object.keys(grants).sort()

  // Collect all unique grant names across clusters
  const allGrants = new Set()
  clusterNames.forEach(name => {
    Object.keys(grants[name] || {}).forEach(g => allGrants.add(g))
  })
  const grantList = [...allGrants].sort()

  // Collect all unique role names across clusters
  const allRoles = new Set()
  clusterNames.forEach(name => {
    Object.keys(roles[name] || {}).forEach(r => allRoles.add(r))
  })
  const roleList = [...allRoles].sort()

  const checkMark = '\u2713'
  const cellBgYes = theme === 'light' ? 'blue.500' : 'blue.400'
  const cellColorYes = 'white'
  const cellBgNo = theme === 'light' ? 'gray.100' : 'gray.700'
  const cellColorNo = theme === 'light' ? 'gray.400' : 'gray.500'
  const stickyBg = theme === 'light' ? 'white' : 'gray.800'

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='xl'>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader fontSize='md'>User Profile</ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={4}>
          <VStack align='stretch' spacing={4}>

            <Box p={3} borderRadius='md' bg={theme === 'light' ? 'gray.50' : 'rgba(255,255,255,0.05)'}>
              <HStack spacing={4}>
                <Text fontSize='sm' fontWeight={600}>User:</Text>
                <Text fontSize='sm'>{user?.DisplayName || user?.User || user?.username || '-'}</Text>
                <Badge bg={user?.AuthType === 'SSO' ? 'purple.600' : 'blue.600'} color='white' size='sm'>
                  {user?.AuthType || 'Local'}
                </Badge>
              </HStack>
              {user?.Email && (
                <HStack spacing={4}>
                  <Text fontSize='sm' fontWeight={600}>Email:</Text>
                  <Text fontSize='sm'>{user.Email}</Text>
                </HStack>
              )}
            </Box>

            <HStack spacing={3} width='100%' justify='space-between'>
              <RMButton
                variant='ghost'
                size='small'
                onClick={toggleTheme}
              >
                <HStack spacing={1}>
                  {theme === 'light' ? <HiMoon color='midnightblue' /> : <HiSun color='gold' />}
                  <Text fontSize='sm'>{theme === 'light' ? 'Dark mode' : 'Light mode'}</Text>
                </HStack>
              </RMButton>
              <RMButton colorScheme='red' onClick={onLogout}>
                Logout
              </RMButton>
            </HStack>

            {clusterNames.length > 0 && roleList.length > 0 && (
              <>
                <Divider />
                <Text fontSize='sm' fontWeight={600} color={theme === 'light' ? 'gray.600' : 'gray.400'}>
                  Roles per cluster
                </Text>
                <Box overflowX='auto' fontSize='xs'>
                  <Table size='sm' variant='simple'>
                    <Thead>
                      <Tr>
                        <Th position='sticky' left={0} bg={stickyBg} zIndex={1}>Role</Th>
                        {clusterNames.map(name => (
                          <Th key={name} textAlign='center'>{name}</Th>
                        ))}
                      </Tr>
                    </Thead>
                    <Tbody>
                      {roleList.map(role => (
                        <Tr key={role}>
                          <Td position='sticky' left={0} bg={stickyBg} fontSize='xs'>{role}</Td>
                          {clusterNames.map(name => (
                            <Td key={name} textAlign='center'>
                              <Badge bg={roles[name]?.[role] ? cellBgYes : cellBgNo} color={roles[name]?.[role] ? cellColorYes : cellColorNo} size='sm'>
                                {roles[name]?.[role] ? checkMark : '-'}
                              </Badge>
                            </Td>
                          ))}
                        </Tr>
                      ))}
                    </Tbody>
                  </Table>
                </Box>
              </>
            )}

            {clusterNames.length > 0 && (
              <>
                <Divider />
                <Text fontSize='sm' fontWeight={600} color={theme === 'light' ? 'gray.600' : 'gray.400'}>
                  Grants per cluster
                </Text>
                <Box maxH='400px' overflowY='auto' overflowX='auto' fontSize='xs'>
                  <Table size='sm' variant='simple'>
                    <Thead>
                      <Tr>
                        <Th position='sticky' left={0} bg={stickyBg} zIndex={1}>Grant</Th>
                        {clusterNames.map(name => (
                          <Th key={name} textAlign='center'>{name}</Th>
                        ))}
                      </Tr>
                    </Thead>
                    <Tbody>
                      {grantList.map(grant => (
                        <Tr key={grant}>
                          <Td position='sticky' left={0} bg={stickyBg} fontSize='xs'>{grant}</Td>
                          {clusterNames.map(name => (
                            <Td key={name} textAlign='center'>
                              <Badge bg={grants[name]?.[grant] ? cellBgYes : cellBgNo} color={grants[name]?.[grant] ? cellColorYes : cellColorNo} size='sm'>
                                {grants[name]?.[grant] ? checkMark : '-'}
                              </Badge>
                            </Td>
                          ))}
                        </Tr>
                      ))}
                    </Tbody>
                  </Table>
                </Box>
              </>
            )}

            {clusterNames.length === 0 && (
              <Text fontSize='sm' color={theme === 'light' ? 'gray.500' : 'gray.500'} textAlign='center' py={4}>
                No cluster grants available
              </Text>
            )}

          </VStack>
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

export default UserInfoPanel
