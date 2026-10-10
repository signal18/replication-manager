import React, { useState } from 'react'
import { Box, Flex, Text } from '@chakra-ui/react'
import { useDispatch, useSelector } from 'react-redux'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import TableType2 from '../../components/TableType2'
import TextForm from '../../components/TextForm'
import RMButton from '../../components/RMButton'
import RMIconButton from '../../components/RMIconButton'
import CommonModal from '../../components/Modals/CommonModal'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import { setGlobalSetting } from '../../redux/globalClustersSlice'
import { globalClustersService } from '../../services/globalClustersService'
import styles from '../ClustersGlobalSettings/styles.module.scss'

// DeclaredCapacity: the infrastructure capacity the ResourceManager computes with, set by
// hand (resource-manager-infra-*, resource-manager-smt-gain; 0 = what the agents report).
// The two measurements only PROPOSE a value, from sysbench on the host replication-manager
// runs on (an unlimited container: it measures the host), representative when every node
// has the same hardware; every field stays editable and overwrites a measured value.
const fields = [
  { key: 'Quota', setting: 'resource-manager-infra-quota-pct', conf: 'resourceManagerInfraQuotaPct', unit: '%', help: 'Share of the metal replication-manager may sell (protects non-repman workloads). The plan pot is capacity x quota.' },
  { key: 'CPU cores', setting: 'resource-manager-infra-cpu-cores', conf: 'resourceManagerInfraCpuCores', unit: 'cores', help: 'Physical cores of the whole infrastructure. 0 = summed from the agents (cores x SMT gain on an SMT node).' },
  { key: 'Memory', setting: 'resource-manager-infra-memory-mb', conf: 'resourceManagerInfraMemoryMb', unit: 'MB', help: 'Memory of the whole infrastructure. 0 = summed from the agents.' },
  { key: 'Disk', setting: 'resource-manager-infra-disk-gb', conf: 'resourceManagerInfraDiskGb', unit: 'GB', help: 'Disk of the whole infrastructure (the agents do not report it).' },
  { key: 'IOPS', setting: 'resource-manager-infra-iops', conf: 'resourceManagerInfraIops', unit: 'IOPS', help: 'Random IOPS of the whole infrastructure (the agents do not report it). **Measure IOPS** runs sysbench fileio (16 KiB random read/write, direct IO, 20 s) in the working directory and proposes host IOPS x agents.', measure: 'iops' },
  { key: 'Network', setting: 'resource-manager-infra-network-mbps', conf: 'resourceManagerInfraNetworkMbps', unit: 'Mb/s', help: 'Network of the whole infrastructure.' },
  { key: 'SMT gain', setting: 'resource-manager-smt-gain', conf: 'resourceManagerSmtGain', unit: 'x', help: 'Throughput of a physical core with all its threads busy, relative to one thread (e.g. 1.15). A DBU core is a real core: on a node with more threads than cores, the capacity counts cores x gain and a DBU core gets threads-per-core / gain logical CPUs of quota. 1 or less = SMT not accounted. **Measure SMT** runs sysbench cpu and random memory access, one thread per core then every thread (about a minute of full CPU load at the lowest priority), and writes the measured gain.', measure: 'smt' }
]

function DeclaredCapacity() {
  const dispatch = useDispatch()
  const config = useSelector((state) => state.globalClusters?.monitor?.config)
  const [info, setInfo] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [running, setRunning] = useState({})
  const [result, setResult] = useState({})

  const help = (f) => (
    <RMIconButton icon={HiQuestionMarkCircle} variant='ghost' iconFontsize='1rem' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }}
      onClick={() => setInfo({ title: f.key, body: `**${f.key}**\n\n${f.help}\n\nThe value is yours: a measurement only proposes one, and any value typed here overwrites it.\n\nConfig: \`${f.setting}\`` })} />
  )

  const measure = (kind, apply) => {
    setRunning((r) => ({ ...r, [kind]: true }))
    setResult((r) => ({ ...r, [kind]: '' }))
    const call = kind === 'smt' ? globalClustersService.calibrateSmtGain(apply) : globalClustersService.calibrateIops(apply)
    call
      .then(({ data }) => setResult((r) => ({ ...r, [kind]: { text: `${data?.message || ''}${data?.applied ? ' — applied' : ''}`, data } })))
      .catch((e) => setResult((r) => ({ ...r, [kind]: { text: `Failed: ${e?.response?.data || e?.message || e}` } })))
      .finally(() => setRunning((r) => ({ ...r, [kind]: false })))
  }

  const dataObject = fields.map((f) => ({
    key: f.key,
    help: help(f),
    value: (
      <Flex align='center' gap={2} wrap='wrap'>
        <TextForm value={config?.[f.conf]} confirmTitle={`Confirm ${f.key} (${f.unit}) to `} onSave={(value) => dispatch(setGlobalSetting({ setting: f.setting, value }))} />
        <Text fontSize='sm' opacity={0.7}>{f.unit}</Text>
        {f.measure && (
          <RMButton isLoading={running[f.measure]} loadingText='Measuring…' onClick={() => setConfirm(f.measure)}>
            {f.measure === 'smt' ? 'Measure SMT' : 'Measure IOPS'}
          </RMButton>
        )}
        {f.measure === 'iops' && result.iops?.data?.infraIops > 0 && !result.iops?.data?.applied && (
          <RMButton onClick={() => dispatch(setGlobalSetting({ setting: f.setting, value: String(result.iops.data.infraIops) }))}>
            Apply {Number(result.iops.data.infraIops).toLocaleString()}
          </RMButton>
        )}
        {f.measure && result[f.measure]?.text && <Box fontSize='sm'>{result[f.measure].text}</Box>}
      </Flex>
    )
  }))

  return (
    <Box mb={4}>
      <Text fontSize='md' fontWeight='bold' mb={1}>Declared capacity</Text>
      <Text fontSize='sm' opacity={0.7} mb={2}>
        Set by hand, 0 = what the agents report. The measurements run sysbench on the replication-manager host and only propose a value: every field can be overwritten.
      </Text>
      <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn />
      {info && (
        <CommonModal isOpen closeModal={() => setInfo(null)} title={info.title} size='xl'
          body={<Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{info.body}</Markdown></Box>} />
      )}
      {confirm && (
        <ConfirmModal isOpen closeModal={() => setConfirm(null)}
          title={confirm === 'smt'
            ? 'Measure the SMT gain with sysbench? About a minute of full CPU load on the replication-manager host, at the lowest priority; the gain is written.'
            : 'Measure the IOPS with sysbench fileio? 2 GB of test files in the working directory for about 30 s, removed after; the result is proposed, applied only when you click Apply.'}
          onConfirmClick={() => { measure(confirm, confirm === 'smt'); setConfirm(null) }} />
      )}
    </Box>
  )
}

export default DeclaredCapacity
