import React, { useEffect, useState } from 'react'
import {
  Box,
  Button,
  Flex,
  HStack,
  useDisclosure,
  Spinner,
  Textarea,
  VStack
} from '@chakra-ui/react'
import { useDispatch, useSelector } from 'react-redux'
import {
  getSQLScriptJobs,
  executeSQLScript,
  triggerScheduledScripts,
  saveSQLScriptJob,
  deleteSQLScriptJob
} from '../../redux/clusterSlice'
import AccordionComponent from '../../components/AccordionComponent'
import ExecuteScriptModal from './ExecuteScriptModal'
import JobManagementModal from './JobManagementModal'
import JobList from './JobList'
import ExecutionHistory from './ExecutionHistory'
import styles from './styles.module.scss'

function SqlScripts({ selectedCluster, user }) {
  const dispatch = useDispatch()

  const jobs = useSelector((state) => state.cluster.sqlScripts.jobs)
  const executionHistory = useSelector((state) => state.cluster.sqlScripts.executionHistory)
  const clusterName = selectedCluster?.name

  const {
    isOpen: isExecuteOpen,
    onOpen: onExecuteOpen,
    onClose: onExecuteClose
  } = useDisclosure()

  const {
    isOpen: isJobManagementOpen,
    onOpen: onJobManagementOpen,
    onClose: onJobManagementClose
  } = useDisclosure()

  const {
    isOpen: isQuickExecuteOpen,
    onToggle: onQuickExecuteToggle
  } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isQuickExecuteOpen')) === false ? false : true
  })

  const {
    isOpen: isScheduledJobsOpen,
    onToggle: onScheduledJobsToggle
  } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isScheduledJobsOpen')) || false
  })

  const {
    isOpen: isHistoryOpen,
    onToggle: onHistoryToggle
  } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isHistoryOpen')) || false
  })

  // Load jobs on component mount and when cluster changes
  useEffect(() => {
    if (clusterName) {
      dispatch(getSQLScriptJobs({ clusterName: clusterName }))
    }
  }, [clusterName, dispatch])

  const handleExecuteScript = async (formData) => {
    const result = await dispatch(
      executeSQLScript({
        clusterName: clusterName,
        payload: formData
      })
    ).unwrap()
    onExecuteClose()
    return result.data
  }

  const handleTriggerScheduled = () => {
    dispatch(triggerScheduledScripts({ clusterName: clusterName }))
  }

  const handleSaveJob = async (jobData) => {
    await dispatch(
      saveSQLScriptJob({
        clusterName: clusterName,
        job: jobData
      })
    ).unwrap()
    dispatch(getSQLScriptJobs({ clusterName: clusterName }))
    onJobManagementClose()
  }

  const handleDeleteJob = async (jobName) => {
    await dispatch(
      deleteSQLScriptJob({
        clusterName: clusterName,
        jobName
      })
    ).unwrap()
    dispatch(getSQLScriptJobs({ clusterName: clusterName }))
  }

  if (!user || !user.grants['db-query-execute']) {
    return null
  }

  return (
    <Flex className={styles.sqlScriptsContainer}>
      {/* Quick Execute Section */}
      <AccordionComponent
        heading="Quick Execute"
        isOpen={isQuickExecuteOpen}
        onToggle={() => {
          onQuickExecuteToggle()
          localStorage.setItem('isQuickExecuteOpen', JSON.stringify(!isQuickExecuteOpen))
        }}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<ExecuteQuickForm onExecute={handleExecuteScript} onOpenAdvanced={onExecuteOpen} />}
      />

      {/* Scheduled Jobs Section */}
      <AccordionComponent
        heading={`Scheduled Jobs (${jobs?.length || 0})`}
        isOpen={isScheduledJobsOpen}
        onToggle={() => {
          onScheduledJobsToggle()
          localStorage.setItem('isScheduledJobsOpen', JSON.stringify(!isScheduledJobsOpen))
        }}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        actions={
          <HStack spacing={2}>
            <Button
              size="sm"
              colorScheme="green"
              onClick={onJobManagementOpen}
            >
              Manage Jobs
            </Button>
            <Button
              size="sm"
              colorScheme="orange"
              onClick={handleTriggerScheduled}
            >
              Trigger All
            </Button>
          </HStack>
        }
        body={
          <JobList
            jobs={jobs || []}
            onDelete={handleDeleteJob}
            onRefresh={() => dispatch(getSQLScriptJobs({ clusterName: clusterName }))}
          />
        }
      />

      {/* Execution History Section */}
      <AccordionComponent
        heading="Execution History"
        isOpen={isHistoryOpen}
        onToggle={() => {
          onHistoryToggle()
          localStorage.setItem('isHistoryOpen', JSON.stringify(!isHistoryOpen))
        }}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<ExecutionHistory history={executionHistory || []} />}
      />

      {/* Modals */}
      <ExecuteScriptModal
        isOpen={isExecuteOpen}
        onClose={onExecuteClose}
        onExecute={handleExecuteScript}
      />

      <JobManagementModal
        isOpen={isJobManagementOpen}
        onClose={onJobManagementClose}
        onSave={handleSaveJob}
        jobs={jobs}
        onDeleteJob={handleDeleteJob}
      />
    </Flex>
  )
}

