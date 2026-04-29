import React from 'react'
import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton,
  Table, Thead, Tbody, Tr, Th, Td, Link, Text, Badge, VStack, HStack, Box
} from '@chakra-ui/react'
import { FiExternalLink } from 'react-icons/fi'

function DocHelpModal({ isOpen, closeModal, tagName, data, error }) {
  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='2xl' scrollBehavior='inside'>
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>Documentation: {tagName}</ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={6}>
          {error && (
            <Text color='orange.300' fontSize='sm' mb={4}>{error}</Text>
          )}
          {data?.variables?.length > 0 ? (
            <VStack spacing={4} align='stretch'>
              <Table size='sm' variant='simple'>
                <Thead>
                  <Tr>
                    <Th>Variable</Th>
                    <Th>Documentation</Th>
                  </Tr>
                </Thead>
                <Tbody>
                  {data.variables.map((v) => (
                    <Tr key={v.name}>
                      <Td>
                        <Text fontSize='sm' fontFamily='mono'>{v.name}</Text>
                        {v.description && <Text fontSize='xs' color='gray.400'>{v.description}</Text>}
                      </Td>
                      <Td>
                        <HStack spacing={2} flexWrap='wrap'>
                          {v.mariadb_url && (
                            <Link href={v.mariadb_url} isExternal color='blue.300' fontSize='xs'>
                              MariaDB <FiExternalLink style={{ display: 'inline', marginLeft: 2 }} />
                            </Link>
                          )}
                          {v.mysql_url && (
                            <Link href={v.mysql_url} isExternal color='teal.300' fontSize='xs'>
                              MySQL <FiExternalLink style={{ display: 'inline', marginLeft: 2 }} />
                            </Link>
                          )}
                          {v.blogs?.map((blog, i) => (
                            <Link key={i} href={blog.url} isExternal color='purple.300' fontSize='xs'>
                              {blog.title} <FiExternalLink style={{ display: 'inline', marginLeft: 2 }} />
                            </Link>
                          ))}
                        </HStack>
                      </Td>
                    </Tr>
                  ))}
                </Tbody>
              </Table>
              {data.unknown_variables?.length > 0 && (
                <Box>
                  <Text fontSize='xs' color='gray.500' mb={1}>
                    Variables not found in documentation database:
                  </Text>
                  <HStack spacing={1} flexWrap='wrap'>
                    {data.unknown_variables.map((v) => (
                      <Badge key={v} colorScheme='gray' fontSize='xs' fontFamily='mono'>{v}</Badge>
                    ))}
                  </HStack>
                </Box>
              )}
            </VStack>
          ) : !error ? (
            <Text color='gray.400' fontSize='sm'>No variables found for this tag.</Text>
          ) : null}
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

export default DocHelpModal
