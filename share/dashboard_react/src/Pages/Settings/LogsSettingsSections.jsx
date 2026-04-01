import { Flex, Spinner, Text } from '@chakra-ui/react'
import { useDispatch, useSelector } from 'react-redux'
import PropTypes from 'prop-types'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import TableType2 from '../../components/TableType2'
import RMIconButton from '../../components/RMIconButton'
import LogSlider from '../../components/Sliders/LogSlider'
import RMSlider from '../../components/Sliders/RMSlider'
import RMSwitch from '../../components/RMSwitch'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import styles from './styles.module.scss'
import { useEffect, useState } from 'react'
import { clusterService } from '../../services/clusterService'
import Dropdown from '../../components/Dropdown'
import NumberInput from '../../components/NumberInput'

function LogsSettingsSections({ selectedCluster, user, onOpenInfoModal }) {
  const dispatch = useDispatch()
  const baseURL = useSelector((state) => state?.auth?.baseURL || '')

  const h = (content, title) => (
    <RMIconButton
      icon={HiQuestionMarkCircle}
      onClick={() => onOpenInfoModal(title, content)}
      iconFontsize='1rem'
      variant='ghost'
      style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }}
    />
  )

  const sl = (setting, configKey) => (
    <LogSlider
      value={selectedCluster?.config?.[configKey]}
      confirmTitle={`Confirm change '${setting}' to: `}
      onChange={(val) =>
        dispatch(
          setSetting({
            clusterName: selectedCluster?.name,
            setting,
            value: val
          })
        )
      }
    />
  )

  const lh = (name, configkey) => `**${name}**\n\nLog verbosity level for this module.\n\n- **0** — disabled\n- **1** — errors only\n- **2** — errors + warnings\n- **3** — informational\n- **4** — debug\n- **5** — trace (very verbose)\n\nConfig: \`${configkey}\``

  const hVerbose = `**Verbose Mode**\n\nGlobal verbose flag. When enabled all modules log at their maximum configured level.\nDisable on production to reduce log volume.\n\nConfig: \`verbose\``
  const hSyslog = `**Log to SysLog**\n\nForwards all log output to the system syslog daemon in addition to the local log file.\n\nConfig: \`log-syslog\``
  const hLogSql = `**Log SQL in Monitoring**\n\nControls verbosity of SQL statements executed during monitoring cycles.\nAt level 4+ every monitoring SQL statement is logged with its result.\n\nConfig: \`log-level-sql\``
  const hLogLevel = `**Log Level**\n\nGlobal log verbosity for all modules not individually configured.\n0 = disabled, 1 = error, 2 = warning, 3 = info, 4 = debug, 5 = trace\n\nConfig: \`log-level\``

  const hRepStatPrint = `**Log Replication Statistics Print**\n\nLogs current replication delay statistics to the log at each monitoring cycle.\n\nConfig: \`print-delay-stat\``
  const hRepStatPrintHistory = `**Log Replication Statistics Print History**\n\nLogs the full delay statistic history to the log. More verbose than Print Replication Statistics.\n\nConfig: \`print-delay-stat-history\``
  const hRepStatPrintInterval = `**Log Replication Statistics Print Interval**\n\nHow often (in seconds) the delay statistics summary is printed when enabled.\nDefault: 60 seconds.\n\nConfig: \`print-delay-stat-interval\``

  const [plugins, setPlugins] = useState([])
  const [pluginsLoading, setPluginsLoading] = useState(false)

  useEffect(() => {
    if (!selectedCluster?.name) return
    setPluginsLoading(true)
    clusterService
      .getClusterPlugins(selectedCluster.name, baseURL)
      .then(({ data }) => setPlugins(Array.isArray(data) ? data : []))
      .catch(() => setPlugins([]))
      .finally(() => setPluginsLoading(false))
  }, [selectedCluster?.name, selectedCluster?.config?.logPlugin])

  // Build a flat list of sub-rows for the "Log Plugins" section.
  // TableType2 supports exactly two levels: top-level items and one level
  // of sub-items. Every entry here must have value = ReactElement.
  const buildPluginSubRows = () => {
    const rows = [
      {
        key: 'Enable Log Plugins',
        value: (
          <RMSwitch
            confirmTitle={'Confirm switch settings for log-plugin?'}
            onChange={() =>
              dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'log-plugin' }))
            }
            isDisabled={user?.grants['cluster-settings'] == false}
            isChecked={selectedCluster?.config?.logPlugin}
          />
        )
      },
      {
        key: 'Plugin Module Verbosity',
        value: (
          <LogSlider
            value={selectedCluster?.config?.logPluginLevel}
            confirmTitle={`Confirm change 'log-level-plugin' to: `}
            onChange={(val) =>
              dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-plugin', value: val }))
            }
          />
        )
      }
    ]

    if (pluginsLoading) {
      rows.push({ key: 'Plugins', value: <Spinner size='sm' /> })
      return rows
    }

    if (plugins.length === 0) {
      rows.push({
        key: 'Plugins',
        value: <Text fontSize='sm' color='gray.400'>No plugins loaded — enable log-plugin and restart</Text>
      })
      return rows
    }

    // One row per plugin config key — flat, no nested arrays
    plugins.forEach((plugin) => {
      // Plugin name header row with enabled toggle
      const isPluginEnabled = plugin.config?.['enabled'] !== 'false'
      rows.push({
        key: (
          <Text fontWeight='semibold' fontSize='sm' color='blue.300'>
            {plugin.name}
          </Text>
        ),
        value: (
          <RMSwitch
            confirmTitle={`Confirm ${isPluginEnabled ? 'disable' : 'enable'} plugin '${plugin.name}'?`}
            onChange={() =>
              dispatch(setSetting({
                clusterName: selectedCluster?.name,
                setting: `plugin-config-${plugin.name}-enabled`,
                value: isPluginEnabled ? 'false' : 'true'
              }))
            }
            isDisabled={user?.grants['cluster-settings'] == false}
            isChecked={isPluginEnabled}
          />
        )
      })

      // One control row per config key
      pluginKnownKeys(plugin.name).forEach((key) => {
        const currentRaw = plugin.config?.[key] ?? pluginKeyDefault(plugin.name, key)

        let control
        if (key === 'min-log-level') {
          const levelOptions = [
            { value: 'System',  label: 'System — startup/shutdown only' },
            { value: 'Note',    label: 'Note — informational+' },
            { value: 'Warning', label: 'Warning — warnings + errors (default)' },
            { value: 'ERROR',   label: 'ERROR — errors only' },
          ]
          control = (
            <Dropdown
              options={levelOptions}
              selectedValue={currentRaw}
              isDisabled={user?.grants['cluster-settings'] == false}
              confirmTitle={`Confirm min-log-level for '${plugin.name}': `}
              onChange={(opt) =>
                dispatch(setSetting({
                  clusterName: selectedCluster?.name,
                  setting: `plugin-config-${plugin.name}-min-log-level`,
                  value: opt.value
                }))
              }
            />
          )
        } else {
          control = (
            <NumberInput
              min={key === 'spike-sigma' ? 0.5 : 1}
              max={key === 'spike-sigma' ? 10 : 8760}
              step={key === 'spike-sigma' ? 0.5 : 1}
              value={key === 'spike-sigma' ? parseFloat(currentRaw) : parseInt(currentRaw, 10)}
              isDisabled={user?.grants['cluster-settings'] == false}
              showConfirmModal={true}
              confirmTitle={`Confirm change '${key}' for '${plugin.name}' to: `}
              onConfirm={(val) =>
                dispatch(setSetting({
                  clusterName: selectedCluster?.name,
                  setting: `plugin-config-${plugin.name}-${key}`,
                  value: String(val)
                }))
              }
            />
          )
        }
        rows.push({
          key: `  ${pluginKeyLabel(plugin.name, key)}`,
          value: control
        })
      })
    })

        return rows
  }


  const dataObject = [
    {
      key: 'Global',
      value: [
        {
          key: 'Verbose Mode',
          help: h(hVerbose, 'Verbose Mode'),
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
          help: h(hSyslog, 'Log to SysLog'),
          value: (
            <RMSwitch
              confirmTitle={'Confirm switch settings for log-syslog?'}
              onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'log-syslog' }))}
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={selectedCluster?.config?.logSyslog}
            />
          )
        },
        { key: 'Log Level', help: h(hLogLevel, 'Log Level'), value: sl('log-level', 'logLevel') }
      ]
    },
    {
      key: 'Monitoring SQL',
      value: [
        { key: 'Log SQL in Monitoring', help: h(hLogSql, 'Log SQL in Monitoring'), value: sl('log-level-sql', 'logSqlLevel') }
      ]
    },
    {
      key: 'Log Plugins',
      value: buildPluginSubRows()
    },
    {
      key: 'Replication Statistics',
      value: [
        {
          key: 'Log Replication Statistics Print',
          help: h(hRepStatPrint, 'Log Replication Statistics Print'),
          value: (
            <RMSwitch
              confirmTitle={'Confirm switch settings for print-delay-stat?'}
              onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'print-delay-stat' }))}
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={selectedCluster?.config?.printDelayStat}
            />
          )
        },
        {
          key: 'Log Replication Statistics Print History',
          help: h(hRepStatPrintHistory, 'Log Replication Statistics Print History'),
          value: (
            <RMSwitch
              confirmTitle={'Confirm switch settings for print-delay-stat-history?'}
              onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'print-delay-stat-history' }))}
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={selectedCluster?.config?.printDelayStatHistory}
            />
          )
        },
        {
          key: 'Log Replication Statistics Print Interval',
          help: h(hRepStatPrintInterval, 'Log Replication Statistics Print Interval'),
          value: (
            <RMSlider
              value={selectedCluster?.config?.printDelayStatInterval}
              max={60}
              showMarkAtInterval={10}
              confirmTitle='Confirm change replication statistics print interval to: '
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
        }
      ]
    },
    {
      key: 'Cluster Operations',
      value: [
        { key: 'Log DB Jobs', help: h(lh('Log DB Jobs', 'log-level-task'), 'Log DB Jobs'), value: sl('log-level-task', 'logTaskLevel') },
        {
          key: 'Log Writer Election',
          help: h(lh('Log Writer Election', 'log-level-writer-election'), 'Log Writer Election'),
          value: sl('log-level-writer-election', 'logWriterElectionLevel')
        },
        { key: 'Log SST', help: h(lh('Log SST', 'log-level-sst'), 'Log SST'), value: sl('log-level-sst', 'logSstLevel') },
        {
          key: 'Log Binlog Purge',
          help: h(lh('Log Binlog Purge', 'log-level-binlog-purge'), 'Log Binlog Purge'),
          value: sl('log-level-binlog-purge', 'logBinlogPurgeLevel')
        }
      ]
    },
    {
      key: 'Core Monitoring',
      value: [
        {
          key: 'Log HeartBeat',
          help: h(lh('Log HeartBeat', 'log-level-heartbeat'), 'Log HeartBeat'),
          value: sl('log-level-heartbeat', 'logHeartbeatLevel')
        },
        {
          key: 'Log Config Load',
          help: h(lh('Log Config Load', 'log-level-config-load'), 'Log Config Load'),
          value: sl('log-level-config-load', 'logConfigLoadLevel')
        },
        {
          key: 'Log Orchestrator',
          help: h(lh('Log Orchestrator', 'log-level-orchestrator'), 'Log Orchestrator'),
          value: sl('log-level-orchestrator', 'logOrchestratorLevel')
        },
        {
          key: 'Log Topology Detection',
          help: h(lh('Log Topology Detection', 'log-level-topology'), 'Log Topology Detection'),
          value: sl('log-level-topology', 'logTopologyLevel')
        }
      ]
    },
    {
      key: 'Backup & Restore',
      value: [
        {
          key: 'Log Backup Stream',
          help: h(lh('Log Backup Stream', 'log-level-backup-stream'), 'Log Backup Stream'),
          value: sl('log-level-backup-stream', 'logBackupStreamLevel')
        },
        {
          key: 'Log Restic',
          help: h(lh('Log Restic', 'log-level-restic'), 'Log Restic'),
          value: sl('log-level-restic', 'logResticLevel')
        },
        {
          key: 'Log Vault',
          help: h(lh('Log Vault', 'log-level-vault'), 'Log Vault'),
          value: sl('log-level-vault', 'logVaultLevel')
        }
      ]
    },
    {
      key: 'Database Log Collection',
      value: [
        {
          key: 'Log Fetch Audit Log',
          help: h(lh('Log Fetch Audit Log', 'log-level-database-audit'), 'Log Fetch Audit Log'),
          value: sl('log-level-database-audit', 'logLevelDatabaseAudit')
        },
        {
          key: 'Log Fetch Error Log',
          help: h(lh('Log Fetch Error Log', 'log-level-database-errors'), 'Log Fetch Error Log'),
          value: sl('log-level-database-errors', 'logLevelDatabaseErrors')
        },
        {
          key: 'Log Fetch SQL Error Log',
          help: h(lh('Log Fetch SQL Error Log', 'log-level-database-sql-errors'), 'Log Fetch SQL Error Log'),
          value: sl('log-level-database-sql-errors', 'logLevelDatabaseSqlErrors')
        },
        {
          key: 'Log Fetch Slow Query',
          help: h(lh('Log Fetch Slow Query', 'log-level-database-slowquery'), 'Log Fetch Slow Query'),
          value: sl('log-level-database-slowquery', 'logLevelDatabaseSlowquery')
        },
        {
          key: 'Log DB Optimize',
          help: h(lh('Log DB Optimize', 'log-level-database-optimize'), 'Log DB Optimize'),
          value: sl('log-level-database-optimize', 'logLevelDatabaseOptimize')
        }
      ]
    },
    {
      key: 'External Integrations',
      value: [
        {
          key: 'Log Graphite',
          help: h(lh('Log Graphite', 'log-level-graphite'), 'Log Graphite'),
          value: sl('log-level-graphite', 'logGraphiteLevel')
        },
        {
          key: 'Log Mailer',
          help: h(lh('Log Mailer', 'log-level-mailer'), 'Log Mailer'),
          value: sl('log-level-mailer', 'logMailerLevel')
        },
        {
          key: 'Log External Script',
          help: h(lh('Log External Script', 'log-level-external-script'), 'Log External Script'),
          value: sl('log-level-external-script', 'logExternalScriptLevel')
        },
        {
          key: 'Log Support',
          help: h(lh('Log Support', 'log-level-support'), 'Log Support'),
          value: sl('log-level-support', 'logSupportLevel')
        }
      ]
    },
    {
      key: 'Proxies',
      value: [
        { key: 'Log Proxy', help: h(lh('Log Proxy', 'log-level-proxy'), 'Log Proxy'), value: sl('log-level-proxy', 'logProxyLevel') },
        { key: 'Log HAProxy', help: h(lh('Log HAProxy', 'log-level-haproxy'), 'Log HAProxy'), value: sl('log-level-haproxy', 'haproxyLogLevel') },
        { key: 'Log ProxySQL', help: h(lh('Log ProxySQL', 'log-level-proxysql'), 'Log ProxySQL'), value: sl('log-level-proxysql', 'proxysqlLogLevel') },
        {
          key: 'Log Proxy Janitor',
          help: h(lh('Log Proxy Janitor', 'log-level-proxyjanitor'), 'Log Proxy Janitor'),
          value: sl('log-level-proxyjanitor', 'proxyjanitorLogLevel')
        },
        { key: 'Log Maxscale', help: h(lh('Log Maxscale', 'log-level-maxscale'), 'Log Maxscale'), value: sl('log-level-maxscale', 'maxscaleLogLevel') }
      ]
    }
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn={true} />
    </Flex>
  )
}

