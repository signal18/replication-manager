import React, { useState, useEffect, useCallback, useRef } from 'react'
import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalFooter, ModalCloseButton,
  Box, Text, Badge, Code, Button, Spinner,
  HStack, Alert, AlertIcon, Tabs, TabList, TabPanels, Tab, TabPanel
} from '@chakra-ui/react'
import { keyframes } from '@emotion/react'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'
import { clusterService } from '../../../services/clusterService'
import { acquireAutoReloadPause, releaseAutoReloadPause } from '../../../utility/autoReloadPause'

const PAGE_BYTES = 262144

// Blink for a FAILED rejoin outcome (draws the eye to a crash needing action).
const blink = keyframes`0%, 100% { opacity: 1 } 50% { opacity: 0.2 }`
const REJOIN_FAILED = ['not-flashback-able', 'no-rejoin-method', 'failed', 'peer-unreachable']
const REJOIN_LABEL = {
  'success': 'Rejoined',
  'no-divergence': 'Rejoined (no divergence)',
  'not-flashback-able': 'Rejoin failed: not flashback-able',
  'no-rejoin-method': 'Rejoin failed: no method',
  'failed': 'Rejoin failed',
  'peer-unreachable': 'Rejoin pending: peer unreachable'
}

// Operator rejoin methods — mirror the backend (crash.go). ALL are always offered:
// the delta verdict informs, it does not gate (testing must not be limited). class
// mirrors RejoinMethodClass: safety × duration, used to group the buttons.
const REJOIN_METHODS = [
  { id: 'flashback', label: 'Flashback', class: 'safe-short', help: 'Rewind the divergent row-DML tail and re-slave (reversible deltas only)' },
  { id: 'reset-master-reslave', label: 'Reset master + re-slave', class: 'safe-short', help: 'RESET MASTER on this server to clear a stuck GTID/binlog position, then re-slave clean' },
  { id: 'rejoin-script', label: 'Custom rejoin script', class: 'safe-short', help: 'Run the operator autorejoin-script (behaviour is whatever the script does)' },
  { id: 'logical-dump', label: 'Logical dump', class: 'safe-long', help: 'mysqldump from the master, then re-slave' },
  { id: 'logical-backup', label: 'Logical backup', class: 'safe-long', help: 'Restore from the logical backup, then re-slave' },
  { id: 'physical-backup', label: 'Physical backup', class: 'safe-long', help: 'Restore from the physical backup, then re-slave' },
  { id: 'ignore-delta-force', label: 'Ignore delta (force)', class: 'unsafe-short', help: 'DISCARD the divergent tail (DATA LOSS) and force re-slave', danger: true },
  { id: 'bootstrap-repli-ftwrl', label: 'Bootstrap + FTWRL', class: 'unsafe-short', help: 'Re-bootstrap replication with a short FTWRL on the master before RESET MASTER (briefly locks the master)', danger: true }
]

// Ordered groups for the rejoin buttons (label + which class they hold).
const REJOIN_GROUPS = [
  { class: 'safe-short', label: 'Safe · fast' },
  { class: 'safe-long', label: 'Safe · full reseed' },
  { class: 'unsafe-short', label: 'Unsafe · fast' }
]

