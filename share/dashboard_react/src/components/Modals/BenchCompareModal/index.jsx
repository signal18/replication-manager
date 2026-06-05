import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton,
  Box, VStack, HStack, Text, Badge, Table, Thead, Tbody, Tr, Th, Td, Select
} from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import { useSelector } from 'react-redux'
import { useTheme } from '../../../ThemeProvider'
import { clusterService } from '../../../services/clusterService'
import RMButton from '../../RMButton'
import parentStyles from '../styles.module.scss'

const METRICS = [
  { value: 'mysql_global_status_queries', label: 'Queries (QPS)' },
  { value: 'mysql_global_status_com_select', label: 'SELECT' },
  { value: 'mysql_global_status_com_insert', label: 'INSERT' },
  { value: 'mysql_global_status_com_update', label: 'UPDATE' },
  { value: 'mysql_global_status_com_delete', label: 'DELETE' },
  { value: 'mysql_global_status_threads_running', label: 'Threads Running' },
  { value: 'mysql_global_status_threads_connected', label: 'Threads Connected' },
  { value: 'mysql_global_status_created_tmp_disk_tables', label: 'Tmp Disk Tables' },
  { value: 'mysql_global_status_innodb_rows_read', label: 'InnoDB Rows Read' },
  { value: 'mysql_global_status_innodb_rows_inserted', label: 'InnoDB Rows Inserted' },
  { value: 'mysql_global_status_bytes_sent', label: 'Bytes Sent' },
  { value: 'mysql_global_status_bytes_received', label: 'Bytes Received' },
]

// Fetch graphite PNG with credentials (img src doesn't send auth)
function GraphiteImage({ url }) {
  const [imgSrc, setImgSrc] = useState(null)
  const [error, setError] = useState(false)

  useEffect(() => {
    if (!url) return
    setError(false)
    fetch(url)
      .then(res => {
        if (!res.ok) throw new Error(res.status)
        return res.blob()
      })
      .then(blob => setImgSrc(URL.createObjectURL(blob)))
      .catch(() => setError(true))
    return () => { if (imgSrc) URL.revokeObjectURL(imgSrc) }
  }, [url])

  if (error) return <Text fontSize='sm' color='red.400'>Failed to load graph</Text>
  if (!imgSrc) return <Text fontSize='sm'>Loading graph...</Text>
  return <img src={imgSrc} alt='Benchmark comparison' style={{ width: '100%' }} />
}

function BenchCompareModal({ isOpen, closeModal, clusterName }) {
  const { theme } = useTheme()
  const baseURL = useSelector((state) => state?.auth?.baseURL)
  const monitor = useSelector((state) => state?.globalClusters?.monitor)
  const clusterData = useSelector((state) => state?.cluster?.clusterData)
  const [runs, setRuns] = useState([])
  const [metric, setMetric] = useState('mysql_global_status_queries')
  const [graphiteUrl, setGraphiteUrl] = useState('')

  const badgeVariant = theme === 'dark' ? 'solid' : 'subtle'

  useEffect(() => {
    if (isOpen && clusterName) {
      clusterService.getSysbenchHistory(clusterName, baseURL)
        .then(res => {
          const data = res.data
          setRuns(Array.isArray(data) ? data : data?.Entries || data?.entries || [])
        })
        .catch(() => setRuns([]))
    }
  }, [isOpen, clusterName, baseURL])

  const buildGraphiteUrl = () => {
    if (runs.length === 0) return

    // Use repman's graphite proxy route, not direct graphite port
    const apiUrl = ''

    // Use graphiteHost from the run entry (@@hostname with graphite replacements)
    // Falls back to first run's graphiteHost or cluster name
    const hostname = runs[0]?.graphiteHost || clusterName

    // Wrap cumulative counters in perSecond() for rate-based display
    const rateMetrics = [
      'mysql_global_status_queries', 'mysql_global_status_com_select',
      'mysql_global_status_com_insert', 'mysql_global_status_com_update',
      'mysql_global_status_com_delete', 'mysql_global_status_created_tmp_disk_tables',
      'mysql_global_status_innodb_rows_read', 'mysql_global_status_innodb_rows_inserted',
      'mysql_global_status_bytes_sent', 'mysql_global_status_bytes_received'
    ]
    const rawMetric = `mysql.${hostname}.${metric}`
    const fullMetric = rateMetrics.includes(metric) ? `perSecond(keepLastValue(${rawMetric}))` : rawMetric

    // Find the newest run to align all others
    let newest = runs[0]
    runs.forEach(r => {
      if (new Date(r.startedAt) > new Date(newest.startedAt)) newest = r
    })

    const newestStart = Math.floor(new Date(newest.startedAt).getTime() / 1000)
    const newestEnd = Math.floor(new Date(newest.endedAt).getTime() / 1000)

    // First run is the reference, others show delta from reference TPS
    const refRun = runs[0]
    const refTPS = refRun?.avgTps || 0

    let targets = []
    runs.forEach((run, i) => {
      const runStart = Math.floor(new Date(run.startedAt).getTime() / 1000)
      let label
      if (i === 0) {
        label = `REF ${run.testType} ${run.threads}t ${run.dbFlavor || ''}/${run.dbVersion || ''} TPS:${run.avgTps?.toFixed(0)}`
      } else {
        const diff = (run.avgTps || 0) - refTPS
        const pct = refTPS > 0 ? ((diff / refTPS) * 100).toFixed(1) : '0'
        const sign = diff >= 0 ? '+' : ''
        label = `${run.testType} ${run.threads}t ${run.dbFlavor || ''}/${run.dbVersion || ''} TPS:${run.avgTps?.toFixed(0)} (${sign}${pct}%)`
      }

      if (runStart === newestStart) {
        targets.push(`alias(${fullMetric},'${label}')`)
      } else {
        const shift = newestStart - runStart
        targets.push(`alias(timeShift(${fullMetric},'${shift}s'),'${label}')`)
      }
    })

    const metricLabel = METRICS.find(m => m.value === metric)?.label || metric
    const params = new URLSearchParams()
    params.set('title', `${metricLabel} — ref: ${refRun.testType} ${refRun.threads}t ${refRun.dbFlavor}/${refRun.dbVersion}`)
    params.set('from', newestStart.toString())
    params.set('until', newestEnd.toString())
    targets.forEach(t => params.append('target', t))
    params.set('format', 'png')
    params.set('width', '800')
    params.set('height', '300')

    setGraphiteUrl(`/graphite/render?${params.toString()}`)
  }

  const formatDate = (t) => t ? new Date(t).toLocaleString() : '-'

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='xl'>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader fontSize='md'>Compare Benchmarks</ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={6}>
          <VStack align='stretch' spacing={4}>

            {runs.length > 0 && (
              <>
                <Box maxH='300px' overflowY='auto' fontSize='xs'>
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
                        </Tr>
                      ))}
                    </Tbody>
                  </Table>
                </Box>

                <HStack spacing={3}>
                  <Select size='sm' value={metric} onChange={(e) => setMetric(e.target.value)} flex={1}>
                    {METRICS.map(m => (
                      <option key={m.value} value={m.value}>{m.label}</option>
                    ))}
                  </Select>
                  <RMButton size='small' colorScheme='blue' onClick={buildGraphiteUrl}>
                    Show Graph
                  </RMButton>
                </HStack>

                {graphiteUrl && (
                  <Box borderRadius='md' overflow='hidden' bg={theme === 'light' ? 'gray.50' : 'rgba(255,255,255,0.05)'} p={2}>
                    <GraphiteImage url={graphiteUrl} />
                  </Box>
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
