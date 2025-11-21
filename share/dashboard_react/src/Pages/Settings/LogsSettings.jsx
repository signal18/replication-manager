import { Flex } from '@chakra-ui/react'
import React, { useEffect } from 'react'
import styles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import LogSlider from '../../components/Sliders/LogSlider'
import RMSwitch from '../../components/RMSwitch'

function LogsSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()

  const dataObject = [
    {
      key: 'Verbose Mode',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for verbose?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'verbose' }))}
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.verbose}
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
        />
      )
    },
     {
      key: 'Log SQL in Monitoring',
      value: (
        <LogSlider
          value={selectedCluster?.config?.logSqlLevel}
          confirmTitle={`Confirm change 'log-sql-level' to: `}
          onChange={(val) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'log-sql-level',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Log Level',
      value: (
        <LogSlider
          value={selectedCluster?.config?.logLevel}
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
          key: 'Log Backup Stream',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logBackupStreamLevel}
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
        },
        {
          key: 'Log Backup Archive Level',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logArchiveLevel}
              confirmTitle={`Confirm change 'log-archive-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-archive-level',
                    value: val
                  })
                )
              }
            />
          )
        },
         {
          key: 'Log Fetch Auditlog Level',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logFetchAuditlogLevel}
              confirmTitle={`Confirm change 'log-fetch-auditlog-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-fetch-auditlog-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log Fetch Errorlog Level',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logFetchErrorlogLevel}
              confirmTitle={`Confirm change 'log-fetch-errorlog-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-fetch-errorlog-level',
                    value: val
                  })
                )
              }
            />
          )
        },
         {
          key: 'Log Fetch Slowquery Level',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logFetchSlowqueryLevel}
              confirmTitle={`Confirm change 'log-fetch-slowquery-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-fetch-slowquery-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log DB Optimize Level',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logOptimizeLevel}
              confirmTitle={`Confirm change 'log-optimize-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-optimize-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log Mailer Level',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logMailerLevel}
              confirmTitle={`Confirm change 'log-mailer-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-mailer-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log Support Level',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logSupportLevel}
              confirmTitle={`Confirm change 'log-support-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-support-level',
                    value: val
                  })
                )
              }
            />
          )
        },
        {
          key: 'Log External Script Level',
          value: (
            <LogSlider
              value={selectedCluster?.config?.logExternalScriptLevel}
              confirmTitle={`Confirm change 'log-external-script-level' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-external-script-level',
                    value: val
                  })
                )
              }
            />
          )
        },
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
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
}

export default LogsSettings
