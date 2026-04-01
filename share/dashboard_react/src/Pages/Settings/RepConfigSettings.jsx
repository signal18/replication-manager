import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import RMSwitch from '../../components/RMSwitch'
import TextForm from '../../components/TextForm'
import NumberInput from '../../components/NumberInput'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'

function RepConfigSettings({ selectedCluster, user, openConfirmModal, closeConfirmModal }) {
  const dispatch = useDispatch()
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  const h = (content, title) => <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />
  const sw = (setting, configKey) => <RMSwitch confirmTitle={`Confirm switch settings for ${setting}?`} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.[configKey]} />

  const hChannel = `**Replication Channel**\n\nName of the replication channel for multi-source replication.\nLeave empty for the default (unnamed) channel.\n\nConfig: \`replication-source-name\``
  const hRetry = `**Master Retry Count**\n\nNumber of times the SQL thread retries a lost connection before giving up.\nDefault: 10. Set to 0 for unlimited retries.\n\nConfig: \`replication-master-retry-count\``
  const hReadOnly = `**Enforce Read Only on Replicas**\n\nSets \`read_only = ON\` on all replica servers.\nPrevents accidental writes to replicas.\n\nConfig: \`force-slave-readonly\``
  const hBinlogRow = `**Enforce Binlog Format ROW**\n\nForces \`binlog_format = ROW\` on all servers.\nRequired for flashback, GTID-based failover, and online schema changes.\n\nConfig: \`force-binlog-row\``
  const hAnnotate = `**Enforce Binlog Row Annotate**\n\nEnables MariaDB's \`binlog_annotate_row_events\`.\nAdds the original SQL statement as a comment before each row event in the binary log.\n\nConfig: \`force-binlog-annotate\``
  const hCompress = `**Enforce Binlog Compression**\n\nEnables binary log compression (MySQL 8.0.20+ / MariaDB 10.6+).\nReduces disk usage and replication network transfer at the cost of CPU.\n\nConfig: \`force-binlog-compress\``
  const hSlowQ = `**Enforce Replication Queries in Slow Query Log**\n\nLogs queries executed by the SQL replication thread to the slow query log.\nUseful for diagnosing slow replication caused by expensive statements.\n\nConfig: \`force-binlog-slowqueries\``
  const hGTID = `**Enforce GTID Replication**\n\nForces GTID mode on all servers.\nSimplifies failover and eliminates the need to track binlog file/position manually.\n\nConfig: \`force-slave-gtid-mode\``
  const hGTIDStrict = `**Enforce Replication Stop When Write on Replica**\n\nEnables \`gtid_strict_mode\` (MariaDB).\nThe SQL thread stops with an error if it encounters a GTID sequence gap, preventing silent data divergence.\n\nConfig: \`force-slave-gtid-mode-strict\``
  const hSemiSync = `**Enforce Semi-Synchronous Replication**\n\nConfigures semi-synchronous replication on all servers.\nThe master waits for at least one replica to acknowledge receipt before committing.\n\nConfig: \`force-slave-semisync\``
  const hStrict = `**Enforce Replication Mode Strict**\n\nThe SQL thread stops with an error when it encounters a row that cannot be found.\nPrevents silent data divergence.\n\nConfig: \`force-slave-strict\``
  const hIdempotent = `**Enforce Replication Mode Idempotent**\n\nThe SQL thread silently ignores missing rows and duplicate key errors.\nOnly enable temporarily during recovery.\n\nConfig: \`force-slave-idempotent\``
  const hSerialized = `**Enforce Replication Parallel Mode: Serialized**\n\nDisables parallel replication. All transactions applied sequentially.\nSafest mode, lowest throughput.\n\nConfig: \`force-slave-parallel-mode (serialized)\``
  const hMinimal = `**Enforce Replication Parallel Mode: Minimal**\n\nParallel only for guaranteed independent transactions.\nGood balance of safety and performance.\n\nConfig: \`force-slave-parallel-mode (minimal)\``
  const hConservative = `**Enforce Replication Parallel Mode: Conservative**\n\nParallel based on the master's binary log group commit order.\n\nConfig: \`force-slave-parallel-mode (conservative)\``
  const hOptimistic = `**Enforce Replication Parallel Mode: Optimistic**\n\nApplies transactions speculatively and rolls back conflicts.\nHighest OLTP throughput.\n\nConfig: \`force-slave-parallel-mode (optimistic)\``
  const hAggressive = `**Enforce Replication Parallel Mode: Aggressive**\n\nMaximum parallelism with minimal dependency checking.\nOnly for read-heavy replicas where occasional lag is acceptable.\n\nConfig: \`force-slave-parallel-mode (aggressive)\``
  const hHeartbeat = `**Enforce Replication Heartbeat**\n\nEnables heartbeat events so the master sends packets to replicas even during idle periods.\nPrevents SQL thread disconnect on long TCP timeouts.\n\nConfig: \`force-slave-heartbeat\``

  const dataObject = [
    { key: 'Replication Channel', help: h(hChannel, 'Replication Channel'), value: (<TextForm value={selectedCluster?.config?.replicationSourceName} confirmTitle={`Confirm staging replication-source-name to `} onSave={(v) => { dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'replication-source-name', value: v })) }} />) },
    { key: 'Master Retry Count', help: h(hRetry, 'Master Retry Count'), value: (<NumberInput min={0} max={Number.MAX_SAFE_INTEGER} inputWidth='100px' value={selectedCluster?.config?.replicationMasterRetryCount} showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'replication-master-retry-count' to: `} isDisabled={user?.grants['cluster-settings'] == false} onConfirm={(v) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'replication-master-retry-count', value: v }))} />) },
    { key: 'Enforce Read Only on Replicas', help: h(hReadOnly, 'Enforce Read Only on Replicas'), value: sw('force-slave-readonly', 'forceSlaveReadonly') },
    { key: 'Enforce Binlog Format ROW', help: h(hBinlogRow, 'Enforce Binlog Format ROW'), value: sw('force-binlog-row', 'forceBinlogRow') },
    { key: 'Enforce Binlog Row Annotate', help: h(hAnnotate, 'Enforce Binlog Row Annotate'), value: sw('force-binlog-annotate', 'forceBinlogAnnotate') },
    { key: 'Enforce Binlog Compression', help: h(hCompress, 'Enforce Binlog Compression'), value: sw('force-binlog-compress', 'forceBinlogCompress') },
    { key: 'Enforce Replication Queries in Slow Query Log', help: h(hSlowQ, 'Enforce Replication Queries in Slow Query Log'), value: sw('force-binlog-slowqueries', 'forceBinlogSlowqueries') },
    { key: 'Enforce GTID Replication', help: h(hGTID, 'Enforce GTID Replication'), value: sw('force-slave-gtid', 'forceSlaveGtidMode') },
    { key: 'Enforce Replication Stop When Write on Replica', help: h(hGTIDStrict, 'Enforce Replication Stop When Write on Replica'), value: sw('force-slave-gtid-mode-strict', 'forceSlaveGtidModeStrict') },
    { key: 'Enforce Semi-Synchronous Replication', help: h(hSemiSync, 'Enforce Semi-Synchronous Replication'), value: sw('force-slave-semisync', 'forceSlaveSemisync') },
    { key: 'Enforce Replication Mode Strict', help: h(hStrict, 'Enforce Replication Mode Strict'), value: sw('force-slave-strict', 'forceSlaveStrict') },
    { key: 'Enforce Replication Mode Idempotent', help: h(hIdempotent, 'Enforce Replication Mode Idempotent'), value: sw('force-slave-idempotent', 'forceSlaveIdempotent') },
    { key: 'Enforce Replication Parallel Mode: Serialized', help: h(hSerialized, 'Enforce Replication Parallel Mode: Serialized'), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-serialized?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-serialized' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'SERIALIZED'} />) },
    { key: 'Enforce Replication Parallel Mode: Minimal', help: h(hMinimal, 'Enforce Replication Parallel Mode: Minimal'), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-minimal?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-minimal' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'MINIMAL'} />) },
    { key: 'Enforce Replication Parallel Mode: Conservative', help: h(hConservative, 'Enforce Replication Parallel Mode: Conservative'), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-conservative?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-conservative' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'CONSERVATIVE'} />) },
    { key: 'Enforce Replication Parallel Mode: Optimistic', help: h(hOptimistic, 'Enforce Replication Parallel Mode: Optimistic'), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-optimistic?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-optimistic' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'OPTIMISTIC'} />) },
    { key: 'Enforce Replication Parallel Mode: Aggressive', help: h(hAggressive, 'Enforce Replication Parallel Mode: Aggressive'), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-aggressive?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-aggressive' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'AGGRESSIVE'} />) },
    { key: 'Enforce Replication Heartbeat', help: h(hHeartbeat, 'Enforce Replication Heartbeat'), value: sw('force-slave-heartbeat', 'forceSlaveHeartbeat') },
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.table} helpColumn={true} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default RepConfigSettings
