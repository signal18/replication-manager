import React, { useEffect, useState } from 'react'
import { Box, Flex, Text, Table, Thead, Tbody, Tr, Th, Td, Progress, Badge } from '@chakra-ui/react'
import { globalClustersService } from '../../services/globalClustersService'
import ChartBarStack from '../../components/ChartBarStack'

const CLUSTER_COLORS = ['#3f8fd0', '#8b5cf6', '#e0603a', '#37a06f', '#d99a2b', '#5aa8e6', '#a98bff', '#ef7a54', '#4dc088', '#eabb52']
const colorFor = (i) => CLUSTER_COLORS[i % CLUSTER_COLORS.length]
// ChartBarStack colours its layers with d3.schemeCategory10 in metricPaths order, so the
// over-time legend below maps cluster i -> SCHEME10[i] to match.
const SCHEME10 = ['#1f77b4', '#ff7f0e', '#2ca02c', '#d62728', '#9467bd', '#8c564b', '#e377c2', '#7f7f7f', '#bcbd22', '#17becf']
// carbonHost mirrors the DB metric-host token: cluster name uppercased, '.'->'-'. The
// mysql.<HOST> series and the emitted resourcemanager.<CLUSTER> series both use it.
const carbonHost = (h) => (h || '').toUpperCase().replace(/[`?()'"<]/g, '-').replace(/\./g, '-').replace(/[ /]/g, '_')

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

  // Cubism context for the over-time ChartBarStack graphs (mirrors the Graphs page).
  const [ctx, setCtx] = useState(null)
  useEffect(() => {
    if (typeof window === 'undefined' || !window.cubism) return
    const c = window.cubism.context().serverDelay(0).clientDelay(0).step(10000).size(360)
    c.start()
    setCtx(c)
    return () => {
      try { c.stop() } catch (e) { /* noop */ }
      setCtx(null)
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

      <Text fontSize='sm' opacity={0.7} mb={1}>
        Compute (APU) — the same metal, 1c/1GB/10GB (no IO) · binding axis:{' '}
        <Text as='span' fontWeight='bold' textTransform='uppercase'>{data.bindingAxisApu || '—'}</Text>
      </Text>
      <Flex gap={8} wrap='wrap' mb={4}>
        <Stat label='Capacity (APU)' value={fmt(data.capacityApu)} />
        <Stat label='Usable APU (× quota)' value={fmt(data.usableApu)} />
        <Stat label='Consumed (APU)' value={fmt(data.consumedApu)} />
        <Stat label='Slack APU / room' value={fmt(data.slackApu)} highlight={data.slackApu <= 0} />
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

      {data.ledger?.known && (() => {
        const L = data.ledger
        const gb = (b) => fmt((b || 0) / 1024 / 1024 / 1024)
        const mb = (b) => fmt((b || 0) / 1024 / 1024)
        const rows = [
          { axis: 'cpu', unit: 'cores', f: (x) => fmt(x?.cores) },
          { axis: 'mem', unit: 'MB', f: (x) => mb(x?.memBytes) },
          { axis: 'io', unit: 'iops', f: (x) => fmt(x?.iops) },
          { axis: 'disk (NVMe)', unit: 'GB', f: (x) => gb(x?.diskBytes) },
        ]
        const neg = (v) => (v < 0 ? { color: 'var(--danger-color, #e0603a)', fontWeight: 600 } : {})
        return (
          <Box mt={6}>
            <Text fontSize='md' fontWeight='bold' mb={1}>Physical ledger — one metal, two pots</Text>
            <Text fontSize='xs' opacity={0.6} mb={2}>
              DBU, APU and BKU share the same metal. Plan pot = capacity × {fmt(L.quotaPct)} % quota − every plan sold (a plan is a guarantee: only other plans bind it). Over-commit pot = capacity − every plan − everything already borrowed (a loan above a plan, never a sale). Precedence: a plan increase always wins over borrowed resources; when the over-commit pot goes negative the borrowed part must give way (GWARN017).
            </Text>
            <Box overflowX='auto'>
              <Table size='sm' variant='simple'>
                <Thead><Tr><Th>Axis</Th><Th isNumeric>Capacity</Th><Th isNumeric>Sellable (quota)</Th><Th isNumeric>Reserved (plans)</Th><Th isNumeric>Borrowed</Th><Th isNumeric>Plan pot</Th><Th isNumeric>Over-commit pot</Th><Th>Unit</Th></Tr></Thead>
                <Tbody>
                  {rows.map((r) => (
                    <Tr key={r.axis}>
                      <Td textTransform='uppercase' fontWeight='semibold'>{r.axis}</Td>
                      <Td isNumeric>{r.f(L.capacity)}</Td>
                      <Td isNumeric>{r.f(L.sellable)}</Td>
                      <Td isNumeric>{r.f(L.reserved)}</Td>
                      <Td isNumeric>{r.f(L.borrowed)}</Td>
                      <Td isNumeric style={neg(L.planPot?.[r.axis === 'cpu' ? 'cores' : r.axis === 'mem' ? 'memBytes' : r.axis === 'io' ? 'iops' : 'diskBytes'])}>{r.f(L.planPot)}</Td>
                      <Td isNumeric style={neg(L.overCommitPot?.[r.axis === 'cpu' ? 'cores' : r.axis === 'mem' ? 'memBytes' : r.axis === 'io' ? 'iops' : 'diskBytes'])}>{r.f(L.overCommitPot)}</Td>
                      <Td>{r.unit}</Td>
                    </Tr>
                  ))}
                </Tbody>
              </Table>
            </Box>
            <Flex gap={6} wrap='wrap' mt={2}>
              <Text fontSize='sm'>Reserved: <b>{fmt(L.reservedUnits?.dbu)} DBU</b> · <b>{fmt(L.reservedUnits?.apu)} APU</b> · <b>{fmt(L.reservedUnits?.bku)} BKU</b></Text>
              <Text fontSize='sm'>Free to sell (plan pot): <b style={neg(L.planPotUnits?.dbu)}>{fmt(L.planPotUnits?.dbu)} DBU</b> · <b style={neg(L.planPotUnits?.apu)}>{fmt(L.planPotUnits?.apu)} APU</b> · <b style={neg(L.planPotUnits?.bku)}>{fmt(L.planPotUnits?.bku)} BKU</b></Text>
              <Text fontSize='sm'>Free to borrow (over-commit pot): <b style={neg(L.borrowPot?.dbu)}>{fmt(L.borrowPot?.dbu)} DBU</b> · <b style={neg(L.borrowPot?.apu)}>{fmt(L.borrowPot?.apu)} APU</b> · <b style={neg(L.borrowPot?.bku)}>{fmt(L.borrowPot?.bku)} BKU</b></Text>
            </Flex>
            {L.overdrawn && (
              <Text fontSize='xs' color='var(--danger-color, #e0603a)' mt={1}>Overdrawn: borrowed resources exceed the unreserved capacity. Plans take precedence, the borrowed part must give way; no further borrow is admitted.</Text>
            )}
          </Box>
        )
      })()}

      {data.clusters && data.clusters.length > 0 && (() => {
        const sumReal = data.clusters.reduce((s, c) => s + (c.dbu || 0), 0)
        const sumPlan = data.clusters.reduce((s, c) => s + (c.planDbu || 0), 0)
        const sumRealApu = data.clusters.reduce((s, c) => s + (c.apu || 0), 0)
        const sumPlanApu = data.clusters.reduce((s, c) => s + (c.planApu || 0), 0)
        const StackBar = ({ title, k, sum, usable, unit }) => {
          const scale = Math.max(usable || 0, sum, ...data.clusters.map((c) => c[k] || 0), 0.0001)
          const markerPct = Math.min(100, (usable / scale) * 100)
          return (
            <Box mb={3}>
              <Flex justify='space-between' mb={1}>
                <Text fontSize='sm' fontWeight='semibold'>{title}</Text>
                <Text fontSize='xs' opacity={0.7}>Σ {fmt(sum)} / usable {fmt(usable)} {unit}</Text>
              </Flex>
              <Box position='relative' h='24px' borderRadius='md' overflow='hidden' style={{ background: 'var(--panel2, #edeff5)' }}>
                <Flex h='100%'>
                  {data.clusters.map((c, i) => {
                    const w = (c[k] / scale) * 100
                    return w > 0.05 ? (
                      <Box key={c.cluster} h='100%' w={`${w}%`} style={{ background: colorFor(i) }} title={`${c.cluster}: ${fmt(c[k])} ${unit}`} />
                    ) : null
                  })}
                </Flex>
                {markerPct > 0 && markerPct < 100 && (
                  <Box position='absolute' top='-3px' bottom='-3px' left={`${markerPct}%`} style={{ borderLeft: '2px dashed var(--text-color)' }} />
                )}
              </Box>
            </Box>
          )
        }
        return (
          <Box mt={6}>
            <Text fontSize='md' fontWeight='bold' mb={2}>Per cluster — DBU <Text as='span' fontSize='xs' opacity={0.6}>(┊ = usable {fmt(data.usableDbu)} DBU · beyond = over-reserved)</Text></Text>
            <StackBar title='Real (consumed)' k='dbu' sum={sumReal} usable={data.usableDbu} unit='DBU' />
            <StackBar title='Plan (reserved)' k='planDbu' sum={sumPlan} usable={data.usableDbu} unit='DBU' />
            <Text fontSize='md' fontWeight='bold' mb={2} mt={4}>Per cluster — APU <Text as='span' fontSize='xs' opacity={0.6}>(┊ = usable {fmt(data.usableApu)} APU · beyond = over-reserved)</Text></Text>
            <StackBar title='Real (consumed)' k='apu' sum={sumRealApu} usable={data.usableApu} unit='APU' />
            <StackBar title='Plan (reserved)' k='planApu' sum={sumPlanApu} usable={data.usableApu} unit='APU' />
            <Flex gap={4} wrap='wrap' mt={2}>
              {data.clusters.map((c, i) => (
                <Flex key={c.cluster} align='center' gap={1}>
                  <Box w='11px' h='11px' borderRadius='2px' style={{ background: colorFor(i) }} />
                  <Text fontSize='xs'>{c.cluster} <Text as='span' opacity={0.6}>· real {fmt(c.dbu)} / plan {fmt(c.planDbu)}</Text></Text>
                </Flex>
              ))}
            </Flex>
          </Box>
        )
      })()}

      {ctx && data.clusters && data.clusters.length > 0 && (
        <Box mt={6}>
          <Text fontSize='md' fontWeight='bold' mb={1}>Historique per cluster</Text>
          <Text fontSize='xs' opacity={0.6} mb={2}>
            Consumed = sumSeries(dbu.&lt;cluster&gt;.*.dbu) per cluster (server series, summed at query) · Plan = resourcemanager.&lt;cluster&gt;.plan_dbu (emitted, historised to trace +1/−1 DBU)
          </Text>
          <ChartBarStack
            context={ctx}
            height={200}
            title='Consumed DBU (per cluster)'
            minYMax={data.usableDbu}
            ceilingLabel={`usable ${fmt(data.usableDbu)} DBU`}
            metricPaths={data.clusters.map((c) => `sumSeries(dbu.${c.cluster}.*.dbu)`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <ChartBarStack
            context={ctx}
            height={200}
            title='Plan DBU (per cluster)'
            metricPaths={data.clusters.map((c) => `resourcemanager.${carbonHost(c.cluster)}.plan_dbu`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <Text fontSize='xs' opacity={0.6} mt={4} mb={1}>
            Overcommit (over-use) = max(0, consumed − plan) · Undercommit (under-use / giveback) = max(0, plan − consumed). Derived at query from consumed &amp; plan (nothing emitted).
          </Text>
          <ChartBarStack
            context={ctx}
            height={200}
            title='Overcommit DBU (over-use: consumed > plan, per cluster)'
            metricPaths={data.clusters.map((c) => `removeBelowValue(diffSeries(sumSeries(dbu.${c.cluster}.*.dbu),resourcemanager.${carbonHost(c.cluster)}.plan_dbu),0)`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <ChartBarStack
            context={ctx}
            height={200}
            title='Undercommit DBU (under-use / giveback: plan > consumed, per cluster)'
            metricPaths={data.clusters.map((c) => `removeBelowValue(diffSeries(resourcemanager.${carbonHost(c.cluster)}.plan_dbu,sumSeries(dbu.${c.cluster}.*.dbu)),0)`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <Text fontSize='md' fontWeight='bold' mt={6} mb={1}>Compute (APU) — proxies + apps</Text>
          <Text fontSize='xs' opacity={0.6} mb={1}>
            Consumed = sumSeries(apu.&lt;cluster&gt;.*.apu) per cluster · Plan = resourcemanager.&lt;cluster&gt;.plan_apu (Σ of proxy + app deployment plans)
          </Text>
          <ChartBarStack
            context={ctx}
            height={200}
            title='Consumed APU (per cluster)'
            minYMax={data.usableApu}
            ceilingLabel={`usable ${fmt(data.usableApu)} APU`}
            metricPaths={data.clusters.map((c) => `sumSeries(apu.${c.cluster}.*.apu)`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <ChartBarStack
            context={ctx}
            height={200}
            title='Plan APU (per cluster)'
            metricPaths={data.clusters.map((c) => `resourcemanager.${carbonHost(c.cluster)}.plan_apu`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <ChartBarStack
            context={ctx}
            height={200}
            title='Overcommit APU (over-use: consumed > plan, per cluster)'
            metricPaths={data.clusters.map((c) => `removeBelowValue(diffSeries(sumSeries(apu.${c.cluster}.*.apu),resourcemanager.${carbonHost(c.cluster)}.plan_apu),0)`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <ChartBarStack
            context={ctx}
            height={200}
            title='Undercommit APU (under-use / giveback: plan > consumed, per cluster)'
            metricPaths={data.clusters.map((c) => `removeBelowValue(diffSeries(resourcemanager.${carbonHost(c.cluster)}.plan_apu,sumSeries(apu.${c.cluster}.*.apu)),0)`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <Text fontSize='md' fontWeight='bold' mt={6} mb={1}>Storage (BKU) — local backups + app disk on the NVMe pool</Text>
          <Text fontSize='xs' opacity={0.6} mb={1}>
            Consumed = bku.&lt;cluster&gt;.local + bku.&lt;cluster&gt;.app_disk (20 GB units; app disk = declared prov-app-disk-size × copies, per app) · Plan = resourcemanager.&lt;cluster&gt;.plan_bku (prov-db-bku)
          </Text>
          <ChartBarStack
            context={ctx}
            height={200}
            title='Consumed BKU (per cluster)'
            metricPaths={data.clusters.map((c) => `sumSeries(bku.${c.cluster}.local,bku.${c.cluster}.app_disk)`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <ChartBarStack
            context={ctx}
            height={200}
            title='Plan BKU (per cluster)'
            metricPaths={data.clusters.map((c) => `resourcemanager.${carbonHost(c.cluster)}.plan_bku`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <ChartBarStack
            context={ctx}
            height={200}
            title='Overcommit BKU (consumed > plan, per cluster; billed with the surcharge, never blocked)'
            metricPaths={data.clusters.map((c) => `removeBelowValue(diffSeries(sumSeries(bku.${c.cluster}.local,bku.${c.cluster}.app_disk),resourcemanager.${carbonHost(c.cluster)}.plan_bku),0)`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <ChartBarStack
            context={ctx}
            height={200}
            title='Undercommit BKU (plan > consumed, per cluster; billed with the reduction)'
            metricPaths={data.clusters.map((c) => `removeBelowValue(diffSeries(resourcemanager.${carbonHost(c.cluster)}.plan_bku,sumSeries(bku.${c.cluster}.local,bku.${c.cluster}.app_disk)),0)`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <Text fontSize='md' fontWeight='bold' mt={6} mb={1}>Archive (BAU) — remote archive, consumer + producer, no plan</Text>
          <Text fontSize='xs' opacity={0.6} mb={1}>
            Consumer = bau.&lt;cluster&gt;.units (what the cluster holds on S3/SFTP) · Producer = bau.&lt;cluster&gt;.producer (declared volumes of its S3-provider apps) · Billed = bau.&lt;cluster&gt;.billed, on usage
          </Text>
          <ChartBarStack
            context={ctx}
            height={200}
            title='Consumer BAU (per cluster)'
            metricPaths={data.clusters.map((c) => `bau.${c.cluster}.units`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <ChartBarStack
            context={ctx}
            height={200}
            title='Producer BAU (per cluster)'
            metricPaths={data.clusters.map((c) => `bau.${c.cluster}.producer`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <ChartBarStack
            context={ctx}
            height={200}
            title='Billed BAU (consumer + producer, rounded up, per cluster)'
            metricPaths={data.clusters.map((c) => `bau.${c.cluster}.billed`)}
            metricLabels={data.clusters.map((c) => c.cluster)}
          />
          <Flex gap={4} wrap='wrap' mt={2}>
            {data.clusters.map((c, i) => (
              <Flex key={c.cluster} align='center' gap={1}>
                <Box w='11px' h='11px' borderRadius='2px' style={{ background: SCHEME10[i % SCHEME10.length] }} />
                <Text fontSize='xs'>{c.cluster}</Text>
              </Flex>
            ))}
          </Flex>
        </Box>
      )}

      {ctx && data.agentList && data.agentList.length > 0 && (
        <Box mt={8}>
          <Text fontSize='md' fontWeight='bold' mb={1}>Per agent — physical load (DBU + APU stacked, over time)</Text>
          <Text fontSize='xs' opacity={0.6} mb={2}>
            Each agent's consumed DBU (databases) + APU (proxies/apps) stacked on the same metal
            (resourcemanager.agent.&lt;agent&gt;.dbu/apu), capped at the agent's total capacity — 1 DBU = 1 APU = 1 core,
            so the stack sits under the node's cores. This is the physical per-node view (pool pressure / the reclaim basis).
          </Text>
          <Flex gap={4} mb={2}>
            <Flex align='center' gap={1}><Box w='11px' h='11px' borderRadius='2px' style={{ background: SCHEME10[0] }} /><Text fontSize='xs'>DBU</Text></Flex>
            <Flex align='center' gap={1}><Box w='11px' h='11px' borderRadius='2px' style={{ background: SCHEME10[1] }} /><Text fontSize='xs'>APU</Text></Flex>
          </Flex>
          {data.agentList.map((ag) => (
            <Box key={ag.token} mb={3}>
              <Text fontSize='sm' fontWeight='semibold' mb={1}>{ag.name} <Text as='span' fontSize='xs' opacity={0.6}>· capacity {fmt(ag.cores)} cores</Text></Text>
              <ChartBarStack
                context={ctx}
                height={320}
                title={`${ag.name} — DBU + APU vs ${fmt(ag.cores)} cores`}
                minYMax={ag.cores}
                ceilingLabel={`${fmt(ag.cores)} cores`}
                metricPaths={[
                  `resourcemanager.agent.${ag.token}.dbu`,
                  `resourcemanager.agent.${ag.token}.apu`
                ]}
              />
            </Box>
          ))}
        </Box>
      )}

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
