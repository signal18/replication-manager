import { Flex } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'

import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { switchSetting } from '../../redux/settingsSlice'
import Scheduler from '../../components/Scheduler'

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
      value: <Scheduler value={selectedCluster?.config?.schedulerDbServersLogicalBackupCron} />
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
