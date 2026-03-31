import { Box, Flex, HStack, Text } from '@chakra-ui/react'
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

  const helpKey = (label, content) => (
    <HStack spacing={1} align="center" width="fit-content">
      <Text>{label}</Text>
      <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(label, content)} />
    </HStack>
  )

  const helpFailoverLimit = `**Failover Limit**\n\nMaximum number of automatic failovers allowed before replication-manager stops attempting further failovers.\nSet to 0 for unlimited.\nAfter the limit is reached, manual intervention is required to reset the counter and re-enable automatic failover.`
  const helpCheckState = `**Checks Failover & Switchover Constraints**\n\nWhen enabled, replication-manager verifies all safety constraints before executing a failover or switchover:\n- Replication lag is within the configured threshold\n- Semi-sync state matches the required level\n- No long-running writes are active\n\nDisabling this forces failover/switchover to proceed even when constraints are not met.`
  const helpAtSync = `**Failover Only on Semi-Sync State Sync**\n\nRestricts automatic failover to situations where the cluster was in a fully acknowledged semi-synchronous state.\nPrevents failover when a replica may be missing the last committed transactions, reducing data loss risk.`
  const helpDivergent = `**Failover Enable on Divergent Data**\n\nAllows failover even when replica datasets are known to diverge from the master.\nNormally replication-manager blocks failover in this case to prevent promoting a replica that is missing data.\nOnly enable if availability takes priority over data consistency.`
  const helpUnsafe = `**Failover Unsafe First Slave**\n\nAllows promoting a replica even if it has not fully applied all relay log events.\nUseful when no fully-caught-up replica is available and partial data loss is acceptable.\nUse with caution — may result in lost transactions.`
  const helpPositional = `**Failover Using Positional Replication**\n\nForces failover to use binlog file/position-based replication instead of GTID.\nUse on older MySQL setups that do not support GTID, or when GTID mode cannot be enabled.`
  const helpPseudoGTID = `**Failover Using Pseudo GTID**\n\nUses positional heartbeat events injected into the binary log to simulate GTID-like failover on servers that do not support native GTID.\nAllows safe failover on MySQL 5.5/5.6 or when GTID is not available.`
  const helpDelayCapture = `**Capture Statistics for Hourly Delay Average**\n\nEnables collection of replication lag measurements at regular intervals.\nThe statistics are averaged per hour and used to detect degrading replica performance over time.\nRequired for **Failover Check Delay Statistics** to function.`
  const helpDelayCheck = `**Failover Check Delay Statistics**\n\nUses the captured hourly delay averages to gate failover decisions.\nIf the average lag for a replica exceeds the threshold, it is not considered a failover candidate even if its current lag is acceptable.\nPrevents promoting a replica that has been consistently slow.`
  const helpDelayRotate = `**Delay Statistic Rotate Hours**\n\nNumber of hours of delay statistics to retain before rotating (discarding the oldest bucket).\nDefault: 24 hours. Maximum: 72 hours.\nIncrease to detect long-term trends in replica lag.`
  const helpPrintStat = `**Print Delay Statistic**\n\nLogs the current replication delay statistics to the replication-manager log at each monitoring cycle.\nUseful for debugging lag trends without querying the API.`
  const helpPrintHistory = `**Print Delay Statistic History**\n\nLogs the full delay statistic history (all retained hours) to the log.\nMore verbose than Print Delay Statistic — use only for short diagnostic sessions.`
  const helpPrintInterval = `**Delay Statistic Print Interval**\n\nHow often (in seconds) the delay statistics summary is printed to the log when Print Delay Statistic is enabled.\nDefault: 60 seconds.`
  const helpSwitchSync = `**Switchover Only on Semi-Sync State Sync**\n\nRestricts switchover to situations where semi-synchronous replication is fully acknowledged.\nPrevents a switchover when replicas may be behind the master, ensuring zero data loss.`
  const helpSwitchLock = `**Switchover Lock Users on Freeze Workload**\n\nDuring switchover, temporarily locks all user accounts on the current master to drain active connections before promoting the new master.\nEnsures a clean traffic cutover with no in-flight writes at the moment of promotion.`
  const helpMaxDelay = `**Switchover Replication Maximum Delay**\n\nMaximum replication lag (in seconds) a replica may have to be considered a valid switchover target.\nReplicas lagging more than this threshold are excluded from the candidate list.\nDefault: 30 seconds.`
  const helpWaitRoute = `**Switchover Wait Unmanaged Proxy Monitor Detection**\n\nSeconds to wait after the switchover completes for unmanaged proxies (HAProxy, external load balancers) to detect the new master.\nDuring this window, traffic is held to prevent split-brain writes.\nDefault: 1 second.`
  const helpMinorRelease = `**Switchover Allow on Minor Release**\n\nAllows switchover even when the candidate replica is running a different minor version than the master.\nBy default, replication-manager blocks switchover across minor versions to prevent incompatibility.\nEnable when you intentionally run mixed versions during a rolling upgrade.`

  const dataObject = [
    { key: helpKey('Failover Limit', helpFailoverLimit), value: (<RMSlider value={selectedCluster?.config?.failoverLimit} confirmTitle={`Confirm change 'failover-limit' to: `} onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'failover-limit', value: val }))} />) },
    { key: helpKey('Checks Failover & Switchover Constraints', helpCheckState), value: (<RMSwitch confirmTitle={'Confirm switch settings for check-replication-state?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'check-replication-state' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.checkReplicationState} />) },
    { key: helpKey('Failover Only on Semi-Sync State Sync', helpAtSync), value: (<RMSwitch confirmTitle={'Confirm switch settings for failover-at-sync?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'failover-at-sync' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.failoverAtSync} />) },
    { key: helpKey('Failover Enable on Divergent Data', helpDivergent), value: (<RMSwitch confirmTitle={'Confirm switch settings for failover-divergent-data?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'failover-divergent-data' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.failoverDivergentData} />) },
    { key: helpKey('Failover Unsafe First Slave', helpUnsafe), value: (<RMSwitch confirmTitle={'Confirm switch settings for failover-restart-unsafe?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'failover-restart-unsafe' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.failoverRestartUnsafe} />) },
    { key: helpKey('Failover Using Positional Replication', helpPositional), value: (<RMSwitch confirmTitle={'Confirm switch settings for force-slave-no-gtid-mode?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-slave-no-gtid-mode' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.forceSlaveNoGtidMode} />) },
    { key: helpKey('Failover Using Pseudo GTID', helpPseudoGTID), value: (<RMSwitch confirmTitle={'Confirm switch settings for autorejoin-slave-positional-heartbeat?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-slave-positional-heartbeat' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.autorejoinSlavePositionalHeartbeat} />) },
    { key: helpKey('Capture Statistics for Hourly Delay Average', helpDelayCapture), value: (<RMSwitch confirmTitle={'Confirm switch settings for delay-stat-capture?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'delay-stat-capture' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.delayStatCapture} />) },
    { key: helpKey('Failover Check Delay Statistics', helpDelayCheck), value: (<RMSwitch confirmTitle={'Confirm switch settings for failover-check-delay-stat?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'failover-check-delay-stat' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.failoverCheckDelayStat} />) },
    { key: helpKey('Delay Statistic Rotate Hours', helpDelayRotate), value: (<RMSlider value={selectedCluster?.config?.delayStatRotate} max={72} showMarkAtInterval={12} confirmTitle='Confirm change delay stat rotate value to: ' onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'delay-stat-rotate', value: val }))} />) },
    { key: helpKey('Print Delay Statistic', helpPrintStat), value: (<RMSwitch confirmTitle={'Confirm switch settings for print-delay-stat?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'print-delay-stat' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.printDelayStat} />) },
    { key: helpKey('Print Delay Statistic History', helpPrintHistory), value: (<RMSwitch confirmTitle={'Confirm switch settings for print-delay-stat-history?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'print-delay-stat-history' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.printDelayStatHistory} />) },
    { key: helpKey('Delay Statistic Print Interval', helpPrintInterval), value: (<RMSlider value={selectedCluster?.config?.printDelayStatInterval} max={60} showMarkAtInterval={10} confirmTitle='Confirm change delay stat rotate value to: ' onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'print-delay-stat-interval', value: val }))} />) },
    { key: helpKey('Switchover Only on Semi-Sync State Sync', helpSwitchSync), value: (<RMSwitch confirmTitle={'Confirm switch settings for switchover-at-sync?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'switchover-at-sync' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.switchoverAtSync} />) },
    { key: helpKey('Switchover Lock Users on Freeze Workload', helpSwitchLock), value: (<RMSwitch confirmTitle={'Confirm switch settings for sswitchover-lock-user-on-freeze?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'switchover-lock-user-on-freeze' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.switchLockUserOnFreeze} />) },
    { key: helpKey('Switchover Replication Maximum Delay', helpMaxDelay), value: (<RMSlider value={selectedCluster?.config?.failoverMaxSlaveDelay} max={100} showMarkAtInterval={20} confirmTitle='Confirm change max delay to: ' onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'failover-max-slave-delay', value: val }))} />) },
    { key: helpKey('Switchover Wait Unmanaged Proxy Monitor Detection', helpWaitRoute), value: (<RMSlider value={selectedCluster?.config?.switchoverWaitRouteChange} confirmTitle='Confirm change wait change route detection to: ' onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'switchover-wait-route-change', value: val }))} />) },
    { key: helpKey('Switchover Allow on Minor Release', helpMinorRelease), value: (<RMSwitch confirmTitle={'Confirm switch settings for switchover-lower-release?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'switchover-lower-release' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.switchoverLowerRelease} />) },
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

export default RepFailOverSettings
