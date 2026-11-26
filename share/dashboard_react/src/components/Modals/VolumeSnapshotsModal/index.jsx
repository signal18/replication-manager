import React, { useState, useEffect, useCallback } from 'react'
import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalFooter, ModalCloseButton,
  Box, Text, Badge, Code, Spinner, HStack, VStack, Alert, AlertIcon, Input,
  Table, Thead, Tbody, Tr, Th, Td
} from '@chakra-ui/react'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'
import RMButton from '../../RMButton'
import { clusterService } from '../../../services/clusterService'
import { formatBytes } from '../../../utility/common'

// Volume snapshots of a server's data volume (#2443): list, take, delete (only the repman-*
// ones replication-manager took), snapback. A snapback is destructive: the operator types the
// server's host to confirm it; the server goes into maintenance, is stopped, rolled back,
// started, and stays in maintenance until the operator takes it out.
function VolumeSnapshotsModal({ isOpen, closeModal, clusterName, server, user }) {
  const { theme } = useTheme()
  const isLight = theme === 'light'
  const badgeVariant = isLight ? 'subtle' : 'solid'

  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(false)
  const [message, setMessage] = useState(null)
  const [pending, setPending] = useState(null) // { action: 'snapback' | 'delete', name }
  const [confirmText, setConfirmText] = useState('')

  const canBackup = !!user?.grants?.['db-backup']
  const canSnapback = !!user?.grants?.['db-restore'] && !!user?.grants?.['db-maintenance']

  const load = useCallback(() => {
    if (!server?.id) return
    setLoading(true)
    clusterService
      .getVolumeSnapshots(clusterName, server.id)
      .then((res) => setData(res.data))
      .catch((err) => setMessage({ ok: false, text: err?.response?.data || err?.message || 'Could not list the snapshots' }))
      .finally(() => setLoading(false))
  }, [clusterName, server?.id])

  useEffect(() => {
    if (isOpen) {
      setMessage(null)
      setPending(null)
      load()
    }
  }, [isOpen, load])

  const report = (promise, okText) =>
    promise
      .then(() => {
        setMessage({ ok: true, text: okText })
        setTimeout(load, 3000)
      })
      .catch((err) => setMessage({ ok: false, text: err?.response?.data || err?.message || 'Refused' }))

  const take = () => report(clusterService.takeVolumeSnapshot(clusterName, server.id), 'Snapshot started: the list refreshes in a few seconds.')

  const confirmPending = () => {
    if (!pending) return
    if (pending.action === 'delete') {
      report(clusterService.deleteVolumeSnapshot(clusterName, server.id, pending.name), `Delete of ${pending.name} started.`)
    } else {
      report(
        clusterService.snapbackVolume(clusterName, server.id, pending.name),
        `Snapback to ${pending.name} started: the server goes into maintenance, is stopped, rolled back and started. It stays in maintenance until you take it out.`
      )
    }
    setPending(null)
    setConfirmText('')
  }

  const lastOp = data?.lastOp
  const running = lastOp?.status === 'running'

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='4xl' scrollBehavior='inside'>
      <ModalOverlay />
      <ModalContent className={isLight ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>Volume Snapshots — {server?.host}:{server?.port}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <VStack align='stretch' spacing={3}>
            {data && (
              <HStack spacing={4} wrap='wrap'>
                <Text fontSize='sm'>Plugin: <Badge variant={badgeVariant}>{data.plugin || '-'}</Badge></Text>
                <Text fontSize='sm'>Volume: <Code>{data.target || '?'}</Code></Text>
                <Text fontSize='sm'>Kept: {data.keep} taken by replication-manager</Text>
                {!data.enabled && <Badge colorScheme='gray' variant={badgeVariant}>off (backup-snapshot)</Badge>}
              </HStack>
            )}
            {data?.error && (
              <Alert status='warning'>
                <AlertIcon />
                {data.error}
              </Alert>
            )}
            {message && (
              <Alert status={message.ok ? 'success' : 'error'}>
                <AlertIcon />
                {String(message.text)}
              </Alert>
            )}
            {lastOp && (
              <Box fontSize='sm'>
                Last action: {lastOp.action} {lastOp.snapshot}{' '}
                <Badge variant={badgeVariant} colorScheme={lastOp.status === 'done' ? 'green' : lastOp.status === 'running' ? 'blue' : 'red'}>
                  {lastOp.status}
                </Badge>
                {lastOp.error && <Text color='red.400'>{lastOp.error}</Text>}
                {lastOp.commands?.length > 0 && (
                  <Box mt={1}>
                    <Text fontSize='xs'>Commands run (replayable by hand):</Text>
                    {lastOp.commands.map((c, i) => (
                      <Code key={i} display='block' fontSize='xs' whiteSpace='pre-wrap'>{c}</Code>
                    ))}
                  </Box>
                )}
              </Box>
            )}
            {loading && <Spinner size='sm' />}
            {pending && (
              <Alert status='error' flexDirection='column' alignItems='flex-start'>
                {pending.action === 'snapback' ? (
                  <>
                    <Text fontWeight='bold'>Snapback of {server?.host}:{server?.port} to {pending.name}</Text>
                    <Text fontSize='sm'>
                      Everything written on this server since the snapshot is lost. The server goes into maintenance, is
                      stopped, its volume rolled back (on ZFS, the snapshots taken after this one are destroyed), then
                      started; it stays in maintenance until you take it out. Its replication resumes from the position
                      the snapshot holds: the master must still have those binary logs.
                    </Text>
                    <Text fontSize='sm' mt={2}>Type the server host <Code>{server?.host}</Code> to confirm:</Text>
                    <Input size='sm' mt={1} value={confirmText} onChange={(e) => setConfirmText(e.target.value)} />
                  </>
                ) : (
                  <Text>Delete the snapshot {pending.name}?</Text>
                )}
                <HStack mt={2}>
                  <RMButton colorScheme='red' isDisabled={pending.action === 'snapback' && confirmText !== server?.host} onClick={confirmPending}>
                    Confirm
                  </RMButton>
                  <RMButton onClick={() => { setPending(null); setConfirmText('') }}>Cancel</RMButton>
                </HStack>
              </Alert>
            )}
            <Table size='sm'>
              <Thead>
                <Tr>
                  <Th>Snapshot</Th>
                  <Th>Created</Th>
                  <Th>Size</Th>
                  <Th>Origin</Th>
                  <Th></Th>
                </Tr>
              </Thead>
              <Tbody>
                {(data?.snapshots || []).slice().reverse().map((s) => (
                  <Tr key={s.name}>
                    <Td><Code fontSize='xs'>{s.name}</Code></Td>
                    <Td fontSize='xs'>{s.created && !s.created.startsWith('0001') ? new Date(s.created).toLocaleString() : '-'}</Td>
                    <Td fontSize='xs'>{formatBytes ? formatBytes(s.size) : s.size}</Td>
                    <Td fontSize='xs'>{s.managed ? 'replication-manager' : 'other (never deleted by it)'}</Td>
                    <Td>
                      <HStack>
                        <RMButton size='xs' colorScheme='red' isDisabled={!canSnapback || running || data?.isMaster}
                          title={data?.isMaster ? 'Refused on the master: switch over first' : ''}
                          onClick={() => setPending({ action: 'snapback', name: s.name })}>
                          Snapback
                        </RMButton>
                        {s.managed && (
                          <RMButton size='xs' isDisabled={!canBackup || running} onClick={() => setPending({ action: 'delete', name: s.name })}>
                            Delete
                          </RMButton>
                        )}
                      </HStack>
                    </Td>
                  </Tr>
                ))}
                {data && (data.snapshots || []).length === 0 && (
                  <Tr><Td colSpan={5} fontSize='sm'>No snapshot.</Td></Tr>
                )}
              </Tbody>
            </Table>
          </VStack>
        </ModalBody>
        <ModalFooter>
          <HStack>
            <RMButton onClick={load} isDisabled={loading}>Refresh</RMButton>
            <RMButton colorScheme='blue' isDisabled={!canBackup || running || !data?.enabled || !!data?.error} onClick={take}>
              Take Snapshot
            </RMButton>
            <RMButton onClick={closeModal}>Close</RMButton>
          </HStack>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default VolumeSnapshotsModal
