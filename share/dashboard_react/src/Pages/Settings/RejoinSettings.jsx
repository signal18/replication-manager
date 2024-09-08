import { Flex } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'

import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { switchSetting } from '../../redux/settingsSlice'

function RejoinSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()

  const {
    settings: {
      arLoading,
      arBackupBinlogLoading,
      arFlashbackOnSyncLoading,
      arFlashbackLoading,
      arMysqldumpLoading,
      arLogicalBackupLoading,
      arPhysicalBackupLoading,
      arForceRestoreLoading,
      autoseedLoading
    }
  } = useSelector((state) => state)

  const dataObject = [
    {
      key: 'Failback',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for autorejoin?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin' }))}
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.autorejoin}
          loading={arLoading}
        />
      )
    },
    {
      key: 'Failback Backup Extra Events',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.autorejoinBackupBinlog}
          isDisabled={user?.grants['cluster-settings'] == false}
          loading={arBackupBinlogLoading}
          confirmTitle={'Confirm switch settings for autorejoin-backup-binlog?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-backup-binlog' }))
          }
        />
      )
    },
    {
      key: 'Failback Fashback when Semi-sync status Sync',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.autorejoinFlashbackOnSync}
          isDisabled={user?.grants['cluster-settings'] == false}
          loading={arFlashbackOnSyncLoading}
          confirmTitle={'Confirm switch settings for autorejoin-flashback-on-sync?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-flashback-on-sync' }))
          }
        />
      )
    },
    {
      key: 'Failback Binlog Flashback',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.autorejoinFlashback}
          isDisabled={user?.grants['cluster-settings'] == false}
          loading={arFlashbackLoading}
          confirmTitle={'Confirm switch settings for autorejoin-flashback?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-flashback' }))
          }
        />
      )
    },
    {
      key: 'Failback Direct Master Dump',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.autorejoinMysqldump}
          isDisabled={user?.grants['cluster-settings'] == false}
          loading={arMysqldumpLoading}
          confirmTitle={'Confirm switch settings for autorejoin-mysqldump?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-mysqldump' }))
          }
        />
      )
    },
    {
      key: 'Failback Via Logical Backup',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.autorejoinLogicalBackup}
          isDisabled={user?.grants['cluster-settings'] == false}
          loading={arLogicalBackupLoading}
          confirmTitle={'Confirm switch settings for autorejoin-logical-backup?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-logical-backup' }))
          }
        />
      )
    },
    {
      key: 'Failback Via Physical Backup',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.autorejoinPhysicalBackup}
          isDisabled={user?.grants['cluster-settings'] == false}
          loading={arPhysicalBackupLoading}
          confirmTitle={'Confirm switch settings for autorejoin-physical-backup?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-physical-backup' }))
          }
        />
      )
    },
    {
      key: 'Force Rejoin With Restore',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.autorejoinForceRestore}
          isDisabled={user?.grants['cluster-settings'] == false}
          loading={arForceRestoreLoading}
          confirmTitle={'Confirm switch settings for autorejoin-force-restore?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-force-restore' }))
          }
        />
      )
    },
    {
      key: 'Auto seed from backup standalone server',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.autoseed}
          isDisabled={user?.grants['cluster-settings'] == false}
          loading={autoseedLoading}
          confirmTitle={'Confirm switch settings for autoseed?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autoseed' }))}
        />
      )
    }
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
}

export default RejoinSettings
