import React, { useEffect, useState } from 'react'
import { Box, Flex, Text, Table, Thead, Tbody, Tr, Th, Td, Progress, Badge } from '@chakra-ui/react'
import { globalClustersService } from '../../services/globalClustersService'

const CLUSTER_COLORS = ['#3f8fd0', '#8b5cf6', '#e0603a', '#37a06f', '#d99a2b', '#5aa8e6', '#a98bff', '#ef7a54', '#4dc088', '#eabb52']
const colorFor = (i) => CLUSTER_COLORS[i % CLUSTER_COLORS.length]

// ResourceManager is the GLOBAL (infra-wide) ResourceManager view: per-axis capacity
// (from resource-manager-infra-* overrides, else summed from the physical agents) vs
// the infra consumed DBU. This is the claim's first gate -- "is there room?". Reads
// GET /api/global/resources (grant-checked server-side).
function ResourceManager() {
  const [data, setData] = useState(null)
  const [err, setErr] = useState(null)

  useEffect(() => {
    let alive = true
    const load = async () => {
      try {
        const res = await globalClustersService.getGlobalResources()
        if (alive) {
          setData(res.data)
          setErr(null)
        }
      } catch (e) {
        if (alive) setErr(e?.response?.status === 403 ? 'Requires global admin grant' : 'Failed to load /api/global/resources')
      }
    }
    load()
    const id = setInterval(load, 10000)
    return () => {
      alive = false
      clearInterval(id)
    }
  }, [])

  const fmt = (n) => (n == null ? '—' : Number(n).toLocaleString(undefined, { maximumFractionDigits: 2 }))

  if (err) return <Box p={4}><Text color='var(--text-color)'>ResourceManager: {err}</Text></Box>
  if (!data) return <Box p={4}><Text color='var(--text-color)'>Loading…</Text></Box>

  const pct = data.usableDbu > 0 ? Math.min(100, (data.consumedDbu / data.usableDbu) * 100) : 0

  return (
    <Box p={4} color='var(--text-color)'>
      <Text fontSize='lg' fontWeight='bold' mb={1}>Infra ResourceManager — capacity vs consumed</Text>
      <Text fontSize='sm' opacity={0.7} mb={4}>
        {data.agents} agent(s) · quota {fmt(data.quotaPct)}% · binding axis:{' '}
        <Text as='span' fontWeight='bold' textTransform='uppercase'>{data.bindingAxis || '—'}</Text>
        {' '}(the scarcest axis caps the whole infra)
      </Text>

      <Flex gap={8} wrap='wrap' mb={4}>
        <Stat label='Capacity (DBU)' value={fmt(data.capacityDbu)} />
        <Stat label='Usable (× quota)' value={fmt(data.usableDbu)} />
        <Stat label='Consumed (DBU)' value={fmt(data.consumedDbu)} />
        <Stat label='Slack / room' value={fmt(data.slackDbu)} highlight={data.slackDbu <= 0} />
      </Flex>

      <Box mb={6} maxW='560px'>
        <Flex justify='space-between' mb={1}>
          <Text fontSize='xs' opacity={0.7}>Usable filled — the claim's first gate</Text>
          <Text fontSize='xs' opacity={0.7}>{fmt(pct)}%</Text>
        </Flex>
        <Progress value={pct} size='sm' borderRadius='full' colorScheme={pct > 90 ? 'red' : pct > 70 ? 'orange' : 'green'} />
        {data.slackDbu <= 0 && (
          <Text fontSize='xs' color='var(--danger-color, #e0603a)' mt={1}>No room — a claim would be refused at capacity level.</Text>
        )}
      </Box>

      <Box overflowX='auto'>
        <Table size='sm' variant='simple'>
          <Thead>
            <Tr>
              <Th>Axis</Th>
              <Th isNumeric>Capacity</Th>
              <Th>Unit</Th>
              <Th>Source</Th>
              <Th isNumeric>Capacity (DBU)</Th>
              <Th isNumeric>Consumed (DBU)</Th>
            </Tr>
          </Thead>
          <Tbody>
            {(data.axes || []).map((a) => (
              <Tr key={a.axis}>
                <Td textTransform='uppercase' fontWeight='semibold'>{a.axis}</Td>
                <Td isNumeric>{fmt(a.capacityRaw)}</Td>
                <Td>{a.unit}</Td>
                <Td>
                  <Badge colorScheme={a.source === 'agents' ? 'green' : 'blue'}>{a.source}</Badge>
                </Td>
                <Td isNumeric>{a.capacityDbu ? fmt(a.capacityDbu) : '—'}</Td>
                <Td isNumeric>{a.consumedDbu ? fmt(a.consumedDbu) : '—'}</Td>
              </Tr>
            ))}
          </Tbody>
        </Table>
      </Box>

      {data.clusters && data.clusters.length > 0 && (() => {
        const sumReal = data.clusters.reduce((s, c) => s + (c.dbu || 0), 0)
        const sumPlan = data.clusters.reduce((s, c) => s + (c.planDbu || 0), 0)
        const scale = Math.max(data.usableDbu || 0, sumPlan, sumReal, 0.0001)
        const markerPct = Math.min(100, (data.usableDbu / scale) * 100)
        const StackBar = ({ title, k, sum }) => (
          <Box mb={3}>
            <Flex justify='space-between' mb={1}>
              <Text fontSize='sm' fontWeight='semibold'>{title}</Text>
              <Text fontSize='xs' opacity={0.7}>Σ {fmt(sum)} / usable {fmt(data.usableDbu)} DBU</Text>
            </Flex>
            <Box position='relative' h='24px' borderRadius='md' overflow='hidden' style={{ background: 'var(--panel2, #edeff5)' }}>
              <Flex h='100%'>
                {data.clusters.map((c, i) => {
                  const w = (c[k] / scale) * 100
                  return w > 0.05 ? (
                    <Box key={c.cluster} h='100%' w={`${w}%`} style={{ background: colorFor(i) }} title={`${c.cluster}: ${fmt(c[k])} DBU`} />
                  ) : null
                })}
              </Flex>
              {markerPct > 0 && markerPct < 100 && (
                <Box position='absolute' top='-3px' bottom='-3px' left={`${markerPct}%`} style={{ borderLeft: '2px dashed var(--text-color)' }} />
              )}
            </Box>
          </Box>
        )
        return (
          <Box mt={6}>
            <Text fontSize='md' fontWeight='bold' mb={2}>Par cluster <Text as='span' fontSize='xs' opacity={0.6}>(┊ = usable {fmt(data.usableDbu)} DBU · au-delà = sur-réservé)</Text></Text>
            <StackBar title='Réel (consommé)' k='dbu' sum={sumReal} />
            <StackBar title='Plan (réservé)' k='planDbu' sum={sumPlan} />
            <Flex gap={4} wrap='wrap' mt={2}>
              {data.clusters.map((c, i) => (
                <Flex key={c.cluster} align='center' gap={1}>
                  <Box w='11px' h='11px' borderRadius='2px' style={{ background: colorFor(i) }} />
                  <Text fontSize='xs'>{c.cluster} <Text as='span' opacity={0.6}>· réel {fmt(c.dbu)} / plan {fmt(c.planDbu)}</Text></Text>
                </Flex>
              ))}
            </Flex>
          </Box>
        )
      })()}

      <Text fontSize='xs' opacity={0.6} mt={3}>
        Source “agents” = summed from the physical agents (cpu/mem); “config” = a
        resource-manager-infra-* override. disk/iops/network have no per-agent source yet,
        so they show only when overridden.
      </Text>
    </Box>
  )
}

function Stat({ label, value, highlight }) {
  return (
    <Box>
      <Text fontSize='xs' opacity={0.7}>{label}</Text>
      <Text fontSize='xl' fontWeight='bold' color={highlight ? 'var(--danger-color, #e0603a)' : undefined}>{value}</Text>
    </Box>
  )
}

export default ResourceManager
