import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'
import NumberInput from '../../components/NumberInput'
import RMSlider from '../../components/Sliders/RMSlider'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'

function MonitoringSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  const h = (content, title) => (
    <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />
  )

  const { settings: { monSaveConfigLoading, monPauseLoading, monCaptureLoading, monSchemaChangeLoading, monInnoDBLoading, monVarDiffLoading, monProcessListLoading, captureTriggerLoading, monIgnoreErrLoading } } = useSelector((state) => state)

  const sw = (setting, configKey, loading) =>
    <RMSwitch confirmTitle={`Confirm switch settings for ${setting}?`} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.[configKey]} loading={loading} />

  const hSaveConfig = `**Monitoring Save Config**\n\nWhen enabled, any setting changed through the API or GUI is immediately persisted to the cluster configuration file on disk.\nWhen disabled, changes apply at runtime only and are lost on the next restart.\n\nConfig: \`monitoring-save-config\``
  const hPause = `**Monitoring Pause**\n\nTemporarily suspends all monitoring cycles for this cluster.\nUseful during planned maintenance windows to avoid spurious alerts or unintended failovers.\n\nConfig: \`monitoring-pause\``
  const hCapture = `**Monitoring Capture On Error**\n\nCaptures a full diagnostic snapshot every time an error state is detected.\nThe snapshot includes SHOW FULL PROCESSLIST, SHOW ENGINE INNODB STATUS, and replication status.\nSnapshots are written to JSON files in the cluster working directory.\n\nConfig: \`monitoring-capture\``
  const hCaptureTrigger = `**Monitoring Capture On Error Trigger**\n\nComma-separated list of error codes that restrict when a capture snapshot is taken.\nLeave empty to capture on every error state transition.\n\nExample: \`ERR00001,ERR00002\`\n\nConfig: \`monitoring-capture-trigger\``
  const hIgnoreErrors = `**Monitoring Ignore Error List**\n\nComma-separated list of error or warning codes that replication-manager will silently suppress.\n\nExample: \`WARN0001,ERR00042\`\n\nConfig: \`monitoring-ignore-errors\``
  const hSchema = `**Monitoring Schema**\n\nTracks DDL changes (CREATE, ALTER, DROP) on all schemas.\nRequires Monitoring Schema Columns and/or Monitoring Schema Indexes to also be enabled for full coverage.\n\nConfig: \`monitoring-schema-change\``
  const hSchemaColumns = `**Monitoring Schema Columns**\n\nIncludes column-level changes in the schema diff.\nRequires Monitoring Schema to be enabled.\n\nConfig: \`monitoring-schema-columns\``
  const hSchemaIndexes = `**Monitoring Schema Indexes**\n\nIncludes index-level changes in the schema diff.\nRequires Monitoring Schema to be enabled.\n\nConfig: \`monitoring-schema-indexes\``
  const hSchemaIgnore = `**Monitoring Schema Ignore Tables**\n\nComma-separated list of tables (in \`schema.table\` format) to exclude from schema change monitoring.\n\nExample: \`mydb.audit_log,mydb.temp_import\`\n\nConfig: \`monitoring-schema-ignore-tables\``
  const hSchemaScanTimeout = `**Monitoring Schema Scan Timeout**\n\nMaximum time in seconds to wait for a schema metadata query to complete.\nDefault: **30 seconds**. Increase for servers with very large numbers of tables.\n\nConfig: \`monitoring-schema-scan-timeout\``
  const hVarDiff = `**Monitoring Variable Diff**\n\nTracks changes in MySQL/MariaDB global variables between monitoring cycles.\nUseful for detecting configuration drift across a cluster.\n\nConfig: \`monitoring-variable-diff\``
  const hProcesslist = `**Monitoring Processlist**\n\nEnables collection of SHOW FULL PROCESSLIST on every monitoring cycle.\nRelated: Inactive (include Sleep connections), Transactions (include innodb_trx), Information Schema (use I_S).\n\nConfig: \`monitoring-processlist\``
  const hProcesslistIS = `**Monitoring Processlist Information Schema**\n\nCollects processlist from \`information_schema.processlist\` instead of SHOW FULL PROCESSLIST.\nAvoids the PROCESS privilege restriction on MySQL 8.0+.\n\nConfig: \`monitoring-processlist-information-schema\``
  const hProcesslistInactive = `**Monitoring Processlist Inactive**\n\nIncludes idle connections (Command = Sleep) in the collected processlist.\nDisabled by default to reduce noise.\n\nConfig: \`monitoring-processlist-inactive\``
  const hProcesslistTx = `**Monitoring Processlist Transactions**\n\nJoins active InnoDB transaction details from \`information_schema.innodb_trx\` into the processlist view.\n\nConfig: \`monitoring-processlist-transactions\``
  const hInnoDB = `**Monitoring InnoDB Status**\n\nEnables periodic collection of SHOW ENGINE INNODB STATUS output.\nUseful for diagnosing lock contention, deadlocks, and buffer pool pressure.\n\nConfig: \`monitoring-innodb-status\``
  const hMutex = `**Monitoring InnoDB Mutex**\n\nCollects mutex wait metrics from Performance Schema for InnoDB mutex instruments.\nRequires Performance Schema to be enabled.\n\nConfig: \`monitoring-performance-schema-mutex\``
  const hLatch = `**Monitoring InnoDB Latch**\n\nCollects read-write lock (latch) wait metrics from Performance Schema for InnoDB rw-lock instruments.\nHigh latch waits indicate concurrency bottlenecks.\n\nConfig: \`monitoring-performance-schema-latch\``
  const hPFSMemory = `**Monitoring Performance Schema Memory**\n\nCollects memory instrument metrics from \`performance_schema.memory_summary_global_by_event_name\`.\nExposes per-subsystem memory consumption in the GUI graphs.\n\nConfig: \`monitoring-performance-schema-memory\``
  const hPFSInstruments = `**Monitoring Performance Schema Instruments**\n\nCollects instrument enable/disable state from \`performance_schema.setup_instruments\`.\nUseful to audit which Performance Schema instruments are active on each server.\n\nConfig: \`monitoring-performance-schema-instruments\``
  const hPFSQueries = `**Monitoring Performance Schema Queries**\n\nPeriodic snapshot of \`events_statements_summary_by_digest\`.\nAt each period the digest table is read then truncated, writing a timestamped JSONL snapshot file.\nRequires \`performance_schema = ON\`.\n\nConfig: \`monitoring-performance-schema-queries\``
  const hPFSPeriod = `**Monitoring Performance Schema Queries Period (hours)**\n\nHow many hours between consecutive PFS digest snapshot flushes.\nDefault: **1 hour**.\n\nConfig: \`monitoring-performance-schema-queries-period\``
  const hPFSExplain = `**Monitoring Performance Schema Queries Explain**\n\nRuns EXPLAIN for each query template captured during a PFS snapshot.\nNew templates are prioritised; existing plans are refreshed oldest-first.\nPlans persisted to \`pfs_explain_cache.jsonl\`.\n\nConfig: \`monitoring-performance-schema-queries-explain\``
  const hPFSDelay = `**Monitoring Performance Schema Queries Explain Delay (ms)**\n\nMilliseconds to sleep between consecutive EXPLAIN calls to spread optimizer load.\nDefault: **200 ms**. Set to 0 to disable throttling.\n\nConfig: \`monitoring-performance-schema-queries-explain-delay\``
  const hPFSPurge = `**Monitoring Performance Schema Queries Explain Purge Period (days)**\n\nAge in days after which a cached explain plan is evicted from memory and disk.\nDefault: **30 days**. Set to 0 to keep plans forever.\n\nConfig: \`monitoring-performance-schema-queries-explain-purge-period\``

  const hRepStatCapture = `**Monitoring Replication Statistics**\n\nEnables collection of replication lag measurements at regular intervals averaged per hour.\nUsed to detect degrading replica performance over time.\nRequired for Failover Candidate Rate Using Statistics to function.\n\nConfig: \`delay-stat-capture\``
  const hRepStatRotate = `**Monitoring Replication Statistics Rotate**\n\nNumber of hours of delay statistics to retain before rotating.\nDefault: 24 hours. Maximum: 72 hours.\n\nConfig: \`delay-stat-rotate\``

  const dataObject = [
    {
      key: 'Monitoring Save Config', value: [
        { key: 'Monitoring Save Config', help: h(hSaveConfig, 'Monitoring Save Config'), value: sw('monitoring-save-config', 'monitoringSaveConfig', monSaveConfigLoading) },
        { key: 'Monitoring Pause', help: h(hPause, 'Monitoring Pause'), value: sw('monitoring-pause', 'monitoringPause', monPauseLoading) },
      ]
    },
    { key: 'Monitoring Capture On Error', help: h(hCapture, 'Monitoring Capture On Error'), value: sw('monitoring-capture', 'monitoringCapture', monCaptureLoading) },
    { key: 'Monitoring Capture On Error Trigger', help: h(hCaptureTrigger, 'Monitoring Capture On Error Trigger'), value: <TextForm value={selectedCluster?.config?.monitoringCaptureTrigger} confirmTitle={`Confirm change 'monitoring-capture-trigger' to `} onSave={(v) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-capture-trigger', value: v.length === 0 ? '{undefined}' : v }))} /> },
    { key: 'Monitoring Ignore Error List', help: h(hIgnoreErrors, 'Monitoring Ignore Error List'), value: <TextForm value={selectedCluster?.config?.monitoringIgnoreErrors} confirmTitle={`Confirm change 'monitoring-ignore-errors' to: `} onSave={(v) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-ignore-errors', value: v.length === 0 ? '{undefined}' : v }))} /> },
    { key: 'Monitoring Schema', help: h(hSchema, 'Monitoring Schema'), value: sw('monitoring-schema-change', 'monitoringSchemaChange', monSchemaChangeLoading) },
    { key: 'Monitoring Schema Columns', help: h(hSchemaColumns, 'Monitoring Schema Columns'), value: sw('monitoring-schema-columns', 'monitoringSchemaColumns', monSchemaChangeLoading) },
    { key: 'Monitoring Schema Indexes', help: h(hSchemaIndexes, 'Monitoring Schema Indexes'), value: sw('monitoring-schema-indexes', 'monitoringSchemaIndexes', monSchemaChangeLoading) },
    { key: 'Monitoring Schema Ignore Tables', help: h(hSchemaIgnore, 'Monitoring Schema Ignore Tables'), value: <TextForm value={selectedCluster?.config?.monitoringSchemaIgnoreTables} confirmTitle={`Confirm change 'monitoring-schema-ignore-tables' to: `} onSave={(v) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-schema-ignore-tables', value: v.length === 0 ? '&nbsp;' : v }))} /> },
    { key: 'Monitoring Schema Scan Timeout', help: h(hSchemaScanTimeout, 'Monitoring Schema Scan Timeout'), value: <NumberInput value={selectedCluster?.config?.monitoringSchemaScanTimeout} showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'monitoring-schema-scan-timeout' to: `} onConfirm={(v) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-schema-scan-timeout', value: v.length === 0 ? '30' : v }))} /> },
    { key: 'Monitoring Variable Diff', help: h(hVarDiff, 'Monitoring Variable Diff'), value: sw('monitoring-variable-diff', 'monitoringVariableDiff', monVarDiffLoading) },
    { key: 'Monitoring Processlist', help: h(hProcesslist, 'Monitoring Processlist'), value: sw('monitoring-processlist', 'monitoringProcesslist', monProcessListLoading) },
    { key: 'Monitoring Processlist Information Schema', help: h(hProcesslistIS, 'Monitoring Processlist Information Schema'), value: sw('monitoring-processlist-information-schema', 'monitoringProcesslistInformationSchema', monProcessListLoading) },
    { key: 'Monitoring Processlist Inactive', help: h(hProcesslistInactive, 'Monitoring Processlist Inactive'), value: sw('monitoring-processlist-inactive', 'monitoringProcesslistInactive', monProcessListLoading) },
    { key: 'Monitoring Processlist Transactions', help: h(hProcesslistTx, 'Monitoring Processlist Transactions'), value: sw('monitoring-processlist-transactions', 'monitoringProcesslistTransactions', monProcessListLoading) },
    { key: 'Monitoring InnoDB Status', help: h(hInnoDB, 'Monitoring InnoDB Status'), value: sw('monitoring-innodb-status', 'monitoringInnoDBStatus', monInnoDBLoading) },
    { key: 'Monitoring InnoDB Mutex', help: h(hMutex, 'Monitoring InnoDB Mutex'), value: sw('monitoring-performance-schema-mutex', 'monitoringPerformanceSchemaMutex', monProcessListLoading) },
    { key: 'Monitoring InnoDB Latch', help: h(hLatch, 'Monitoring InnoDB Latch'), value: sw('monitoring-performance-schema-latch', 'monitoringPerformanceSchemaLatch', monProcessListLoading) },
    { key: 'Monitoring Replication Statistics', help: h(hRepStatCapture, 'Monitoring Replication Statistics'), value: sw('delay-stat-capture', 'delayStatCapture') },
    { key: 'Monitoring Replication Statistics Rotate', help: h(hRepStatRotate, 'Monitoring Replication Statistics Rotate'), value: (<RMSlider value={selectedCluster?.config?.delayStatRotate} max={72} showMarkAtInterval={12} confirmTitle='Confirm change replication statistics rotate to: ' onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'delay-stat-rotate', value: val }))} />) },
    {
      key: 'Monitoring Performance Schema Queries', value: [
        { key: 'Monitoring Performance Schema Memory', help: h(hPFSMemory, 'Monitoring Performance Schema Memory'), value: sw('monitoring-performance-schema-memory', 'monitoringPerformanceSchemaMemory') },
        { key: 'Monitoring Performance Schema Instruments', help: h(hPFSInstruments, 'Monitoring Performance Schema Instruments'), value: sw('monitoring-performance-schema-instruments', 'monitoringPerformanceIntruments') },
        { key: 'Monitoring Performance Schema Queries', help: h(hPFSQueries, 'Monitoring Performance Schema Queries'), value: sw('monitoring-performance-schema-queries', 'monitoringPerformanceSchemaQueries') },
        { key: 'Monitoring Performance Schema Queries Period (hours)', help: h(hPFSPeriod, 'PFS Queries Period'), value: <NumberInput value={selectedCluster?.config?.monitoringPerformanceSchemaQueriesPeriod} min={1} max={168} showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'monitoring-performance-schema-queries-period' to: `} onConfirm={(v) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-performance-schema-queries-period', value: v.length === 0 ? '1' : v }))} /> },
        { key: 'Monitoring Performance Schema Queries Explain', help: h(hPFSExplain, 'PFS Queries Explain'), value: sw('monitoring-performance-schema-queries-explain', 'monitoringPerformanceSchemaQueriesExplain') },
        { key: 'Monitoring Performance Schema Queries Explain Delay (ms)', help: h(hPFSDelay, 'PFS Queries Explain Delay'), value: <NumberInput value={selectedCluster?.config?.monitoringPerformanceSchemaQueriesExplainDelay} min={0} max={5000} step={50} showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'monitoring-performance-schema-queries-explain-delay' to: `} onConfirm={(v) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-performance-schema-queries-explain-delay', value: v.length === 0 ? '200' : v }))} /> },
        { key: 'Monitoring Performance Schema Queries Explain Purge Period (days)', help: h(hPFSPurge, 'PFS Queries Explain Purge Period'), value: <NumberInput value={selectedCluster?.config?.monitoringPerformanceSchemaQueriesExplainPurgePeriod} min={0} max={365} showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'monitoring-performance-schema-queries-explain-purge-period' to: `} onConfirm={(v) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-performance-schema-queries-explain-purge-period', value: v.length === 0 ? '30' : v }))} /> },
      ]
    }
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

export default MonitoringSettings
