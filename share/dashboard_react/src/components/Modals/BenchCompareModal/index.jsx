import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton,
  Box, VStack, HStack, Text, Badge, Divider, Table, Thead, Tbody, Tr, Th, Td, Select
} from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import { useSelector } from 'react-redux'
import { useTheme } from '../../../ThemeProvider'
import { clusterService } from '../../../services/clusterService'
import parentStyles from '../styles.module.scss'

const METRICS = [
  { value: 'mysql_global_status_questions', label: 'Questions (QPS)' },
  { value: 'mysql_global_status_com_select', label: 'SELECT' },
  { value: 'mysql_global_status_com_insert', label: 'INSERT' },
  { value: 'mysql_global_status_com_update', label: 'UPDATE' },
  { value: 'mysql_global_status_com_delete', label: 'DELETE' },
  { value: 'mysql_global_status_threads_running', label: 'Threads Running' },
  { value: 'mysql_global_status_connections', label: 'Connections' },
  { value: 'mysql_global_status_created_tmp_disk_tables', label: 'Tmp Disk Tables' },
  { value: 'mysql_global_status_innodb_row_lock_waits', label: 'InnoDB Row Lock Waits' },
]

function BenchCompareModal({ isOpen, closeModal, clusterName }) {
  const { theme } = useTheme()
  const baseURL = useSelector((state) => state?.auth?.baseURL)
  const [runs, setRuns] = useState([])
  const [metric, setMetric] = useState('mysql_global_status_questions')
  const [compareResult, setCompareResult] = useState(null)
  const [loading, setLoading] = useState(false)

  const badgeVariant = theme === 'dark' ? 'solid' : 'subtle'

  useEffect(() => {
    if (isOpen && clusterName) {
      clusterService.getSysbenchHistory(clusterName, baseURL)
        .then(res => {
          const data = res.data
          // http-logs returns the SysbenchLog struct with entries field
          setRuns(Array.isArray(data) ? data : data?.Entries || data?.entries || [])
        })
        .catch(() => setRuns([]))
    }
  }, [isOpen, clusterName, baseURL])

  const handleCompare = () => {
    setLoading(true)
    clusterService.getSysbenchCompare(clusterName, metric, baseURL)
      .then(res => setCompareResult(res.data))
      .catch(() => setCompareResult(null))
      .finally(() => setLoading(false))
  }

  const formatDate = (t) => t ? new Date(t).toLocaleString() : '-'
  const formatDuration = (start, end) => {
    if (!start || !end) return '-'
    const ms = new Date(end) - new Date(start)
    return `${Math.round(ms / 1000)}s`
  }

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='xl'>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader fontSize='md'>Compare Benchmarks</ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={6}>
          <VStack align='stretch' spacing={4}>

            <HStack spacing={3}>
              <Select size='sm' value={metric} onChange={(e) => setMetric(e.target.value)} flex={1}>
                {METRICS.map(m => (
                  <option key={m.value} value={m.value}>{m.label}</option>
                ))}
              </Select>
            </HStack>

            {runs.length > 0 && (
              <>
                <Box maxH='400px' overflowY='auto' fontSize='xs'>
                  <Table size='sm' variant='simple'>
                    <Thead>
                      <Tr>
                        <Th>#</Th>
                        <Th>Date</Th>
                        <Th>Test</Th>
                        <Th>Threads</Th>
                        <Th>Avg TPS</Th>
                        <Th>Avg Lat</Th>
                        <Th>DBU</Th>
                        <Th>TPS/DBU</Th>
                        <Th>Flavor</Th>
                        <Th>Tags</Th>
                      </Tr>
                    </Thead>
                    <Tbody>
                      {runs.map((run, i) => (
                        <Tr key={i}>
                          <Td>{i + 1}</Td>
                          <Td>{formatDate(run.startedAt)}</Td>
                          <Td>
                            <Badge variant={badgeVariant} colorScheme='blue' size='sm'>{run.testType}</Badge>
                          </Td>
                          <Td>{run.threads}</Td>
                          <Td fontWeight={600}>{run.avgTps?.toFixed(1)}</Td>
                          <Td>{run.avgLatency?.toFixed(2)}ms</Td>
                          <Td>{run.clusterDbu?.toFixed(1)}</Td>
                          <Td>{run.tpsPerDbu?.toFixed(2)}</Td>
                          <Td>{run.dbFlavor} {run.dbVersion}</Td>
                          <Td maxW='150px' isTruncated>{run.configTags}</Td>
                        </Tr>
                      ))}
                    </Tbody>
                  </Table>
                </Box>

                {compareResult?.graphiteUrl && (
                  <>
                    <Divider />
                    <Text fontSize='sm' fontWeight={600}>
                      Graphite overlay: {METRICS.find(m => m.value === metric)?.label}
                    </Text>
                    <Box borderRadius='md' overflow='hidden'>
                      <img
                        src={compareResult.graphiteUrl + '&width=800&height=300&format=png'}
                        alt='Benchmark comparison'
                        style={{ width: '100%' }}
                      />
                    </Box>
                  </>
                )}
              </>
            )}

            {runs.length === 0 && (
              <Text fontSize='sm' color={theme === 'light' ? 'gray.500' : 'gray.500'} textAlign='center' py={4}>
                No benchmark runs recorded
              </Text>
            )}

          </VStack>
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

export default BenchCompareModal
