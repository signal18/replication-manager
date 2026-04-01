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

  const h = (content, title) => <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} />

  const cronHelp = (name, desc, configkey) => `**${name}**\n\n${desc}\n\nThe switch enables or disables the job entirely. Cron uses 6-field extended format:\n\`\`\`\nsecond minute hour day-of-month month day-of-week\n\`\`\`\nExample: \`0 0 2 * * *\` = every day at 02:00.\n\nConfig: \`${configkey}\``

  const hScheduler = `**Scheduler**\n\nMaster switch for the internal job scheduler.\nWhen disabled, all scheduled jobs are suspended regardless of their individual cron settings.\n\nConfig: \`monitoring-scheduler\``
  const hSSH = cronHelp('Run Jobs via SSH', 'Executes SSH-based jobs on database servers at the configured cron schedule.', 'scheduler-jobs-ssh / scheduler-jobs-ssh-cron')
  const hPersistent = `**Analyze Tables Use PERSISTENT**\n\nWhen enabled, \`ANALYZE TABLE\` uses the \`PERSISTENT\` option (MariaDB).\nPersistent statistics survive server restarts and are stored in \`mysql.table_stats\`.\n\nConfig: \`analyze-use-persistent\``
  const hLogical = cronHelp('Logical Backup', 'Schedules periodic logical backups using mysqldump or mydumper.', 'scheduler-db-servers-logical-backup / scheduler-db-servers-logical-backup-cron')
  const hPhysical = cronHelp('Physical Backup', 'Schedules periodic physical backups using Xtrabackup or Mariabackup.', 'scheduler-db-servers-physical-backup / scheduler-db-servers-physical-backup-cron')
  const hOptimize = cronHelp('Optimize Tables', 'Schedules periodic OPTIMIZE TABLE operations to reclaim fragmented space.', 'scheduler-db-servers-optimize / scheduler-db-servers-optimize-cron')
  const hAnalyze = cronHelp('Analyze Tables', 'Schedules periodic ANALYZE TABLE to refresh index statistics.', 'scheduler-db-servers-analyze / scheduler-db-servers-analyze-cron')
  const hFetchLogs = cronHelp('Fetch Logs', 'Schedules periodic collection of error logs, slow query logs, and audit logs from all servers.', 'scheduler-db-servers-logs / scheduler-db-servers-logs-cron')
  const hRotateLogs = cronHelp('Rotate Log Tables', 'Schedules rotation of MySQL system log tables to prevent unbounded growth.', 'scheduler-db-servers-logs-table-rotate / scheduler-db-servers-logs-table-rotate-cron')
