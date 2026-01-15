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
  Alert,
  AlertIcon
} from '@chakra-ui/react'
import styles from './styles.module.scss'

function ExecuteScriptModal({ isOpen, onClose, onExecute, loading }) {
  const [tab, setTab] = useState(0)
  const [scriptPath, setScriptPath] = useState('')
  const [scriptContent, setScriptContent] = useState('')
  const [targetDatabase, setTargetDatabase] = useState('')
  const [targetServer, setTargetServer] = useState('master')
  const [timeout, setTimeout] = useState(300)
  const [executing, setExecuting] = useState(false)
  const [result, setResult] = useState(null)

  const handleExecute = async () => {
    if (tab === 0 && !scriptPath.trim()) {
      alert('Please enter script path')
      return
    }
    if (tab === 1 && !scriptContent.trim()) {
      alert('Please enter SQL content')
      return
    }

    setExecuting(true)
    try {
      const resultData = await onExecute({
        scriptPath: tab === 0 ? scriptPath : '',
        scriptContent: tab === 1 ? scriptContent : '',
        targetDatabase,
        targetServer,
        timeout: parseInt(timeout)
      })
      setResult(resultData)
    } finally {
      setExecuting(false)
    }
  }

  const handleClose = () => {
    setTab(0)
    setScriptPath('')
    setScriptContent('')
    setTargetDatabase('')
    setTargetServer('master')
    setTimeout(300)
    setResult(null)
    onClose()
  }

  if (result) {
    return (
      <Modal isOpen={isOpen} onClose={handleClose} size="lg">
        <ModalOverlay />
        <ModalContent>
          <ModalHeader>Execution Result</ModalHeader>
          <ModalCloseButton />
          <ModalBody>
            <VStack spacing={4} align="stretch">
              <Alert
                status={result.status === 'success' ? 'success' : 'error'}
                variant="subtle"
                flexDirection="column"
                alignItems="flex-start"
                p={4}
                borderRadius="md"
              >
                <HStack>
                  <AlertIcon />
                  <Box fontWeight="bold">
                    Status: {result.status?.toUpperCase()}
                  </Box>
                </HStack>
              </Alert>

              <Box>
                <strong>Duration:</strong> {result.duration?.toFixed(2)} seconds
              </Box>
              <Box>
                <strong>Rows Affected:</strong> {result.rowsAffected || 0}
              </Box>
              <Box>
                <strong>Server:</strong> {result.serverUrl}
              </Box>
              <Box>
                <strong>Database:</strong> {result.targetDatabase || 'N/A'}
              </Box>

              {result.errorMessage && (
                <Box
                  bg="red.50"
                  p={3}
                  borderRadius="md"
                  borderLeft="4px solid red"
                >
                  <strong>Error:</strong>
                  <Box fontSize="sm" color="red.700" mt={2}>
                    {result.errorMessage}
                  </Box>
                </Box>
              )}

              {result.scriptPath && (
                <Box>
                  <strong>Script:</strong> {result.scriptPath}
                </Box>
              )}
            </VStack>
          </ModalBody>
          <ModalFooter>
            <Button colorScheme="blue" onClick={handleClose}>
              Close
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    )
  }

  return (
    <Modal isOpen={isOpen} onClose={handleClose} size="2xl">
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>Execute SQL Script</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Tabs index={tab} onChange={setTab}>
            <TabList>
              <Tab>From File</Tab>
              <Tab>Inline SQL</Tab>
            </TabList>

            <TabPanels>
              {/* From File Tab */}
              <TabPanel>
                <VStack spacing={4}>
                  <FormControl>
                    <FormLabel>Script Path</FormLabel>
                    <Input
                      type="text"
                      placeholder="/path/to/script.sql"
                      value={scriptPath}
                      onChange={(e) => setScriptPath(e.target.value)}
                    />
                  </FormControl>
                  <Alert status="info">
                    <AlertIcon />
                    Specify the full path to the SQL script file on the server
                  </Alert>
                </VStack>
              </TabPanel>

              {/* Inline SQL Tab */}
              <TabPanel>
                <FormControl>
                  <FormLabel>SQL Content</FormLabel>
                  <Textarea
                    value={scriptContent}
                    onChange={(e) => setScriptContent(e.target.value)}
                    placeholder="Enter SQL commands here..."
                    rows={10}
                    fontFamily="monospace"
                    fontSize="sm"
                  />
                </FormControl>
              </TabPanel>
            </TabPanels>
          </Tabs>

           {/* Common Options */}
           <VStack spacing={4} mt={6}>
             <FormControl>
               <FormLabel>Target Database (Optional)</FormLabel>
               <Input
                 type="text"
                 placeholder="Leave empty to use default"
                 value={targetDatabase}
                 onChange={(e) => setTargetDatabase(e.target.value)}
               />
             </FormControl>

             <HStack spacing={4} w="100%">
               <FormControl flex={1}>
                 <FormLabel>Timeout (seconds)</FormLabel>
                 <Input
                   type="number"
                   value={timeout}
                   onChange={(e) => setTimeout(e.target.value)}
                   min="10"
                   max="3600"
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
                 <strong>Target Server:</strong> Master (Primary) - Scripts will execute on the primary database server
               </Text>
             </Box>
           </VStack>
        </ModalBody>

        <ModalFooter>
          <HStack spacing={2}>
            <Button variant="ghost" onClick={handleClose}>
              Cancel
            </Button>
            <Button
              colorScheme="blue"
              onClick={handleExecute}
              isLoading={executing || loading}
              disabled={!scriptPath.trim() && !scriptContent.trim()}
            >
              Execute
            </Button>
          </HStack>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default ExecuteScriptModal
