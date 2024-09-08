import { Flex } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import RMSwitch from '../../components/RMSwitch'
import RMSlider from '../../components/Sliders/RMSlider'

function RepConfigSettings({ selectedCluster, user, openConfirmModal, closeConfirmModal }) {
  const dispatch = useDispatch()

  const dataObject = [
    {
      key: 'Enforce read only on replicas',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-slave-readonly?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-slave-readonly'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceSlaveReadonly}
        />
      )
    },
    {
      key: 'Enforce binlog format in row',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-binlog-row?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-binlog-row'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceBinlogRow}
        />
      )
    },
    {
      key: 'Enforce binlog row log with original statement',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-binlog-annotate?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-binlog-annotate'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceBinlogAnnotate}
        />
      )
    },
    {
      key: 'Enforce binlog compression',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-binlog-compress?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-binlog-compress'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceBinlogCompress}
        />
      )
    },
    {
      key: 'Enforce replication queries in slow queries log',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-binlog-compress?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-binlog-compress'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceBinlogSlowqueries}
        />
      )
    },
    {
      key: 'Enforce GTID replication',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-slave-gtid?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-slave-gtid'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceSlaveGtidMode}
        />
      )
    },
    {
      key: 'Enforce replication stop when write on replicas',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-slave-gtid-mode-strict?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-slave-gtid-mode-strict'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceSlaveGtidModeStrict}
        />
      )
    },
    {
      key: 'Enforce replication semi-synchronus',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-slave-semisync?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-slave-semisync'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceSlaveSemisync}
        />
      )
    },
    {
      key: 'Enforce replication mode strict, sql thread error when dataset diverge',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-slave-strict?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-slave-strict'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceSlaveStrict}
        />
      )
    },
    {
      key: 'Enforce replication mode idempotent, sql thread repair based on binlog row image when dataset diverge',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-slave-idempotent?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-slave-idempotent'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceSlaveIdempotent}
        />
      )
    },
    {
      key: 'Enforce replication parallel mode serialized',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-slave-serialized?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-slave-serialized'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'SERIALIZED'}
        />
      )
    },
    {
      key: 'Enforce replication parallel mode minimal',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-slave-minimal?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-slave-minimal'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'MINIMAL'}
        />
      )
    },
    {
      key: 'Enforce replication parallel mode conservative',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-slave-conservative?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-slave-conservative'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'CONSERVATIVE'}
        />
      )
    },
    {
      key: 'Enforce replication parallel mode optimistic',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-slave-optimistic?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-slave-optimistic'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'OPTIMISTIC'}
        />
      )
    },
    {
      key: 'Enforce replication parallel mode aggressive',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-slave-aggressive?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-slave-aggressive'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceSlaveParallelMode?.toUpperCase() == 'AGGRESSIVE'}
        />
      )
    },
    {
      key: 'Enforce replication heartbeat avoid SQL thread disconnect when no writes during full TCP socket timelife',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-slave-heartbeat?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-slave-heartbeat'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceSlaveHeartbeat}
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

export default RepConfigSettings
