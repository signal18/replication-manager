import React from 'react'
import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton,
  Code, Box, Text, Divider, Table, Thead, Tbody, Tr, Th, Td, Link,
  HStack, Badge
} from '@chakra-ui/react'
import { FiExternalLink } from 'react-icons/fi'

function TagContentModal({ isOpen, closeModal, tagName, data }) {
  const content = data?.content || ''
  const docHelp = data?.doc_help

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='2xl' scrollBehavior='inside'>
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>Tag: {tagName}</ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={6}>
          {content ? (
            <Box
              as='pre'
              p={4}
              bg='gray.800'
              color='green.200'
              borderRadius='md'
              fontSize='sm'
              overflowX='auto'
              whiteSpace='pre-wrap'
              wordBreak='break-all'
              maxH='40vh'
              overflowY='auto'
            >
              <Code bg='transparent' color='inherit' whiteSpace='pre-wrap'>
                {content}
              </Code>
            </Box>
          ) : (
            <Text color='gray.400' fontSize='sm'>No config file found for this tag. Run config generation first.</Text>
          )}

          {docHelp?.variables?.length > 0 && (
            <>
              <Divider my={4} borderColor='whiteAlpha.300' />
              <Text fontSize='sm' fontWeight='bold' mb={2} color='blue.200'>
                Documentation Links
              </Text>
              <Table size='sm' variant='simple'>
                <Thead>
                  <Tr>
                    <Th>Variable</Th>
                    <Th>Documentation</Th>
                  </Tr>
                </Thead>
                <Tbody>
                  {docHelp.variables.map((v) => (
                    <Tr key={v.name}>
                      <Td>
                        <Text fontSize='xs' fontFamily='mono'>{v.name}</Text>
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
              {docHelp.unknown_variables?.length > 0 && (
                <Box mt={2}>
                  <Text fontSize='xs' color='gray.500' mb={1}>
                    Variables not in documentation database:
                  </Text>
                  <HStack spacing={1} flexWrap='wrap'>
                    {docHelp.unknown_variables.map((v) => (
                      <Badge key={v} colorScheme='gray' fontSize='xs' fontFamily='mono'>{v}</Badge>
                    ))}
                  </HStack>
                </Box>
              )}
            </>
          )}
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

export default TagContentModal
