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
  AlertDescription
} from '@chakra-ui/react'
import React from 'react'
import RMButton from '../../RMButton'
import styles from './styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'

/**
 * AdvancedReseedModal - Enhanced confirmation modal for database reseed operations
 * 
 * Provides detailed information about the reseed operation including:
 * - Operation type and destructive nature
 * - Server details (host, port, ID)
 * - Backup tool being used
 * - Warning messages about data loss
 * 
 * @param {Object} props - Component properties
 * @param {boolean} props.isOpen - Whether modal is open
 * @param {Function} props.closeModal - Function to close the modal
 * @param {Function} props.onConfirm - Function to execute on confirmation
 * @param {string} props.operationType - Type of reseed operation (logical-backup, logical-master, physical-backup)
 * @param {Object} props.serverInfo - Server information object
 * @param {string} props.serverInfo.id - Server ID
 * @param {string} props.serverInfo.host - Server hostname/IP
 * @param {number} props.serverInfo.port - Server port
 * @param {string} props.backupType - Type of backup tool being used
 */
function AdvancedReseedModal({
  isOpen,
  closeModal,
  onConfirm,
  operationType,
  serverInfo,
  backupType
}) {
  const { theme } = useTheme()

  // Determine operation details based on type
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
            {/* Warning Alert */}
            <Alert status="warning" borderRadius="md">
              <AlertIcon />
              <Box>
                <AlertTitle>Destructive Operation</AlertTitle>
                <AlertDescription>
                  This operation will completely replace the database contents. All current data on this server will be lost.
                </AlertDescription>
              </Box>
            </Alert>

            {/* Server Information */}
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

            {/* Operation Details */}
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

            {/* Description */}
            <Box className={styles.descriptionSection}>
              <Text fontSize="sm" color="gray.600">
                {details.description}
              </Text>
            </Box>

            {/* Additional Info Based on Operation Type */}
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
          <RMButton colorScheme='red' size='medium' onClick={onConfirm}>
            Confirm Reseed
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default AdvancedReseedModal
