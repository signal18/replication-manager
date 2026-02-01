import {
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Box,
  Text,
  VStack,
  HStack,
  Divider,
  Alert,
  AlertIcon,
  AlertTitle,
  AlertDescription,
  FormControl,
  FormLabel,
  Checkbox,
  Spinner,
  Badge
} from '@chakra-ui/react'
import React, { useState, useEffect, useMemo, useCallback } from 'react'
import { useSelector } from 'react-redux'
import RMButton from '../../RMButton'
import styles from './styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'
import {
  extractServerInfoFromPath,
  formatLocalDateTime,
  formatMetadataTimestamp,
  getPreferredMetadata,
  getSnapshotMetadataByMethod,
  parseSnapshotTags
} from './helpers'
import SnapshotDetailsPanel from './SnapshotDetailsPanel'
import ResticStrategyBox from './ResticStrategyBox'
import SnapshotSelector from './SnapshotSelector'
import useResticSnapshots from './useResticSnapshots'

const LATEST_SESSION_FILTER = 'latest-per-session'

const renderStatusBadge = (status) => {
  switch (status) {
    case 'available':
      return (
        <Badge colorScheme='green' fontSize='0.65rem' variant='subtle'>
          Available
        </Badge>
      )
    case 'orphaned':
      return (
        <Badge colorScheme='yellow' fontSize='0.65rem' variant='subtle'>
          Orphaned
        </Badge>
      )
    default:
      return null
  }
}

