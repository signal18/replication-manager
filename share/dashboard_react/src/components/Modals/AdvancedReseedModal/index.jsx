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
  Select,
  FormControl,
  FormLabel,
  Checkbox,
  Input,
  Spinner,
  Badge,
  Wrap,
  WrapItem
} from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import RMButton from '../../RMButton'
import styles from './styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'
import { getResticSnapshot } from '../../../redux/clusterSlice'

const LATEST_SESSION_FILTER = 'latest-per-session'

const OPERATION_METHOD_MAP = {
  'logical-backup': 'logical',
  'logical-master': 'logical',
  'physical-backup': 'physical'
}

const getSnapshotMetadataByMethod = (snapshot, method) => {
  if (!snapshot?.metadata || !method) {
    return null
  }
  const normalized = method.toLowerCase()
  return snapshot.metadata.find((meta) => meta?.backupMethod?.toLowerCase() === normalized) || null
}

const getPreferredMetadata = (snapshot, operationType) => {
  const preferredMethod = OPERATION_METHOD_MAP[operationType] || 'logical'
  const fallbackMethod = preferredMethod === 'logical' ? 'physical' : 'logical'
  return getSnapshotMetadataByMethod(snapshot, preferredMethod) || getSnapshotMetadataByMethod(snapshot, fallbackMethod)
}

const formatMetadataTimestamp = (meta, formatter) => {
  if (!meta) {
    return null
  }
  if (meta.startTime) {
    return formatter(meta.startTime)
  }
  if (meta.endTime) {
    return formatter(meta.endTime)
  }
  return null
}

