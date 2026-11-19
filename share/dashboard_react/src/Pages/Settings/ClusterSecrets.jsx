import {
  Box,
  Code,
  Flex,
  IconButton,
  Table,
  Tbody,
  Td,
  Text,
  Th,
  Thead,
  Tooltip,
  Tr,
  useClipboard
} from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import { HiOutlineClipboardCopy } from 'react-icons/hi'
import RMButton from '../../components/RMButton'
import { clusterService } from '../../services/clusterService'

// The cluster's credentials in clear (GET /api/clusters/{cluster}/secrets, grant cluster-show-secrets).
// A read only: nothing here changes a credential. The values live in this component's state only
// (never the redux store, never the browser's storage) and are dropped on Hide and when the panel
// closes or the cluster changes.

const CopyButton = ({ value }) => {
  const { hasCopied, onCopy } = useClipboard(value || '')
  return (
    <Tooltip label={hasCopied ? 'Copied' : 'Copy'} hasArrow>
      <IconButton size='xs' variant='ghost' aria-label='Copy' icon={<HiOutlineClipboardCopy />} onClick={onCopy} />
    </Tooltip>
  )
}

const Row = ({ s }) => (
  <Tr>
    <Td>
      <Text fontWeight='semibold'>{s.label}</Text>
      <Text fontSize='xs' color='gray.500'>
        {s.key}
      </Text>
    </Td>
    <Td>
      {s.user ? (
        <Flex align='center' gap='1'>
          <Code>{s.user}</Code>
          <CopyButton value={s.user} />
        </Flex>
      ) : (
        '—'
      )}
    </Td>
    <Td>
      <Flex align='center' gap='1'>
        <Code style={{ wordBreak: 'break-all', userSelect: 'all' }}>{s.password}</Code>
        <CopyButton value={s.password} />
      </Flex>
    </Td>
  </Tr>
)

function ClusterSecrets({ clusterName, user, enabled }) {
  const [secrets, setSecrets] = useState(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const g = user?.grants || {}

  // a cluster change drops what was shown
  useEffect(() => () => setSecrets(null), [clusterName])

  if (!g['cluster-show-secrets'] || enabled === false) {
    return null
  }

  const reveal = () => {
    setLoading(true)
    setError('')
    clusterService
      .getClusterSecrets(clusterName)
      .then((res) => setSecrets(res.data))
      .catch((e) => setError(e?.response?.data || e?.message || 'refused'))
      .finally(() => setLoading(false))
  }

  return (
    <Box>
      <Flex align='center' gap='3' wrap='wrap'>
        {secrets ? (
          <RMButton onClick={() => setSecrets(null)}>Hide</RMButton>
        ) : (
          <RMButton isLoading={loading} onClick={reveal}>
            Reveal
          </RMButton>
        )}
        <Text as='span' fontSize='sm' color='gray.500'>
          shows the credentials in clear; changes nothing; each reveal is written to the security log
        </Text>
      </Flex>
      {error && (
        <Text mt='2' color='red.500'>
          {String(error)}
        </Text>
      )}
      {secrets && (
        <Table size='sm' mt='3'>
          <Thead>
            <Tr>
              <Th>Credential</Th>
              <Th>User</Th>
              <Th>Password</Th>
            </Tr>
          </Thead>
          <Tbody>
            {(secrets.secrets || []).map((s, i) => (
              <Row key={s.key + i} s={s} />
            ))}
            {secrets.instanceAdmin && <Row s={secrets.instanceAdmin} />}
            {!(secrets.secrets || []).length && !secrets.instanceAdmin && (
              <Tr>
                <Td colSpan={3}>No credential is set on this cluster.</Td>
              </Tr>
            )}
          </Tbody>
        </Table>
      )}
    </Box>
  )
}

export default ClusterSecrets
