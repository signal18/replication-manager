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
                      <Th>Source</Th>
                      <Th>Link</Th>
                    </Tr>
                  </Thead>
                  <Tbody>
                    {docHelp.variables.flatMap((v) => {
                      // Build sorted list: official docs first, then blogs alphabetically
                      const links = []
                      if (v.mariadb_url) {
                        links.push({ name: v.name, source: 'MariaDB', title: 'Official Documentation', url: v.mariadb_url, order: 0, color: isLight ? 'blue.600' : 'blue.300' })
                      }
                      if (v.mysql_url) {
                        links.push({ name: v.name, source: 'MySQL', title: 'Official Documentation', url: v.mysql_url, order: 1, color: isLight ? 'teal.600' : 'teal.300' })
                      }
                      if (v.blogs) {
                        v.blogs.forEach((blog) => {
                          links.push({ name: v.name, source: 'Blog', title: blog.title, url: blog.url, order: 2, color: isLight ? 'purple.600' : 'purple.300' })
                        })
                      }
                      links.sort((a, b) => a.order - b.order || a.title.localeCompare(b.title))
                      return links
                    }).map((link, i) => (
                      <Tr key={`${link.name}-${i}`}>
                        <Td>
                          <Text fontSize='xs' fontFamily='mono'>{link.name}</Text>
                        </Td>
                        <Td>
                          <Badge colorScheme={link.order === 0 ? 'blue' : link.order === 1 ? 'teal' : 'purple'} fontSize='xs'>
                            {link.source}
                          </Badge>
                        </Td>
                        <Td>
                          <Link href={link.url} isExternal color={link.color} fontSize='xs'>
                            {link.title} <FiExternalLink style={{ display: 'inline', marginLeft: 2 }} />
                          </Link>
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
