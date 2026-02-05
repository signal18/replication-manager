import { Box, Flex, HStack, Stack, Text } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from '../styles.module.scss'

import { useDispatch, useSelector } from 'react-redux'
import { convertObjectToArrayForDropdown, formatBytes } from '../../../utility/common'
import CommonModal from '../../../components/Modals/CommonModal'
import modalStyles from '../../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import BackupSnapshotsSettings from './BackupSnapshotsSettings'
import PhysicalBackupSettings from './PhysicalBackupSettings'
import BackupBinlogSettings from './BackupBinlogSettings'
import tableStyles from '../../../components/TableType2/styles.module.scss'
import LogicalBackupSettings from './LogicalBackupSettings'
import BackupStreamingSettings from './BackupStreamingSettings'
import CompressionBufferSettings from './CompressionBufferSettings'
import TabItems from '../../../components/TabItems'
import FreeSpaceSettings from './FreeSpaceSettings'

const sizeGenerator = () => {
  const result = []
  let i = 1024;
  while (i <= 1024 * 1024 * 1024) {
    result.push(i)
    i = i * 2
  }

  return result.map((size) => {
    return { name: formatBytes(size, 0), value: size }
  })
}

function BackupSettings({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const joinClasses = (...classes) => classes.filter(Boolean).join(' ')
  const [logicalBackupOptions, setLogicalBackupOptions] = useState([])
  const [physicalBackupOptions, setPhysicalBackupOptions] = useState([])
  const [sizeOptions] = useState(sizeGenerator())
  const [isResticRepoConfigOpen, setIsResticRepoConfigOpen] = useState(true)
  const [activeTabIndex, setActiveTabIndex] = useState(0)
  const [action, setAction] = useState({
    title: '',
    type: '',
    body: <></>
  })
  const { title } = action
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)

  const openCommonModal = () => {
    setIsCommonModalOpen(true)
  }

  const closeCommonModal = () => {
    setIsCommonModalOpen(false)
  }

  const renderInfoModalBody = (content) => (
    <Box className={joinClasses(modalStyles.infoTooltip, styles.infoTooltip)}>
      <Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown>
    </Box>
  )

  const openInfoModal = (titleText, content) => {
    setAction({
      title: titleText,
      type: '',
      body: renderInfoModalBody(content)
    })
    openCommonModal()
  }

  const handleResticRepoToggle = () => {
    setIsResticRepoConfigOpen((prev) => !prev)
  }

  const handleTabChange = (index) => {
    if (typeof window === 'undefined') {
      setActiveTabIndex(index)
      return
    }

    const scrollY = window.scrollY
    setActiveTabIndex(index)
    requestAnimationFrame(() => {
      window.scrollTo({ top: scrollY })
    })
  }

  useEffect(() => {
    if (monitor?.backupLogicalList) {
      setLogicalBackupOptions(convertObjectToArrayForDropdown(monitor.backupLogicalList))
    }
    if (monitor?.backupPhysicalList) {
      setPhysicalBackupOptions(convertObjectToArrayForDropdown(monitor.backupPhysicalList))
    }
  }, [monitor?.backupLogicalList, monitor?.backupPhysicalList])

  return (
    <Flex justify='space-between' gap='0' direction='column' w='full'>
      <Box className={styles.tabItemsBorder} w='full'>
        <TabItems
          className={joinClasses(tableStyles.container, styles.tabItemsFullWidth)}
          options={['Backups', 'Binlog', 'Compression & Streaming', 'Snapshots', 'Free space']}
          tabIndex={activeTabIndex}
          onChange={handleTabChange}
          tabContents={[
            <Stack key='backups' spacing={{ base: 3, lg: 4 }} width='100%'>
              <LogicalBackupSettings
                selectedCluster={selectedCluster}
                user={user}
                dispatch={dispatch}
                logicalBackupOptions={logicalBackupOptions}
                onOpenInfoModal={openInfoModal}
              />
              <PhysicalBackupSettings
                clusterName={selectedCluster?.name}
                config={selectedCluster?.config}
                user={user}
                physicalBackupOptions={physicalBackupOptions}
                onOpenPhysicalPostScriptInfo={() =>
                  openInfoModal('Backup Physical Post-Script', BackupPostScriptRequirement)
                }
              />
            </Stack>,
            <Stack key='binlog' spacing={{ base: 3, lg: 4 }} width='100%'>
              <BackupBinlogSettings
                clusterName={selectedCluster?.name}
                config={selectedCluster?.config}
                user={user}
              />
            </Stack>,
            <Stack key='compression-streaming' spacing={{ base: 3, lg: 4 }} width='100%'>
              <CompressionBufferSettings
                clusterName={selectedCluster?.name}
                config={selectedCluster?.config}
                user={user}
                sizeOptions={sizeOptions}
                dispatch={dispatch}
              />
              <BackupStreamingSettings selectedCluster={selectedCluster} dispatch={dispatch} />
            </Stack>,
            <Stack key='snapshots' spacing={{ base: 3, lg: 4 }} width='100%'>
              <Box className={styles.panel} w='full'>
                <HStack spacing={2} className={styles.panelHeader}>
                  <Stack spacing={1} className={styles.panelHeaderContent}>
                    <Text className={styles.panelTitle}>Snapshots</Text>
                    <Text className={styles.panelDescription}>Manage restic snapshots and repo settings.</Text>
                  </Stack>
                </HStack>
                <Box className={styles.panelBody} display='block'>
                  <BackupSnapshotsSettings
                    selectedCluster={selectedCluster}
                    user={user}
                    dispatch={dispatch}
                    onOpenInfoModal={openInfoModal}
                    isResticRepoConfigOpen={isResticRepoConfigOpen}
                    onToggleResticRepoConfig={handleResticRepoToggle}
                  />
                </Box>
              </Box>
            </Stack>,
            <Stack key='free-space' spacing={{ base: 3, lg: 4 }} width='100%'>
              <Box className={styles.panel} w='full'>
                <HStack spacing={2} className={styles.panelHeader}>
                  <Stack spacing={1} className={styles.panelHeaderContent}>
                    <Text className={styles.panelTitle}>Check free space</Text>
                    <Text className={styles.panelDescription}>
                      Control disk usage thresholds and backup size estimation.
                    </Text>
                  </Stack>
                </HStack>
                <Box id='free-space-content' className={styles.panelBody} display='block'>
                  <FreeSpaceSettings
                    selectedCluster={selectedCluster}
                    user={user}
                    dispatch={dispatch}
                  />
                </Box>
              </Box>
            </Stack>
          ]}
        />
      </Box>
      {isCommonModalOpen && (
        <CommonModal
          isOpen={isCommonModalOpen}
          size='lg'
          title={title}
          body={action.body}
          contentClassName={joinClasses(modalStyles.infoModalContent, styles.infoModalContent)}
          headerClassName={joinClasses(modalStyles.infoModalHeader, styles.infoModalHeader)}
          bodyClassName={joinClasses(modalStyles.infoModalBody, styles.infoModalBody)}
          closeModal={() => {
            closeCommonModal()
          }}
        />
      )}
    </Flex>
  )
}

export default BackupSettings
