import { HStack, Spinner, Text } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import { globalClustersService } from '../../services/globalClustersService'
import TagPill from '../../components/TagPill'

// The Cloud18 login of this replication-manager (#2159): made with the account it is registered
// with, kept and refreshed; refused when Cloud18 refuses that account (register again).
const stateColor = { connected: 'green', registered: 'blue', refused: 'orange', none: 'gray' }
const stateText = {
  none: 'Not registered to Cloud18',
  registered: 'Registered: signs in at the next use',
  refused: 'Cloud18 refuses the registered account: register again'
}

function Cloud18Identity() {
  const [status, setStatus] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    globalClustersService.getCloud18Identity()
      .then(({ data }) => setStatus(data))
      .catch((e) => setError(`${e?.response?.data || e?.message || e}`))
  }, [])

  if (!status && !error) return <Spinner size='sm' />
  const state = status?.state || 'none'
  return (
    <HStack spacing={2} align='center' wrap='wrap'>
      <TagPill colorScheme={stateColor[state] || 'gray'} text={state.toUpperCase()} />
      {status?.user && <Text fontSize='sm'>{status.user}</Text>}
      {stateText[state] && <Text fontSize='sm' opacity={0.7}>{stateText[state]}</Text>}
      {status?.reason && <Text fontSize='sm' opacity={0.7}>{status.reason}</Text>}
      {error && <Text fontSize='sm' color='red.500'>{error}</Text>}
    </HStack>
  )
}

export default Cloud18Identity
