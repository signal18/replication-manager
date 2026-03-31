import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { switchSetting } from '../../redux/settingsSlice'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'

function RejoinSettings({ selectedCluster, user, openConfirmModal }) {
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

  const { settings: { arLoading, arBackupBinlogLoading, arFlashbackOnSyncLoading, arFlashbackLoading, arMysqldumpLoading, arLogicalBackupLoading, arPhysicalBackupLoading, arForceRestoreLoading, autoseedLoading } } = useSelector((state) => state)

  const helpFailback = `**Failback (Auto Rejoin)**\n\nWhen enabled, replication-manager automatically reintegrates a former master or lagging replica back into the cluster after it recovers.\nThe rejoin method depends on the other Failback settings below.\nDisable if you prefer manual control over when and how nodes rejoin.`
  const helpBackupBinlog = `**Failback Backup Extra Events**\n\nWhen enabled, replication-manager saves binary log events that occurred on the new master between the old master's crash and the failover.\nThese events can later be replayed to recover transactions that were not yet replicated at the time of the failure.`
  const helpFlashbackSync = `**Failback Flashback When Semi-Sync Status Sync**\n\nEnables flashback-based rejoin when the cluster was in a semi-synchronous replication sync state at the time of failover.\nFlashback rolls back the divergent transactions on the rejoining node rather than doing a full reseed, which is much faster.`
  const helpFlashback = `**Failback Binlog Flashback**\n\nUses MariaDB's FLASHBACK feature to reverse divergent transactions on the rejoining node.\nFaster than a full mysqldump reseed for nodes that are only slightly ahead of the new master.\nRequires MariaDB 10.2+ with binlog format ROW.`
  const helpMysqldump = `**Failback Direct Master Dump**\n\nReseeds the rejoining node by streaming a live mysqldump from the current master.\nSlower than flashback but works on all MySQL/MariaDB versions and does not require binlog.\nSafe choice when the node is heavily diverged.`
  const helpLogicalBackup = `**Failback Via Logical Backup**\n\nReseeds the rejoining node from the most recent logical backup (mysqldump, mydumper).\nAvoids putting load on the live master at the cost of using a potentially older backup.\nRequires a valid logical backup to be available.`
  const helpPhysicalBackup = `**Failback Via Physical Backup**\n\nReseeds the rejoining node from the most recent physical backup (Xtrabackup, Mariabackup).\nFastest restore for large datasets.\nRequires a valid physical backup to be available and compatible with the server version.`
  const helpForceRestore = `**Force Rejoin With Restore**\n\nForces a full restore even if the node appears to be only slightly behind.\nUse when automatic divergence detection is unreliable or when you want to guarantee a clean state.`
  const helpAutoseed = `**Auto Seed From Backup Standalone Server**\n\nWhen enabled, a standalone server (not yet part of the cluster) is automatically seeded from the latest backup and added to the replication topology.\nUseful for adding new nodes without manual intervention.`

  const dataObject = [
    { key: helpKey('Failback', helpFailback), value: (<RMSwitch confirmTitle={'Confirm switch settings for autorejoin?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.autorejoin} loading={arLoading} />) },
    { key: helpKey('Failback Backup Extra Events', helpBackupBinlog), value: (<RMSwitch isChecked={selectedCluster?.config?.autorejoinBackupBinlog} isDisabled={user?.grants['cluster-settings'] == false} loading={arBackupBinlogLoading} confirmTitle={'Confirm switch settings for autorejoin-backup-binlog?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-backup-binlog' }))} />) },
    { key: helpKey('Failback Flashback When Semi-Sync Status Sync', helpFlashbackSync), value: (<RMSwitch isChecked={selectedCluster?.config?.autorejoinFlashbackOnSync} isDisabled={user?.grants['cluster-settings'] == false} loading={arFlashbackOnSyncLoading} confirmTitle={'Confirm switch settings for autorejoin-flashback-on-sync?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-flashback-on-sync' }))} />) },
    { key: helpKey('Failback Binlog Flashback', helpFlashback), value: (<RMSwitch isChecked={selectedCluster?.config?.autorejoinFlashback} isDisabled={user?.grants['cluster-settings'] == false} loading={arFlashbackLoading} confirmTitle={'Confirm switch settings for autorejoin-flashback?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-flashback' }))} />) },
    { key: helpKey('Failback Direct Master Dump', helpMysqldump), value: (<RMSwitch isChecked={selectedCluster?.config?.autorejoinMysqldump} isDisabled={user?.grants['cluster-settings'] == false} loading={arMysqldumpLoading} confirmTitle={'Confirm switch settings for autorejoin-mysqldump?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-mysqldump' }))} />) },
    { key: helpKey('Failback Via Logical Backup', helpLogicalBackup), value: (<RMSwitch isChecked={selectedCluster?.config?.autorejoinLogicalBackup} isDisabled={user?.grants['cluster-settings'] == false} loading={arLogicalBackupLoading} confirmTitle={'Confirm switch settings for autorejoin-logical-backup?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-logical-backup' }))} />) },
    { key: helpKey('Failback Via Physical Backup', helpPhysicalBackup), value: (<RMSwitch isChecked={selectedCluster?.config?.autorejoinPhysicalBackup} isDisabled={user?.grants['cluster-settings'] == false} loading={arPhysicalBackupLoading} confirmTitle={'Confirm switch settings for autorejoin-physical-backup?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-physical-backup' }))} />) },
    { key: helpKey('Force Rejoin With Restore', helpForceRestore), value: (<RMSwitch isChecked={selectedCluster?.config?.autorejoinForceRestore} isDisabled={user?.grants['cluster-settings'] == false} loading={arForceRestoreLoading} confirmTitle={'Confirm switch settings for autorejoin-force-restore?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autorejoin-force-restore' }))} />) },
    { key: helpKey('Auto Seed From Backup Standalone Server', helpAutoseed), value: (<RMSwitch isChecked={selectedCluster?.config?.autoseed} isDisabled={user?.grants['cluster-settings'] == false} loading={autoseedLoading} confirmTitle={'Confirm switch settings for autoseed?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'autoseed' }))} />) },
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

export default RejoinSettings
