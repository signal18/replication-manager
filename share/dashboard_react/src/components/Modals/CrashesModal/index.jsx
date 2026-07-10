import React, { useState, useEffect, useRef } from 'react'
import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalFooter, ModalCloseButton,
  Text, Badge, Table, Thead, Tbody, Tr, Th, Td, Button,
  HStack, VStack, Box, Alert, AlertIcon, IconButton
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
  const [expanded, setExpanded] = useState(null)
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
    // The lost-events API resolves host:port as well as server id; ts pins the
    // specific historical divergence.
    return { id: url, host, port, ts: crash?.UnixTimestamp || 0 }
  }

  // Global CSS forces --text-color !important on badge text: 'subtle' gives a
  // readable dark-on-light badge in light mode, 'solid' a white-on-color badge
  // in dark mode. Anything else washes out in one of the two themes.
  const badgeVariant = isLight ? 'subtle' : 'solid'

  const verdict = (crash) => {
    if (!crash.deltaAnalyzed) return <Badge variant={badgeVariant} colorScheme='orange'>not analyzed</Badge>
    if (crash.deltaFlashable) return <Badge variant={badgeVariant} colorScheme='green'>flashback-able</Badge>
    return <Badge variant={badgeVariant} colorScheme='red'>not flashback-able</Badge>
  }

  // The exact anchors captured at the moment the new master was elected — the
  // frame every recovery is computed against. The old-master position is the
  // start of the divergent tail (where the elected slave stopped reading); the
  // election GTID is the SET gtid_slave_pos target for a logical rejoin.
  const anchorRow = (crash) => {
    const gtid = crash.failoverIOGtidString || '-'
    const oldPos = `${crash.FailoverMasterLogFile || '?'}:${crash.FailoverMasterLogPos || '?'}`
    const newPos = `${crash.NewMasterLogFile || '?'}:${crash.NewMasterLogPos || '?'}`
    const item = (label, value, hint) => (
      <Box>
        <Text fontSize='2xs' textTransform='uppercase' opacity={0.7}>{label}</Text>
        <Text fontSize='xs' fontFamily='mono'>{value}</Text>
        {hint && <Text fontSize='2xs' opacity={0.6}>{hint}</Text>}
      </Box>
    )
    return (
      <Box
        bg={isLight ? 'gray.50' : 'whiteAlpha.100'}
        borderLeft='3px solid'
        borderColor={isLight ? 'gray.300' : 'whiteAlpha.400'}
      >
        <VStack align='stretch' spacing={2} py={2} px={3}>
          {item('Election GTID', gtid, 'gtid_slave_pos target for logical rejoin of the old master')}
          {item(`Old master ${crash.URL || ''} stopped at`, oldPos, 'start of the divergent tail — capture anchor')}
          {item(`New master ${crash.ElectedMasterURL || ''} at election`, newPos, '')}
        </VStack>
      </Box>
    )
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
                  <Th w='1'></Th>
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
                  <React.Fragment key={i}>
                    <Tr>
                      <Td px={1}>
                        <IconButton
                          size='xs' variant='ghost' aria-label='Show election anchors'
                          fontSize='sm'
                          icon={<span>{expanded === i ? '▾' : '▸'}</span>}
                          onClick={() => setExpanded(expanded === i ? null : i)}
                        />
                      </Td>
                      <Td fontSize='xs'>{crash.UnixTimestamp ? new Date(crash.UnixTimestamp * 1000).toLocaleString() : '-'}</Td>
                      <Td fontSize='xs' fontFamily='mono'>{crash.URL}</Td>
                      <Td fontSize='xs' fontFamily='mono'>{crash.ElectedMasterURL}</Td>
                      <Td fontSize='xs'>{crash.Switchover ? 'switchover' : 'failover'}</Td>
                      <Td>
                        <HStack spacing={1}>
                          {verdict(crash)}
                          {crash.deltaAnalyzed && <Badge variant={badgeVariant}>{crash.deltaTransactions} tx</Badge>}
                          {crash.deltaDdl > 0 && <Badge variant={badgeVariant} colorScheme='red'>{crash.deltaDdl} DDL</Badge>}
                        </HStack>
                      </Td>
                      <Td>
                        <Button size='xs' onClick={() => setViewCrash(crash)}>
                          Lost Events
                        </Button>
                      </Td>
                    </Tr>
                    {expanded === i && (
                      <Tr>
                        <Td></Td>
                        <Td colSpan={6} p={0}>{anchorRow(crash)}</Td>
                      </Tr>
                    )}
                  </React.Fragment>
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
