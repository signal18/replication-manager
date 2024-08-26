import { Flex } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import RMSwitch from '../../components/RMSwitch'
import RMSlider from '../../components/Sliders/RMSlider'

function ReplicationSettings({ selectedCluster, user, openConfirmModal, closeConfirmModal }) {
  const dispatch = useDispatch()

  const {
    settings: {
      failoverLimitLoading,
      replicationStateLoading,
      failoverAtSyncLoading,
      failoverRestartUnsfLoading,
      forceSlaveNoGtidModeLoading,
      autorejoinLoading,
      delayStatCaptLoading,
      failoverCheckDelayStatLoading,
      delayStatRotateLoading,
      printDelayStatLoading,
      printDelayStatHistLoading,
      printDelayStatInvlLoading,
      switchoverAtSyncLoading,
      failoverMaxSlaveDelayLoading,
      switchoverRouteChngLoading,
      switchoverLowerRlsLoading,
      forceSlaveReadonlyLoading,
      forceBinlogRowLoading,
      forceBinlogAnnoLoading,
      forceBinlogCompLoading,
      forceBinlogSlowquryLoading,
      forceSlaveGtidMdLoading,
      forceSlaveGtidMdStctLoading,
      forceSlaveSemisyncLoading,
      forceSlaveStrictLoading,
      forceSlaveIdemLoading,
      forceSlaveSerializeLoading,
      forceSlaveMinimalLoading,
      forceSlaveConservLoading,
      forceSlaveOptiLoading,
      forceSlaveAggrLoading,
      forceSlaveHrtbtLoading
    }
  } = useSelector((state) => state)

  const dataObject = [
    {
      key: 'Failover Limit',
      value: (
        <RMSlider
          value={selectedCluster?.config?.failoverLimit}
          loading={failoverLimitLoading}
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
          loading={replicationStateLoading}
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
          loading={failoverAtSyncLoading}
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
          loading={failoverRestartUnsfLoading}
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
          loading={forceSlaveNoGtidModeLoading}
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
          loading={autorejoinLoading}
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
          loading={delayStatCaptLoading}
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
          loading={failoverCheckDelayStatLoading}
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
          loading={delayStatRotateLoading}
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
          loading={printDelayStatLoading}
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
          loading={printDelayStatHistLoading}
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
          loading={printDelayStatInvlLoading}
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
          loading={switchoverAtSyncLoading}
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
          loading={failoverMaxSlaveDelayLoading}
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
          loading={switchoverRouteChngLoading}
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
          loading={switchoverLowerRlsLoading}
        />
      )
    },
    {
      key: 'Enforce replication options',
      value: [
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
              loading={forceSlaveReadonlyLoading}
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
              loading={forceBinlogRowLoading}
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
              loading={forceBinlogAnnoLoading}
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
              loading={forceBinlogCompLoading}
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
              loading={forceBinlogSlowquryLoading}
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
              loading={forceSlaveGtidMdLoading}
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
              loading={forceSlaveGtidMdStctLoading}
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
              loading={forceSlaveSemisyncLoading}
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
              loading={forceSlaveStrictLoading}
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
              loading={forceSlaveIdemLoading}
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
              loading={forceSlaveSerializeLoading}
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
              loading={forceSlaveMinimalLoading}
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
              loading={forceSlaveConservLoading}
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
              loading={forceSlaveOptiLoading}
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
              loading={forceSlaveAggrLoading}
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
              loading={forceSlaveHrtbtLoading}
            />
          )
        }
      ]
    }
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
}

export default ReplicationSettings