// ---- Plugin config metadata -------------------------------------------------

function pluginKnownKeys(pluginName) {
  switch (pluginName) {
    case 'errorlog':
      return ['timeframe-hours', 'min-log-level', 'spike-sigma']
    case 'sqlerrorlog':
    case 'slowlog':
      return ['timeframe-hours', 'spike-sigma']
    case 'auditlog':
      return ['current-window-hours', 'baseline-window-hours', 'spike-sigma']
    default:
      return ['spike-sigma']
  }
}

function pluginKeyLabel(pluginName, key) {
  const labels = {
    'timeframe-hours': 'Timeframe (hours)',
    'current-window-hours': 'Current window (hours)',
    'baseline-window-hours': 'Baseline window (hours)',
    'spike-sigma': 'Spike threshold (σ)',
    'min-log-level': 'Min log level (errorlog only)'
  }
  return labels[key] || key
}

function pluginKeyDefault(pluginName, key) {
  if (pluginName === 'auditlog') {
    if (key === 'current-window-hours') return '1'
    if (key === 'baseline-window-hours') return '24'
  }
  if (key === 'spike-sigma') return '2'
  if (key === 'min-log-level') return 'Warning'
  return '24'
}

LogsSettingsSections.propTypes = {
  selectedCluster: PropTypes.object,
  user: PropTypes.object,
  onOpenInfoModal: PropTypes.func
}

export default LogsSettingsSections
