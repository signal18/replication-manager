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
  const monitor = useSelector((state) => state?.globalClusters?.monitor)
  const clusterData = useSelector((state) => state?.cluster?.clusterData)
  const [runs, setRuns] = useState([])
  const [metric, setMetric] = useState('mysql_global_status_questions')
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

    // Get master hostname for metric path
    const servers = clusterData?.servers || []
    const master = servers.find(s => s.isMaster || s.state === 'Master') || servers[0]
    const masterHostname = master?.hostname || clusterName
    const hostname = masterHostname.replace(/\./g, '-').replace(/[`? ()/<'"]/g, '-')

    const fullMetric = `mysql.${hostname}.${metric}`

    // Find the newest run to align all others
    let newest = runs[0]
    runs.forEach(r => {
      if (new Date(r.startedAt) > new Date(newest.startedAt)) newest = r
    })

    const newestStart = Math.floor(new Date(newest.startedAt).getTime() / 1000)
    const newestEnd = Math.floor(new Date(newest.endedAt).getTime() / 1000)

    let targets = []
    runs.forEach((run, i) => {
      const label = `Run${i + 1} ${run.testType} ${run.threads}t ${run.dbFlavor || ''}/${run.dbVersion || ''}`
      const runStart = Math.floor(new Date(run.startedAt).getTime() / 1000)

      if (runStart === newestStart) {
        targets.push(`alias(${fullMetric},'${label}')`)
      } else {
        const shift = newestStart - runStart
        targets.push(`alias(timeShift(${fullMetric},'${shift}s'),'${label}')`)
      }
    })

    const params = new URLSearchParams()
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
                    <img
                      src={graphiteUrl}
                      alt='Benchmark comparison'
                      style={{ width: '100%' }}
                      onError={(e) => { e.target.style.display = 'none' }}
                    />
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
