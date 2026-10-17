import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'
import remarkGfm from 'remark-gfm'
import { useArchiveSettings } from './ArchiveListSettings'

// ARCHIVE: what leaves the cluster's own backup directory -- the cluster's archives, each with
// its own settings (#2091), and the S3 target of the streamed backups. Split from
// the Backup section on 2026-09-30 (Stéphane): Backup = how the dumps are taken, Archive = where
// they are kept beyond the local backup storage.
function ArchiveSettings({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const joinClasses = (...classes) => classes.filter(Boolean).join(' ')
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

  const hStreamingEndpoint = `**Backup Streaming Endpoint**\n\nS3-compatible endpoint URL for streaming backup storage.\nExample: \`https://s3.amazonaws.com\`\n\nConfig: \`backup-streaming-endpoint\``
  const hStreamingRegion = `**Backup Streaming Region**\n\nCloud region for the streaming backup bucket.\nExample: \`eu-west-1\`\n\nConfig: \`backup-streaming-region\``
  const hStreamingBucket = `**Backup Streaming Bucket**\n\nS3 bucket name used for streaming backup storage.\n\nConfig: \`backup-streaming-bucket\``


  // the archives, each with its own settings (#2091), then the streamed backups' S3 target
  const { rows: archiveRows, modals: archiveModals } = useArchiveSettings({ clusterName: selectedCluster?.name, user, help: h, config: selectedCluster?.config })

  const dataObject = [
    ...archiveRows,
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
      {archiveModals}
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
