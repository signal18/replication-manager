import { HStack, Link, Spinner, Text } from '@chakra-ui/react'
import React, { useCallback, useEffect, useState } from 'react'
import { globalClustersService } from '../../services/globalClustersService'
import TagPill from '../../components/TagPill'
import RMButton from '../../components/RMButton'
import ConfirmModal from '../../components/Modals/ConfirmModal'

// The Cloud18 login of this replication-manager (#2159): Connect asks the realm for a code, the
// person opens the realm's page in any browser, logs in and types it; the row then shows who
// connected the instance. Polled while a login waits.
const stateColor = { connected: 'green', pending: 'blue', reconnect: 'orange', none: 'gray' }

function Cloud18Identity() {
  const [status, setStatus] = useState(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirmDisconnect, setConfirmDisconnect] = useState(false)

  const load = useCallback(() => {
    globalClustersService.getCloud18Identity()
      .then(({ data }) => setStatus(data))
      .catch((e) => setError(`${e?.response?.data || e?.message || e}`))
  }, [])

  useEffect(() => { load() }, [load])

  useEffect(() => {
    if (status?.state !== 'pending') return
    const timer = setInterval(load, 3000)
    return () => clearInterval(timer)
  }, [status?.state, load])

  const call = (request) => {
    setBusy(true)
    setError('')
    request()
      .then(({ data }) => setStatus(data))
      .catch((e) => setError(`${e?.response?.data || e?.message || e}`))
      .finally(() => setBusy(false))
  }

  if (!status && !error) return <Spinner size='sm' />
  const state = status?.state || 'none'
  const device = status?.device
  return (
    <HStack spacing={2} align='center' wrap='wrap'>
      <TagPill colorScheme={stateColor[state] || 'gray'} text={state.toUpperCase()} />
      {(state === 'connected' || state === 'reconnect') && status?.user && <Text fontSize='sm'>{status.user}</Text>}
      {state === 'pending' && device && (
        <>
          <Text fontSize='sm'>Open</Text>
          <Link fontSize='sm' href={device.verificationUriComplete || device.verificationUri} isExternal textDecoration='underline'>
            {device.verificationUri}
          </Link>
          <Text fontSize='sm'>and enter</Text>
          <Text fontSize='md' fontWeight='bold' fontFamily='mono'>{device.userCode}</Text>
          <Spinner size='xs' />
        </>
      )}
      {status?.reason && state !== 'connected' && <Text fontSize='sm' opacity={0.7}>{status.reason}</Text>}
      {state !== 'connected' && (
        <RMButton isLoading={busy} onClick={() => call(globalClustersService.connectCloud18Identity)}>
          {state === 'pending' ? 'New code' : state === 'reconnect' ? 'Reconnect' : 'Connect'}
        </RMButton>
      )}
      {(state === 'connected' || state === 'reconnect') && (
        <RMButton isLoading={busy} onClick={() => setConfirmDisconnect(true)}>Disconnect</RMButton>
      )}
      {error && <Text fontSize='sm' color='red.500'>{error}</Text>}
      {confirmDisconnect && (
        <ConfirmModal
          isOpen={confirmDisconnect}
          closeModal={() => setConfirmDisconnect(false)}
          title='Disconnect this replication-manager from the Cloud18 identity? The SSO archive storages and the Cloud18 vault refuse it until someone connects it again.'
          onConfirmClick={() => { setConfirmDisconnect(false); call(globalClustersService.disconnectCloud18Identity) }}
        />
      )}
    </HStack>
  )
}

export default Cloud18Identity