// Quick Execute Form Component
const ExecuteQuickForm = ({ onExecute, loading, onOpenAdvanced }) => {
  const [scriptContent, setScriptContent] = useState('')
  const [targetDatabase, setTargetDatabase] = useState('')
  const [targetServer, setTargetServer] = useState('master')
  const [timeout, setTimeout] = useState(300)
  const [isExecuting, setIsExecuting] = useState(false)

  const handleExecute = async () => {
    if (!scriptContent.trim()) {
      return
    }

    setIsExecuting(true)
    try {
      await onExecute({
        scriptContent,
        targetDatabase,
        targetServer,
        timeout: parseInt(timeout)
      })

      // Clear form
      setScriptContent('')
      setTargetDatabase('')
      setTargetServer('master')
      setTimeout(300)
    } catch (err) {
      // Error handled by Redux
    } finally {
      setIsExecuting(false)
    }
  }

  return (
    <Box p={4}>
      <VStack spacing={4} align="stretch">
        <Textarea
          value={scriptContent}
          onChange={(e) => setScriptContent(e.target.value)}
          placeholder="Enter SQL commands here..."
          rows={10}
          fontFamily="monospace"
          fontSize="sm"
        />

        <HStack spacing={4}>
          <Box flex={1}>
            <label style={{ fontSize: '14px', fontWeight: 500, marginBottom: '8px', display: 'block' }}>Target Database (Optional)</label>
            <input
              style={{
                width: '100%',
                padding: '8px 12px',
                borderRadius: '4px',
                border: '1px solid #E2E8F0',
                fontSize: '14px',
                fontFamily: 'inherit',
                boxSizing: 'border-box'
              }}
              type="text"
              value={targetDatabase}
              onChange={(e) => setTargetDatabase(e.target.value)}
              placeholder="e.g., mydb"
            />
          </Box>

          <Box flex={1}>
            <label style={{ fontSize: '14px', fontWeight: 500, marginBottom: '8px', display: 'block' }}>Timeout (seconds)</label>
            <input
              style={{
                width: '100%',
                padding: '8px 12px',
                borderRadius: '4px',
                border: '1px solid #E2E8F0',
                fontSize: '14px',
                fontFamily: 'inherit',
                boxSizing: 'border-box'
              }}
              type="number"
              value={timeout}
              onChange={(e) => setTimeout(e.target.value)}
              min="10"
              max="3600"
            />
          </Box>
        </HStack>
        
        <Box
          style={{
            padding: '12px',
            backgroundColor: '#F7FAFC',
            borderRadius: '4px',
            border: '1px solid #E2E8F0',
            fontSize: '14px'
          }}
        >
          <strong style={{ color: '#2D3748' }}>Target Server:</strong> <span style={{ color: '#4A5568' }}>Master (Primary)</span>
        </Box>

        <HStack spacing={2}>
          <Button
            colorScheme="blue"
            onClick={handleExecute}
            isLoading={isExecuting || loading}
            disabled={!scriptContent.trim()}
          >
            Execute
          </Button>
          <Button
            variant="outline"
            onClick={onOpenAdvanced}
          >
            Advanced Options
          </Button>
        </HStack>
      </VStack>
    </Box>
  )
}

export default SqlScripts
