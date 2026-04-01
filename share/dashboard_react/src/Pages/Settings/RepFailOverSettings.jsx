import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import RMSwitch from '../../components/RMSwitch'
import RMSlider from '../../components/Sliders/RMSlider'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'

function RepFailOverSettings({ selectedCluster, user, openConfirmModal, closeConfirmModal }) {
  const dispatch = useDispatch()
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  const h = (content, title) => <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />
  const sw = (setting, configKey) => <RMSwitch confirmTitle={`Confirm switch settings for ${setting}?`} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.[configKey]} />
  const sl = (setting, configKey, min, max, interval, title) => <RMSlider value={selectedCluster?.config?.[configKey]} min={min} max={max} showMarkAtInterval={interval} confirmTitle={`Confirm change '${setting}' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting, value: val }))} />

  const hLimit = `**Failover Limit**\n\nMaximum number of automatic failovers allowed before replication-manager stops.\nSet to 0 for unlimited. Manual intervention required after the limit is reached.\n\nConfig: \`failover-limit\``
  const hCheck = `**Checks Failover & Switchover Constraints**\n\nVerifies all safety constraints before executing: lag threshold, semi-sync state, and absence of long-running writes.\nDisabling forces failover even when constraints are not met.\n\nConfig: \`check-replication-state\``
  const hAtSync = `**Failover Only on Semi-Sync State Sync**\n\nRestricts automatic failover to situations where the cluster was in a fully acknowledged semi-synchronous state.\nPrevents failover when a replica may be missing the last committed transactions.\n\nConfig: \`failover-at-sync\``
  const hDivergent = `**Failover Enable on Divergent Data**\n\nAllows failover even when replica datasets are known to diverge from the master.\nOnly enable if availability takes priority over data consistency.\n\nConfig: \`failover-divergent-data\``
  const hUnsafe = `**Failover Unsafe First Slave**\n\nAllows promoting a replica even if it has not fully applied all relay log events.\nMay result in lost transactions. Use with caution.\n\nConfig: \`failover-restart-unsafe\``
  const hPositional = `**Failover Using Positional Replication**\n\nForces failover to use binlog file/position-based replication instead of GTID.\nUse on older MySQL setups that do not support GTID.\n\nConfig: \`force-slave-no-gtid-mode\``
  const hPseudoGTID = `**Failover Using Pseudo GTID**\n\nUses positional heartbeat events to simulate GTID-like failover on servers without native GTID support.\n\nConfig: \`autorejoin-slave-positional-heartbeat\``
  const hDelayCapture = `**Capture Statistics for Hourly Delay Average**\n\nEnables collection of replication lag measurements averaged per hour.\nRequired for Failover Candidate Rate Using Statistics to function.\n\nConfig: \`delay-stat-capture\``
  const hDelayCheck = `**Failover Candidate Rate Using Statistics**\n\nUses hourly delay averages to gate failover decisions.\nPrevents promoting a replica that has been consistently slow.\n\nConfig: \`failover-check-delay-stat\``
  const hDelayRotate = `**Delay Statistic Rotate Hours**\n\nNumber of hours of delay statistics to retain. Default: 24 hours. Maximum: 72 hours.\n\nConfig: \`delay-stat-rotate\``
  const hPrintStat = `**Print Delay Statistic**\n\nLogs current replication delay statistics at each monitoring cycle.\n\nConfig: \`print-delay-stat\``
  const hPrintHistory = `**Print Delay Statistic History**\n\nLogs the full delay statistic history. More verbose than Print Delay Statistic.\n\nConfig: \`print-delay-stat-history\``
  const hPrintInterval = `**Delay Statistic Print Interval**\n\nHow often (in seconds) the delay statistics summary is printed when enabled. Default: 60 seconds.\n\nConfig: \`print-delay-stat-interval\``
  const hSwitchSync = `**Switchover Only on Semi-Sync State Sync**\n\nRestricts switchover to situations where semi-synchronous replication is fully acknowledged.\nEnsures zero data loss during switchover.\n\nConfig: \`switchover-at-sync\``
  const hSwitchLock = `**Switchover Lock Users on Freeze Workload**\n\nTemporarily locks all user accounts on the current master during switchover to drain active connections.\nEnsures a clean traffic cutover with no in-flight writes at the moment of promotion.\n\nConfig: \`switchover-lock-user-on-freeze\``
  const hMaxDelay = `**Switchover Replication Maximum Delay**\n\nMaximum replication lag (seconds) a replica may have to be a valid switchover target.\nDefault: 30 seconds.\n\nConfig: \`failover-max-slave-delay\``
  const hWaitRoute = `**Switchover Wait Unmanaged Proxy Monitor**\n\nSeconds to wait after switchover for unmanaged proxies to detect the new master. Default: 1 second.\n\nConfig: \`switchover-wait-route-change\``
  const hMinorRelease = `**Switchover Allow on Minor Release**\n\nAllows switchover when the candidate replica runs a different minor version than the master.\nEnable during rolling upgrades.\n\nConfig: \`switchover-lower-release\``

  const dataObject = [
    { key: 'Failover Limit', help: h(hLimit, 'Failover Limit'), value: (<RMSlider value={selectedCluster?.config?.failoverLimit} confirmTitle={`Confirm change 'failover-limit' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'failover-limit', value: val }))} />) },
    { key: 'Checks Failover & Switchover Constraints', help: h(hCheck, 'Checks Failover & Switchover Constraints'), value: sw('check-replication-state', 'checkReplicationState') },
    { key: 'Failover Only on Semi-Sync State Sync', help: h(hAtSync, 'Failover Only on Semi-Sync State Sync'), value: sw('failover-at-sync', 'failoverAtSync') },
    { key: 'Failover Enable on Divergent Data', help: h(hDivergent, 'Failover Enable on Divergent Data'), value: sw('failover-divergent-data', 'failoverDivergentData') },
    { key: 'Failover Unsafe First Slave', help: h(hUnsafe, 'Failover Unsafe First Slave'), value: sw('failover-restart-unsafe', 'failoverRestartUnsafe') },
    { key: 'Failover Using Positional Replication', help: h(hPositional, 'Failover Using Positional Replication'), value: sw('force-slave-no-gtid-mode', 'forceSlaveNoGtidMode') },
    { key: 'Failover Using Pseudo GTID', help: h(hPseudoGTID, 'Failover Using Pseudo GTID'), value: sw('autorejoin-slave-positional-heartbeat', 'autorejoinSlavePositionalHeartbeat') },
    { key: 'Failover Candidate Rate Using Statistics', help: h(hDelayCheck, 'Failover Candidate Rate Using Statistics'), value: sw('failover-check-delay-stat', 'failoverCheckDelayStat') },
    { key: 'Switchover Only on Semi-Sync State Sync', help: h(hSwitchSync, 'Switchover Only on Semi-Sync State Sync'), value: sw('switchover-at-sync', 'switchoverAtSync') },
    { key: 'Switchover Lock Users on Freeze Workload', help: h(hSwitchLock, 'Switchover Lock Users on Freeze Workload'), value: sw('switchover-lock-user-on-freeze', 'switchLockUserOnFreeze') },
    { key: 'Switchover Replication Maximum Delay', help: h(hMaxDelay, 'Switchover Replication Maximum Delay'), value: (<RMSlider value={selectedCluster?.config?.failoverMaxSlaveDelay} max={100} showMarkAtInterval={20} confirmTitle='Confirm change max delay to: ' onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'failover-max-slave-delay', value: val }))} />) },
    { key: 'Switchover Wait Unmanaged Proxy Monitor', help: h(hWaitRoute, 'Switchover Wait Unmanaged Proxy Monitor'), value: (<RMSlider value={selectedCluster?.config?.switchoverWaitRouteChange} confirmTitle='Confirm change wait change route detection to: ' onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'switchover-wait-route-change', value: val }))} />) },
    { key: 'Switchover Allow on Minor Release', help: h(hMinorRelease, 'Switchover Allow on Minor Release'), value: sw('switchover-lower-release', 'switchoverLowerRelease') },
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

export default RepFailOverSettings
