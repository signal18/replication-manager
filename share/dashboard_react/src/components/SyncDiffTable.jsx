import React from 'react'
import { Badge, Box, Stack, Table, Tbody, Td, Text, Th, Thead, Tr } from '@chakra-ui/react'
import { formatSyncValue } from './syncDiffUtils'

const getPreviewStatusMeta = (status) => {
  if (status === 'will_change') return { color: 'blue', label: 'Customized' }
  if (status === 'no_change') return { color: 'green', label: 'Matches provider' }
  if (status === 'provider_missing') return { color: 'red', label: 'Provider missing' }
  if (status === 'error') return { color: 'red', label: 'Sync error' }
  return { color: 'gray', label: 'Unknown' }
}

const renderPreviewDetails = (result) => {
  const changes = Array.isArray(result?.changes) ? result.changes : []
  const warnings = Array.isArray(result?.warnings) ? result.warnings : []
  const unchangedFields = Array.isArray(result?.unchangedFields) ? result.unchangedFields : []
  const preservedEntries = Object.entries(result?.preservedFields || {})

  return (
    <Stack spacing={1}>
      {warnings.length > 0 && (
        <Box>
          {warnings.map((warning, idx) => (
            <Text key={`${warning}-${idx}`} fontSize='xs' color='orange.600'>
              ⚠ {warning}
            </Text>
          ))}
        </Box>
      )}

      {changes.length > 0 ? (
        <Box>
          {changes.map((change) => (
            <Text key={change.field} fontSize='xs'>
              <Text as='span' fontWeight='semibold'>{change.field}</Text>
              : {formatSyncValue(change.before)} → {formatSyncValue(change.after)}
            </Text>
          ))}
        </Box>
      ) : (
        <Text fontSize='xs' color='gray.600'>
          {result?.errorMessage || 'No provider-managed field changes.'}
        </Text>
      )}

      {unchangedFields.length > 0 && (
        <Text fontSize='xs' color='gray.500'>
          Unchanged provider fields: {unchangedFields.join(', ')}
        </Text>
      )}

      {preservedEntries.length > 0 && (
        <Box>
          <Text fontSize='xs' fontWeight='semibold' color='gray.700'>Preserved</Text>
          {preservedEntries.map(([key, value]) => (
            <Text key={key} fontSize='xs' color='gray.500'>
              {key}: {formatSyncValue(value)}
            </Text>
          ))}
        </Box>
      )}
    </Stack>
  )
}

function SyncDiffTable({ results = [] }) {
  return (
    <Table size='sm' variant='simple'>
      <Thead>
        <Tr>
          <Th>App</Th>
          <Th>Mount</Th>
          <Th>Status</Th>
          <Th>Diff</Th>
        </Tr>
      </Thead>
      <Tbody>
        {results.map((result, idx) => {
          const statusMeta = getPreviewStatusMeta(result?.status)
          return (
            <Tr key={`${result?.target?.appId}-${result?.target?.mountName}-${idx}`}>
              <Td>{result?.target?.appName || result?.target?.appId || '—'}</Td>
              <Td>{result?.target?.mountName || '—'}</Td>
              <Td>
                <Badge colorScheme={statusMeta.color}>{statusMeta.label}</Badge>
              </Td>
              <Td>
                {renderPreviewDetails(result)}
              </Td>
            </Tr>
          )
        })}
      </Tbody>
    </Table>
  )
}

export default SyncDiffTable