// Viewer for the LOST EVENTS of a server's last divergence: what the old
// master wrote past the failover election point (forward pane), and — when
// the delta is flashback-able — the exact undo mysqlbinlog --flashback
// would execute (flashback pane). Paginated by binary position in the
// decoded file: "Load more" appends the next chunk, never the whole file
// (a divergence on a busy master decodes to gigabytes).
function LostEventsModal({ isOpen, closeModal, clusterName, server }) {
  const { theme } = useTheme()
  const isLight = theme === 'light'
  // Global CSS forces --text-color !important on badge text: 'subtle' reads in
  // light mode, 'solid' in dark. See CrashesModal for the same constraint.
  const badgeVariant = isLight ? 'subtle' : 'solid'

  const [crash, setCrash] = useState(null)
  const [noCrash, setNoCrash] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const emptyPane = { lines: [], nextPos: 0, eof: false, size: 0, started: false }
  const [panes, setPanes] = useState({ forward: { ...emptyPane }, flashback: { ...emptyPane } })
  const [rejoining, setRejoining] = useState('')
  const [rejoinMsg, setRejoinMsg] = useState(null)
  const [methodStatus, setMethodStatus] = useState({}) // id -> {available, reason}
  const pauseRef = useRef(false)

  const doRejoin = useCallback((m) => {
    if (m.danger && !window.confirm(`${m.label} on ${server?.host}:${server?.port}?\n\nThis is an UNSAFE rejoin — ${m.help}. It cannot be undone.`)) {
      return
    }
    setRejoining(m.id)
    setRejoinMsg(null)
    clusterService
      .rejoinCluster(clusterName, m.id)
      .then(() => setRejoinMsg({ ok: true, text: `Rejoin armed via "${m.label}" — the next monitor tick runs it (one attempt). Watch the server state and this viewer for the outcome.` }))
      .catch((err) => setRejoinMsg({ ok: false, text: err?.response?.data || err?.message || 'Rejoin request failed' }))
      .finally(() => setRejoining(''))
  }, [clusterName, server?.id, server?.host, server?.port])

  const loadPage = useCallback(
    (file, pos) => {
      setLoading(true)
      clusterService
        .getServerLostEvents(clusterName, server?.id, { file, pos, bytes: PAGE_BYTES, ts: server?.ts || 0 })
        .then((res) => {
          const data = res.data
          setCrash(data.crash)
          setNoCrash(false)
          if (Array.isArray(data.rejoinMethods)) {
            const map = {}
            data.rejoinMethods.forEach((m) => { map[m.method] = { available: m.available, reason: m.reason } })
            setMethodStatus(map)
          }
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
    // Freeze the global auto-refresh while the viewer is open so the record
    // (and the modal built on it) cannot be swept out from under the operator
    // on the next poll cycle.
    if (isOpen && !pauseRef.current) {
      acquireAutoReloadPause()
      pauseRef.current = true
      setCrash(null)
      setNoCrash(false)
      setError('')
      setPanes({ forward: { ...emptyPane }, flashback: { ...emptyPane } })
      loadPage('forward', 0)
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
          height='55vh'
          overflowY='scroll'
          borderRadius='md'
          sx={{ overscrollBehavior: 'contain' }}
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
                    <Badge variant={badgeVariant} colorScheme='green'>Flashback-able</Badge>
                  ) : (
                    <Badge variant={badgeVariant} colorScheme='red'>Not flashback-able</Badge>
                  )
                ) : (
                  <Badge variant={badgeVariant} colorScheme='orange'>Not analyzed</Badge>
                )}
                {crash.rejoinResult && (
                  <Badge
                    variant={badgeVariant}
                    colorScheme={REJOIN_FAILED.includes(crash.rejoinResult) ? 'red' : 'green'}
                    animation={REJOIN_FAILED.includes(crash.rejoinResult) ? `${blink} 1s ease-in-out infinite` : undefined}
                  >
                    {REJOIN_LABEL[crash.rejoinResult] || crash.rejoinResult}
                  </Badge>
                )}
                <Badge variant={badgeVariant}>{crash.deltaTransactions} transactions</Badge>
                <Badge variant={badgeVariant}>{crash.deltaRowEvents} row events</Badge>
                {crash.deltaDdl > 0 && <Badge variant={badgeVariant} colorScheme='red'>{crash.deltaDdl} DDL</Badge>}
                {crash.deltaStatementDml > 0 && <Badge variant={badgeVariant} colorScheme='red'>{crash.deltaStatementDml} statement DML</Badge>}
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
        <ModalFooter flexDirection='column' alignItems='stretch' gap={2}>
          {crash && (
            <Box>
              <Text fontSize='xs' mb={1} opacity={0.8}>Rejoin {server?.host}:{server?.port} — pick a method (all runnable; the verdict only informs):</Text>
              {REJOIN_GROUPS.map((g) => {
                const methods = REJOIN_METHODS.filter((m) => m.class === g.class)
                if (methods.length === 0) return null
                return (
                  <Box key={g.class} mb={1.5}>
                    <Text fontSize='2xs' textTransform='uppercase' letterSpacing='wide' opacity={0.6} mb={0.5}>{g.label}</Text>
                    <HStack spacing={2} flexWrap='wrap'>
                      {methods.map((m) => {
                        const st = methodStatus[m.id]
                        const unavailable = st && st.available === false
                        return (
                          <Button
                            key={m.id}
                            size='xs'
                            colorScheme={m.danger ? 'red' : 'blue'}
                            variant={m.danger ? 'solid' : 'outline'}
                            isLoading={rejoining === m.id}
                            isDisabled={rejoining !== '' || unavailable}
                            onClick={() => doRejoin(m)}
                            title={unavailable ? st.reason : m.help}
                          >
                            {m.label}
                          </Button>
                        )
                      })}
                    </HStack>
                  </Box>
                )
              })}
              {rejoinMsg && (
                <Alert status={rejoinMsg.ok ? 'success' : 'error'} borderRadius='md' mt={2} py={1}>
                  <AlertIcon />
                  <Text fontSize='sm'>{rejoinMsg.text}</Text>
                </Alert>
              )}
            </Box>
          )}
          <HStack justify='flex-end'>
            <Button variant='ghost' onClick={closeModal}>Close</Button>
          </HStack>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default LostEventsModal
