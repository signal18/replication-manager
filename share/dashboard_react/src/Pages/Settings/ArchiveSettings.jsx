import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'
import remarkGfm from 'remark-gfm'
import NumberInput from '../../components/NumberInput'
import BackupSnapshotsSettings from './BackupSnapshotsSettings'

// ARCHIVE: what leaves the cluster's own backup directory -- the restic repository (local or
// remote, its purge strategy and mounts) and the S3 target of the streamed backups. Split from
// the Backup section on 2026-09-30 (Stéphane): Backup = how the dumps are taken, Archive = where
// they are kept beyond the local backup storage.
function ArchiveSettings({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const joinClasses = (...classes) => classes.filter(Boolean).join(' ')
  const [isResticRepoConfigOpen, setIsResticRepoConfigOpen] = useState(true)
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)
  const openInfoModal = (titleText, content) => {
    setAction({ title: titleText, body: (<Box className={joinClasses(modalStyles.infoTooltip, styles.infoTooltip)}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box>) })
    setIsCommonModalOpen(true)
  }
  const h = (content, title) => (
    <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)}
      iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />
  )
  const handleResticRepoToggle = () => setIsResticRepoConfigOpen((prev) => !prev)

  const hStreamingEndpoint = `**Backup Streaming Endpoint**\n\nS3-compatible endpoint URL for streaming backup storage.\nExample: \`https://s3.amazonaws.com\`\n\nConfig: \`backup-streaming-endpoint\``
  const hStreamingRegion = `**Backup Streaming Region**\n\nCloud region for the streaming backup bucket.\nExample: \`eu-west-1\`\n\nConfig: \`backup-streaming-region\``
  const hStreamingBucket = `**Backup Streaming Bucket**\n\nS3 bucket name used for streaming backup storage.\n\nConfig: \`backup-streaming-bucket\``
  const hResticPurgeThreshold = `**Custom Threshold for Purging Old Restic Backups**\n\nDisk usage percentage at which the oldest Restic backups are automatically purged.\nSet to 0 to follow the critical threshold.\n\nConfig: \`backup-restic-purge-oldest-on-disk-threshold\``
  const hResticPurgeOnDisk = `**Purge Oldest Restic Backups if Disk Usage Exceeds Threshold**\n\nWhen enabled, automatically removes the oldest Restic backup snapshots when disk usage exceeds the configured threshold.\n\nConfig: \`backup-restic-purge-oldest-on-disk-space\``

  const dataObject = [
    ...BackupSnapshotsSettings({ selectedCluster, user, dispatch, onOpenInfoModal: openInfoModal, isResticRepoConfigOpen, onToggleResticRepoConfig: handleResticRepoToggle }),
    {
      key: 'Restic Disk Usage',
      value: [
        { key: 'Purge Oldest Restic on Low Disk', help: h(hResticPurgeOnDisk, 'Purge Oldest Restic Backups if Disk Usage Exceeds Threshold'), value: (<RMSwitch isChecked={selectedCluster?.config?.backupResticPurgeOldestOnDiskSpace} isDisabled={user?.grants['cluster-settings'] == false} confirmTitle={'Confirm switch settings for backup-restic-purge-oldest-on-disk-space?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-restic-purge-oldest-on-disk-space' }))} />) },
        { key: 'Restic Purge Threshold', help: h(hResticPurgeThreshold, 'Custom Threshold for Purging Old Restic Backups'), value: (<NumberInput min={0} max={100} value={selectedCluster?.config?.backupResticPurgeOldestOnDiskThreshold} showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change restic threshold to: `} onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-restic-purge-oldest-on-disk-threshold', value }))} />) },
      ]
    },
    {
      key: 'Backup Streaming Endpoint',
      help: h(hStreamingEndpoint, 'Backup Streaming Endpoint'),
      value: (
        <TextForm value={selectedCluster?.config?.backupStreamingEndpoint} confirmTitle={`Confirm backup-streaming-endpoint to `}
          className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-streaming-endpoint', value }))} />
      )
    },
    {
      key: 'Backup Streaming Region',
      help: h(hStreamingRegion, 'Backup Streaming Region'),
      value: (
        <TextForm value={selectedCluster?.config?.backupStreamingRegion} confirmTitle={`Confirm backup-streaming-region to `}
          className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-streaming-region', value }))} />
      )
    },
    {
      key: 'Backup Streaming Bucket',
      help: h(hStreamingBucket, 'Backup Streaming Bucket'),
      value: (
        <TextForm value={selectedCluster?.config?.backupStreamingBucket} confirmTitle={`Confirm backup-streaming-bucket to `}
          className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-streaming-bucket', value }))} />
      )
    },
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn={true} />
      {isCommonModalOpen && (
        <CommonModal isOpen={isCommonModalOpen} size='lg' title={action.title} body={action.body}
          contentClassName={joinClasses(modalStyles.infoModalContent, styles.infoModalContent)}
          headerClassName={joinClasses(modalStyles.infoModalHeader, styles.infoModalHeader)}
          bodyClassName={joinClasses(modalStyles.infoModalBody, styles.infoModalBody)}
          closeModal={() => setIsCommonModalOpen(false)} />
      )}
    </Flex>
  )
}

export default ArchiveSettings