const parseSnapshotTags = (tags = []) => {
  const meta = {
    sessionId: null,
    backupType: null,
    backupTool: null,
    status: 'legacy',
    isLatestView: false
  }

  tags.forEach((tagRaw) => {
    if (typeof tagRaw !== 'string') {
      return
    }
    const tag = tagRaw.trim()
    const normalized = tag.toLowerCase()

    if (tag.startsWith('session:')) {
      meta.sessionId = tag.substring('session:'.length)
      meta.isLatestView = true
    } else if (tag.startsWith('backup-type:')) {
      meta.backupType = tag.substring('backup-type:'.length)
    } else if (tag.startsWith('backup-tool:')) {
      meta.backupTool = tag.substring('backup-tool:'.length)
    }

    if (normalized === 'status:orphaned' || normalized === 'state:orphaned' || normalized === 'orphaned') {
      meta.status = 'orphaned'
    }
  })

  if (meta.status !== 'orphaned' && meta.sessionId) {
    meta.status = 'available'
  }

  return meta
}

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
  const dispatch = useDispatch()

  const [selectedSnapshot, setSelectedSnapshot] = useState('')
  const [isLoadingSnapshots, setIsLoadingSnapshots] = useState(false)
  const [useResticSnapshot, setUseResticSnapshot] = useState(false)
  const [resticStrategy, setResticStrategy] = useState('auto')
  const [resticCleanup, setResticCleanup] = useState(true)
  const [resticTempDir, setResticTempDir] = useState('')

  const snapshots = useSelector((state) => state.cluster.restic.snapshots)

  const extractServerInfoFromPath = (path, tags = []) => {
    try {
      if (!path) {
        return {
          clusterName: 'N/A',
          serverHost: 'N/A',
          serverPort: 'N/A',
          isAdhoc: false,
          backupTool: null,
          epoch: null
        }
      }

      const segments = path.split('/')
      if (segments.length < 2) {
        return {
          clusterName: 'N/A',
          serverHost: 'N/A',
          serverPort: 'N/A',
          isAdhoc: false,
          backupTool: null,
          epoch: null
        }
      }

      const isAdhocTagged = tags.some((tag) => tag === 'adhoc' || tag.includes('line:adhoc'))
      const lastSegment = segments[segments.length - 1]

      const mysqldumpPattern = /^mysqldump\.(\d+)\.sql\.gz$/
      const dumplingPattern = /^dumpling\.(\d+)$/
      const mydumperPattern = /^mydumper\.(\d+)$/

      let isAdhoc = false
      let backupTool = null
      let epoch = null
      let serverSegmentIndex = segments.length - 1

      if (mysqldumpPattern.test(lastSegment)) {
        isAdhoc = true
        backupTool = 'mysqldump'
        epoch = lastSegment.match(mysqldumpPattern)[1]
        serverSegmentIndex = segments.length - 2
      } else if (dumplingPattern.test(lastSegment)) {
        isAdhoc = true
        backupTool = 'dumpling'
        epoch = lastSegment.match(dumplingPattern)[1]
        serverSegmentIndex = segments.length - 2
      } else if (mydumperPattern.test(lastSegment)) {
        isAdhoc = true
        backupTool = 'mydumper'
        epoch = lastSegment.match(mydumperPattern)[1]
        serverSegmentIndex = segments.length - 2
      } else if (isAdhocTagged) {
        isAdhoc = true
      }

      const serverSegment = segments[serverSegmentIndex]
      const clusterName = segments[serverSegmentIndex - 1] || 'N/A'

      const serverParts = serverSegment.split('_')
      if (serverParts.length >= 2) {
        const serverPort = serverParts[serverParts.length - 1]
        const serverHost = serverParts.slice(0, -1).join('_')

        return {
          clusterName,
          serverHost,
          serverPort,
          isAdhoc,
          backupTool,
          epoch
        }
      }

      return {
        clusterName,
        serverHost: serverSegment,
        serverPort: 'N/A',
        isAdhoc,
        backupTool,
        epoch
      }
    } catch (error) {
      return {
        clusterName: 'N/A',
        serverHost: 'N/A',
        serverPort: 'N/A',
        isAdhoc: false,
        backupTool: null,
        epoch: null
      }
    }
  }

  const formatEpochDateTime = (epoch) => {
    try {
      const date = new Date(parseInt(epoch) * 1000)
      return date.toLocaleString('en-US', {
        year: 'numeric',
        month: 'short',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false
      })
    } catch (error) {
      return epoch
    }
  }

  const formatLocalDateTime = (timestamp) => {
    try {
      const date = new Date(timestamp)
      return date.toLocaleString('en-US', {
        year: 'numeric',
        month: 'short',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false
      })
    } catch (error) {
      return timestamp
    }
  }

  const MetadataSummary = ({ label, metadata, theme }) => {
    if (!metadata) {
      return null
    }
    return (
      <Box borderWidth='1px' borderRadius='md' p={3} bg={theme === 'light' ? 'white' : 'gray.800'} width='100%'>
        <Text fontWeight='bold' fontSize='sm' mb={2}>
          {label}
        </Text>
        <VStack align='stretch' spacing={1} fontSize='sm'>
          <HStack>
            <Text fontWeight='semibold' minW='100px'>
              Start:
            </Text>
            <Text>{formatMetadataTimestamp(metadata, formatLocalDateTime) || 'N/A'}</Text>
          </HStack>
          {metadata.endTime && (
            <HStack>
              <Text fontWeight='semibold' minW='100px'>
                End:
              </Text>
              <Text>{formatLocalDateTime(metadata.endTime)}</Text>
            </HStack>
          )}
          {metadata.backupTool && (
            <HStack>
              <Text fontWeight='semibold' minW='100px'>
                Tool:
              </Text>
              <Text fontFamily='monospace'>{metadata.backupTool}</Text>
            </HStack>
          )}
          {metadata.backupSessionID && (
            <HStack align='flex-start'>
              <Text fontWeight='semibold' minW='100px'>
                Session:
              </Text>
              <Text fontSize='xs' fontFamily='monospace' wordBreak='break-all'>
                {metadata.backupSessionID}
              </Text>
            </HStack>
          )}
        </VStack>
      </Box>
    )
  }

  const getSelectedSnapshotObject = () => {
    if (!selectedSnapshot || !snapshots) return null
    return snapshots.find((snapshot) => snapshot.id === selectedSnapshot)
  }

  useEffect(() => {
    if (!isOpen) {
      return
    }
    if (!useResticSnapshot) {
      return
    }
    if (clusterName && (operationType === 'logical-backup' || operationType === 'physical-backup')) {
      setIsLoadingSnapshots(true)
      dispatch(getResticSnapshot({ clusterName, filter: LATEST_SESSION_FILTER })).finally(() =>
        setIsLoadingSnapshots(false)
      )
    }
  }, [isOpen, clusterName, operationType, useResticSnapshot, dispatch])

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
      setResticTempDir('')
    }
  }, [isOpen])

  useEffect(() => {
    if (useResticSnapshot) {
      return
    }
    setResticStrategy('auto')
    setResticCleanup(true)
    setResticTempDir('')
  }, [useResticSnapshot])

  const handleConfirm = () => {
    onConfirm({
      useRestic: useResticSnapshot,
      snapshotId: selectedSnapshot,
      strategy: useResticSnapshot ? resticStrategy : undefined,
      cleanup: useResticSnapshot ? resticCleanup : undefined,
      tempDir: useResticSnapshot && resticTempDir ? resticTempDir : undefined
    })
  }

  const getOperationDetails = () => {
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
  }

  const details = getOperationDetails()
  const selectedSnapshotObj = getSelectedSnapshotObject()
  const selectedSnapshotTags = selectedSnapshotObj ? parseSnapshotTags(selectedSnapshotObj.tags || []) : null
  const selectedSnapshotStatusBadge = selectedSnapshotTags ? renderStatusBadge(selectedSnapshotTags.status) : null
  const selectedSnapshotPrimaryPath = selectedSnapshotObj?.paths?.[0] || ''
  const selectedSnapshotPathInfo = selectedSnapshotObj
    ? extractServerInfoFromPath(selectedSnapshotPrimaryPath, selectedSnapshotObj.tags || [])
    : null
  const resolvedBackupTool =
    selectedSnapshotTags?.backupTool || selectedSnapshotPathInfo?.backupTool || backupType || null
  const preferredMetadata = selectedSnapshotObj ? getPreferredMetadata(selectedSnapshotObj, operationType) : null
  const logicalMetadata = selectedSnapshotObj ? getSnapshotMetadataByMethod(selectedSnapshotObj, 'logical') : null
  const physicalMetadata = selectedSnapshotObj ? getSnapshotMetadataByMethod(selectedSnapshotObj, 'physical') : null
  const resolvedMethod =
    selectedSnapshotTags?.backupType ||
    preferredMetadata?.backupMethod ||
    (operationType === 'physical-backup' ? 'physical' : 'logical')
  const resolvedCreatedTime = preferredMetadata
    ? formatMetadataTimestamp(preferredMetadata, formatLocalDateTime)
    : selectedSnapshotObj
      ? formatLocalDateTime(selectedSnapshotObj.time)
      : null
  const selectedMetadataReady = selectedSnapshotObj?.metadataReady ?? false
  const selectedMetadataStatus = selectedSnapshotObj?.metadataStatus || 'unknown'
  const selectedMetadataError = selectedSnapshotObj?.metadataError || ''
  const isMetadataLoading = useResticSnapshot && Boolean(selectedSnapshotObj) && !selectedMetadataReady
  const dumpAllowed = resolvedMethod !== 'physical' && (resolvedBackupTool || '').toLowerCase() === 'mysqldump'

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
                    <Checkbox isChecked={useResticSnapshot} onChange={(e) => setUseResticSnapshot(e.target.checked)}>
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
                        <HStack spacing={2} mb={2}>
                          <Badge colorScheme='green' variant='subtle' fontSize='0.65rem'>
                            latest
                          </Badge>
                          <Text fontSize='xs' color={theme === 'light' ? 'gray.600' : 'gray.300'}>
                            Showing the most recent snapshot per backup session ({LATEST_SESSION_FILTER})
                          </Text>
                        </HStack>
                        <Select
                          value={selectedSnapshot}
                          onChange={(e) => setSelectedSnapshot(e.target.value)}
                          placeholder='Select a snapshot'
                          mb={3}
                        >
                          {snapshots.map((snapshot) => {
                            const preferredMeta = getPreferredMetadata(snapshot, operationType)
                            const formattedTime =
                              formatMetadataTimestamp(preferredMeta, formatLocalDateTime) ||
                              formatLocalDateTime(snapshot.time)
                            const primaryPath = snapshot.paths?.[0] || ''
                            const { clusterName, serverHost, serverPort, isAdhoc, backupTool } =
                              extractServerInfoFromPath(primaryPath, snapshot.tags || [])
                            const tagMeta = parseSnapshotTags(snapshot.tags || [])
                            let displayText = `${snapshot.short_id} - ${formattedTime} - ${clusterName} - ${serverHost}:${serverPort}`
                            if (isAdhoc && backupTool) {
                              displayText += ` (adhoc:${backupTool})`
                            }
                            if (preferredMeta?.backupMethod && preferredMeta?.backupMethod !== tagMeta.backupType) {
                              displayText += ` (${preferredMeta.backupMethod})`
                            }
                            if (tagMeta.status === 'available') {
                              displayText += ' [latest]'
                            } else if (tagMeta.status === 'orphaned') {
                              displayText += ' [orphaned]'
                            }
                            if (!snapshot.metadataReady) {
                              displayText += ' [metadata pending]'
                            }

                            return (
                              <option key={snapshot.id} value={snapshot.id}>
                                {displayText}
                              </option>
                            )
                          })}
                        </Select>

                        <Box
                          borderWidth='1px'
                          borderRadius='md'
                          p={3}
                          bg={theme === 'light' ? 'gray.50' : 'gray.700'}
                          mb={3}
                        >
                          <VStack align='stretch' spacing={3}>
                            <FormControl>
                              <FormLabel fontWeight='bold' fontSize='sm'>
                                Restic Strategy
                              </FormLabel>
                              <Select value={resticStrategy} onChange={(e) => setResticStrategy(e.target.value)}>
                                <option value='auto'>Auto (recommended)</option>
                                <option value='restore'>Restore (extract to disk)</option>
                                <option value='mount'>Mount (FUSE)</option>
                                {dumpAllowed && <option value='dump'>Dump (stream)</option>}
                              </Select>
                              <Text fontSize='xs' color={theme === 'light' ? 'gray.600' : 'gray.300'} mt={1}>
                                Auto picks the best strategy based on backup type. Mount requires FUSE; dump is for
                                single-file logical backups.
                              </Text>
                            </FormControl>
                            {resticStrategy !== 'mount' && (
                              <FormControl>
                                <FormLabel fontWeight='bold' fontSize='sm'>
                                  Temporary Directory (optional)
                                </FormLabel>
                                <Input
                                  value={resticTempDir}
                                  onChange={(e) => setResticTempDir(e.target.value)}
                                  placeholder='/var/lib/repman/restic-tmp'
                                />
                                <Text fontSize='xs' color={theme === 'light' ? 'gray.600' : 'gray.300'} mt={1}>
                                  Leave empty to use server default.
                                </Text>
                              </FormControl>
                            )}
                            <FormControl>
                              <Checkbox isChecked={resticCleanup} onChange={(e) => setResticCleanup(e.target.checked)}>
                                Cleanup temporary files after reseed
                              </Checkbox>
                            </FormControl>
                          </VStack>
                        </Box>

                        {useResticSnapshot && selectedSnapshotObj && (
                          <Box
                            borderWidth='1px'
                            borderRadius='md'
                            p={4}
                            bg={theme === 'light' ? 'gray.50' : 'gray.700'}
                          >
                            <Text
                              fontWeight='bold'
                              mb={3}
                              fontSize='sm'
                              color={theme === 'light' ? 'gray.700' : 'gray.200'}
                            >
                              Snapshot Details
                            </Text>
                            <VStack align='stretch' spacing={2}>
                              <HStack>
                                <Text fontWeight='semibold' fontSize='sm' minW='130px'>
                                  Hostname:
                                </Text>
                                <Text fontSize='sm' fontFamily='monospace'>
                                  {selectedSnapshotObj.hostname || 'N/A'}
                                </Text>
                              </HStack>

                              <HStack align='flex-start'>
                                <Text fontWeight='semibold' fontSize='sm' minW='130px'>
                                  Snapshot ID:
                                </Text>
                                <Text fontSize='xs' fontFamily='monospace' wordBreak='break-all'>
                                  {selectedSnapshotObj.id}
                                </Text>
                              </HStack>

                              <HStack>
                                <Text fontWeight='semibold' fontSize='sm' minW='130px'>
                                  Created:
                                </Text>
                                <Text fontSize='sm'>{resolvedCreatedTime || 'N/A'}</Text>
                              </HStack>

                              {selectedSnapshotStatusBadge && (
                                <HStack>
                                  <Text fontWeight='semibold' fontSize='sm' minW='130px'>
                                    Status:
                                  </Text>
                                  {selectedSnapshotStatusBadge}
                                  {selectedSnapshotTags?.isLatestView && (
                                    <Badge colorScheme='green' variant='outline' fontSize='0.6rem' ml={1}>
                                      latest
                                    </Badge>
                                  )}
                                </HStack>
                              )}

                              {selectedSnapshotTags?.sessionId && (
                                <HStack align='flex-start'>
                                  <Text fontWeight='semibold' fontSize='sm' minW='130px'>
                                    Session ID:
                                  </Text>
                                  <Text fontSize='xs' fontFamily='monospace' wordBreak='break-all'>
                                    {selectedSnapshotTags.sessionId}
                                  </Text>
                                </HStack>
                              )}

                              <HStack>
                                <Text fontWeight='semibold' fontSize='sm' minW='130px'>
                                  Metadata:
                                </Text>
                                <Badge colorScheme={selectedMetadataReady ? 'green' : 'orange'} fontSize='xs'>
                                  {selectedMetadataStatus}
                                </Badge>
                              </HStack>
                              {selectedMetadataError && (
                                <Text fontSize='xs' color='red.400' pl={selectedMetadataReady ? 0 : 0}>
                                  {selectedMetadataError}
                                </Text>
                              )}

                              {resolvedBackupTool && (
                                <HStack>
                                  <Text fontWeight='semibold' fontSize='sm' minW='130px'>
                                    Backup Tool:
                                  </Text>
                                  <HStack spacing={2}>
                                    <Text fontSize='sm' fontFamily='monospace'>
                                      {resolvedBackupTool}
                                    </Text>
                                    {selectedSnapshotPathInfo?.isAdhoc && (
                                      <Badge colorScheme='purple' fontSize='xs'>
                                        adhoc
                                      </Badge>
                                    )}
                                  </HStack>
                                </HStack>
                              )}

                              {resolvedMethod && (
                                <HStack>
                                  <Text fontWeight='semibold' fontSize='sm' minW='130px'>
                                    Method:
                                  </Text>
                                  <Text fontSize='sm' fontFamily='monospace'>
                                    {resolvedMethod}
                                  </Text>
                                </HStack>
                              )}

                              {selectedSnapshotPathInfo?.epoch && (
                                <HStack>
                                  <Text fontWeight='semibold' fontSize='sm' minW='130px'>
                                    Backup Timestamp:
                                  </Text>
                                  <Text fontSize='sm'>{formatEpochDateTime(selectedSnapshotPathInfo.epoch)}</Text>
                                </HStack>
                              )}

                              {(logicalMetadata || physicalMetadata) && (
                                <VStack align='stretch' spacing={2} mt={2}>
                                  <MetadataSummary label='Logical backup' metadata={logicalMetadata} theme={theme} />
                                  <MetadataSummary label='Physical backup' metadata={physicalMetadata} theme={theme} />
                                </VStack>
                              )}

                              {selectedSnapshotObj.tags && selectedSnapshotObj.tags.length > 0 && (
                                <HStack align='flex-start'>
                                  <Text fontWeight='semibold' fontSize='sm' minW='130px'>
                                    Tags:
                                  </Text>
                                  <Wrap>
                                    {selectedSnapshotObj.tags.map((tag, index) => (
                                      <WrapItem key={index}>
                                        <Badge colorScheme='blue' fontSize='xs'>
                                          {tag}
                                        </Badge>
                                      </WrapItem>
                                    ))}
                                  </Wrap>
                                </HStack>
                              )}

                              {selectedSnapshotObj.paths && selectedSnapshotObj.paths.length > 0 && (
                                <VStack align='stretch' spacing={1}>
                                  <Text fontWeight='semibold' fontSize='sm'>
                                    Paths:
                                  </Text>
                                  <VStack align='stretch' spacing={1} pl={4}>
                                    {selectedSnapshotObj.paths.map((path, index) => (
                                      <Text
                                        key={index}
                                        fontSize='xs'
                                        fontFamily='monospace'
                                        color={theme === 'light' ? 'gray.600' : 'gray.400'}
                                      >
                                        • {path}
                                      </Text>
                                    ))}
                                  </VStack>
                                </VStack>
                              )}
                            </VStack>
                          </Box>
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
            isDisabled={
              (operationType === 'logical-backup' || operationType === 'physical-backup') &&
              useResticSnapshot &&
              (!selectedSnapshot || isLoadingSnapshots || isMetadataLoading)
            }
          >
            Confirm Reseed
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default AdvancedReseedModal
