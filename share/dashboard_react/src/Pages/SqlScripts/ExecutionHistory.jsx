import React, { useMemo } from 'react'
import {
  Box,
  VStack,
  HStack,
  Text,
  Badge,
  Alert,
  AlertIcon,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  TableContainer
} from '@chakra-ui/react'
import styles from './styles.module.scss'

function ExecutionHistory({ history = [] }) {
  const sortedHistory = useMemo(() => {
    return [...history].sort((a, b) =>
      new Date(b.timestamp) - new Date(a.timestamp)
    )
  }, [history])

  const getStatusColor = (status) => {
    switch (status?.toLowerCase()) {
      case 'success':
        return 'green'
      case 'error':
        return 'red'
      case 'timeout':
        return 'orange'
      default:
        return 'gray'
    }
  }

  const formatDuration = (duration) => {
    if (typeof duration === 'number') {
      return `${duration.toFixed(2)}s`
    }
    return '-'
  }

  const formatTimestamp = (ts) => {
    if (!ts) return '-'
    const date = new Date(ts)
    return date.toLocaleString()
  }

  if (!sortedHistory || sortedHistory.length === 0) {
    return (
      <Alert status="info">
        <AlertIcon />
        No execution history yet. Execute a script to see it here.
      </Alert>
    )
  }

  return (
    <VStack spacing={4} align="stretch">
      <HStack justify="space-between">
        <Text fontSize="lg" fontWeight="bold">
          Execution History ({sortedHistory.length})
        </Text>
        <Text fontSize="sm" color="gray.500">
          Most recent first
        </Text>
      </HStack>

      <TableContainer
        className={styles.historyTable}
        borderRadius="md"
        borderWidth="1px"
        borderColor="gray.200"
      >
        <Table size="sm" variant="striped">
          <Thead bg="gray.50">
            <Tr>
              <Th>Time</Th>
              <Th>Status</Th>
              <Th>Duration</Th>
              <Th>Rows</Th>
              <Th>Server</Th>
              <Th>Database</Th>
              <Th>Details</Th>
            </Tr>
          </Thead>
          <Tbody>
            {sortedHistory.map((entry, idx) => (
              <Tr key={idx} _hover={{ bg: 'gray.50' }}>
                <Td fontSize="sm">
                  {formatTimestamp(entry.timestamp)}
                </Td>
                <Td>
                  <Badge colorScheme={getStatusColor(entry.status)}>
                    {entry.status?.toUpperCase()}
                  </Badge>
                </Td>
                <Td fontSize="sm" fontFamily="monospace">
                  {formatDuration(entry.duration)}
                </Td>
                <Td fontSize="sm" fontFamily="monospace">
                  {entry.rowsAffected || '0'}
                </Td>
                <Td fontSize="sm" noOfLines={1}>
                  {entry.serverUrl || '-'}
                </Td>
                <Td fontSize="sm" noOfLines={1}>
                  {entry.targetDatabase || '-'}
                </Td>
                <Td>
                  <ExecutionDetailPopper entry={entry} />
                </Td>
              </Tr>
            ))}
          </Tbody>
        </Table>
      </TableContainer>

      {/* Summary Stats */}
      <Box bg="gray.50" p={4} borderRadius="md">
        <VStack align="flex-start" spacing={2} fontSize="sm">
          <HStack>
            <Text fontWeight="bold">Total Executions:</Text>
            <Text>{sortedHistory.length}</Text>
          </HStack>
          <HStack>
            <Text fontWeight="bold">Successful:</Text>
            <Badge colorScheme="green">
              {sortedHistory.filter(e => e.status === 'success').length}
            </Badge>
          </HStack>
          <HStack>
            <Text fontWeight="bold">Failed:</Text>
            <Badge colorScheme="red">
              {sortedHistory.filter(e => e.status === 'error').length}
            </Badge>
          </HStack>
          {sortedHistory.some(e => e.status === 'timeout') && (
            <HStack>
              <Text fontWeight="bold">Timeouts:</Text>
              <Badge colorScheme="orange">
                {sortedHistory.filter(e => e.status === 'timeout').length}
              </Badge>
            </HStack>
          )}
        </VStack>
      </Box>
    </VStack>
  )
}

// Popover for execution details
const ExecutionDetailPopper = ({ entry }) => {
  const [showDetails, setShowDetails] = React.useState(false)

  return (
    <Box position="relative">
      <Text
        cursor="pointer"
        color="blue.500"
        onClick={() => setShowDetails(!showDetails)}
        fontSize="sm"
        _hover={{ textDecoration: 'underline' }}
      >
        {showDetails ? 'Hide' : 'Show'}
      </Text>

      {showDetails && (
        <Box
          position="absolute"
          top="100%"
          right={0}
          mt={2}
          bg="white"
          borderWidth="1px"
          borderRadius="md"
          p={3}
          boxShadow="lg"
          zIndex={10}
          minW="300px"
          maxW="400px"
        >
          <VStack align="flex-start" spacing={2} fontSize="xs">
            {entry.scriptPath && (
              <Box>
                <Text fontWeight="bold" color="gray.600">Script:</Text>
                <Text fontFamily="monospace" noOfLines={2} color="gray.700">
                  {entry.scriptPath}
                </Text>
              </Box>
            )}
            {entry.errorMessage && (
              <Box
                bg="red.50"
                p={2}
                borderRadius="sm"
                borderLeft="2px solid red"
                w="100%"
              >
                <Text fontWeight="bold" color="red.700" mb={1}>Error:</Text>
                <Text fontFamily="monospace" fontSize="xs" color="red.600" noOfLines={3}>
                  {entry.errorMessage}
                </Text>
              </Box>
            )}
            <HStack spacing={4} fontSize="xs">
              <Box>
                <Text color="gray.500">Job ID:</Text>
                <Text fontFamily="monospace">{entry.jobId || '-'}</Text>
              </Box>
              <Box>
                <Text color="gray.500">Target:</Text>
                <Text>{entry.targetServer || '-'}</Text>
              </Box>
            </HStack>
          </VStack>
        </Box>
      )}
    </Box>
  )
}

export default ExecutionHistory
