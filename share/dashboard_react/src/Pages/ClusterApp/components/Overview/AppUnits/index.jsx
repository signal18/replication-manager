import { useMemo } from 'react'
import styles from '../styles.module.scss'
import { Text, VStack } from '@chakra-ui/react'
import { useSelector } from 'react-redux'
import TableType2 from '../../../../../components/TableType2'
import PropTypes from 'prop-types'
import { getUnitRatios } from '../../../../../utility/unitRatios'
import { convertSize } from '../../../../../utility/common'

// Infra Resources of one app: everything here is DERIVED from the declared shape
// (prov-app-cpu-cores / memory / disk) and the topology, at the manager's ratio of the
// app's profile (APU, or DBU when app-stateful). Nothing is stored per app: the history
// is the graphite series apu.<cluster>.<app>.plan_apu / plan_dbu next to the consumed
// series (Stéphane 2026-09-29: the unit count is tracked, never a field).
function AppUnits({ config, appConfig }) {
  const clusterData = useSelector((state) => state.cluster?.clusterData)
  const ratios = getUnitRatios(clusterData)
  const stateful = !!appConfig?.appStateful
  const ratio = stateful ? ratios.database : ratios.compute
  const unitName = stateful ? 'DBU' : 'APU'
  const baseCore = ratio.coresPerUnit || 1
  const baseMem = ratio.memMBPerUnit || 1
  const baseDisk = ratio.diskGBPerUnit || 1

  const cores = parseInt(appConfig?.provAppCpuCores) || 0
  const memMB = parseFloat(convertSize(appConfig?.provAppMemory, 'M', 'M')) || 0
  const diskGB = parseFloat(convertSize(appConfig?.provAppDiskSize, 'G', 'G')) || 0

  const unitsPerInstance = useMemo(() => {
    if (!cores && !memMB && !diskGB) return 1
    return Math.max(1, Math.ceil(Math.max(cores / baseCore, memMB / baseMem, diskGB / baseDisk)))
  }, [cores, memMB, diskGB, baseCore, baseMem, baseDisk])

  const bindingAxis = useMemo(() => {
    const c = cores / baseCore, m = memMB / baseMem, d = diskGB / baseDisk
    if (m >= c && m >= d) return 'memory'
    if (d >= c && d >= m) return 'disk'
    return 'cpu'
  }, [cores, memMB, diskGB, baseCore, baseMem, baseDisk])

  const topology = appConfig?.provAppHaTopology || config?.provAppHaTopology || 'failover'
  const agentCount = useMemo(() => {
    const raw = appConfig?.provAppAgents || ''
    const list = typeof raw === 'string' ? raw.split(',').filter((a) => a.trim()) : (Array.isArray(raw) ? raw.filter(Boolean) : [])
    return list.length || 1
  }, [appConfig?.provAppAgents])
  const instances = topology === 'flex' ? agentCount : 1

  const dataObject = useMemo(() => [
    { key: 'Profile', value: (<Text>{stateful ? 'Stateful (Database ratio, DBU)' : 'Compute (APU)'} — 1 {unitName} = {baseCore} core, {baseMem >= 1024 ? `${baseMem / 1024} GB` : `${baseMem} MB`}, {baseDisk} GB</Text>) },
    { key: 'Declared shape (per instance)', value: (<Text>{cores} cores, {memMB} MB, {diskGB} GB</Text>) },
    { key: `Units per instance`, value: (<Text>{unitsPerInstance} {unitName} (binds on {bindingAxis})</Text>) },
    { key: 'Instances', value: (<Text>{instances} ({topology === 'flex' ? `flex: one per agent, ${agentCount} agent${agentCount !== 1 ? 's' : ''}` : 'failover: one, the other agents hold a copy of the volume'})</Text>) },
    { key: 'Planned units', value: (<Text>{unitsPerInstance * instances} {unitName} — series apu.&lt;cluster&gt;.&lt;app&gt;.{stateful ? 'plan_dbu' : 'plan_apu'}</Text>) },
  ], [stateful, unitName, baseCore, baseMem, baseDisk, cores, memMB, diskGB, unitsPerInstance, bindingAxis, instances, topology, agentCount])

  return (
    <VStack>
      <TableType2 dataArray={dataObject} className={styles.table} />
    </VStack>
  )
}

export default AppUnits

AppUnits.propTypes = {
  config: PropTypes.shape({
    provAppHaTopology: PropTypes.string,
  }),
  appConfig: PropTypes.shape({
    appStateful: PropTypes.bool,
    provAppAgents: PropTypes.oneOfType([PropTypes.array, PropTypes.string]),
    provAppHaTopology: PropTypes.string,
    provAppCpuCores: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
    provAppMemory: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
    provAppDiskSize: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  }),
}
