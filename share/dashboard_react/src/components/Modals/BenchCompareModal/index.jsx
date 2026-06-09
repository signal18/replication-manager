import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton,
  Box, VStack, HStack, Text, Badge, Table, Thead, Tbody, Tr, Th, Td, Select, Spinner, Checkbox
} from '@chakra-ui/react'
import React, { useState, useEffect, useRef } from 'react'
import { useSelector } from 'react-redux'
import * as d3 from 'd3'
import { useTheme } from '../../../ThemeProvider'
import { clusterService } from '../../../services/clusterService'
import RMButton from '../../RMButton'
import parentStyles from '../styles.module.scss'

const GRAPHITE_METRICS = [
  { value: 'mysql_global_status_queries', label: 'Queries (QPS)', rate: true },
  { value: 'mysql_global_status_com_select', label: 'SELECT/s', rate: true },
  { value: 'mysql_global_status_com_insert', label: 'INSERT/s', rate: true },
  { value: 'mysql_global_status_com_update', label: 'UPDATE/s', rate: true },
  { value: 'mysql_global_status_com_delete', label: 'DELETE/s', rate: true },
  { value: 'mysql_global_status_threads_running', label: 'Threads Running', rate: false },
  { value: 'mysql_global_status_threads_connected', label: 'Threads Connected', rate: false },
  { value: 'mysql_global_status_created_tmp_disk_tables', label: 'Tmp Disk Tables/s', rate: true },
  { value: 'mysql_global_status_innodb_rows_read', label: 'InnoDB Rows Read/s', rate: true },
  { value: 'mysql_global_status_innodb_rows_inserted', label: 'InnoDB Rows Inserted/s', rate: true },
  { value: 'mysql_global_status_bytes_sent', label: 'Bytes Sent/s', rate: true },
  { value: 'mysql_global_status_bytes_received', label: 'Bytes Received/s', rate: true },
]

const SCALE_METRICS = [
  { value: 'avgTps', label: 'TPS' },
  { value: 'avgLatency', label: 'Latency (ms)' },
  { value: 'tpsPerDbu', label: 'TPS/DBU' },
]

const COLORS = ['#3182ce', '#e53e3e', '#38a169', '#d69e2e', '#805ad5', '#dd6b20', '#319795', '#b83280']

const isScaleRun = (run) => run.scaleResults?.length > 0

// Build a label showing what changed vs the reference run
function buildSeriesLabel(run, refRun, index) {
  if (index === 0) {
    const threads = isScaleRun(run) ? 'scale' : `${run.threads}t`
    return `REF ${run.testType} ${threads} ${run.dbFlavor || ''}/${run.dbVersion || ''}`
  }
  const diffs = []
  if (run.testType !== refRun.testType) diffs.push(run.testType)
  if (!isScaleRun(run) && !isScaleRun(refRun) && run.threads !== refRun.threads) diffs.push(`${run.threads}t`)
  if (run.dbFlavor !== refRun.dbFlavor || run.dbVersion !== refRun.dbVersion) {
    diffs.push(`${run.dbFlavor || ''}/${run.dbVersion || ''}`)
  }
  if (run.proxyType !== refRun.proxyType) diffs.push(run.proxyType)
  if (run.configTags !== refRun.configTags) diffs.push(`tags:${run.configTags}`)
  if (diffs.length === 0) {
    return `Run${index + 1} ${new Date(run.startedAt).toLocaleString()}`
  }
  return diffs.join(' ')
}

