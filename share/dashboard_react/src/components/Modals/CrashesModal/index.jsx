import React, { useState, useEffect, useRef } from 'react'
import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalFooter, ModalCloseButton,
  Text, Badge, Table, Thead, Tbody, Tr, Th, Td, Button,
  HStack, Alert, AlertIcon
} from '@chakra-ui/react'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'
import LostEventsModal from '../LostEventsModal'
import { acquireAutoReloadPause, releaseAutoReloadPause } from '../../../utility/autoReloadPause'

// Crash history of the cluster (dbServersCrashes): one row per failover /
// switchover crash record, with the lost-events verdict of the divergence
// when analyzed. "Lost Events" chains into the Last Divergence viewer.
function CrashesModal({ isOpen, closeModal, clusterName, crashes }) {
  const { theme } = useTheme()
  const isLight = theme === 'light'
  const [viewCrash, setViewCrash] = useState(null)
  const pauseRef = useRef(false)

  useEffect(() => {
    if (isOpen && !pauseRef.current) {
      acquireAutoReloadPause()
      pauseRef.current = true
    } else if (!isOpen && pauseRef.current) {
      releaseAutoReloadPause()
      pauseRef.current = false
    }
    return () => {
      if (pauseRef.current) {
        releaseAutoReloadPause()
        pauseRef.current = false
      }
    }
  }, [isOpen])

  const list = [...(crashes || [])].reverse()

  const crashServer = (crash) => {
    const url = crash?.URL || ''
    const [host, port] = url.split(':')
    // The lost-events API resolves host:port as well as server id.
    return { id: url, host, port }
  }

  const verdict = (crash) => {
    if (!crash.deltaAnalyzed) return <Badge colorScheme='orange'>not analyzed</Badge>
    if (crash.deltaFlashable) return <Badge colorScheme='green'>flashback-able</Badge>
    return <Badge colorScheme='red'>not flashback-able</Badge>
  }

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='5xl' scrollBehavior='inside'>
      <ModalOverlay />
      <ModalContent className={isLight ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>Crashes — {clusterName}</ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={4}>
          {list.length === 0 ? (
            <Alert status='success' borderRadius='md'>
              <AlertIcon />
              <Text fontSize='sm'>No crash recorded. Records are kept while a diverged server still needs recovery, then purged when every database is healthy.</Text>
            </Alert>
          ) : (
            <Table size='sm' variant='simple'>
              <Thead>
                <Tr>
                  <Th>Time</Th>
                  <Th>Failed Master</Th>
                  <Th>Elected Master</Th>
                  <Th>Type</Th>
                  <Th>Divergence</Th>
                  <Th></Th>
                </Tr>
              </Thead>
              <Tbody>
                {list.map((crash, i) => (
                  <Tr key={i}>
                    <Td fontSize='xs'>{crash.UnixTimestamp ? new Date(crash.UnixTimestamp * 1000).toLocaleString() : '-'}</Td>
                    <Td fontSize='xs' fontFamily='mono'>{crash.URL}</Td>
                    <Td fontSize='xs' fontFamily='mono'>{crash.ElectedMasterURL}</Td>
                    <Td fontSize='xs'>{crash.Switchover ? 'switchover' : 'failover'}</Td>
                    <Td>
                      <HStack spacing={1}>
                        {verdict(crash)}
                        {crash.deltaAnalyzed && <Badge>{crash.deltaTransactions} tx</Badge>}
                        {crash.deltaDdl > 0 && <Badge colorScheme='red'>{crash.deltaDdl} DDL</Badge>}
                      </HStack>
                    </Td>
                    <Td>
                      <Button size='xs' onClick={() => setViewCrash(crash)}>
                        Lost Events
                      </Button>
                    </Td>
                  </Tr>
                ))}
              </Tbody>
            </Table>
          )}
        </ModalBody>
        <ModalFooter>
          <Button variant='ghost' onClick={closeModal}>Close</Button>
        </ModalFooter>
      </ModalContent>

      {viewCrash && (
        <LostEventsModal
          isOpen={!!viewCrash}
          closeModal={() => setViewCrash(null)}
          clusterName={clusterName}
          server={crashServer(viewCrash)}
        />
      )}
    </Modal>
  )
}

export default CrashesModal
