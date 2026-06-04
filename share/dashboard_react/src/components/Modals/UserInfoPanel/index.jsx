import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton,
  Box, VStack, HStack, Text, Badge, Divider, Flex, Table, Thead, Tbody, Tr, Th, Td
} from '@chakra-ui/react'
import React from 'react'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'

function UserInfoPanel({ isOpen, closeModal, user, clusters }) {
  const { theme } = useTheme()

  const grants = user?.grants || {}
  const clusterNames = Object.keys(grants).sort()

  // Collect all unique grant names across clusters
  const allGrants = new Set()
  clusterNames.forEach(name => {
    Object.keys(grants[name] || {}).forEach(g => allGrants.add(g))
  })
  const grantList = [...allGrants].sort()

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='xl'>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader fontSize='md'>User Profile</ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={6}>
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
              <HStack spacing={4}>
                <Text fontSize='sm' fontWeight={600}>Role:</Text>
                <Text fontSize='sm'>{user?.Role || '-'}</Text>
              </HStack>
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
                                ? <Badge colorScheme='green' size='sm'>Y</Badge>
                                : <Badge colorScheme='gray' size='sm'>-</Badge>
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
      </ModalContent>
    </Modal>
  )
}

export default UserInfoPanel
