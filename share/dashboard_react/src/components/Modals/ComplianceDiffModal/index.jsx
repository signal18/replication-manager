import React, { useState } from 'react'
import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalFooter, ModalCloseButton,
  Box, Text, Badge, Code, Table, Thead, Tbody, Tr, Th, Td, Button, Spinner,
  Accordion, AccordionItem, AccordionButton, AccordionPanel, AccordionIcon,
  HStack, Alert, AlertIcon
} from '@chakra-ui/react'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'

function ComplianceDiffModal({ isOpen, closeModal, diffData, loading, onAccept, acceptLoading }) {
  const { theme } = useTheme()
  const isLight = theme === 'light'

  const actionColor = {
    added: 'green',
    removed: 'red',
    modified: 'orange'
  }

  const renderChanges = (changes, title) => {
    if (!changes || changes.length === 0) return null
    return (
      <Box mb={4}>
        <Text fontWeight='bold' fontSize='sm' mb={2}>{title} ({changes.length} change{changes.length > 1 ? 's' : ''})</Text>
        <Accordion allowMultiple>
          {changes.map((change, i) => (
            <AccordionItem key={i} border='none' mb={1}>
              <AccordionButton px={2} py={1} borderRadius='md' bg={isLight ? 'gray.50' : 'whiteAlpha.50'}>
                <HStack flex='1' spacing={2}>
                  <Badge colorScheme={actionColor[change.action]} fontSize='xs'>{change.action}</Badge>
                  <Text fontSize='xs' fontFamily='mono'>{change.tag}</Text>
                </HStack>
                <AccordionIcon />
              </AccordionButton>
              <AccordionPanel px={2} py={2}>
                {change.action === 'modified' && (
                  <Table size='sm' variant='simple'>
                    <Thead>
                      <Tr>
                        <Th fontSize='xs' w='50%'>Previous</Th>
                        <Th fontSize='xs' w='50%'>New</Th>
                      </Tr>
                    </Thead>
                    <Tbody>
                      <Tr>
                        <Td verticalAlign='top'>
                          <Code
                            as='pre' display='block' p={2} fontSize='xs'
                            bg={isLight ? 'red.50' : 'red.900'} color={isLight ? 'gray.800' : 'red.200'}
                            whiteSpace='pre-wrap' maxH='200px' overflowY='auto' borderRadius='md'
                          >{change.old_cnf}</Code>
                        </Td>
                        <Td verticalAlign='top'>
                          <Code
                            as='pre' display='block' p={2} fontSize='xs'
                            bg={isLight ? 'green.50' : 'green.900'} color={isLight ? 'gray.800' : 'green.200'}
                            whiteSpace='pre-wrap' maxH='200px' overflowY='auto' borderRadius='md'
                          >{change.new_cnf}</Code>
                        </Td>
                      </Tr>
                    </Tbody>
                  </Table>
                )}
                {change.action === 'added' && (
                  <Code
                    as='pre' display='block' p={2} fontSize='xs'
                    bg={isLight ? 'green.50' : 'green.900'} color={isLight ? 'gray.800' : 'green.200'}
                    whiteSpace='pre-wrap' maxH='200px' overflowY='auto' borderRadius='md'
                  >{change.new_cnf}</Code>
                )}
                {change.action === 'removed' && (
                  <Code
                    as='pre' display='block' p={2} fontSize='xs'
                    bg={isLight ? 'red.50' : 'red.900'} color={isLight ? 'gray.800' : 'red.200'}
                    whiteSpace='pre-wrap' maxH='200px' overflowY='auto' borderRadius='md'
                  >{change.old_cnf}</Code>
                )}
              </AccordionPanel>
            </AccordionItem>
          ))}
        </Accordion>
      </Box>
    )
  }

  const totalChanges = (diffData?.db_changes?.length || 0) + (diffData?.prx_changes?.length || 0)

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='4xl' scrollBehavior='inside'>
      <ModalOverlay />
      <ModalContent className={isLight ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>Compliance Update Review</ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={4}>
          {loading ? (
            <HStack justify='center' py={8}><Spinner size='lg' /></HStack>
          ) : !diffData?.has_old ? (
            <Alert status='info' borderRadius='md'>
              <AlertIcon />
              <Text fontSize='sm'>No previous compliance on disk to compare against. This is the first compliance version for this cluster.</Text>
            </Alert>
          ) : totalChanges === 0 ? (
            <Alert status='success' borderRadius='md'>
              <AlertIcon />
              <Text fontSize='sm'>No changes detected between previous and current compliance.</Text>
            </Alert>
          ) : (
            <>
              <Alert status='warning' borderRadius='md' mb={4}>
                <AlertIcon />
                <Text fontSize='sm'>
                  {totalChanges} tag{totalChanges > 1 ? 's' : ''} changed.
                  {' '}Review the changes below and click Accept to apply the new compliance.
                </Text>
              </Alert>
              {diffData?.db_changes?.length > 0 && renderChanges(diffData.db_changes, 'Database Compliance')}
              {diffData?.prx_changes?.length > 0 && renderChanges(diffData.prx_changes, 'Proxy Compliance')}
            </>
          )}
        </ModalBody>
        <ModalFooter>
          <Button variant='ghost' mr={3} onClick={closeModal}>Close</Button>
          {onAccept && (
            <Button
              colorScheme='blue'
              isLoading={acceptLoading}
              onClick={onAccept}
            >
              Accept Compliance Update
            </Button>
          )}
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default ComplianceDiffModal
