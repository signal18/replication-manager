import { Box, Flex, HStack, Text } from '@chakra-ui/react'
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

  const helpKey = (label, content) => (
    <HStack spacing={1} align="center" width="fit-content">
      <Text>{label}</Text>
      <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(label, content)} />
    </HStack>
  )

  const helpChannel = `**Replication Channel**\n\nName of the replication channel used for multi-source replication.\nLeave empty for the default (unnamed) channel.\nMust match the channel name configured on the replica with \`CHANGE MASTER TO ... FOR CHANNEL 'name'\`.`
  const helpRetry = `**Master Retry Count**\n\nNumber of times the SQL thread retries a lost connection to the master before giving up.\nDefault: 10. Set to 0 for unlimited retries.\nIncrease for unreliable network links.`
  const helpReadOnly = `**Enforce Read Only on Replicas**\n\nSets \`read_only = ON\` on all replica servers.\nPrevents accidental writes to replicas and ensures only the master accepts write traffic.\nHighly recommended for all production clusters.`
  const helpBinlogRow = `**Enforce Binlog Format ROW**\n\nForces \`binlog_format = ROW\` on all servers.\nROW format provides the most reliable replication and is required for flashback, GTID-based failover, and online schema changes.\nAvoid STATEMENT or MIXED format in new deployments.`
  const helpAnnotate = `**Enforce Binlog Row Annotate**\n\nEnables MariaDB's \`binlog_annotate_row_events\` option.\nAdds the original SQL statement as a comment in the binary log before each row event.\nMakes the binary log easier to read and audit without affecting replication behaviour.`
  const helpCompress = `**Enforce Binlog Compression**\n\nEnables binary log compression (MySQL 8.0.20+ / MariaDB 10.6+).\nReduces disk usage and network transfer for replication at the cost of additional CPU.\nMost effective for workloads with large row images (wide tables, BLOBs).`
  const helpSlowQueries = `**Enforce Replication Queries in Slow Query Log**\n\nLogs queries executed by the SQL replication thread to the slow query log.\nUseful for diagnosing slow replication caused by expensive statements.\nControlled by \`log_slow_slave_statements\` / \`log_slow_replica_statements\`.`
  const helpGTID = `**Enforce GTID Replication**\n\nForces GTID mode on all servers (\`gtid_mode = ON\` on MySQL, \`gtid_strict_mode = ON\` on MariaDB).\nGTID replication simplifies failover and eliminates the need to track binlog file/position manually.\nRequired for automatic failover in most configurations.`
  const helpGTIDStrict = `**Enforce Replication Stop When Write on Replica**\n\nEnables \`gtid_strict_mode\` (MariaDB) or equivalent.\nThe SQL thread stops with an error if it encounters a transaction that would create a GTID sequence gap.\nPrevents silent data divergence caused by direct writes to replicas.`
  const helpSemiSync = `**Enforce Semi-Synchronous Replication**\n\nConfigures semi-synchronous replication on the master and all replicas.\nThe master waits for at least one replica to acknowledge receipt of a transaction before committing.\nReduces data loss on failover but increases write latency.`
  const helpStrict = `**Enforce Replication Mode Strict**\n\nThe SQL thread stops with an error when it encounters a row that cannot be found (delete/update on non-existent row).\nPrevents silent data divergence but may cause replication to stop after certain DDL or direct writes to replicas.`
  const helpIdempotent = `**Enforce Replication Mode Idempotent**\n\nThe SQL thread silently ignores missing rows (delete/update on non-existent row) and duplicate key errors.\nUsed to resync a diverged replica without stopping replication.\nOnly enable temporarily during recovery — idempotent mode can mask real data issues.`
  const helpSerialized = `**Enforce Replication Parallel Mode: Serialized**\n\nDisables parallel replication. All transactions are applied sequentially by a single SQL thread.\nSafest mode with zero risk of dependency conflicts, but slowest throughput.`
  const helpMinimal = `**Enforce Replication Parallel Mode: Minimal**\n\nAllows transactions to be applied in parallel only if they are guaranteed to be independent (based on commit order).\nGood balance of safety and performance for most workloads.`
  const helpConservative = `**Enforce Replication Parallel Mode: Conservative**\n\nParallel execution based on the master's binary log group commit order.\nSafer than optimistic mode while still providing parallelism for workloads with natural commit grouping.`
  const helpOptimistic = `**Enforce Replication Parallel Mode: Optimistic**\n\nApplies transactions in parallel speculatively and rolls back conflicts.\nHighest throughput for OLTP workloads but can cause replica lag spikes during rollback storms.`
  const helpAggressive = `**Enforce Replication Parallel Mode: Aggressive**\n\nMaximum parallelism — transactions are applied with minimal dependency checking.\nHighest risk of conflicts and potential replication errors.\nOnly recommended for read-heavy replicas where occasional lag is acceptable.`
  const helpHeartbeat = `**Enforce Replication Heartbeat**\n\nEnables replication heartbeat events (\`MASTER_HEARTBEAT_PERIOD\`).\nThe master sends a heartbeat packet to each replica at regular intervals even when there are no changes.\nPrevents the SQL thread from disconnecting during idle periods on long TCP socket timeouts.`

  const dataObject = [
    { key: helpKey('Replication Channel', helpChannel), value: (<TextForm value={selectedCluster?.config?.replicationSourceName} confirmTitle={`Confirm staging replication-source-name to `} onSave={(value) => { dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'replication-source-name', value })) }} />) },
    { key: helpKey('Master Retry Count', helpRetry), value: (<NumberInput min={0} max={Number.MAX_SAFE_INTEGER} inputWidth='100px' value={selectedCluster?.config?.replicationMasterRetryCount} showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'replication-master-retry-count' to: `} isDisabled={user?.grants['cluster-settings'] == false} onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'replication-master-retry-count', value }))} />) },
    { key: helpKey('Enforce Read Only on Replicas', helpReadOnly), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-readonly?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-readonly' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveReadonly} />) },
    { key: helpKey('Enforce Binlog Format ROW', helpBinlogRow), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-binlog-row?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-binlog-row' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceBinlogRow} />) },
    { key: helpKey('Enforce Binlog Row Annotate', helpAnnotate), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-binlog-annotate?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-binlog-annotate' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceBinlogAnnotate} />) },
    { key: helpKey('Enforce Binlog Compression', helpCompress), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-binlog-compress?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-binlog-compress' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceBinlogCompress} />) },
    { key: helpKey('Enforce Replication Queries in Slow Query Log', helpSlowQueries), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-binlog-slowqueries?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-binlog-slowqueries' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceBinlogSlowqueries} />) },
    { key: helpKey('Enforce GTID Replication', helpGTID), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-gtid?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-gtid' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveGtidMode} />) },
    { key: helpKey('Enforce Replication Stop When Write on Replica', helpGTIDStrict), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-gtid-mode-strict?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-gtid-mode-strict' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveGtidModeStrict} />) },
    { key: helpKey('Enforce Semi-Synchronous Replication', helpSemiSync), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-semisync?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-semisync' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveSemisync} />) },
    { key: helpKey('Enforce Replication Mode Strict', helpStrict), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-strict?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-strict' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveStrict} />) },
    { key: helpKey('Enforce Replication Mode Idempotent', helpIdempotent), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-idempotent?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-idempotent' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveIdempotent} />) },
    { key: helpKey('Enforce Replication Parallel Mode: Serialized', helpSerialized), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-serialized?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-serialized' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'SERIALIZED'} />) },
    { key: helpKey('Enforce Replication Parallel Mode: Minimal', helpMinimal), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-minimal?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-minimal' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'MINIMAL'} />) },
    { key: helpKey('Enforce Replication Parallel Mode: Conservative', helpConservative), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-conservative?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-conservative' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'CONSERVATIVE'} />) },
    { key: helpKey('Enforce Replication Parallel Mode: Optimistic', helpOptimistic), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-optimistic?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-optimistic' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'OPTIMISTIC'} />) },
    { key: helpKey('Enforce Replication Parallel Mode: Aggressive', helpAggressive), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-aggressive?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-aggressive' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'AGGRESSIVE'} />) },
    { key: helpKey('Enforce Replication Heartbeat', helpHeartbeat), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-heartbeat?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-heartbeat' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveHeartbeat} />) },
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.table} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default RepConfigSettings
