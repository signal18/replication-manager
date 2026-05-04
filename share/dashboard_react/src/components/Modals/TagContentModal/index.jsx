import React from 'react'
import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton,
  Code, Box, Text, Divider, Table, Thead, Tbody, Tr, Th, Td, Link,
  HStack, Badge
} from '@chakra-ui/react'
import { FiExternalLink } from 'react-icons/fi'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'

function TagContentModal({ isOpen, closeModal, tagName, data }) {
  const content = data?.content || ''
  const docHelp = data?.doc_help
  const { theme } = useTheme()
  const isLight = theme === 'light'

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='3xl' scrollBehavior='inside'>
      <ModalOverlay />
      <ModalContent className={isLight ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>Tag: {tagName}</ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={6}>
          {content ? (
            <Box
              as='pre'
              p={4}
              bg={isLight ? 'gray.50' : 'gray.800'}
              color={isLight ? 'gray.800' : 'green.200'}
              border='1px solid'
              borderColor={isLight ? 'gray.200' : 'whiteAlpha.200'}
              borderRadius='md'
              fontSize='sm'
              overflowX='auto'
              whiteSpace='pre-wrap'
              wordBreak='break-all'
              maxH='50vh'
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
              <Divider my={4} borderColor={isLight ? 'gray.200' : 'whiteAlpha.300'} />
              <Text fontSize='sm' fontWeight='bold' mb={2} color={isLight ? 'blue.600' : 'blue.200'}>
                Documentation Links
              </Text>
              <Box maxH='40vh' overflowY='auto'>
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
                              <Link href={v.mariadb_url} isExternal color={isLight ? 'blue.600' : 'blue.300'} fontSize='xs'>
                                MariaDB <FiExternalLink style={{ display: 'inline', marginLeft: 2 }} />
                              </Link>
                            )}
                            {v.mysql_url && (
                              <Link href={v.mysql_url} isExternal color={isLight ? 'teal.600' : 'teal.300'} fontSize='xs'>
                                MySQL <FiExternalLink style={{ display: 'inline', marginLeft: 2 }} />
                              </Link>
                            )}
                            {v.blogs?.map((blog, i) => (
                              <Link key={i} href={blog.url} isExternal color={isLight ? 'purple.600' : 'purple.300'} fontSize='xs'>
                                {blog.title} <FiExternalLink style={{ display: 'inline', marginLeft: 2 }} />
                              </Link>
                            ))}
                          </HStack>
                        </Td>
                      </Tr>
                    ))}
                  </Tbody>
                </Table>
              </Box>
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