// Generic d3 line chart — used for both graphite time-series and scale charts
function LineChart({ series, theme, xLabel, yLabel, xAccessor = 'x', yAccessor = 'y' }) {
  const svgRef = useRef()

  useEffect(() => {
    if (!svgRef.current || series.length === 0) return

    const svg = d3.select(svgRef.current)
    svg.selectAll('*').remove()

    const margin = { top: 20, right: 180, bottom: 35, left: 60 }
    const width = 800 - margin.left - margin.right
    const height = 280 - margin.top - margin.bottom

    const g = svg.append('g').attr('transform', `translate(${margin.left},${margin.top})`)

    const validSeries = series.filter(s => s.data.length > 0)
    if (validSeries.length === 0) return

    const maxX = d3.max(validSeries, s => d3.max(s.data, d => d[xAccessor])) || 100
    const maxY = d3.max(validSeries, s => d3.max(s.data, d => d[yAccessor])) || 1

    const x = d3.scaleLinear().domain([0, maxX]).range([0, width])
    const y = d3.scaleLinear().domain([0, maxY * 1.1]).range([height, 0])

    const textColor = theme === 'dark' ? '#e2e8f0' : '#333'
    const gridColor = theme === 'dark' ? 'rgba(255,255,255,0.1)' : 'rgba(0,0,0,0.1)'

    // Grid lines
    g.append('g')
      .call(d3.axisLeft(y).ticks(5).tickSize(-width))
      .selectAll('.tick line').attr('stroke', gridColor)
    g.selectAll('.domain').attr('stroke', textColor)

    // Axes
    g.append('g')
      .attr('transform', `translate(0,${height})`)
      .call(d3.axisBottom(x).ticks(10))
      .selectAll('text').attr('fill', textColor)
    g.append('g')
      .call(d3.axisLeft(y).ticks(5))
      .selectAll('text').attr('fill', textColor)
    g.selectAll('.domain').attr('stroke', textColor)

    // Lines + dots
    const line = d3.line().x(d => x(d[xAccessor])).y(d => y(d[yAccessor])).curve(d3.curveMonotoneX).defined(d => d[yAccessor] != null)
    validSeries.forEach(s => {
      g.append('path')
        .datum(s.data)
        .attr('fill', 'none')
        .attr('stroke', s.color)
        .attr('stroke-width', s.isRef ? 2.5 : 1.5)
        .attr('stroke-dasharray', s.isRef ? null : '6,3')
        .attr('d', line)
      // Data point dots for scale charts (few points)
      if (s.data.length <= 20) {
        g.selectAll(null).data(s.data.filter(d => d[yAccessor] != null)).enter()
          .append('circle')
          .attr('cx', d => x(d[xAccessor]))
          .attr('cy', d => y(d[yAccessor]))
          .attr('r', 3)
          .attr('fill', s.color)
      }
    })

    // Legend
    const legend = g.append('g').attr('transform', `translate(${width + 10}, 0)`)
    validSeries.forEach((s, i) => {
      const row = legend.append('g').attr('transform', `translate(0,${i * 18})`)
      row.append('line').attr('x1', 0).attr('x2', 14).attr('y1', 5).attr('y2', 5)
        .attr('stroke', s.color).attr('stroke-width', s.isRef ? 2.5 : 1.5)
        .attr('stroke-dasharray', s.isRef ? null : '6,3')
      row.append('text').attr('x', 18).attr('y', 9).text(s.label)
        .attr('fill', textColor).attr('font-size', '10px')
    })

    // Axis labels
    g.append('text').attr('x', width / 2).attr('y', height + 30).attr('text-anchor', 'middle')
      .attr('fill', textColor).attr('font-size', '11px').text(xLabel)
    g.append('text').attr('transform', 'rotate(-90)').attr('x', -height / 2).attr('y', -45)
      .attr('text-anchor', 'middle').attr('fill', textColor).attr('font-size', '11px').text(yLabel)

  }, [series, theme, xLabel, yLabel, xAccessor, yAccessor])

  return <svg ref={svgRef} width={800} height={280} />
}