function AdvancedReseedModal({ isOpen, closeModal, onConfirm, operationType, clusterName, serverInfo, backupType }) {
  const { theme } = useTheme()

  const [selectedSnapshot, setSelectedSnapshot] = useState('')
  const [useResticSnapshot, setUseResticSnapshot] = useState(false)
  const [resticStrategy, setResticStrategy] = useState('auto')
  const [resticCleanup, setResticCleanup] = useState(true)
  const [resticUseTempDir, setResticUseTempDir] = useState(true)
  const [resticTempDir, setResticTempDir] = useState('')

  const snapshots = useSelector((state) => state.cluster.restic.snapshots)
  const clusterConfig = useSelector((state) => state.cluster.clusterData?.config)
  const { isLoadingSnapshots } = useResticSnapshots({
    isOpen,
    useResticSnapshot,
    clusterName,
    operationType,
    filter: LATEST_SESSION_FILTER
  })

  const selectedSnapshotObj = useMemo(() => {
    if (!selectedSnapshot || !snapshots) return null
    return snapshots.find((snapshot) => snapshot.id === selectedSnapshot)
  }, [selectedSnapshot, snapshots])

  useEffect(() => {
    if (!useResticSnapshot) {
      return
    }
    if (snapshots && snapshots.length > 0 && !selectedSnapshot) {
      setSelectedSnapshot(snapshots[0].id)
    }
  }, [snapshots, selectedSnapshot, useResticSnapshot])

  useEffect(() => {
    if (!isOpen) {
      setSelectedSnapshot('')
      setUseResticSnapshot(false)
      setResticStrategy('auto')
      setResticCleanup(true)
      setResticUseTempDir(true)
      setResticTempDir('')
    }
  }, [isOpen])

  useEffect(() => {
    if (useResticSnapshot) {
      return
    }
    setResticStrategy('auto')
    setResticCleanup(true)
    setResticUseTempDir(true)
    setResticTempDir('')
  }, [useResticSnapshot])

  const handleConfirm = useCallback(() => {
    onConfirm({
      useRestic: useResticSnapshot,
      snapshotId: selectedSnapshot,
      strategy: useResticSnapshot ? resticStrategy : undefined,
      cleanup: useResticSnapshot && resticStrategy === 'restore' && resticUseTempDir ? resticCleanup : undefined,
      useTempDir: useResticSnapshot ? resticUseTempDir : undefined,
      tempDir:
        useResticSnapshot && resticStrategy === 'restore' && resticUseTempDir && resticTempDir ? resticTempDir : undefined
    })
  }, [
    onConfirm,
    resticCleanup,
    resticStrategy,
    resticTempDir,
    resticUseTempDir,
    selectedSnapshot,
    useResticSnapshot
  ])

  const handleUseResticSnapshotChange = useCallback((event) => {
    setUseResticSnapshot(event.target.checked)
  }, [])

  const details = useMemo(() => {
    switch (operationType) {
      case 'logical-backup':
        return {
          title: 'Reseed Logical From Backup',
          description: 'This operation will restore the database from a logical backup using ' + backupType,
          source: 'Existing backup',
          method: 'Logical restore',
          icon: '📦'
        }
      case 'logical-master':
        return {
          title: 'Reseed Logical From Master',
          description: 'This operation will create a fresh logical dump from the master using ' + backupType,
          source: 'Current master database',
          method: 'Logical dump and restore',
          icon: '🔄'
        }
      case 'physical-backup':
        return {
          title: 'Reseed Physical From Backup',
          description: 'This operation will restore the database from a physical backup using ' + backupType,
          source: 'Existing physical backup',
          method: 'Physical file restore',
          icon: '💾'
        }
      default:
        return {
          title: 'Reseed Database',
          description: 'This operation will reseed the database',
          source: 'Unknown',
          method: 'Unknown',
          icon: '⚠️'
        }
    }
  }, [backupType, operationType])

  const snapshotDerived = useMemo(() => {
    const resolvedMethodFallback = operationType === 'physical-backup' ? 'physical' : 'logical'
    const resolvedBackupToolFallback = backupType || null
    if (!selectedSnapshotObj) {
      const normalizedFallbackTool = (resolvedBackupToolFallback || '').toLowerCase()
      const isDirectoryFallbackTool = ['mydumper', 'dumpling'].includes(normalizedFallbackTool)
      const isStreamableFallbackTool = Boolean(normalizedFallbackTool) && !isDirectoryFallbackTool
      const dumpAllowedFallback =
        resolvedMethodFallback === 'physical' ||
        (resolvedMethodFallback !== 'physical' && isStreamableFallbackTool)

      return {
        selectedSnapshotTags: null,
        selectedSnapshotStatusBadge: null,
        selectedSnapshotPathInfo: null,
        logicalMetadata: null,
        physicalMetadata: null,
        resolvedMethod: resolvedMethodFallback,
        resolvedBackupTool: resolvedBackupToolFallback,
        resolvedCreatedTime: null,
        selectedMetadataReady: false,
        selectedMetadataStatus: 'unknown',
        selectedMetadataError: '',
        dumpAllowed: dumpAllowedFallback
      }
    }

    const selectedSnapshotTags = parseSnapshotTags(selectedSnapshotObj.tags || [])
    const selectedSnapshotStatusBadge = selectedSnapshotTags ? renderStatusBadge(selectedSnapshotTags.status) : null
    const selectedSnapshotPrimaryPath = selectedSnapshotObj.paths?.[0] || ''
    const selectedSnapshotPathInfo = extractServerInfoFromPath(selectedSnapshotPrimaryPath, selectedSnapshotObj.tags || [])
    const preferredMetadata = getPreferredMetadata(selectedSnapshotObj, operationType)
    const logicalMetadata = getSnapshotMetadataByMethod(selectedSnapshotObj, 'logical')
    const physicalMetadata = getSnapshotMetadataByMethod(selectedSnapshotObj, 'physical')
    const normalizedTagBackupType = selectedSnapshotTags?.backupType ? selectedSnapshotTags.backupType.toLowerCase() : null
    const normalizedMetadataMethod = preferredMetadata?.backupMethod ? preferredMetadata.backupMethod.toLowerCase() : null
    const resolvedMethod =
      normalizedMetadataMethod ||
      (normalizedTagBackupType === 'logical' || normalizedTagBackupType === 'physical' ? normalizedTagBackupType : null) ||
      resolvedMethodFallback
    const resolvedBackupTool =
      preferredMetadata?.backupTool || selectedSnapshotTags?.backupTool || selectedSnapshotPathInfo?.backupTool || resolvedBackupToolFallback
    const resolvedCreatedTime = preferredMetadata
      ? formatMetadataTimestamp(preferredMetadata, formatLocalDateTime)
      : formatLocalDateTime(selectedSnapshotObj.time)
    const selectedMetadataReady = selectedSnapshotObj.metadataReady ?? false
    const selectedMetadataStatus = selectedSnapshotObj.metadataStatus || 'unknown'
    const selectedMetadataError = selectedSnapshotObj.metadataError || ''
    const normalizedBackupTool = (resolvedBackupTool || '').toLowerCase()
    const isDirectoryBackupTool = ['mydumper', 'dumpling'].includes(normalizedBackupTool)
    const isStreamableBackupTool = Boolean(normalizedBackupTool) && !isDirectoryBackupTool
    const dumpAllowed = resolvedMethod === 'physical' || (resolvedMethod !== 'physical' && isStreamableBackupTool)

    return {
      selectedSnapshotTags,
      selectedSnapshotStatusBadge,
      selectedSnapshotPathInfo,
      logicalMetadata,
      physicalMetadata,
      resolvedMethod,
      resolvedBackupTool,
      resolvedCreatedTime,
      selectedMetadataReady,
      selectedMetadataStatus,
      selectedMetadataError,
      dumpAllowed
    }
  }, [backupType, operationType, selectedSnapshotObj])

  const {
    selectedSnapshotTags,
    selectedSnapshotStatusBadge,
    selectedSnapshotPathInfo,
    logicalMetadata,
    physicalMetadata,
    resolvedMethod,
    resolvedBackupTool,
    resolvedCreatedTime,
    selectedMetadataReady,
    selectedMetadataStatus,
    selectedMetadataError,
    dumpAllowed
  } = snapshotDerived

  const isMetadataLoading = useMemo(() => {
    return useResticSnapshot && Boolean(selectedSnapshotObj) && !selectedMetadataReady
  }, [selectedSnapshotObj, selectedMetadataReady, useResticSnapshot])

  const resticTempDirPlaceholder = useMemo(() => {
    const monitoringDatadir = clusterConfig?.monitoringDatadir
    return (
      clusterConfig?.backupResticReseedTempDir+"/{snapshotID}" ||
      (monitoringDatadir ? `${monitoringDatadir}/backup/restic_temp/{snapshotID}` : '{cluster-datadir}/backup/restic_temp/{snapshotID}')
    )
  }, [clusterConfig?.backupResticReseedTempDir, clusterConfig?.monitoringDatadir])

  const isConfirmDisabled = useMemo(() => {
    return (
      (operationType === 'logical-backup' || operationType === 'physical-backup') &&
      useResticSnapshot &&
      (!selectedSnapshot || isLoadingSnapshots || isMetadataLoading)
    )
  }, [isLoadingSnapshots, isMetadataLoading, operationType, selectedSnapshot, useResticSnapshot])

  useEffect(() => {
    if (!useResticSnapshot) {
      return
    }
    if (resticStrategy === 'dump' && !dumpAllowed) {
      setResticStrategy('auto')
    }
  }, [useResticSnapshot, resticStrategy, dumpAllowed])

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='xl'>
      <ModalOverlay />
      <ModalContent
        className={`${styles.modalContent} ${theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}`}
      >
        <ModalHeader className={styles.modalHeader}>
          <HStack spacing={2}>
            <Text fontSize='xl'>{details.icon}</Text>
            <Text>{details.title}</Text>
          </HStack>
        </ModalHeader>
        <ModalCloseButton />

        <ModalBody className={styles.modalBody}>
          <VStack spacing={4} align='stretch'>
            <Alert status='warning' borderRadius='md'>
              <AlertIcon />
              <Box>
                <AlertTitle>Destructive Operation</AlertTitle>
                <AlertDescription>
                  This operation will completely replace the database contents. All current data on this server will be
                  lost.
                </AlertDescription>
              </Box>
            </Alert>

            <Box className={styles.infoSection}>
              <Text fontWeight='bold' mb={2}>
                Target Server:
              </Text>
              <VStack align='stretch' spacing={1} pl={4}>
                <HStack>
                  <Text fontWeight='semibold' minW='80px'>
                    Host:
                  </Text>
                  <Text fontFamily='monospace'>
                    {serverInfo.host}:{serverInfo.port}
                  </Text>
                </HStack>
                <HStack>
                  <Text fontWeight='semibold' minW='80px'>
                    Server ID:
                  </Text>
                  <Text fontFamily='monospace'>{serverInfo.id}</Text>
                </HStack>
              </VStack>
            </Box>

            <Divider />

            <Box className={styles.infoSection}>
              <Text fontWeight='bold' mb={2}>
                Operation Details:
              </Text>
              <VStack align='stretch' spacing={1} pl={4}>
                <HStack>
                  <Text fontWeight='semibold' minW='120px'>
                    Backup Tool:
                  </Text>
                  <Text fontFamily='monospace' color='blue.500'>
                    {backupType}
                  </Text>
                </HStack>
                <HStack>
                  <Text fontWeight='semibold' minW='120px'>
                    Source:
                  </Text>
                  <Text>{details.source}</Text>
                </HStack>
                <HStack>
                  <Text fontWeight='semibold' minW='120px'>
                    Method:
                  </Text>
                  <Text>{details.method}</Text>
                </HStack>
              </VStack>
            </Box>

            <Box className={styles.descriptionSection}>
              <Text fontSize='sm' color='gray.600'>
                {details.description}
              </Text>
            </Box>

            {(operationType === 'logical-backup' || operationType === 'physical-backup') && (
              <>
                <Divider />
                <Box className={styles.infoSection}>
                  <FormControl mb={3}>
                    <Checkbox isChecked={useResticSnapshot} onChange={handleUseResticSnapshotChange}>
                      Use restic snapshot
                    </Checkbox>
                  </FormControl>
                  <FormControl isRequired={useResticSnapshot}>
                    <FormLabel fontWeight='bold'>Select Backup Snapshot:</FormLabel>
                    {!useResticSnapshot ? (
                      <Alert status='info' borderRadius='md' size='sm'>
                        <AlertIcon />
                        <AlertDescription fontSize='sm'>
                          Restic snapshot selection is disabled. Restore will use the current backup configuration.
                        </AlertDescription>
                      </Alert>
                    ) : isLoadingSnapshots ? (
                      <HStack spacing={2} p={2}>
                        <Spinner size='sm' />
                        <Text fontSize='sm'>Loading snapshots...</Text>
                      </HStack>
                    ) : snapshots && snapshots.length > 0 ? (
                      <>
                        <SnapshotSelector
                          snapshots={snapshots}
                          selectedSnapshot={selectedSnapshot}
                          setSelectedSnapshot={setSelectedSnapshot}
                          operationType={operationType}
                          theme={theme}
                          filterLabel={LATEST_SESSION_FILTER}
                        />

                        <ResticStrategyBox
                          theme={theme}
                          resticStrategy={resticStrategy}
                          setResticStrategy={setResticStrategy}
                          resticCleanup={resticCleanup}
                          setResticCleanup={setResticCleanup}
                          resticUseTempDir={resticUseTempDir}
                          setResticUseTempDir={setResticUseTempDir}
                          resticTempDir={resticTempDir}
                          setResticTempDir={setResticTempDir}
                          resticTempDirPlaceholder={resticTempDirPlaceholder}
                          dumpAllowed={dumpAllowed}
                        />

                        {useResticSnapshot && selectedSnapshotObj && (
                          <SnapshotDetailsPanel
                            theme={theme}
                            snapshot={selectedSnapshotObj}
                            statusBadge={selectedSnapshotStatusBadge}
                            snapshotTags={selectedSnapshotTags}
                            createdTime={resolvedCreatedTime}
                            metadataReady={selectedMetadataReady}
                            metadataStatus={selectedMetadataStatus}
                            metadataError={selectedMetadataError}
                            backupTool={resolvedBackupTool}
                            pathInfo={selectedSnapshotPathInfo}
                            resolvedMethod={resolvedMethod}
                            logicalMetadata={logicalMetadata}
                            physicalMetadata={physicalMetadata}
                          />
                        )}
                      </>
                    ) : (
                      <Alert status='warning' borderRadius='md' size='sm'>
                        <AlertIcon />
                        <AlertDescription fontSize='sm'>
                          No snapshots available. Please create a backup first.
                        </AlertDescription>
                      </Alert>
                    )}
                  </FormControl>
                </Box>
              </>
            )}

            {operationType === 'logical-master' && (
              <Alert status='info' borderRadius='md' size='sm'>
                <AlertIcon />
                <AlertDescription fontSize='sm'>
                  The master database will remain available during this operation. Replication will be reconfigured
                  after restore.
                </AlertDescription>
              </Alert>
            )}

            {operationType === 'physical-backup' && (
              <Alert status='info' borderRadius='md' size='sm'>
                <AlertIcon />
                <AlertDescription fontSize='sm'>
                  The database will be stopped during the physical restore operation and restarted automatically.
                </AlertDescription>
              </Alert>
            )}
          </VStack>
        </ModalBody>

        {isMetadataLoading && useResticSnapshot && (
          <Alert status='info' borderRadius='md' fontSize='sm'>
            <AlertIcon />
            Snapshot metadata is still loading. Please wait until metadata is ready to proceed.
          </Alert>
        )}

        <ModalFooter gap={3}>
          <RMButton variant='outline' colorScheme='white' size='medium' onClick={closeModal}>
            Cancel
          </RMButton>
          <RMButton
            colorScheme='red'
            size='medium'
            onClick={handleConfirm}
            isDisabled={isConfirmDisabled}
          >
            Confirm Reseed
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default AdvancedReseedModal
