import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton,
  Box, VStack, HStack, Text, Badge, Divider, Flex
} from '@chakra-ui/react'
import React, { useState } from 'react'
import RMButton from '../../RMButton'
import InterventionModal from '../InterventionModal'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'

function InterventionPanel({ isOpen, closeModal, isGlobal = false, isActive, current, history = [], suppressedAlerts = 0, activeCount = 0, onStart, onEnd }) {
  const { theme } = useTheme()
  const [isNewModalOpen, setIsNewModalOpen] = useState(false)

  const formatDuration = (start, end) => {
    const s = new Date(start)
    const e = end ? new Date(end) : new Date()
    const diffMs = e - s
    const mins = Math.floor(diffMs / 60000)
    const hrs = Math.floor(mins / 60)
    if (hrs > 0) return `${hrs}h ${mins % 60}m`
    return `${mins}m`
  }

  const formatTime = (t) => {
    if (!t) return '-'
    return new Date(t).toLocaleString()
  }

  return (
    <>
      <Modal isOpen={isOpen} onClose={closeModal} size='lg'>
        <ModalOverlay />
        <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
          <ModalHeader fontSize='md'>Interventions</ModalHeader>
          <ModalCloseButton />
          <ModalBody pb={6}>
            <VStack align='stretch' spacing={4}>

              {isGlobal && activeCount > 0 && !current && (
                <Box p={3} borderRadius='md' border='1px solid' borderColor='orange.400' bg={theme === 'light' ? 'orange.50' : 'rgba(237,137,54,0.1)'}>
                  <Flex justify='space-between' align='center'>
                    <HStack>
                      <Badge colorScheme='orange'>{activeCount} Active</Badge>
                      <Text fontSize='sm'>{activeCount} cluster{activeCount > 1 ? 's' : ''} in intervention</Text>
                    </HStack>
                    <RMButton size='xs' colorScheme='red' onClick={onEnd}>
                      Close All
                    </RMButton>
                  </Flex>
                </Box>
              )}

              {isActive && current && (
                <Box p={3} borderRadius='md' border='1px solid' borderColor='orange.400' bg={theme === 'light' ? 'orange.50' : 'rgba(237,137,54,0.1)'}>
                  <Flex justify='space-between' align='center' mb={2}>
                    <HStack>
                      <Badge colorScheme='orange'>{isGlobal && activeCount > 1 ? `${activeCount} Active` : 'Active'}</Badge>
                      <Text fontSize='sm' fontWeight={600}>{current.reason}</Text>
                    </HStack>
                    <RMButton size='xs' colorScheme='red' onClick={onEnd}>
                      {isGlobal ? 'Close All' : 'Close'}
                    </RMButton>
                  </Flex>
                  <HStack spacing={4} fontSize='xs' color={theme === 'light' ? 'gray.600' : 'gray.400'}>
                    <Text>By: {current.user}</Text>
                    <Text>Started: {formatTime(current.startedAt)}</Text>
                    <Text>Duration: {formatDuration(current.startedAt)}</Text>
                    <Text>Suppressed: {suppressedAlerts}</Text>
                  </HStack>
                </Box>
              )}

              {!isActive && (
                <RMButton colorScheme='orange' onClick={() => setIsNewModalOpen(true)}>
                  Start Intervention
                </RMButton>
              )}

              {history.length > 0 && (
                <>
                  <Divider />
                  <Text fontSize='sm' fontWeight={600} color={theme === 'light' ? 'gray.600' : 'gray.400'}>
                    History ({history.length})
                  </Text>
                  <VStack align='stretch' spacing={2} maxH='300px' overflowY='auto'>
                    {[...history].reverse().map((entry, i) => (
                      <Box key={i} p={2} borderRadius='md' bg={theme === 'light' ? 'gray.50' : 'rgba(255,255,255,0.05)'} fontSize='xs'>
                        <Text fontWeight={600} mb={1}>{entry.reason}</Text>
                        <HStack spacing={3} color={theme === 'light' ? 'gray.500' : 'gray.500'}>
                          <Text>By: {entry.user}</Text>
                          <Text>{formatTime(entry.startedAt)}</Text>
                          <Text>{formatDuration(entry.startedAt, entry.endedAt)}</Text>
                          <Text>Suppressed: {entry.suppressedAlerts || 0}</Text>
                          {entry.scope === 'global' && <Badge size='sm' colorScheme='purple'>Global</Badge>}
                        </HStack>
                      </Box>
                    ))}
                  </VStack>
                </>
              )}

              {!isActive && history.length === 0 && (
                <Text fontSize='sm' color={theme === 'light' ? 'gray.500' : 'gray.500'} textAlign='center' py={4}>
                  No interventions recorded
                </Text>
              )}

            </VStack>
          </ModalBody>
        </ModalContent>
      </Modal>

      {isNewModalOpen && (
        <InterventionModal
          isOpen={isNewModalOpen}
          closeModal={() => setIsNewModalOpen(false)}
          onStart={({ reason }) => {
            onStart(reason)
            setIsNewModalOpen(false)
          }}
        />
      )}
    </>
  )
}

export default InterventionPanel
