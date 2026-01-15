import React from 'react'
import {
  Box,
  VStack,
  HStack,
  Button,
  Text,
  Badge,
  useDisclosure,
  Alert,
  AlertIcon
} from '@chakra-ui/react'
import { HiTrash, HiRefresh } from 'react-icons/hi'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import styles from './styles.module.scss'

function JobList({ jobs = [], onDelete, onRefresh, loading }) {
  const [deleteTarget, setDeleteTarget] = React.useState(null)
  const [isDeleting, setIsDeleting] = React.useState(false)

  const handleDelete = async (jobName) => {
    setIsDeleting(true)
    try {
      await onDelete(jobName)
      setDeleteTarget(null)
    } finally {
      setIsDeleting(false)
    }
  }

  const getStatusColor = (status) => {
    switch (status?.toLowerCase()) {
      case 'success':
        return 'green'
      case 'error':
        return 'red'
      case 'timeout':
        return 'orange'
      default:
        return 'gray'
    }
  }

  const getEnabledColor = (enabled) => {
    return enabled ? 'green' : 'gray'
  }

  if (!jobs || jobs.length === 0) {
    return (
      <VStack spacing={4} align="center" py={10}>
        <Alert status="info" maxW="400px">
          <AlertIcon />
          No scheduled SQL script jobs. Create one to get started!
        </Alert>
        <Button colorScheme="green" onClick={onRefresh} isLoading={loading}>
          Refresh
        </Button>
      </VStack>
    )
  }

  return (
    <>
      <VStack spacing={4} align="stretch">
        {/* Header */}
        <HStack justify="space-between">
          <Text fontSize="lg" fontWeight="bold">
            Scheduled Jobs ({jobs.length})
          </Text>
          <Button
            size="sm"
            leftIcon={<HiRefresh />}
            onClick={onRefresh}
            isLoading={loading}
          >
            Refresh
          </Button>
        </HStack>

        {/* Jobs List */}
        {jobs.map((job) => (
          <Box
            key={job.name}
            className={styles.jobCard}
            bg="white"
            p={4}
            borderRadius="lg"
            borderWidth="1px"
            borderColor="gray.200"
            _hover={{ borderColor: 'blue.300', boxShadow: 'md' }}
            transition="all 0.2s"
          >
            <VStack align="stretch" spacing={3}>
              {/* Job Title and Status */}
              <HStack justify="space-between">
                <Box>
                  <HStack spacing={3} mb={1}>
                    <Text fontSize="lg" fontWeight="bold">
                      {job.name}
                    </Text>
                    <Badge colorScheme={getEnabledColor(job.enabled)}>
                      {job.enabled ? 'ENABLED' : 'DISABLED'}
                    </Badge>
                    {job.lastStatus && (
                      <Badge colorScheme={getStatusColor(job.lastStatus)}>
                        {job.lastStatus.toUpperCase()}
                      </Badge>
                    )}
                  </HStack>
                </Box>
                <HStack spacing={2}>
                  <Button
                    size="sm"
                    colorScheme="red"
                    variant="outline"
                    leftIcon={<HiTrash />}
                    onClick={() => setDeleteTarget(job.name)}
                  >
                    Delete
                  </Button>
                </HStack>
              </HStack>

              {/* Job Details Grid */}
              <Box className={styles.jobDetailsGrid}>
                <JobDetailItem
                  label="Schedule"
                  value={job.cronSchedule || '-'}
                  icon="📅"
                />
                <JobDetailItem
                  label="Script"
                  value={job.scriptPath || 'Inline script'}
                  icon="📄"
                />
                <JobDetailItem
                  label="Database"
                  value={job.targetDatabase || '-'}
                  icon="🗄️"
                />
                <JobDetailItem
                  label="Target"
                  value={job.targetServer || 'master'}
                  icon="🎯"
                />
                <JobDetailItem
                  label="Timeout"
                  value={`${job.timeout}s`}
                  icon="⏱️"
                />
                <JobDetailItem
                  label="Retries"
                  value={`${job.maxRetries}`}
                  icon="🔄"
                />
              </Box>

              {/* Execution Info */}
              {(job.lastRun || job.lastResult) && (
                <Box
                  bg="gray.50"
                  p={3}
                  borderRadius="md"
                  borderLeft="4px solid blue"
                >
                  <VStack align="flex-start" spacing={1} fontSize="sm">
                    {job.lastRun && (
                      <HStack>
                        <Text fontWeight="bold" minW="100px">
                          Last Run:
                        </Text>
                        <Text>
                          {new Date(job.lastRun).toLocaleString()}
                        </Text>
                      </HStack>
                    )}
                    {job.lastResult && (
                      <HStack>
                        <Text fontWeight="bold" minW="100px">
                          Last Result:
                        </Text>
                        <Text noOfLines={2} color="gray.600">
                          {job.lastResult}
                        </Text>
                      </HStack>
                    )}
                  </VStack>
                </Box>
              )}
            </VStack>
          </Box>
        ))}
      </VStack>

      {/* Delete Confirmation Modal */}
      <ConfirmModal
        isOpen={!!deleteTarget}
        title="Delete Job"
        onConfirm={() => handleDelete(deleteTarget)}
        onCancel={() => setDeleteTarget(null)}
        isLoading={isDeleting}
        isDangerous
      >
        Are you sure you want to delete the job "{deleteTarget}"? This action cannot be undone.
      </ConfirmModal>
    </>
  )
}

// Helper component for job detail items
const JobDetailItem = ({ label, value, icon = '•' }) => (
  <Box>
    <Text fontSize="xs" color="gray.500" fontWeight="bold">
      {icon} {label}
    </Text>
    <Text fontSize="sm" fontWeight="500" noOfLines={1}>
      {value}
    </Text>
  </Box>
)

export default JobList
