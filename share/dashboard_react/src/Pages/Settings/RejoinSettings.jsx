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

  const h = (content, title) => <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} />

  const { settings: { arLoading, arBackupBinlogLoading, arFlashbackOnSyncLoading, arFlashbackLoading, arMysqldumpLoading, arLogicalBackupLoading, arPhysicalBackupLoading, arForceRestoreLoading, autoseedLoading } } = useSelector((state) => state)

  const sw = (setting, configKey, loading) => <RMSwitch confirmTitle={`Confirm switch settings for ${setting}?`} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.[configKey]} loading={loading} />

  const hFailback = `**Failback (Auto Rejoin)**\n\nAutomatically reintegrates a former master or lagging replica back into the cluster after it recovers.\nThe rejoin method depends on the other Failback settings below.\n\nConfig: \`autorejoin\``
  const hBinlog = `**Failback Backup Extra Events**\n\nSaves binary log events that occurred on the new master between the crash and the failover.\nCan later be replayed to recover transactions not yet replicated at the time of failure.\n\nConfig: \`autorejoin-backup-binlog\``
  const hFlashbackSync = `**Failback Flashback When Semi-Sync Sync**\n\nEnables flashback-based rejoin when the cluster was in a semi-synchronous sync state at failover.\nRolls back divergent transactions rather than doing a full reseed.\n\nConfig: \`autorejoin-flashback-on-sync\``
  const hFlashback = `**Failback Binlog Flashback**\n\nUses MariaDB's FLASHBACK to reverse divergent transactions on the rejoining node.\nFaster than a full mysqldump reseed. Requires MariaDB 10.2+ with ROW binlog format.\n\nConfig: \`autorejoin-flashback\``
  const hDump = `**Failback Direct Master Dump**\n\nReseeds the rejoining node by streaming a live mysqldump from the current master.\nSafe choice when the node is heavily diverged.\n\nConfig: \`autorejoin-mysqldump\``
  const hLogical = `**Failback Via Logical Backup**\n\nReseeds from the most recent logical backup (mysqldump, mydumper).\nAvoids load on the live master at the cost of using a potentially older backup.\n\nConfig: \`autorejoin-logical-backup\``
  const hPhysical = `**Failback Via Physical Backup**\n\nReseeds from the most recent physical backup (Xtrabackup, Mariabackup).\nFastest restore for large datasets.\n\nConfig: \`autorejoin-physical-backup\``
  const hForce = `**Force Rejoin With Restore**\n\nForces a full restore even if the node appears only slightly behind.\nUse when automatic divergence detection is unreliable.\n\nConfig: \`autorejoin-force-restore\``
  const hAutoseed = `**Auto Seed From Backup Standalone Server**\n\nAutomatically seeds a standalone server from the latest backup and adds it to the replication topology.\n\nConfig: \`autoseed\``

  const dataObject = [
    { key: 'Failback', help: h(hFailback, 'Failback'), value: sw('autorejoin', 'autorejoin', arLoading) },
    { key: 'Failback Backup Extra Events', help: h(hBinlog, 'Failback Backup Extra Events'), value: sw('autorejoin-backup-binlog', 'autorejoinBackupBinlog', arBackupBinlogLoading) },
    { key: 'Failback Flashback When Semi-Sync Status Sync', help: h(hFlashbackSync, 'Failback Flashback When Semi-Sync Status Sync'), value: sw('autorejoin-flashback-on-sync', 'autorejoinFlashbackOnSync', arFlashbackOnSyncLoading) },
    { key: 'Failback Binlog Flashback', help: h(hFlashback, 'Failback Binlog Flashback'), value: sw('autorejoin-flashback', 'autorejoinFlashback', arFlashbackLoading) },
    { key: 'Failback Direct Master Dump', help: h(hDump, 'Failback Direct Master Dump'), value: sw('autorejoin-mysqldump', 'autorejoinMysqldump', arMysqldumpLoading) },
    { key: 'Failback Via Logical Backup', help: h(hLogical, 'Failback Via Logical Backup'), value: sw('autorejoin-logical-backup', 'autorejoinLogicalBackup', arLogicalBackupLoading) },
    { key: 'Failback Via Physical Backup', help: h(hPhysical, 'Failback Via Physical Backup'), value: sw('autorejoin-physical-backup', 'autorejoinPhysicalBackup', arPhysicalBackupLoading) },
    { key: 'Force Rejoin With Restore', help: h(hForce, 'Force Rejoin With Restore'), value: sw('autorejoin-force-restore', 'autorejoinForceRestore', arForceRestoreLoading) },
    { key: 'Auto Seed From Backup Standalone Server', help: h(hAutoseed, 'Auto Seed From Backup Standalone Server'), value: sw('autoseed', 'autoseed', autoseedLoading) },
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.table} helpColumn={true} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default RejoinSettings
