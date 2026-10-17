import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import TableType2 from '../../components/TableType2'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'
import remarkGfm from 'remark-gfm'
import { useArchiveSettings } from './ArchiveListSettings'

// ARCHIVE: what leaves the cluster's own backup directory -- the cluster's archives, each with
// its own settings (#2091). The backups folder mount is the Backup section's. Split from
// the Backup section on 2026-09-30 (Stéphane): Backup = how the dumps are taken, Archive = where
// they are kept beyond the local backup storage.
function ArchiveSettings({ selectedCluster, user }) {
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

  // the archives, each with its own settings (#2091), then the streamed backups' S3 target
  const { rows: archiveRows, modals: archiveModals } = useArchiveSettings({ clusterName: selectedCluster?.name, user, help: h, config: selectedCluster?.config })

  const dataObject = [
    ...archiveRows
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
