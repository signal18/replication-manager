import { Flex } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'

import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import Scheduler from './Scheduler'

function SchedulerSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()

  const dataObject = [
    {
      key: 'Scheduler',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-scheduler?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-scheduler' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringScheduler}
        />
      )
    },
    {
      key: 'Schedule Logical Backup',
      value: (
        <Scheduler
          user={user}
          value={selectedCluster?.config?.schedulerDbServersLogicalBackupCron} //'0 0 1-21 5-9 7-8 1,2,3' //'0 0 1-21 5 7-8 *' //'0 0 1-21 5-9 7-8 *' //'0 0 1-21/3 * * *' // '0 0/45 1-23 * * *' //{selectedCluster?.config?.schedulerDbServersLogicalBackupCron}
          switchConfirmTitle={'Confirm switch settings for scheduler-db-servers-logical-backup?'}
          isSwitchChecked={selectedCluster?.config?.schedulerDbServersLogicalBackup}
          confirmTitle={'Confirm save logical backup scheduler to: '}
          onSwitchChange={() =>
            dispatch(
              switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-logical-backup' })
            )
          }
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'scheduler-db-servers-logical-backup-cron',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Schedule Physical Backup',
      value: (
        <Scheduler
          user={user}
          value={selectedCluster?.config?.schedulerDbServersPhysicalBackupCron}
          switchConfirmTitle={'Confirm switch settings for scheduler-db-servers-physical-backup?'}
          isSwitchChecked={selectedCluster?.config?.schedulerDbServersPhysicalBackup}
          confirmTitle={'Confirm save physical backup scheduler to: '}
          onSwitchChange={() =>
            dispatch(
              switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-physical-backup' })
            )
          }
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'scheduler-db-servers-physical-backup-cron',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Schedule Optimize Tables',
      value: (
        <Scheduler
          user={user}
          value={selectedCluster?.config?.schedulerDbServersOptimizeCron}
          switchConfirmTitle={'Confirm switch settings for scheduler-db-servers-optimize?'}
          isSwitchChecked={selectedCluster?.config?.schedulerDbServersOptimize}
          confirmTitle={'Confirm optimize backup scheduler to: '}
          onSwitchChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-optimize' }))
          }
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'scheduler-db-servers-optimize-cron',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Schedule Analyze Tables',
      value: (
        <Scheduler
          user={user}
          value={selectedCluster?.config?.schedulerDbServersAnalyzeCron}
          switchConfirmTitle={'Confirm switch settings for scheduler-db-servers-analyze?'}
          isSwitchChecked={selectedCluster?.config?.schedulerDbServersAnalyze}
          confirmTitle={'Confirm save analyze backup scheduler to: '}
          onSwitchChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-analyze' }))
          }
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'scheduler-db-servers-analyze-cron',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Schedule Fetch Error Log',
      value: (
        <Scheduler
          user={user}
          value={selectedCluster?.config?.schedulerDbServersLogsCron}
          switchConfirmTitle={'Confirm switch settings for scheduler-db-servers-logs?'}
          isSwitchChecked={selectedCluster?.config?.schedulerDbServersLogs}
          confirmTitle={'Confirm save logs scheduler to: '}
          onSwitchChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-logs' }))
          }
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'scheduler-db-servers-logs-cron',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Schedule Rotate Log Tables',
      value: (
        <Scheduler
          user={user}
          value={selectedCluster?.config?.schedulerDbServersLogsTableRotateCron}
          switchConfirmTitle={'Confirm switch settings for scheduler-db-servers-logs-table-rotate?'}
          isSwitchChecked={selectedCluster?.config?.schedulerDbServersLogsTableRotate}
          confirmTitle={'Confirm save LogsTableRotate scheduler to: '}
          onSwitchChange={() =>
            dispatch(
              switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-db-servers-logs-table-rotate' })
            )
          }
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'scheduler-db-servers-logs-table-rotate-cron',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Schedule Rolling Restart',
      value: (
        <Scheduler
          user={user}
          value={selectedCluster?.config?.schedulerRollingRestartCron}
          switchConfirmTitle={'Confirm switch settings for scheduler-rolling-restart?'}
          isSwitchChecked={selectedCluster?.config?.schedulerRollingRestart}
          confirmTitle={'Confirm save RollingRestart scheduler to: '}
          onSwitchChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-rolling-restart' }))
          }
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'scheduler-rolling-restart-cron',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Schedule Rolling Reprov',
      value: (
        <Scheduler
          user={user}
          value={selectedCluster?.config?.schedulerRollingReprovCron}
          switchConfirmTitle={'Confirm switch settings for scheduler-rolling-reprov?'}
          isSwitchChecked={selectedCluster?.config?.schedulerRollingReprov}
          confirmTitle={'Confirm save RollingReprov scheduler to: '}
          onSwitchChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-rolling-reprov' }))
          }
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'scheduler-rolling-reprov-cron',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Schedule rotate SLA',
      value: (
        <Scheduler
          user={user}
          hasSwitch={false}
          value={selectedCluster?.config?.schedulerSlaRotateCron}
          confirmTitle={'Confirm save SlaRotate scheduler to: '}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'scheduler-sla-rotate-cron',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Schedule dbjob SSH',
      value: (
        <Scheduler
          user={user}
          value={selectedCluster?.config?.schedulerJobsSshCron}
          switchConfirmTitle={'Confirm switch settings for scheduler-jobs-ssh?'}
          isSwitchChecked={selectedCluster?.config?.schedulerJobsSsh}
          confirmTitle={'Confirm save JobsSsh scheduler to: '}
          onSwitchChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-jobs-ssh' }))
          }
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'scheduler-jobs-ssh-cron',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Schedule Disable Alerting',
      value: (
        <Scheduler
          user={user}
          value={selectedCluster?.config?.schedulerAlertDisableCron}
          switchConfirmTitle={'Confirm switch settings for scheduler-alert-disable?'}
          isSwitchChecked={selectedCluster?.config?.schedulerAlertDisable}
          confirmTitle={'Confirm save alert disable scheduler to: '}
          onSwitchChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-alert-disable' }))
          }
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'scheduler-alert-disable-cron',
                value: value
              })
            )
          }
        />
      )
    }
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2
        dataArray={dataObject}
        className={styles.table}
        labelClassName={styles.label}
        valueClassName={styles.value}
        rowDivider={true}
        rowClassName={styles.row}
      />
    </Flex>
  )
}

export default SchedulerSettings
