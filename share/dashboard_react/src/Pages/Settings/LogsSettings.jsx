import { Flex } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import LogSlider from '../../components/Sliders/LogSlider'
import RMSwitch from '../../components/RMSwitch'

function LogsSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()

  const {
    settings: {
      verboseLoading,
      logSqlInMonLoading,
      logSysLogLoading,
      logLevelLoading,
      logTaskLoading,
      logWriterEleLoading,
      logSSTLoading,
      logheartbeatLoading,
      logConfigLoadLoading,
      logGitLoading,
      logBackupStrmLoading,
      logOrcheLoading,
      logVaultLoading,
      logTopologyLoading,
      logGraphiteLoading,
      logBinlogLoading,
      logProxyLoading,
      logHAProxyLoading,
      logProxySqlLoading,
      logProxyJanitorLoading,
      logMaxscaleLoading
    }
  } = useSelector((state) => state)

  const dataObject = [
    {
      key: 'Verbose Mode',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for verbose?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'verbose' }))}
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.verbose}
          loading={verboseLoading}
        />
      )
    },
    {
      key: 'Log SQL in Monitoring',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for log-sql-in-monitoring?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'log-sql-in-monitoring' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.logSqlInMonitoring}
          loading={logSqlInMonLoading}
        />
      )
    },
    {
      key: 'Log to SysLog',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for log-syslog?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'log-syslog' }))}
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.logSyslog}
          loading={logSysLogLoading}
        />
      )
    },
    {
      key: 'Log Level',
      value: (
        <LogSlider
          value={selectedCluster?.config?.logLevel}
          loading={logLevelLoading}
          confirmTitle={`Confirm change 'log-level' to: `}
          onChange={(val) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'log-level',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Toggle Log Level Per Module',
      value: [
        {
          key: 'Log DB Jobs',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logTaskLevel}
              loading={logTaskLoading}
              confirmTitle={`Confirm change 'log-task-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-task-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log writer election',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logWriterElectionLevel}
              loading={logWriterEleLoading}
              confirmTitle={`Confirm change 'log-writer-election-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-writer-election-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log SST',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logSstLevel}
              loading={logSSTLoading}
              confirmTitle={`Confirm change 'log-sst-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-sst-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log HeartBeat',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logHeartbeatLevel}
              loading={logheartbeatLoading}
              confirmTitle={`Confirm change 'log-heartbeat-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-heartbeat-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log Config Load',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logConfigLoadLevel}
              loading={logConfigLoadLoading}
              confirmTitle={`Confirm change 'log-config-load-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-config-load-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log GIT',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logGitLevel}
              loading={logGitLoading}
              confirmTitle={`Confirm change 'log-git-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-git-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log Backup Stream',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logBackupStreamLevel}
              loading={logBackupStrmLoading}
              confirmTitle={`Confirm change 'log-backup-stream-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-backup-stream-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log Orchestrator',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logOrchestratorLevel}
              loading={logOrcheLoading}
              confirmTitle={`Confirm change 'log-orchestrator-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-orchestrator-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log Vault',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logVaultLevel}
              loading={logVaultLoading}
              confirmTitle={`Confirm change 'log-vault-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-vault-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log Topology Detection',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logTopologyLevel}
              loading={logTopologyLoading}
              confirmTitle={`Confirm change 'log-topology-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-topology-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log Graphite',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logGraphiteLevel}
              loading={logGraphiteLoading}
              confirmTitle={`Confirm change 'log-graphite-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-graphite-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log Binlog Purge',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logBinlogPurgeLevel}
              loading={logBinlogLoading}
              confirmTitle={`Confirm change 'log-binlog-purge-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-binlog-purge-level',
                    value: val
                  })
                )
              }
            />
          )
        }
      ]
    },

    {
      key: 'Log Proxy',
      value: [
        {
          key: 'Log Proxy',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logProxyLevel}
              loading={logProxyLoading}
              confirmTitle={`Confirm change 'log-proxy-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-proxy-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log HAProxy',
          value: (
            <LogSlider
              value={selectedCluster?.config?.haproxyLogLevel}
              loading={logHAProxyLoading}
              confirmTitle={`Confirm change 'haproxy-log-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'haproxy-log-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log ProxySQL',
          value: (
            <LogSlider
              value={selectedCluster?.config?.proxysqlLogLevel}
              loading={logProxySqlLoading}
              confirmTitle={`Confirm change 'proxysql-log-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'proxysql-log-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log Proxy Janitor',
          value: (
            <LogSlider
              value={selectedCluster?.config?.proxyjanitorLogLevel}
              loading={logProxyJanitorLoading}
              confirmTitle={`Confirm change 'proxyjanitor-log-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'proxyjanitor-log-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log Maxscale',
          value: (
            <LogSlider
              value={selectedCluster?.config?.maxscaleLogLevel}
              loading={logMaxscaleLoading}
              confirmTitle={`Confirm change 'maxscale-log-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'maxscale-log-level',
                    value: val
                  })
                )
              }
            />
          )
        }
      ]
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

export default LogsSettings
