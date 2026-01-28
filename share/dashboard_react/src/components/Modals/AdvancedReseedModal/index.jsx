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

function AdvancedReseedModal({
  isOpen,
  closeModal,
  onConfirm,
  operationType,
  clusterName,
  serverInfo,
  backupType
}) {
  const { theme } = useTheme()
  const dispatch = useDispatch()
  
  const [selectedSnapshot, setSelectedSnapshot] = useState('')
  const [isLoadingSnapshots, setIsLoadingSnapshots] = useState(false)
  
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
      
      const isAdhocTagged = tags.some(tag => tag === 'adhoc' || tag.includes('line:adhoc'))
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

  const getSelectedSnapshotObject = () => {
    if (!selectedSnapshot || !snapshots) return null
    return snapshots.find(snapshot => snapshot.id === selectedSnapshot)
  }

  useEffect(() => {
    if (isOpen && clusterName && (operationType === 'logical-backup' || operationType === 'physical-backup')) {
      setIsLoadingSnapshots(true)
      dispatch(getResticSnapshot({ clusterName }))
        .finally(() => setIsLoadingSnapshots(false))
    }
  }, [isOpen, clusterName, operationType, dispatch])

  useEffect(() => {
    if (snapshots && snapshots.length > 0 && !selectedSnapshot) {
      setSelectedSnapshot(snapshots[0].id)
    }
  }, [snapshots, selectedSnapshot])

  useEffect(() => {
    if (!isOpen) {
      setSelectedSnapshot('')
    }
  }, [isOpen])

  const handleConfirm = () => {
    onConfirm(selectedSnapshot)
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

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size="xl">
      <ModalOverlay />
      <ModalContent
        className={`${styles.modalContent} ${theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}`}>
        <ModalHeader className={styles.modalHeader}>
          <HStack spacing={2}>
            <Text fontSize="xl">{details.icon}</Text>
            <Text>{details.title}</Text>
          </HStack>
        </ModalHeader>
        <ModalCloseButton />
        
        <ModalBody className={styles.modalBody}>
          <VStack spacing={4} align="stretch">
            <Alert status="warning" borderRadius="md">
              <AlertIcon />
              <Box>
                <AlertTitle>Destructive Operation</AlertTitle>
                <AlertDescription>
                  This operation will completely replace the database contents. All current data on this server will be lost.
                </AlertDescription>
              </Box>
            </Alert>

            <Box className={styles.infoSection}>
              <Text fontWeight="bold" mb={2}>Target Server:</Text>
              <VStack align="stretch" spacing={1} pl={4}>
                <HStack>
                  <Text fontWeight="semibold" minW="80px">Host:</Text>
                  <Text fontFamily="monospace">{serverInfo.host}:{serverInfo.port}</Text>
                </HStack>
                <HStack>
                  <Text fontWeight="semibold" minW="80px">Server ID:</Text>
                  <Text fontFamily="monospace">{serverInfo.id}</Text>
                </HStack>
              </VStack>
            </Box>

            <Divider />

            <Box className={styles.infoSection}>
              <Text fontWeight="bold" mb={2}>Operation Details:</Text>
              <VStack align="stretch" spacing={1} pl={4}>
                <HStack>
                  <Text fontWeight="semibold" minW="120px">Backup Tool:</Text>
                  <Text fontFamily="monospace" color="blue.500">{backupType}</Text>
                </HStack>
                <HStack>
                  <Text fontWeight="semibold" minW="120px">Source:</Text>
                  <Text>{details.source}</Text>
                </HStack>
                <HStack>
                  <Text fontWeight="semibold" minW="120px">Method:</Text>
                  <Text>{details.method}</Text>
                </HStack>
              </VStack>
            </Box>

            <Box className={styles.descriptionSection}>
              <Text fontSize="sm" color="gray.600">
                {details.description}
              </Text>
            </Box>

            {(operationType === 'logical-backup' || operationType === 'physical-backup') && (
              <>
                <Divider />
                <Box className={styles.infoSection}>
                  <FormControl isRequired>
                    <FormLabel fontWeight="bold">Select Backup Snapshot:</FormLabel>
                    {isLoadingSnapshots ? (
                      <HStack spacing={2} p={2}>
                        <Spinner size="sm" />
                        <Text fontSize="sm">Loading snapshots...</Text>
                      </HStack>
                    ) : snapshots && snapshots.length > 0 ? (
                      <>
                        <Select
                          value={selectedSnapshot}
                          onChange={(e) => setSelectedSnapshot(e.target.value)}
                          placeholder="Select a snapshot"
                          mb={3}
                        >
                          {snapshots.map((snapshot) => {
                            const formattedTime = formatLocalDateTime(snapshot.time)
                            const primaryPath = snapshot.paths?.[0] || ''
                            const { clusterName, serverHost, serverPort, isAdhoc, backupTool } = 
                              extractServerInfoFromPath(primaryPath, snapshot.tags || [])
                            
                            let displayText = `${snapshot.short_id} - ${formattedTime} - ${clusterName} - ${serverHost}:${serverPort}`
                            if (isAdhoc && backupTool) {
                              displayText += ` (adhoc:${backupTool})`
                            }
                            
                            return (
                              <option key={snapshot.id} value={snapshot.id}>
                                {displayText}
                              </option>
                            )
                          })}
                        </Select>

                        {selectedSnapshotObj && (
                          <Box 
                            borderWidth="1px" 
                            borderRadius="md" 
                            p={4} 
                            bg={theme === 'light' ? 'gray.50' : 'gray.700'}
                          >
                            <Text fontWeight="bold" mb={3} fontSize="sm" color={theme === 'light' ? 'gray.700' : 'gray.200'}>
                              Snapshot Details
                            </Text>
                            <VStack align="stretch" spacing={2}>
                              <HStack>
                                <Text fontWeight="semibold" fontSize="sm" minW="130px">Hostname:</Text>
                                <Text fontSize="sm" fontFamily="monospace">{selectedSnapshotObj.hostname || 'N/A'}</Text>
                              </HStack>

                              <HStack align="flex-start">
                                <Text fontWeight="semibold" fontSize="sm" minW="130px">Snapshot ID:</Text>
                                <Text fontSize="xs" fontFamily="monospace" wordBreak="break-all">
                                  {selectedSnapshotObj.id}
                                </Text>
                              </HStack>

                              <HStack>
                                <Text fontWeight="semibold" fontSize="sm" minW="130px">Created:</Text>
                                <Text fontSize="sm">{formatLocalDateTime(selectedSnapshotObj.time)}</Text>
                              </HStack>

                              {(() => {
                                const primaryPath = selectedSnapshotObj.paths?.[0] || ''
                                const { isAdhoc, backupTool, epoch } = 
                                  extractServerInfoFromPath(primaryPath, selectedSnapshotObj.tags || [])
                                
                                if (isAdhoc && backupTool) {
                                  return (
                                    <>
                                      <HStack>
                                        <Text fontWeight="semibold" fontSize="sm" minW="130px">Backup Tool:</Text>
                                        <HStack spacing={2}>
                                          <Text fontSize="sm" fontFamily="monospace">{backupTool}</Text>
                                          <Badge colorScheme="purple" fontSize="xs">adhoc</Badge>
                                        </HStack>
                                      </HStack>
                                      {epoch && (
                                        <HStack>
                                          <Text fontWeight="semibold" fontSize="sm" minW="130px">Backup Timestamp:</Text>
                                          <Text fontSize="sm">{formatEpochDateTime(epoch)}</Text>
                                        </HStack>
                                      )}
                                    </>
                                  )
                                }
                                return null
                              })()}

                              {selectedSnapshotObj.tags && selectedSnapshotObj.tags.length > 0 && (
                                <HStack align="flex-start">
                                  <Text fontWeight="semibold" fontSize="sm" minW="130px">Tags:</Text>
                                  <Wrap>
                                    {selectedSnapshotObj.tags.map((tag, index) => (
                                      <WrapItem key={index}>
                                        <Badge colorScheme="blue" fontSize="xs">
                                          {tag}
                                        </Badge>
                                      </WrapItem>
                                    ))}
                                  </Wrap>
                                </HStack>
                              )}

                              {selectedSnapshotObj.paths && selectedSnapshotObj.paths.length > 0 && (
                                <VStack align="stretch" spacing={1}>
                                  <Text fontWeight="semibold" fontSize="sm">Paths:</Text>
                                  <VStack align="stretch" spacing={1} pl={4}>
                                    {selectedSnapshotObj.paths.map((path, index) => (
                                      <Text key={index} fontSize="xs" fontFamily="monospace" color={theme === 'light' ? 'gray.600' : 'gray.400'}>
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
                      <Alert status="warning" borderRadius="md" size="sm">
                        <AlertIcon />
                        <AlertDescription fontSize="sm">
                          No snapshots available. Please create a backup first.
                        </AlertDescription>
                      </Alert>
                    )}
                  </FormControl>
                </Box>
              </>
            )}

            {operationType === 'logical-master' && (
              <Alert status="info" borderRadius="md" size="sm">
                <AlertIcon />
                <AlertDescription fontSize="sm">
                  The master database will remain available during this operation. Replication will be reconfigured after restore.
                </AlertDescription>
              </Alert>
            )}

            {operationType === 'physical-backup' && (
              <Alert status="info" borderRadius="md" size="sm">
                <AlertIcon />
                <AlertDescription fontSize="sm">
                  The database will be stopped during the physical restore operation and restarted automatically.
                </AlertDescription>
              </Alert>
            )}
          </VStack>
        </ModalBody>

        <ModalFooter gap={3}>
          <RMButton variant='outline' colorScheme='white' size='medium' onClick={closeModal}>
            Cancel
          </RMButton>
          <RMButton 
            colorScheme='red' 
            size='medium' 
            onClick={handleConfirm}
            isDisabled={
              (operationType === 'logical-backup' || operationType === 'physical-backup') 
              && (!selectedSnapshot || isLoadingSnapshots)
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
