import React, { useState } from 'react'
import {
  Modal,
  ModalOverlay,
  ModalContent,
  ModalHeader,
  ModalCloseButton,
  ModalBody,
  ModalFooter,
  Button,
  VStack,
  HStack,
  Box,
  Tabs,
  TabList,
  Tab,
  TabPanels,
  TabPanel,
  FormControl,
  FormLabel,
  Input,
  Select,
  Textarea,
  Switch,
  Alert,
  AlertIcon,
  Text
} from '@chakra-ui/react'
import { HiTrash, HiPencil } from 'react-icons/hi'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import styles from './styles.module.scss'

function JobManagementModal({
  isOpen,
  onClose,
  onSave,
  jobs = [],
  onDeleteJob,
  loading
}) {
  const [tab, setTab] = useState(0)
  const [formData, setFormData] = useState(getEmptyJobForm())
  const [confirmDelete, setConfirmDelete] = useState(null)
  const [saving, setSaving] = useState(false)
  const [editingJobName, setEditingJobName] = useState(null)

  function getEmptyJobForm() {
    return {
      name: '',
      scriptPath: '',
      scriptContent: '',
      targetDatabase: '',
      targetServer: 'master',
      cronSchedule: '0 2 * * *',
      enabled: true,
      runOnce: false,
      maxRetries: 3,
      timeout: 300
    }
  }

  const handleNewJob = () => {
    setFormData(getEmptyJobForm())
    setEditingJobName(null)
    setTab(1)
  }

  const handleEditJob = (job) => {
    setFormData(job)
    setEditingJobName(job.name)
    setTab(1)
  }

  const handleSaveJob = async () => {
    if (!formData.name.trim()) {
      alert('Please enter job name')
      return
    }
    if (!formData.scriptPath.trim() && !formData.scriptContent.trim()) {
      alert('Please enter script path or content')
      return
    }

    setSaving(true)
    try {
      await onSave(formData)
      setFormData(getEmptyJobForm())
      setEditingJobName(null)
      setTab(0)
    } finally {
      setSaving(false)
    }
  }

  const handleDeleteClick = (job) => {
    setConfirmDelete(job.name)
  }

  const handleConfirmDelete = async () => {
    if (confirmDelete) {
      await onDeleteJob(confirmDelete)
      setConfirmDelete(null)
    }
  }

  const handleFormChange = (field, value) => {
    setFormData(prev => ({
      ...prev,
      [field]: value
    }))
  }

  const handleClose = () => {
    setTab(0)
    setFormData(getEmptyJobForm())
    setEditingJobName(null)
    onClose()
  }

  return (
    <>
      <Modal isOpen={isOpen} onClose={handleClose} size="2xl">
        <ModalOverlay />
        <ModalContent maxH="90vh" overflowY="auto">
          <ModalHeader>SQL Script Job Management</ModalHeader>
          <ModalCloseButton />
          <ModalBody>
            <Tabs index={tab} onChange={setTab}>
              <TabList>
                <Tab>Jobs List ({jobs?.length || 0})</Tab>
                <Tab>{editingJobName ? 'Edit Job' : 'New Job'}</Tab>
              </TabList>

              <TabPanels>
                {/* Jobs List Tab */}
                <TabPanel>
                  {jobs && jobs.length > 0 ? (
                    <VStack spacing={3} align="stretch">
                      {jobs.map((job) => (
                        <Box
                          key={job.name}
                          bg="gray.50"
                          p={4}
                          borderRadius="md"
                          borderLeft="4px solid blue"
                        >
                          <HStack justify="space-between" mb={2}>
                            <Box>
                              <Text fontWeight="bold" fontSize="lg">
                                {job.name}
                              </Text>
                              <Text fontSize="sm" color="gray.600">
                                Status: {job.enabled ? '✓ Enabled' : '✗ Disabled'}
                              </Text>
                            </Box>
                            <HStack spacing={2}>
                              <Button
                                size="sm"
                                leftIcon={<HiPencil />}
                                onClick={() => handleEditJob(job)}
                              >
                                Edit
                              </Button>
                              <Button
                                size="sm"
                                colorScheme="red"
                                leftIcon={<HiTrash />}
                                onClick={() => handleDeleteClick(job)}
                              >
                                Delete
                              </Button>
                            </HStack>
                          </HStack>

                          <VStack align="flex-start" fontSize="sm" spacing={1}>
                            {job.cronSchedule && (
                              <Box>
                                <strong>Schedule:</strong> {job.cronSchedule}
                              </Box>
                            )}
                            {job.scriptPath && (
                              <Box>
                                <strong>Path:</strong> {job.scriptPath}
                              </Box>
                            )}
                            {job.targetDatabase && (
                              <Box>
                                <strong>Database:</strong> {job.targetDatabase}
                              </Box>
                            )}
                            {job.targetServer && (
                              <Box>
                                <strong>Server:</strong> {job.targetServer}
                              </Box>
                            )}
                            {job.lastRun && (
                              <Box>
                                <strong>Last Run:</strong>{' '}
                                {new Date(job.lastRun).toLocaleString()}
                              </Box>
                            )}
                            {job.lastStatus && (
                              <Box>
                                <strong>Last Status:</strong> {job.lastStatus}
                              </Box>
                            )}
                          </VStack>
                        </Box>
                      ))}

                      <Button
                        colorScheme="green"
                        mt={4}
                        onClick={handleNewJob}
                        w="100%"
                      >
                        + Create New Job
                      </Button>
                    </VStack>
                  ) : (
                    <VStack spacing={4} align="center" py={8}>
                      <Text color="gray.500">No jobs created yet</Text>
                      <Button
                        colorScheme="green"
                        onClick={handleNewJob}
                      >
                        Create First Job
                      </Button>
                    </VStack>
                  )}
                </TabPanel>

                {/* Job Form Tab */}
                <TabPanel>
                  <VStack spacing={4} align="stretch">
                    <FormControl isRequired>
                      <FormLabel>Job Name</FormLabel>
                      <Input
                        type="text"
                        placeholder="e.g., daily-cleanup"
                        value={formData.name}
                        onChange={(e) => handleFormChange('name', e.target.value)}
                        isDisabled={!!editingJobName}
                      />
                    </FormControl>

                    <FormControl isRequired>
                      <FormLabel>Script Path</FormLabel>
                      <Input
                        type="text"
                        placeholder="/path/to/script.sql"
                        value={formData.scriptPath}
                        onChange={(e) => handleFormChange('scriptPath', e.target.value)}
                      />
                    </FormControl>

                    <FormControl>
                      <FormLabel>Or Inline Script Content</FormLabel>
                      <Textarea
                        placeholder="SQL commands..."
                        value={formData.scriptContent}
                        onChange={(e) => handleFormChange('scriptContent', e.target.value)}
                        rows={6}
                        fontFamily="monospace"
                        fontSize="sm"
                      />
                    </FormControl>

                    <FormControl isRequired>
                      <FormLabel>Cron Schedule</FormLabel>
                      <Input
                        type="text"
                        placeholder="0 2 * * * (daily at 2 AM)"
                        value={formData.cronSchedule}
                        onChange={(e) => handleFormChange('cronSchedule', e.target.value)}
                      />
                      <Text fontSize="xs" color="gray.500" mt={1}>
                        Format: minute hour day month dayOfWeek (e.g., "0 * * * *" = every hour)
                      </Text>
                    </FormControl>

                    <HStack spacing={4}>
                      <FormControl flex={1}>
                        <FormLabel>Target Database</FormLabel>
                        <Input
                          type="text"
                          placeholder="Leave empty for default"
                          value={formData.targetDatabase}
                          onChange={(e) => handleFormChange('targetDatabase', e.target.value)}
                        />
                      </FormControl>

                      <FormControl flex={1}>
                        <FormLabel>Timeout (seconds)</FormLabel>
                        <Input
                          type="number"
                          value={formData.timeout}
                          onChange={(e) => handleFormChange('timeout', parseInt(e.target.value))}
                          min="10"
                          max="3600"
                        />
                      </FormControl>

                      <FormControl flex={1}>
                        <FormLabel>Max Retries</FormLabel>
                        <Input
                          type="number"
                          value={formData.maxRetries}
                          onChange={(e) => handleFormChange('maxRetries', parseInt(e.target.value))}
                          min="0"
                          max="10"
                        />
                      </FormControl>
                    </HStack>

                    <Box
                      p={3}
                      bg="gray.50"
                      borderRadius="md"
                      borderWidth="1px"
                      borderColor="gray.200"
                      w="100%"
                    >
                      <Text fontSize="sm" color="gray.600">
                        <strong>Target Server:</strong> Master (Primary) - Jobs will execute on the primary database server
                      </Text>
                    </Box>

                    <HStack spacing={6}>
                      <FormControl display="flex" alignItems="center">
                        <FormLabel mb={0}>Enabled</FormLabel>
                        <Switch
                          isChecked={formData.enabled}
                          onChange={(e) => handleFormChange('enabled', e.target.checked)}
                        />
                      </FormControl>

                      <FormControl display="flex" alignItems="center">
                        <FormLabel mb={0}>Run Once</FormLabel>
                        <Switch
                          isChecked={formData.runOnce}
                          onChange={(e) => handleFormChange('runOnce', e.target.checked)}
                        />
                      </FormControl>
                    </HStack>

                    <Alert status="info">
                      <AlertIcon />
                      Define when this job should run and its execution parameters
                    </Alert>
                  </VStack>
                </TabPanel>
              </TabPanels>
            </Tabs>
          </ModalBody>

          <ModalFooter>
            <HStack spacing={2}>
              <Button variant="ghost" onClick={handleClose}>
                Close
              </Button>
              {tab === 1 && (
                <Button
                  colorScheme="blue"
                  onClick={handleSaveJob}
                  isLoading={saving || loading}
                >
                  Save Job
                </Button>
              )}
            </HStack>
          </ModalFooter>
        </ModalContent>
      </Modal>

      {/* Confirm Delete Modal */}
      <ConfirmModal
        isOpen={!!confirmDelete}
        title="Delete Job"
        onConfirm={handleConfirmDelete}
        onCancel={() => setConfirmDelete(null)}
        isLoading={loading}
        isDangerous
      >
        Are you sure you want to delete the job "{confirmDelete}"?
      </ConfirmModal>
    </>
  )
}

export default JobManagementModal
