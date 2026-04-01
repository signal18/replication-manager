import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import LogSlider from '../../components/Sliders/LogSlider'
import RMSwitch from '../../components/RMSwitch'
import RMSlider from '../../components/Sliders/RMSlider'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'

function LogsSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  const h = (content, title) => (
    <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />
  )

  const sl = (setting, configKey) => <LogSlider value={selectedCluster?.config?.[configKey]} confirmTitle={`Confirm change '${setting}' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting, value: val }))} />
  const lh = (name, configkey) => `**${name}**\n\nLog verbosity level for this module.\n\n- **0** — disabled\n- **1** — errors only\n- **2** — errors + warnings\n- **3** — informational\n- **4** — debug\n- **5** — trace (very verbose)\n\nConfig: \`${configkey}\``

  const hVerbose = `**Verbose Mode**\n\nGlobal verbose flag. When enabled all modules log at their maximum configured level.\nDisable on production to reduce log volume.\n\nConfig: \`verbose\``
  const hSyslog = `**Log to SysLog**\n\nForwards all log output to the system syslog daemon in addition to the local log file.\n\nConfig: \`log-syslog\``
  const hLogSql = `**Log SQL in Monitoring**\n\nControls verbosity of SQL statements executed during monitoring cycles.\nAt level 4+ every monitoring SQL statement is logged with its result.\n\nConfig: \`log-level-sql\``
  const hLogLevel = `**Log Level**\n\nGlobal log verbosity for all modules not individually configured.\n0 = disabled, 1 = error, 2 = warning, 3 = info, 4 = debug, 5 = trace\n\nConfig: \`log-level\``

  const hRepStatPrint = `**Log Replication Statistics Print**\n\nLogs current replication delay statistics to the log at each monitoring cycle.\n\nConfig: \`print-delay-stat\``
  const hRepStatPrintHistory = `**Log Replication Statistics Print History**\n\nLogs the full delay statistic history to the log. More verbose than Print Replication Statistics.\n\nConfig: \`print-delay-stat-history\``
  const hRepStatPrintInterval = `**Log Replication Statistics Print Interval**\n\nHow often (in seconds) the delay statistics summary is printed when enabled.\nDefault: 60 seconds.\n\nConfig: \`print-delay-stat-interval\``

  const dataObject = [
    { key: 'Verbose Mode', help: h(hVerbose, 'Verbose Mode'), value: (<RMSwitch confirmTitle={'Confirm switch settings for verbose?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'verbose' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.verbose} />) },
    { key: 'Log to SysLog', help: h(hSyslog, 'Log to SysLog'), value: (<RMSwitch confirmTitle={'Confirm switch settings for log-syslog?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'log-syslog' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.logSyslog} />) },
    { key: 'Log SQL in Monitoring', help: h(hLogSql, 'Log SQL in Monitoring'), value: sl('log-level-sql', 'logSqlLevel') },
    { key: 'Log Level', help: h(hLogLevel, 'Log Level'), value: sl('log-level', 'logLevel') },
    {
      key: 'Toggle Log Level Per Module', value: [
        { key: 'Log DB Jobs', help: h(lh('Log DB Jobs', 'log-level-task'), 'Log DB Jobs'), value: sl('log-level-task', 'logTaskLevel') },
        { key: 'Log Writer Election', help: h(lh('Log Writer Election', 'log-level-writer-election'), 'Log Writer Election'), value: sl('log-level-writer-election', 'logWriterElectionLevel') },
        { key: 'Log SST', help: h(lh('Log SST', 'log-level-sst'), 'Log SST'), value: sl('log-level-sst', 'logSstLevel') },
        { key: 'Log HeartBeat', help: h(lh('Log HeartBeat', 'log-level-heartbeat'), 'Log HeartBeat'), value: sl('log-level-heartbeat', 'logHeartbeatLevel') },
        { key: 'Log Config Load', help: h(lh('Log Config Load', 'log-level-config-load'), 'Log Config Load'), value: sl('log-level-config-load', 'logConfigLoadLevel') },
        { key: 'Log Backup Stream', help: h(lh('Log Backup Stream', 'log-level-backup-stream'), 'Log Backup Stream'), value: sl('log-level-backup-stream', 'logBackupStreamLevel') },
        { key: 'Log Orchestrator', help: h(lh('Log Orchestrator', 'log-level-orchestrator'), 'Log Orchestrator'), value: sl('log-level-orchestrator', 'logOrchestratorLevel') },
        { key: 'Log Vault', help: h(lh('Log Vault', 'log-level-vault'), 'Log Vault'), value: sl('log-level-vault', 'logVaultLevel') },
        { key: 'Log Topology Detection', help: h(lh('Log Topology Detection', 'log-level-topology'), 'Log Topology Detection'), value: sl('log-level-topology', 'logTopologyLevel') },
        { key: 'Log Graphite', help: h(lh('Log Graphite', 'log-level-graphite'), 'Log Graphite'), value: sl('log-level-graphite', 'logGraphiteLevel') },
        { key: 'Log Binlog Purge', help: h(lh('Log Binlog Purge', 'log-level-binlog-purge'), 'Log Binlog Purge'), value: sl('log-level-binlog-purge', 'logBinlogPurgeLevel') },
        { key: 'Log Restic', help: h(lh('Log Restic', 'log-level-restic'), 'Log Restic'), value: sl('log-level-restic', 'logResticLevel') },
        { key: 'Log Fetch Audit Log Level', help: h(lh('Log Fetch Audit Log', 'log-level-database-audit'), 'Log Fetch Audit Log'), value: sl('log-level-database-audit', 'logLevelDatabaseAudit') },
        { key: 'Log Fetch Error Log Level', help: h(lh('Log Fetch Error Log', 'log-level-database-errors'), 'Log Fetch Error Log'), value: sl('log-level-database-errors', 'logLevelDatabaseErrors') },
        { key: 'Log Fetch SQL Error Log Level', help: h(lh('Log Fetch SQL Error Log', 'log-level-database-sql-errors'), 'Log Fetch SQL Error Log'), value: sl('log-level-database-sql-errors', 'logLevelDatabaseSqlErrors') },
        { key: 'Log Fetch Slow Query Level', help: h(lh('Log Fetch Slow Query', 'log-level-database-slowquery'), 'Log Fetch Slow Query'), value: sl('log-level-database-slowquery', 'logLevelDatabaseSlowquery') },
        { key: 'Log DB Optimize Level', help: h(lh('Log DB Optimize', 'log-level-database-optimize'), 'Log DB Optimize'), value: sl('log-level-database-optimize', 'logLevelDatabaseOptimize') },
        { key: 'Log Mailer Level', help: h(lh('Log Mailer', 'log-level-mailer'), 'Log Mailer'), value: sl('log-level-mailer', 'logMailerLevel') },
        { key: 'Log Support Level', help: h(lh('Log Support', 'log-level-support'), 'Log Support'), value: sl('log-level-support', 'logSupportLevel') },
        { key: 'Log External Script Level', help: h(lh('Log External Script', 'log-level-external-script'), 'Log External Script'), value: sl('log-level-external-script', 'logExternalScriptLevel') },
      ]
    },
    { key: 'Log Replication Statistics Print', help: h(hRepStatPrint, 'Log Replication Statistics Print'), value: (<RMSwitch confirmTitle={'Confirm switch settings for print-delay-stat?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'print-delay-stat' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.printDelayStat} />) },
    { key: 'Log Replication Statistics Print History', help: h(hRepStatPrintHistory, 'Log Replication Statistics Print History'), value: (<RMSwitch confirmTitle={'Confirm switch settings for print-delay-stat-history?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'print-delay-stat-history' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.printDelayStatHistory} />) },
    { key: 'Log Replication Statistics Print Interval', help: h(hRepStatPrintInterval, 'Log Replication Statistics Print Interval'), value: (<RMSlider value={selectedCluster?.config?.printDelayStatInterval} max={60} showMarkAtInterval={10} confirmTitle='Confirm change replication statistics print interval to: ' onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'print-delay-stat-interval', value: val }))} />) },
        {
      key: 'Log Proxy', value: [
        { key: 'Log Proxy', help: h(lh('Log Proxy', 'log-level-proxy'), 'Log Proxy'), value: sl('log-level-proxy', 'logProxyLevel') },
        { key: 'Log HAProxy', help: h(lh('Log HAProxy', 'log-level-haproxy'), 'Log HAProxy'), value: sl('log-level-haproxy', 'haproxyLogLevel') },
        { key: 'Log ProxySQL', help: h(lh('Log ProxySQL', 'log-level-proxysql'), 'Log ProxySQL'), value: sl('log-level-proxysql', 'proxysqlLogLevel') },
        { key: 'Log Proxy Janitor', help: h(lh('Log Proxy Janitor', 'log-level-proxyjanitor'), 'Log Proxy Janitor'), value: sl('log-level-proxyjanitor', 'proxyjanitorLogLevel') },
        { key: 'Log Maxscale', help: h(lh('Log Maxscale', 'log-level-maxscale'), 'Log Maxscale'), value: sl('log-level-maxscale', 'maxscaleLogLevel') },
      ]
    }
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn={true} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default LogsSettings
