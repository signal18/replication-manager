import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import Scheduler from './Scheduler'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'

function SchedulerSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  const helpKey = (label, content) => (
    <Box as="span" display="inline">
      {label}
      <Box as="span" display="inline-flex" verticalAlign="middle" ml={1}>
        <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(label, content)} />
      </Box>
    </Box>
  )

  const cronHelp = (name, desc) => `**${name}**\n\n${desc}\n\nThe switch enables or disables the job entirely. The cron field uses 6-field extended cron format:\n\`\`\`\nsecond minute hour day-of-month month day-of-week\n\`\`\`\nExample: \`0 0 2 * * *\` = every day at 02:00.`

  const helpScheduler = `**Scheduler**\n\nMaster switch for the replication-manager internal job scheduler.\nWhen disabled, all scheduled jobs (backups, log fetching, rolling restarts, etc.) are suspended regardless of their individual cron settings.\nEnable this before configuring individual job schedules below.`
  const helpSSH = cronHelp('Run Jobs via SSH', 'Executes SSH-based jobs on database servers at the configured cron schedule.\nSSH jobs are used for operations that require direct OS-level access, such as custom scripts or tasks not available through the MySQL protocol.')
  const helpAnalyzePersistent = `**Analyze Tables Use PERSISTENT**\n\nWhen enabled, \`ANALYZE TABLE\` commands use the \`PERSISTENT\` option (MariaDB).\nPersistent statistics are stored in the \`mysql.table_stats\` and \`mysql.column_stats\` tables and survive server restarts.\nRecommended for tables with skewed data distributions.`
  const helpLogicalBackup = cronHelp('Logical Backup', 'Schedules periodic logical backups using mysqldump or mydumper.\nLogical backups are portable and human-readable but slower to restore than physical backups.\nSuitable for small to medium datasets or when point-in-time restore flexibility is required.')
  const helpPhysicalBackup = cronHelp('Physical Backup', 'Schedules periodic physical backups using Xtrabackup or Mariabackup.\nPhysical backups are faster to restore for large datasets but less portable.\nRecommended for production clusters with strict RTO requirements.')
  const helpOptimize = cronHelp('Optimize Tables', 'Schedules periodic OPTIMIZE TABLE operations.\nOptimization reclaims fragmented space and rebuilds table statistics.\nRun during low-traffic windows — OPTIMIZE TABLE acquires a table lock on non-InnoDB engines.')
  const helpAnalyze = cronHelp('Analyze Tables', 'Schedules periodic ANALYZE TABLE operations to refresh index statistics.\nAccurate statistics are critical for the query optimizer to choose efficient execution plans.\nLess invasive than OPTIMIZE TABLE and safe to run more frequently.')
  const helpFetchLogs = cronHelp('Fetch Logs', 'Schedules periodic collection of database error logs, slow query logs, and audit logs from all servers.\nLogs are transferred to the replication-manager working directory and made available in the GUI log viewer.')
  const helpRotateLogs = cronHelp('Rotate Log Tables', 'Schedules rotation of MySQL system log tables (\`mysql.slow_log\`, \`mysql.general_log\`).\nRotation archives the current table and creates a fresh empty one, preventing unbounded growth.')
  const helpRollingRestart = cronHelp('Rolling Restart', 'Schedules a rolling restart of all cluster nodes.\nNodes are restarted one at a time (replicas first, then master via a switchover) to apply configuration changes without cluster downtime.')
  const helpRollingReprov = cronHelp('Rolling Reprovision', 'Schedules a rolling re-provisioning of cluster nodes.\nEach node is destroyed and rebuilt from scratch using the current provisioning configuration.\nUseful for applying OS-level changes or replacing degraded nodes.')
  const helpSlaRotate = cronHelp('Rotate SLA', 'Schedules rotation of SLA (Service Level Agreement) availability statistics.\nOlder SLA buckets are archived and a new measurement window begins.')
  const helpDisableAlert = cronHelp('Disable Alerting', 'Schedules a window during which all alert notifications are suppressed.\nUseful for planned maintenance periods when alerts would be noisy and expected.')
  const helpSchemaMonitor = cronHelp('Monitor Schema Changes', 'Schedules schema change detection scans.\nInstead of scanning on every monitoring cycle, schema comparisons are triggered only at the configured cron schedule.\nReduces load on \`INFORMATION_SCHEMA\` for large databases.')
  const helpChecksumTables = cronHelp('Monitor Checksum Tables', 'Schedules periodic table checksum verification across master and replicas.\nDetects silent data divergence that replication monitoring alone cannot catch.\nUse \`pt-table-checksum\` compatible checksums.')

  const dataObject = [
    { key: helpKey('Scheduler', helpScheduler), value: (<RMSwitch confirmTitle={'Confirm switch settings for monitoring-scheduler?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-scheduler' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.monitoringScheduler} />) },
    { key: helpKey('Run Jobs via SSH', helpSSH), value: (<Scheduler user={user} value={selectedCluster?.config?.schedulerJobsSshCron} switchConfirmTitle={'Confirm switch settings for scheduler-jobs-ssh?'} isSwitchChecked={selectedCluster?.config?.schedulerJobsSsh} confirmTitle={'Confirm save JobsSsh scheduler to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-jobs-ssh' }))} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-jobs-ssh-cron', value }))} />) },
    { key: helpKey('Analyze Tables Use PERSISTENT', helpAnalyzePersistent), value: (<RMSwitch confirmTitle={'Confirm switch settings for analyze-use-persistent?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'analyze-use-persistent' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.analyzeUsePersistent} />) },
    ...(selectedCluster?.config?.monitoringScheduler ? [
      { key: helpKey('Logical Backup', helpLogicalBackup), value: (<Scheduler user={user} value={selectedCluster?.config?.schedulerDbServersLogicalBackupCron} switchConfirmTitle={'Confirm switch settings for scheduler-db-servers-logical-backup?'} isSwitchChecked={selectedCluster?.config?.schedulerDbServersLogicalBackup} confirmTitle={'Confirm save logical backup scheduler to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-logical-backup' }))} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-logical-backup-cron', value }))} />) },
      { key: helpKey('Physical Backup', helpPhysicalBackup), value: (<Scheduler user={user} value={selectedCluster?.config?.schedulerDbServersPhysicalBackupCron} switchConfirmTitle={'Confirm switch settings for scheduler-db-servers-physical-backup?'} isSwitchChecked={selectedCluster?.config?.schedulerDbServersPhysicalBackup} confirmTitle={'Confirm save physical backup scheduler to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-physical-backup' }))} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-physical-backup-cron', value }))} />) },
      { key: helpKey('Optimize Tables', helpOptimize), value: (<Scheduler user={user} value={selectedCluster?.config?.schedulerDbServersOptimizeCron} switchConfirmTitle={'Confirm switch settings for scheduler-db-servers-optimize?'} isSwitchChecked={selectedCluster?.config?.schedulerDbServersOptimize} confirmTitle={'Confirm optimize backup scheduler to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-optimize' }))} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-optimize-cron', value }))} />) },
      { key: helpKey('Analyze Tables', helpAnalyze), value: (<Scheduler user={user} value={selectedCluster?.config?.schedulerDbServersAnalyzeCron} switchConfirmTitle={'Confirm switch settings for scheduler-db-servers-analyze?'} isSwitchChecked={selectedCluster?.config?.schedulerDbServersAnalyze} confirmTitle={'Confirm save analyze backup scheduler to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-analyze' }))} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-analyze-cron', value }))} />) },
      { key: helpKey('Fetch Logs', helpFetchLogs), value: (<Scheduler user={user} value={selectedCluster?.config?.schedulerDbServersLogsCron} switchConfirmTitle={'Confirm switch settings for scheduler-db-servers-logs?'} isSwitchChecked={selectedCluster?.config?.schedulerDbServersLogs} confirmTitle={'Confirm save logs scheduler to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-logs' }))} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-logs-cron', value }))} />) },
      { key: helpKey('Rotate Log Tables', helpRotateLogs), value: (<Scheduler user={user} value={selectedCluster?.config?.schedulerDbServersLogsTableRotateCron} switchConfirmTitle={'Confirm switch settings for scheduler-db-servers-logs-table-rotate?'} isSwitchChecked={selectedCluster?.config?.schedulerDbServersLogsTableRotate} confirmTitle={'Confirm save LogsTableRotate scheduler to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-logs-table-rotate' }))} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-logs-table-rotate-cron', value }))} />) },
      { key: helpKey('Rolling Restart', helpRollingRestart), value: (<Scheduler user={user} value={selectedCluster?.config?.schedulerRollingRestartCron} switchConfirmTitle={'Confirm switch settings for scheduler-rolling-restart?'} isSwitchChecked={selectedCluster?.config?.schedulerRollingRestart} confirmTitle={'Confirm save RollingRestart scheduler to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-rolling-restart' }))} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-rolling-restart-cron', value }))} />) },
      { key: helpKey('Rolling Reprovision', helpRollingReprov), value: (<Scheduler user={user} value={selectedCluster?.config?.schedulerRollingReprovCron} switchConfirmTitle={'Confirm switch settings for scheduler-rolling-reprov?'} isSwitchChecked={selectedCluster?.config?.schedulerRollingReprov} confirmTitle={'Confirm save RollingReprov scheduler to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-rolling-reprov' }))} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-rolling-reprov-cron', value }))} />) },
      { key: helpKey('Rotate SLA', helpSlaRotate), value: (<Scheduler user={user} hasSwitch={false} value={selectedCluster?.config?.schedulerSlaRotateCron} confirmTitle={'Confirm save SlaRotate scheduler to: '} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-sla-rotate-cron', value }))} />) },
      { key: helpKey('Disable Alerting', helpDisableAlert), value: (<Scheduler user={user} value={selectedCluster?.config?.schedulerAlertDisableCron} switchConfirmTitle={'Confirm switch settings for scheduler-alert-disable?'} isSwitchChecked={selectedCluster?.config?.schedulerAlertDisable} confirmTitle={'Confirm save alert disable scheduler to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-alert-disable' }))} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-alert-disable-cron', value }))} />) },
      { key: helpKey('Monitor Schema Changes', helpSchemaMonitor), value: (<Scheduler user={user} value={selectedCluster?.config?.monitoringSchemaSchedulerCron} switchConfirmTitle={'Confirm switch settings for monitoring-schema-scheduler?'} isSwitchChecked={selectedCluster?.config?.monitoringSchemaScheduler} confirmTitle={'Confirm save monitoring schema scheduler to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-schema-scheduler' }))} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-schema-scheduler-cron', value }))} />) },
      { key: helpKey('Monitor Checksum Tables', helpChecksumTables), value: (<Scheduler user={user} value={selectedCluster?.config?.monitoringChecksumSchedulerCron} switchConfirmTitle={'Confirm switch settings for monitoring-checksum-scheduler?'} isSwitchChecked={selectedCluster?.config?.monitoringChecksumScheduler} confirmTitle={'Confirm save monitoring checksum tables scheduler to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-checksum-scheduler' }))} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-checksum-scheduler-cron', value }))} />) },
    ] : [])
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

export default SchedulerSettings
