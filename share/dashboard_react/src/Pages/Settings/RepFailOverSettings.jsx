import { Flex } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import RMSwitch from '../../components/RMSwitch'
import RMSlider from '../../components/Sliders/RMSlider'

function RepFailOverSettings({ selectedCluster, user, openConfirmModal, closeConfirmModal }) {
  const dispatch = useDispatch()

  const dataObject = [
    {
      key: 'Failover Limit',
      value: (
        <RMSlider
          value={selectedCluster?.config?.failoverLimit}
          confirmTitle={`Confirm change 'failover-limit' to: `}
          onChange={(val) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'failover-limit',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Checks failover & switchover constraints',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for check-replication-state?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'check-replication-state' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.checkReplicationState}
        />
      )
    },
    {
      key: 'Failover only on semi-sync state is in sync',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for failover-at-sync?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'failover-at-sync' }))}
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.failoverAtSync}
        />
      )
    },
    {
      key: 'Failover unsafe first slave',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for failover-restart-unsafe?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'failover-restart-unsafe' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.failoverRestartUnsafe}
        />
      )
    },
    {
      key: 'Failover using positional replication',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for force-slave-no-gtid-mode?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-no-gtid-mode' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.forceSlaveNoGtidMode}
        />
      )
    },
    {
      key: 'Failover using pseudo GTID',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for autorejoin-slave-positional-heartbeat?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'autorejoin-slave-positional-heartbeat'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.autorejoinSlavePositionalHeartbeat}
        />
      )
    },
    {
      key: 'Capture statistic for hourly delay average',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for delay-stat-capture?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'delay-stat-capture'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.delayStatCapture}
        />
      )
    },
    {
      key: 'Failover check delay statistics',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for failover-check-delay-stat?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'failover-check-delay-stat'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.failoverCheckDelayStat}
        />
      )
    },
    {
      key: 'Delay Statistic Rotate Hours',
      value: (
        <RMSlider
          value={selectedCluster?.config?.delayStatRotate}
          max={72}
          showMarkAtInterval={12}
          confirmTitle='Confirm change delay stat rotate value to: '
          onChange={(val) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'delay-stat-rotate',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Print delay statistic',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for print-delay-stat?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'print-delay-stat'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.printDelayStat}
        />
      )
    },
    {
      key: 'Print delay statistic history',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for print-delay-stat-history?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'print-delay-stat-history'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.printDelayStatHistory}
        />
      )
    },
    {
      key: 'Delay Statistic Print Interval',
      value: (
        <RMSlider
          value={selectedCluster?.config?.printDelayStatInterval}
          max={60}
          showMarkAtInterval={10}
          confirmTitle='Confirm change delay stat rotate value to: '
          onChange={(val) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'print-delay-stat-interval',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Switchover only on semi-sync state in sync',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for switchover-at-sync?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'switchover-at-sync'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.switchoverAtSync}
        />
      )
    },
    {
      key: 'Switchover replication maximum delay',
      value: (
        <RMSlider
          value={selectedCluster?.config?.failoverMaxSlaveDelay}
          max={100}
          showMarkAtInterval={20}
          confirmTitle='Confirm change max delay to: '
          onChange={(val) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'failover-max-slave-delay',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Switchover wait unmanaged proxy monitor detection',
      value: (
        <RMSlider
          value={selectedCluster?.config?.switchoverWaitRouteChange}
          confirmTitle='Confirm change wait change route detection to: '
          onChange={(val) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'switchover-wait-route-change',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Switchover allow on minor release',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for switchover-lower-release?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'switchover-lower-release'
              })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.switchoverLowerRelease}
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

export default RepFailOverSettings
