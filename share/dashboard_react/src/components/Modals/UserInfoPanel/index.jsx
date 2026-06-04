import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton, ModalFooter,
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

  // Collect active roles per cluster
  const getUserRoles = (clusterName) => {
    const r = roles[clusterName] || {}
    return Object.keys(r).filter(k => r[k]).sort()
  }

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
                <Text fontSize='sm'>{user?.User || user?.username || '-'}</Text>
              </HStack>
              {user?.Email && (
                <HStack spacing={4}>
                  <Text fontSize='sm' fontWeight={600}>Email:</Text>
                  <Text fontSize='sm'>{user.Email}</Text>
                </HStack>
              )}
              {clusterNames.length > 0 && (
                <HStack spacing={4} flexWrap='wrap'>
                  <Text fontSize='sm' fontWeight={600}>Roles:</Text>
                  {clusterNames.map(name => {
                    const r = getUserRoles(name)
                    return r.length > 0 ? (
                      <HStack key={name} spacing={1}>
                        <Text fontSize='xs' color={theme === 'light' ? 'gray.500' : 'gray.400'}>{name}:</Text>
                        {r.map(role => <Badge key={role} colorScheme='purple' size='sm'>{role}</Badge>)}
                      </HStack>
                    ) : null
                  })}
                </HStack>
              )}
            </Box>

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
                        <Th position='sticky' left={0} bg={theme === 'light' ? 'white' : 'gray.800'} zIndex={1}>Grant</Th>
                        {clusterNames.map(name => (
                          <Th key={name} textAlign='center'>{name}</Th>
                        ))}
                      </Tr>
                    </Thead>
                    <Tbody>
                      {grantList.map(grant => (
                        <Tr key={grant}>
                          <Td position='sticky' left={0} bg={theme === 'light' ? 'white' : 'gray.800'} fontSize='xs'>{grant}</Td>
                          {clusterNames.map(name => (
                            <Td key={name} textAlign='center'>
                              {grants[name]?.[grant]
                                ? <Badge bg='green.600' color='white' size='sm'>Y</Badge>
                                : <Badge bg={theme === 'light' ? 'gray.200' : 'gray.600'} color={theme === 'light' ? 'gray.500' : 'gray.400'} size='sm'>-</Badge>
                              }
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
        <ModalFooter>
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
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default UserInfoPanel
