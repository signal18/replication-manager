import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import LogSlider from '../../components/Sliders/LogSlider'
import RMSwitch from '../../components/RMSwitch'
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

  const helpKey = (label, content) => (
    <Box as="span" display="inline">
      {label}
      <Box as="span" display="inline-flex" verticalAlign="middle" ml={1}>
        <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(label, content)} />
      </Box>
    </Box>
  )

  const sliderHelp = (name, setting) => `**${name}**\n\nLog verbosity level for the **${setting}** module.\n\n- **0** — disabled\n- **1** — errors only\n- **2** — errors + warnings\n- **3** — informational\n- **4** — debug (verbose)\n- **5** — trace (very verbose, high volume)`

  const helpVerbose = `**Verbose Mode**\n\nGlobal verbose flag. When enabled, all modules log at their maximum configured level.\nEquivalent to setting every module log level to debug.\nDisable on production to reduce log volume.`
  const helpSyslog = `**Log to SysLog**\n\nWhen enabled, all log output is forwarded to the system syslog daemon in addition to the local log file.\nUseful for centralised log aggregation with rsyslog or journald.`
  const helpLogSql = `**Log SQL in Monitoring**\n\nControls verbosity of SQL statements executed during monitoring cycles.\nIncrease to debug slow or failing monitoring queries.\nAt level 4+ every monitoring SQL statement is logged with its result.`
  const helpLogLevel = `**Log Level**\n\nGlobal log verbosity for all modules not individually configured.\n\n- 0 = disabled, 1 = error, 2 = warning, 3 = info, 4 = debug, 5 = trace`

  const dataObject = [
    { key: helpKey('Verbose Mode', helpVerbose), value: (<RMSwitch confirmTitle={'Confirm switch settings for verbose?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'verbose' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.verbose} />) },
    { key: helpKey('Log to SysLog', helpSyslog), value: (<RMSwitch confirmTitle={'Confirm switch settings for log-syslog?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'log-syslog' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.logSyslog} />) },
    { key: helpKey('Log SQL in Monitoring', helpLogSql), value: (<LogSlider value={selectedCluster?.config?.logSqlLevel} confirmTitle={`Confirm change 'log-level-sql' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-sql', value: val }))} />) },
    { key: helpKey('Log Level', helpLogLevel), value: (<LogSlider value={selectedCluster?.config?.logLevel} confirmTitle={`Confirm change 'log-level' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level', value: val }))} />) },
    {
      key: 'Toggle Log Level Per Module',
      value: [
        { key: helpKey('Log DB Jobs', sliderHelp('Log DB Jobs', 'log-level-task')), value: (<LogSlider value={selectedCluster?.config?.logTaskLevel} confirmTitle={`Confirm change 'log-level-task' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-task', value: val }))} />) },
        { key: helpKey('Log Writer Election', sliderHelp('Log Writer Election', 'log-level-writer-election')), value: (<LogSlider value={selectedCluster?.config?.logWriterElectionLevel} confirmTitle={`Confirm change 'log-level-writer-election' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-writer-election', value: val }))} />) },
        { key: helpKey('Log SST', sliderHelp('Log SST', 'log-level-sst')), value: (<LogSlider value={selectedCluster?.config?.logSstLevel} confirmTitle={`Confirm change 'log-level-sst' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-sst', value: val }))} />) },
        { key: helpKey('Log HeartBeat', sliderHelp('Log HeartBeat', 'log-level-heartbeat')), value: (<LogSlider value={selectedCluster?.config?.logHeartbeatLevel} confirmTitle={`Confirm change 'log-level-heartbeat' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-heartbeat', value: val }))} />) },
        { key: helpKey('Log Config Load', sliderHelp('Log Config Load', 'log-level-config-load')), value: (<LogSlider value={selectedCluster?.config?.logConfigLoadLevel} confirmTitle={`Confirm change 'log-level-config-load' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-config-load', value: val }))} />) },
        { key: helpKey('Log Backup Stream', sliderHelp('Log Backup Stream', 'log-level-backup-stream')), value: (<LogSlider value={selectedCluster?.config?.logBackupStreamLevel} confirmTitle={`Confirm change 'log-level-backup-stream' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-backup-stream', value: val }))} />) },
        { key: helpKey('Log Orchestrator', sliderHelp('Log Orchestrator', 'log-level-orchestrator')), value: (<LogSlider value={selectedCluster?.config?.logOrchestratorLevel} confirmTitle={`Confirm change 'log-level-orchestrator' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-orchestrator', value: val }))} />) },
        { key: helpKey('Log Vault', sliderHelp('Log Vault', 'log-level-vault')), value: (<LogSlider value={selectedCluster?.config?.logVaultLevel} confirmTitle={`Confirm change 'log-level-vault' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-vault', value: val }))} />) },
        { key: helpKey('Log Topology Detection', sliderHelp('Log Topology Detection', 'log-level-topology')), value: (<LogSlider value={selectedCluster?.config?.logTopologyLevel} confirmTitle={`Confirm change 'log-level-topology' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-topology', value: val }))} />) },
        { key: helpKey('Log Graphite', sliderHelp('Log Graphite', 'log-level-graphite')), value: (<LogSlider value={selectedCluster?.config?.logGraphiteLevel} confirmTitle={`Confirm change 'log-level-graphite' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-graphite', value: val }))} />) },
        { key: helpKey('Log Binlog Purge', sliderHelp('Log Binlog Purge', 'log-level-binlog-purge')), value: (<LogSlider value={selectedCluster?.config?.logBinlogPurgeLevel} confirmTitle={`Confirm change 'log-level-binlog-purge' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-binlog-purge', value: val }))} />) },
        { key: helpKey('Log Restic', sliderHelp('Log Restic', 'log-level-restic')), value: (<LogSlider value={selectedCluster?.config?.logResticLevel} confirmTitle={`Confirm change 'log-level-restic' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-restic', value: val }))} />) },
        { key: helpKey('Log Fetch Audit Log Level', sliderHelp('Log Fetch Audit Log', 'log-level-database-audit')), value: (<LogSlider value={selectedCluster?.config?.logLevelDatabaseAudit} confirmTitle={`Confirm change 'log-level-database-audit' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-database-audit', value: val }))} />) },
        { key: helpKey('Log Fetch Error Log Level', sliderHelp('Log Fetch Error Log', 'log-level-database-errors')), value: (<LogSlider value={selectedCluster?.config?.logLevelDatabaseErrors} confirmTitle={`Confirm change 'log-level-database-errors' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-database-errors', value: val }))} />) },
        { key: helpKey('Log Fetch SQL Error Log Level', sliderHelp('Log Fetch SQL Error Log', 'log-level-database-sql-errors')), value: (<LogSlider value={selectedCluster?.config?.logLevelDatabaseSqlErrors} confirmTitle={`Confirm change 'log-level-database-sql-errors' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-database-sql-errors', value: val }))} />) },
        { key: helpKey('Log Fetch Slow Query Level', sliderHelp('Log Fetch Slow Query', 'log-level-database-slowquery')), value: (<LogSlider value={selectedCluster?.config?.logLevelDatabaseSlowquery} confirmTitle={`Confirm change 'log-level-database-slowquery' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-database-slowquery', value: val }))} />) },
        { key: helpKey('Log DB Optimize Level', sliderHelp('Log DB Optimize', 'log-level-database-optimize')), value: (<LogSlider value={selectedCluster?.config?.logLevelDatabaseOptimize} confirmTitle={`Confirm change 'log-level-database-optimize' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-database-optimize', value: val }))} />) },
        { key: helpKey('Log Mailer Level', sliderHelp('Log Mailer', 'log-level-mailer')), value: (<LogSlider value={selectedCluster?.config?.logMailerLevel} confirmTitle={`Confirm change 'log-level-mailer' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-mailer', value: val }))} />) },
        { key: helpKey('Log Support Level', sliderHelp('Log Support', 'log-level-support')), value: (<LogSlider value={selectedCluster?.config?.logSupportLevel} confirmTitle={`Confirm change 'log-level-support' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-support', value: val }))} />) },
        { key: helpKey('Log External Script Level', sliderHelp('Log External Script', 'log-level-external-script')), value: (<LogSlider value={selectedCluster?.config?.logExternalScriptLevel} confirmTitle={`Confirm change 'log-level-external-script' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-external-script', value: val }))} />) },
      ]
    },
    {
      key: 'Log Proxy',
      value: [
        { key: helpKey('Log Proxy', sliderHelp('Log Proxy', 'log-level-proxy')), value: (<LogSlider value={selectedCluster?.config?.logProxyLevel} confirmTitle={`Confirm change 'log-level-proxy' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-proxy', value: val }))} />) },
        { key: helpKey('Log HAProxy', sliderHelp('Log HAProxy', 'log-level-haproxy')), value: (<LogSlider value={selectedCluster?.config?.haproxyLogLevel} confirmTitle={`Confirm change 'log-level-haproxy' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-haproxy', value: val }))} />) },
        { key: helpKey('Log ProxySQL', sliderHelp('Log ProxySQL', 'log-level-proxysql')), value: (<LogSlider value={selectedCluster?.config?.proxysqlLogLevel} confirmTitle={`Confirm change 'log-level-proxysql' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-proxysql', value: val }))} />) },
        { key: helpKey('Log Proxy Janitor', sliderHelp('Log Proxy Janitor', 'log-level-proxyjanitor')), value: (<LogSlider value={selectedCluster?.config?.proxyjanitorLogLevel} confirmTitle={`Confirm change 'log-level-proxyjanitor' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-proxyjanitor', value: val }))} />) },
        { key: helpKey('Log Maxscale', sliderHelp('Log Maxscale', 'log-level-maxscale')), value: (<LogSlider value={selectedCluster?.config?.maxscaleLogLevel} confirmTitle={`Confirm change 'log-level-maxscale' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-maxscale', value: val }))} />) },
      ]
    }
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.table} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default LogsSettings
