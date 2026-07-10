import React, { useState, useEffect, useCallback } from 'react'
import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalFooter, ModalCloseButton,
  Box, Text, Badge, Code, Button, Spinner,
  HStack, Alert, AlertIcon, Tabs, TabList, TabPanels, Tab, TabPanel
} from '@chakra-ui/react'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'
import { clusterService } from '../../../services/clusterService'

const PAGE_BYTES = 262144

// Viewer for the LOST EVENTS of a server's last divergence: what the old
// master wrote past the failover election point (forward pane), and — when
// the delta is flashback-able — the exact undo mysqlbinlog --flashback
// would execute (flashback pane). Paginated by binary position in the
// decoded file: "Load more" appends the next chunk, never the whole file
// (a divergence on a busy master decodes to gigabytes).
function LostEventsModal({ isOpen, closeModal, clusterName, server }) {
  const { theme } = useTheme()
  const isLight = theme === 'light'

  const [crash, setCrash] = useState(null)
  const [noCrash, setNoCrash] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const emptyPane = { lines: [], nextPos: 0, eof: false, size: 0, started: false }
  const [panes, setPanes] = useState({ forward: { ...emptyPane }, flashback: { ...emptyPane } })

  const loadPage = useCallback(
    (file, pos) => {
      setLoading(true)
      clusterService
        .getServerLostEvents(clusterName, server?.id, { file, pos, bytes: PAGE_BYTES })
        .then((res) => {
          const data = res.data
          setCrash(data.crash)
          setNoCrash(false)
          if (data.page) {
            setPanes((prev) => ({
              ...prev,
              [file]: {
                lines: pos === 0 ? data.page.lines : [...prev[file].lines, ...(data.page.lines || [])],
                nextPos: data.page.nextPos,
                eof: data.page.eof,
                size: data.page.size,
                started: true
              }
            }))
          }
        })
        .catch((err) => {
          if (err?.response?.status === 404) {
            setNoCrash(true)
          } else {
            setError(err?.response?.data || err?.message || 'Could not load lost events')
          }
        })
        .finally(() => setLoading(false))
    },
    [clusterName, server?.id]
  )

  useEffect(() => {
    if (isOpen) {
      setCrash(null)
      setNoCrash(false)
      setError('')
      setPanes({ forward: { ...emptyPane }, flashback: { ...emptyPane } })
      loadPage('forward', 0)
    }
  }, [isOpen, loadPage])

  const renderPane = (file) => {
    const pane = panes[file]
    return (
      <Box>
        <Code
          as='pre'
          display='block'
          p={2}
          fontSize='xs'
          bg={isLight ? 'gray.50' : 'blackAlpha.400'}
          whiteSpace='pre'
          overflowX='auto'
          maxH='50vh'
          overflowY='auto'
          borderRadius='md'
        >
          {pane.lines.join('\n') || (pane.started && pane.eof ? '-- empty --' : '')}
        </Code>
        <HStack mt={2} justify='space-between'>
          <Text fontSize='xs' color='gray.500'>
            {pane.size > 0 ? `${Math.min(pane.nextPos, pane.size)} / ${pane.size} bytes` : ''}
          </Text>
          {!pane.eof && pane.started && (
            <Button size='sm' isLoading={loading} onClick={() => loadPage(file, pane.nextPos)}>
              Load more
            </Button>
          )}
        </HStack>
      </Box>
    )
  }

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='5xl' scrollBehavior='inside'>
      <ModalOverlay />
      <ModalContent className={isLight ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>Last Divergence — {server?.host}:{server?.port}</ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={4}>
          {error ? (
            <Alert status='error' borderRadius='md'>
              <AlertIcon />
              <Text fontSize='sm'>{String(error)}</Text>
            </Alert>
          ) : noCrash ? (
            <Alert status='info' borderRadius='md'>
              <AlertIcon />
              <Text fontSize='sm'>No divergence recorded for this server: no crash record with captured lost events exists.</Text>
            </Alert>
          ) : !crash && loading ? (
            <HStack justify='center' py={8}><Spinner size='lg' /></HStack>
          ) : crash ? (
            <>
              <HStack mb={3} spacing={2} flexWrap='wrap'>
                {crash.deltaAnalyzed ? (
                  crash.deltaFlashable ? (
                    <Badge colorScheme='green'>Flashback-able</Badge>
                  ) : (
                    <Badge colorScheme='red'>Not flashback-able</Badge>
                  )
                ) : (
                  <Badge colorScheme='orange'>Not analyzed</Badge>
                )}
                <Badge>{crash.deltaTransactions} transactions</Badge>
                <Badge>{crash.deltaRowEvents} row events</Badge>
                {crash.deltaDdl > 0 && <Badge colorScheme='red'>{crash.deltaDdl} DDL</Badge>}
                {crash.deltaStatementDml > 0 && <Badge colorScheme='red'>{crash.deltaStatementDml} statement DML</Badge>}
              </HStack>
              {crash.deltaAnalyzed && !crash.deltaFlashable && (
                <Alert status='warning' borderRadius='md' mb={3}>
                  <AlertIcon />
                  <Text fontSize='sm'>
                    This delta cannot be flashed back{crash.deltaDdl > 0 ? ' (contains DDL)' : crash.deltaStatementDml > 0 ? ' (contains statement-format writes)' : ' (empty capture)'}.
                    Recovery options: reseed this server from the master, or forward-apply the statements below on the master to close the gap.
                  </Text>
                </Alert>
              )}
              <Tabs size='sm' variant='enclosed' onChange={(i) => {
                const file = i === 1 ? 'flashback' : 'forward'
                if (!panes[file].started) loadPage(file, 0)
              }}>
                <TabList>
                  <Tab>Statements</Tab>
                  <Tab isDisabled={!crash.deltaFlashbackDecoded}>Flashback (undo)</Tab>
                </TabList>
                <TabPanels>
                  <TabPanel px={0}>{renderPane('forward')}</TabPanel>
                  <TabPanel px={0}>{renderPane('flashback')}</TabPanel>
                </TabPanels>
              </Tabs>
            </>
          ) : null}
        </ModalBody>
        <ModalFooter>
          <Button variant='ghost' onClick={closeModal}>Close</Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default LostEventsModal
