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
          confirmTitle={`Confirm change 'log-level-sql' to: `}
          onChange={(val) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'log-level-sql',
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
              confirmTitle={`Confirm change 'log-level-task' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-task',
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
              confirmTitle={`Confirm change 'log-level-writer-election' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-writer-election',
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
              confirmTitle={`Confirm change 'log-level-sst' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-sst',
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
              confirmTitle={`Confirm change 'log-level-heartbeat' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-heartbeat',
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
              confirmTitle={`Confirm change 'log-level-config-load' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-config-load',
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
              confirmTitle={`Confirm change 'log-level-backup-stream' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-backup-stream',
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
              confirmTitle={`Confirm change 'log-level-orchestrator' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-orchestrator',
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
              confirmTitle={`Confirm change 'log-level-vault' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-vault',
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
              confirmTitle={`Confirm change 'log-level-topology' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-topology',
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
              confirmTitle={`Confirm change 'log-level-graphite' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-graphite',
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
              confirmTitle={`Confirm change 'log-level-binlog-purge' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-binlog-purge',
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
              confirmTitle={`Confirm change 'log-level-archive' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-archive',
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
              value={selectedCluster?.config?.logLevelDatabaseAudit}
              confirmTitle={`Confirm change 'log-level-database-audit' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-database-audit',
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
              value={selectedCluster?.config?.logLevelDatabaseErrors}
              confirmTitle={`Confirm change 'log-level-database-errors' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-database-errors',
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
              value={selectedCluster?.config?.logLevelDatabaseSlowquery}
              confirmTitle={`Confirm change 'log-level-database-slowquery' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-database-slowquery',
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
              value={selectedCluster?.config?.logLevelDatabaseOptimize}
              confirmTitle={`Confirm change 'log-level-database-optimize' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-database-optimize',
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
              confirmTitle={`Confirm change 'log-level-mailer' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-mailer',
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
              confirmTitle={`Confirm change 'log-level-support' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-support',
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
              confirmTitle={`Confirm change 'log-level-external-script' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-external-script',
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
              confirmTitle={`Confirm change 'log-level-proxy' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-proxy',
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
              confirmTitle={`Confirm change 'log-level-haproxy' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-haproxy',
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
              confirmTitle={`Confirm change 'log-level-proxysql' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-proxysql',
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
              confirmTitle={`Confirm change 'log-level-proxyjanitor' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-proxyjanitor',
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
              confirmTitle={`Confirm change 'log-level-maxscale' to: `}
              onChange={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'log-level-maxscale',
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
