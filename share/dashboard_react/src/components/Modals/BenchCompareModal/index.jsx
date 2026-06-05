import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton,
  Box, VStack, HStack, Text, Badge, Table, Thead, Tbody, Tr, Th, Td, Select
} from '@chakra-ui/react'
import React, { useState, useEffect, useRef } from 'react'
import { useSelector } from 'react-redux'
import * as d3 from 'd3'
import { useTheme } from '../../../ThemeProvider'
import { clusterService } from '../../../services/clusterService'
import RMButton from '../../RMButton'
import parentStyles from '../styles.module.scss'

const RECORD_METRICS = [
  { value: 'TPS', label: 'TPS' },
  { value: 'QPS', label: 'QPS' },
  { value: 'Latency', label: 'Latency (ms)' },
  { value: 'ReadQPS', label: 'Read QPS' },
  { value: 'WriteQPS', label: 'Write QPS' },
  { value: 'ErrorPerSec', label: 'Errors/s' },
]

const COLORS = ['#3182ce', '#e53e3e', '#38a169', '#d69e2e', '#805ad5', '#dd6b20', '#319795', '#b83280']

function BenchChart({ runs, metric, theme }) {
  const svgRef = useRef()

  useEffect(() => {
    if (!svgRef.current || runs.length === 0) return

    const svg = d3.select(svgRef.current)
    svg.selectAll('*').remove()

    const margin = { top: 20, right: 120, bottom: 30, left: 50 }
    const width = 750 - margin.left - margin.right
    const height = 250 - margin.top - margin.bottom

    const g = svg.append('g').attr('transform', `translate(${margin.left},${margin.top})`)

    // Collect all data series
    const series = runs.map((run, i) => {
      const records = run.records || []
      return {
        label: i === 0
          ? `REF ${run.testType} ${run.threads}t`
          : `${run.testType} ${run.threads}t`,
        color: COLORS[i % COLORS.length],
        data: records.map((r, j) => ({ second: r.Second || j, value: r[metric] || 0 }))
      }
    }).filter(s => s.data.length > 0)

    if (series.length === 0) return

    const maxX = d3.max(series, s => d3.max(s.data, d => d.second)) || 100
    const maxY = d3.max(series, s => d3.max(s.data, d => d.value)) || 1

    const x = d3.scaleLinear().domain([0, maxX]).range([0, width])
    const y = d3.scaleLinear().domain([0, maxY * 1.1]).range([height, 0])

    const textColor = theme === 'dark' ? '#e2e8f0' : '#333'
    const gridColor = theme === 'dark' ? 'rgba(255,255,255,0.1)' : 'rgba(0,0,0,0.1)'

    // Grid
    g.append('g')
      .attr('transform', `translate(0,${height})`)
      .call(d3.axisBottom(x).ticks(10))
      .selectAll('text').attr('fill', textColor)
    g.append('g')
      .call(d3.axisLeft(y).ticks(5))
      .selectAll('text').attr('fill', textColor)
    g.selectAll('.domain').attr('stroke', textColor)
    g.selectAll('.tick line').attr('stroke', gridColor)

    // Lines
    const line = d3.line().x(d => x(d.second)).y(d => y(d.value)).curve(d3.curveMonotoneX)
    series.forEach(s => {
      g.append('path')
        .datum(s.data)
        .attr('fill', 'none')
        .attr('stroke', s.color)
        .attr('stroke-width', 1.5)
        .attr('d', line)
    })

    // Legend
    const legend = g.append('g').attr('transform', `translate(${width + 10}, 0)`)
    series.forEach((s, i) => {
      const row = legend.append('g').attr('transform', `translate(0,${i * 16})`)
      row.append('rect').attr('width', 10).attr('height', 10).attr('fill', s.color)
      row.append('text').attr('x', 14).attr('y', 9).text(s.label)
        .attr('fill', textColor).attr('font-size', '10px')
    })

    // Axis labels
    g.append('text').attr('x', width / 2).attr('y', height + 28).attr('text-anchor', 'middle')
      .attr('fill', textColor).attr('font-size', '11px').text('Seconds')

  }, [runs, metric, theme])

  return <svg ref={svgRef} width={750} height={250} />
}

function BenchCompareModal({ isOpen, closeModal, clusterName }) {
  const { theme } = useTheme()
  const baseURL = useSelector((state) => state?.auth?.baseURL)
  const [runs, setRuns] = useState([])
  const [metric, setMetric] = useState('TPS')
  const [showChart, setShowChart] = useState(false)

  const badgeVariant = theme === 'dark' ? 'solid' : 'subtle'

  useEffect(() => {
    if (isOpen && clusterName) {
      setShowChart(false)
      clusterService.getSysbenchHistory(clusterName, baseURL)
        .then(res => {
          const data = res.data
          setRuns(Array.isArray(data) ? data : data?.Entries || data?.entries || [])
        })
        .catch(() => setRuns([]))
    }
  }, [isOpen, clusterName, baseURL])

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
                    {RECORD_METRICS.map(m => (
                      <option key={m.value} value={m.value}>{m.label}</option>
                    ))}
                  </Select>
                  <RMButton size='small' colorScheme='blue' onClick={() => setShowChart(true)}>
                    Show Graph
                  </RMButton>
                </HStack>

                {showChart && (
                  <Box borderRadius='md' overflow='hidden' bg={theme === 'light' ? 'gray.50' : 'rgba(255,255,255,0.05)'} p={2}>
                    <BenchChart runs={runs} metric={metric} theme={theme} />
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