function BenchCompareModal({ isOpen, closeModal, clusterName }) {
  const { theme } = useTheme()
  const baseURL = useSelector((state) => state?.auth?.baseURL)
  const [runs, setRuns] = useState([])
  const [selected, setSelected] = useState(new Set())
  const [metric, setMetric] = useState('mysql_global_status_queries')
  const [scaleMetric, setScaleMetric] = useState('avgTps')
  const [chartSeries, setChartSeries] = useState([])
  const [chartMode, setChartMode] = useState(null) // 'graphite' | 'scale'
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const badgeVariant = theme === 'dark' ? 'solid' : 'subtle'

  useEffect(() => {
    if (isOpen && clusterName) {
      setChartSeries([])
      setChartMode(null)
      setError('')
      setSelected(new Set())
      clusterService.getSysbenchHistory(clusterName, baseURL)
        .then(res => {
          const data = res.data
          const entries = Array.isArray(data) ? data : data?.Entries || data?.entries || []
          setRuns(entries)
          if (entries.length > 0) setSelected(new Set([entries.length - 1]))
        })
        .catch(() => setRuns([]))
    }
  }, [isOpen, clusterName, baseURL])

  const toggleSelect = (i) => {
    setSelected(prev => {
      const next = new Set(prev)
      if (next.has(i)) next.delete(i)
      else next.add(i)
      return next
    })
  }

  const toggleAll = () => {
    if (selected.size === runs.length) setSelected(new Set())
    else setSelected(new Set(runs.map((_, i) => i)))
  }

  // Check if any selected run is a scale run
  const selectedRuns = runs.filter((_, i) => selected.has(i))
  const hasScaleRuns = selectedRuns.some(isScaleRun)

  // Show scale chart from embedded scaleResults data
  const showScaleChart = () => {
    const sorted = [...selectedRuns].sort((a, b) => new Date(a.startedAt) - new Date(b.startedAt))
    const refRun = sorted[0]

    let idx = 0
    const series = sorted.map((run) => {
      const i = idx++
      if (isScaleRun(run)) {
        return {
          label: buildSeriesLabel(run, refRun, i),
          color: COLORS[i % COLORS.length],
          isRef: i === 0,
          data: run.scaleResults.map(sp => ({ x: sp.threads, y: sp[scaleMetric] || 0 }))
        }
      }
      // Normal single-thread run: plot as a single point on the threads axis
      const valMap = { avgTps: run.avgTps, avgLatency: run.avgLatency, tpsPerDbu: run.tpsPerDbu }
      return {
        label: buildSeriesLabel(run, refRun, i),
        color: COLORS[i % COLORS.length],
        isRef: i === 0,
        data: [{ x: run.threads, y: valMap[scaleMetric] || 0 }]
      }
    })

    setChartSeries(series)
    setChartMode('scale')
    setError(series.length === 0 ? 'No data in selected runs' : '')
  }

  // Fetch graphite time-series for all selected runs (normal + scale)
  const fetchGraphiteData = async () => {
    if (selectedRuns.length === 0) return
    setLoading(true)
    setError('')
    setChartSeries([])

    const metricDef = GRAPHITE_METRICS.find(m => m.value === metric)
    const sorted = [...selectedRuns].sort((a, b) => new Date(a.startedAt) - new Date(b.startedAt))
    const refRun = sorted[0]

    const newest = sorted[sorted.length - 1]
    const newestStart = Math.floor(new Date(newest.startedAt).getTime() / 1000)
    const newestEnd = Math.floor(new Date(newest.endedAt).getTime() / 1000)

    try {
      const results = await Promise.all(sorted.map(async (run, i) => {
        const hostname = run.graphiteHost || clusterName
        const rawMetric = `mysql.${hostname}.${metric}`
        const wrapped = metricDef?.rate
          ? `perSecond(keepLastValue(${rawMetric}))`
          : rawMetric

        const runStart = Math.floor(new Date(run.startedAt).getTime() / 1000)
        let target
        if (runStart === newestStart) {
          target = wrapped
        } else {
          const shift = newestStart - runStart
          target = `timeShift(${wrapped},'${shift}s')`
        }

        const params = new URLSearchParams()
        params.set('format', 'raw')
        params.set('from', newestStart.toString())
        params.set('until', newestEnd.toString())
        params.set('target', `alias(${target},'')`)
        params.set('noCache', '1')

        const res = await fetch(`/graphite/render?${params.toString()}`)
        if (!res.ok) throw new Error(`Graphite returned ${res.status}`)
        const text = await res.text()
        if (!text.trim()) return null

        const parts = text.split('|')
        if (parts.length !== 2) return null
        const timeInfo = parts[0].split(',')
        const step = parseInt(timeInfo[3])
        const values = parts[1].split(',')

        const runDuration = Math.floor(new Date(run.endedAt).getTime() / 1000) - Math.floor(new Date(run.startedAt).getTime() / 1000)
        const data = values.map((v, j) => ({
          x: j * step,
          y: v === 'None' ? null : parseFloat(v)
        })).filter(d => d.x <= runDuration + step)

        return {
          label: buildSeriesLabel(run, refRun, i),
          color: COLORS[i % COLORS.length],
          isRef: i === 0,
          data
        }
      }))

      const valid = results.filter(Boolean)
      if (valid.length === 0) {
        setError('No data returned from graphite for any run')
      }
      setChartSeries(valid)
      setChartMode('graphite')
    } catch (e) {
      setError(`Graphite fetch failed: ${e.message}`)
    } finally {
      setLoading(false)
    }
  }

  const formatDate = (t) => t ? new Date(t).toLocaleString() : '-'
  const graphiteLabel = GRAPHITE_METRICS.find(m => m.value === metric)?.label || metric
  const scaleLabel = SCALE_METRICS.find(m => m.value === scaleMetric)?.label || scaleMetric

  const formatThreads = (run) => {
    if (isScaleRun(run)) {
      const pts = run.scaleResults
      return `${pts[0]?.threads}→${pts[pts.length - 1]?.threads}`
    }
    return run.threads
  }

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='xl'>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent} maxW='860px'>
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
                        <Th px={1}><Checkbox size='sm' isChecked={selected.size === runs.length} isIndeterminate={selected.size > 0 && selected.size < runs.length} onChange={toggleAll} /></Th>
                        <Th>#</Th>
                        <Th>Date</Th>
                        <Th>Test</Th>
                        <Th>Threads</Th>
                        <Th>Peak TPS</Th>
                        <Th>Avg Lat</Th>
                        <Th>DBU</Th>
                        <Th>TPS/DBU</Th>
                        <Th>Flavor</Th>
                      </Tr>
                    </Thead>
                    <Tbody>
                      {runs.map((run, i) => (
                        <Tr key={i} opacity={selected.has(i) ? 1 : 0.5} cursor='pointer' onClick={() => toggleSelect(i)}>
                          <Td px={1}><Checkbox size='sm' isChecked={selected.has(i)} onChange={() => toggleSelect(i)} /></Td>
                          <Td>{i + 1}</Td>
                          <Td>{formatDate(run.startedAt)}</Td>
                          <Td>
                            <HStack spacing={1}>
                              <Badge variant={badgeVariant} colorScheme='blue' size='sm'>{run.testType}</Badge>
                              {isScaleRun(run) && <Badge variant={badgeVariant} colorScheme='purple' size='sm'>scale</Badge>}
                            </HStack>
                          </Td>
                          <Td>{formatThreads(run)}</Td>
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

                {/* Scale chart: threads vs metric — works for scale runs (curves) + normal runs (points) */}
                <HStack spacing={3}>
                  <Select size='sm' value={scaleMetric} onChange={(e) => setScaleMetric(e.target.value)} flex={1}>
                    {SCALE_METRICS.map(m => (
                      <option key={m.value} value={m.value}>{m.label}</option>
                    ))}
                  </Select>
                  <RMButton size='small' colorScheme='purple' onClick={showScaleChart} isDisabled={selected.size === 0}>
                    Scale Graph ({selected.size})
                  </RMButton>
                </HStack>

                {/* Normal runs: graphite metric selector + show button */}
                <HStack spacing={3}>
                  <Select size='sm' value={metric} onChange={(e) => setMetric(e.target.value)} flex={1}>
                    {GRAPHITE_METRICS.map(m => (
                      <option key={m.value} value={m.value}>{m.label}</option>
                    ))}
                  </Select>
                  <RMButton size='small' colorScheme='blue' onClick={fetchGraphiteData} isDisabled={loading || selected.size === 0}>
                    {loading ? <Spinner size='xs' /> : `Graphite Graph (${selectedRuns.length})`}
                  </RMButton>
                </HStack>

                {error && (
                  <Text fontSize='sm' color='red.400' textAlign='center'>{error}</Text>
                )}

                {chartSeries.length > 0 && (
                  <Box borderRadius='md' overflow='hidden' bg={theme === 'light' ? 'gray.50' : 'rgba(255,255,255,0.05)'} p={2}>
                    <LineChart
                      series={chartSeries}
                      theme={theme}
                      xLabel={chartMode === 'scale' ? 'Threads' : 'Seconds (from benchmark start)'}
                      yLabel={chartMode === 'scale' ? scaleLabel : graphiteLabel}
                    />
                  </Box>
                )}
              </>
            )}

            {runs.length === 0 && (
              <Text fontSize='sm' color='gray.500' textAlign='center' py={4}>
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