const hRollingRestart = cronHelp('Rolling Restart', 'Schedules a rolling restart of all cluster nodes (replicas first, then master via switchover).', 'scheduler-rolling-restart / scheduler-rolling-restart-cron')  
const hRollingReprov = cronHelp('Rolling Reprovision', 'Schedules a rolling re-provisioning of cluster nodes from scratch.', 'scheduler-rolling-reprov / scheduler-rolling-reprov-cron')
  const hSlaRotate = cronHelp('Rotate SLA', 'Schedules rotation of SLA availability statistics.', 'scheduler-sla-rotate-cron')
  const hDisableAlert = cronHelp('Disable Alerting', 'Schedules a window during which all alert notifications are suppressed.', 'scheduler-alert-disable / scheduler-alert-disable-cron')
  const hSchemaMonitor = cronHelp('Monitor Schema Changes', 'Schedules schema change detection scans instead of scanning every monitoring cycle.', 'monitoring-schema-scheduler / monitoring-schema-scheduler-cron')
  const hChecksumTables = cronHelp('Monitor Checksum Tables', 'Schedules periodic table checksum verification to detect silent data divergence.', 'monitoring-checksum-scheduler / monitoring-checksum-scheduler-cron')

  const sc = (sw, swKey, cronKey, cronSetting, title, hasSwitch = true) => (
    <Scheduler user={user} value={selectedCluster?.config?.[cronKey]} switchConfirmTitle={`Confirm switch settings for ${sw}?`} isSwitchChecked={selectedCluster?.config?.[swKey]} confirmTitle={`Confirm save ${title} scheduler to: `} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: sw }))} onSave={(v) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: cronSetting, value: v }))} hasSwitch={hasSwitch} />
  )

  const dataObject = [
    { key: 'Scheduler', help: h(hScheduler, 'Scheduler'), value: (<RMSwitch confirmTitle={'Confirm switch settings for monitoring-scheduler?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-scheduler' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.monitoringScheduler} />) },
    { key: 'Run Jobs via SSH', help: h(hSSH, 'Run Jobs via SSH'), value: sc('scheduler-jobs-ssh', 'schedulerJobsSsh', 'schedulerJobsSshCron', 'scheduler-jobs-ssh-cron', 'Run Jobs via SSH') },
    { key: 'Analyze Tables Use PERSISTENT', help: h(hPersistent, 'Analyze Tables Use PERSISTENT'), value: (<RMSwitch confirmTitle={'Confirm switch settings for analyze-use-persistent?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'analyze-use-persistent' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.analyzeUsePersistent} />) },
    ...(selectedCluster?.config?.monitoringScheduler ? [
      { key: 'Logical Backup', help: h(hLogical, 'Logical Backup'), value: sc('scheduler-db-servers-logical-backup', 'schedulerDbServersLogicalBackup', 'schedulerDbServersLogicalBackupCron', 'scheduler-db-servers-logical-backup-cron', 'Logical Backup') },
      { key: 'Physical Backup', help: h(hPhysical, 'Physical Backup'), value: sc('scheduler-db-servers-physical-backup', 'schedulerDbServersPhysicalBackup', 'schedulerDbServersPhysicalBackupCron', 'scheduler-db-servers-physical-backup-cron', 'Physical Backup') },
      { key: 'Optimize Tables', help: h(hOptimize, 'Optimize Tables'), value: sc('scheduler-db-servers-optimize', 'schedulerDbServersOptimize', 'schedulerDbServersOptimizeCron', 'scheduler-db-servers-optimize-cron', 'Optimize Tables') },
      { key: 'Analyze Tables', help: h(hAnalyze, 'Analyze Tables'), value: sc('scheduler-db-servers-analyze', 'schedulerDbServersAnalyze', 'schedulerDbServersAnalyzeCron', 'scheduler-db-servers-analyze-cron', 'Analyze Tables') },
      { key: 'Fetch Logs', help: h(hFetchLogs, 'Fetch Logs'), value: sc('scheduler-db-servers-logs', 'schedulerDbServersLogs', 'schedulerDbServersLogsCron', 'scheduler-db-servers-logs-cron', 'Fetch Logs') },
      { key: 'Rotate Log Tables', help: h(hRotateLogs, 'Rotate Log Tables'), value: sc('scheduler-db-servers-logs-table-rotate', 'schedulerDbServersLogsTableRotate', 'schedulerDbServersLogsTableRotateCron', 'scheduler-db-servers-logs-table-rotate-cron', 'Rotate Log Tables') },
      { key: 'Rolling Restart', help: h(hRollingRestart, 'Rolling Restart'), value: sc('scheduler-rolling-restart', 'schedulerRollingRestart', 'schedulerRollingRestartCron', 'scheduler-rolling-restart-cron', 'Rolling Restart') },
      { key: 'Rolling Reprovision', help: h(hRollingReprov, 'Rolling Reprovision'), value: sc('scheduler-rolling-reprov', 'schedulerRollingReprov', 'schedulerRollingReprovCron', 'scheduler-rolling-reprov-cron', 'Rolling Reprovision') },
      { key: 'Rotate SLA', help: h(hSlaRotate, 'Rotate SLA'), value: sc(null, null, 'schedulerSlaRotateCron', 'scheduler-sla-rotate-cron', 'Rotate SLA', false) },
      { key: 'Disable Alerting', help: h(hDisableAlert, 'Disable Alerting'), value: sc('scheduler-alert-disable', 'schedulerAlertDisable', 'schedulerAlertDisableCron', 'scheduler-alert-disable-cron', 'Disable Alerting') },
      { key: 'Monitor Schema Changes', help: h(hSchemaMonitor, 'Monitor Schema Changes'), value: sc('monitoring-schema-scheduler', 'monitoringSchemaScheduler', 'monitoringSchemaSchedulerCron', 'monitoring-schema-scheduler-cron', 'Monitor Schema Changes') },
      { key: 'Monitor Checksum Tables', help: h(hChecksumTables, 'Monitor Checksum Tables'), value: sc('monitoring-checksum-scheduler', 'monitoringChecksumScheduler', 'monitoringChecksumSchedulerCron', 'monitoring-checksum-scheduler-cron', 'Monitor Checksum Tables') },
    ] : [])
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

export default SchedulerSettings
