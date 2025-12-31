import React, { useState, useMemo, useEffect } from 'react'
import {
  Modal,
  ModalOverlay,
  ModalContent,
  ModalHeader,
  ModalFooter,
  ModalBody,
  ModalCloseButton,
  Button,
  VStack,
  HStack,
  Text,
  Box,
  Badge,
  Textarea,
  FormControl,
  FormLabel,
  FormHelperText,
  Alert,
  AlertIcon,
  AlertTitle,
  AlertDescription,
  Tabs,
  TabList,
  TabPanels,
  Tab,
  TabPanel,
  Code,
  Switch,
  Input,
} from '@chakra-ui/react'
import { TbAlertCircle } from 'react-icons/tb'

/**
 * Modal for editing variable values with support for complex variables
 * Supports merging for variables like optimizer_switch, performance_schema_instrument, plugin_load_add
 */
function EditVariableModal({ isOpen, onClose, variableData, onSave }) {
  if (!variableData) return null

  const { variableName, cfgValue, value, runtimeValue, preservedValue } = variableData

  // Complex variables that need special handling for merging
  const complexVariables = [
    'optimizer_switch',
    'performance_schema_instrument',
    'replicate_do_db',
    'replicate_ignore_db',
    'replicate_do_table',
    'replicate_ignore_table',
    'replicate_wild_do_table',
    'replicate_wild_ignore_table',
    'replicate_rewrite_db',
    'binlog_do_db',
    'binlog_ignore_db',
    'plugin_load_add'
  ]

  const isComplexVariable = complexVariables.includes(variableName)

  // State for editing
  const [editMode, setEditMode] = useState('simple') // 'simple' or 'advanced'
  const [customValue, setCustomValue] = useState(preservedValue || runtimeValue || value || '')
  const [baseValue, setBaseValue] = useState('runtime') // 'config', 'deployed', or 'runtime'
  const [mergeMode, setMergeMode] = useState(false)
  const [overrideItems, setOverrideItems] = useState([])

  // Initialize override items when merge mode is enabled
  useEffect(() => {
    if (mergeMode && isComplexVariable) {
      initializeMergeItems()
    }
  }, [mergeMode, baseValue])

  const parseParts = (str) => {
    if (!str) return []
    
    // Handle different separators
    const separators = ['\n', ',', ';']
    
    for (const sep of separators) {
      if (str.includes(sep)) {
        const parts = str.split(sep).map(part => part.trim()).filter(part => part.length > 0)
        if (parts.length > 0) {
          return parts
        }
      }
    }
    
    return [str]
  }

  const initializeMergeItems = () => {
    let base = runtimeValue
    if (baseValue === 'config') base = cfgValue
    else if (baseValue === 'deployed') base = value

    const parts = parseParts(base)
    const items = parts.map(part => {
      const [key, val] = part.split('=')
      return {
        key: key?.trim() || '',
        value: val?.trim() || '',
        action: 'keep' // 'keep', 'override', 'add', 'remove'
      }
    })
    setOverrideItems(items)
  }

  const addOverrideItem = () => {
    setOverrideItems([...overrideItems, { key: '', value: '', action: 'add' }])
  }

  const updateOverrideItem = (index, field, value) => {
    const newItems = [...overrideItems]
    newItems[index][field] = value
    setOverrideItems(newItems)
  }

  const removeOverrideItem = (index) => {
    const newItems = overrideItems.filter((_, i) => i !== index)
    setOverrideItems(newItems)
  }

  const buildMergedValue = () => {
    if (!mergeMode) {
      return customValue
    }

    // Build the merged value from override items
    const parts = []
    for (const item of overrideItems) {
      if (item.action === 'remove') continue
      if (!item.key) continue
      
      if (item.value) {
        parts.push(`${item.key}=${item.value}`)
      } else {
        parts.push(item.key)
      }
    }

    // Use the appropriate separator for this variable type
    let separator = ','
    if (variableName === 'performance_schema_instrument') {
      separator = '\n'
    }

    return parts.join(separator)
  }

  const handleSave = () => {
    const finalValue = buildMergedValue()
    onSave(variableName, finalValue)
    onClose()
  }

  const getBaseValueDisplay = () => {
    switch (baseValue) {
      case 'config':
        return cfgValue
      case 'deployed':
        return value
      case 'runtime':
        return runtimeValue
      default:
        return ''
    }
  }

  const hasDiff = cfgValue !== value
  const hasPreserve = preservedValue != null
  const isPFSInstrument = variableName === 'performance_schema_instrument'
  const runtimeDiffersFromPreserved = hasPreserve && runtimeValue !== preservedValue && !isPFSInstrument

  return (
    <Modal isOpen={isOpen} onClose={onClose} size="6xl" scrollBehavior="inside">
      <ModalOverlay />
      <ModalContent maxH="90vh">
        <ModalHeader>
          <VStack align="start" spacing={2}>
            <HStack>
              <Text>Override Variable: {variableName}</Text>
              {hasDiff && (
                <Badge colorScheme="orange">Has Differences</Badge>
              )}
              {hasPreserve && (
                <Badge colorScheme="blue">Preserved</Badge>
              )}
              {isComplexVariable && (
                <Badge colorScheme="purple">Complex Variable</Badge>
              )}
              {isPFSInstrument && (
                <Badge colorScheme="yellow">Performance Schema Instrument</Badge>
              )}
            </HStack>
          </VStack>
        </ModalHeader>
        <ModalCloseButton />
        
        <ModalBody>
          <VStack align="stretch" spacing={6}>
            {/* Special notice for PFS Instruments */}
            {isPFSInstrument && (
              <Alert status="info" borderRadius="md">
                <AlertIcon />
                <Box>
                  <AlertTitle>Performance Schema Instrument Variable</AlertTitle>
                  <AlertDescription fontSize="sm">
                    This variable controls performance schema instruments. Runtime values are managed through the PFS Instruments page. 
                    Use this editor to set custom preserved values for instruments configuration.
                  </AlertDescription>
                </Box>
              </Alert>
            )}

            {/* Warning for runtime changes */}
            {runtimeDiffersFromPreserved && (
              <Alert status="warning" borderRadius="md">
                <AlertIcon as={TbAlertCircle} />
                <Box>
                  <AlertTitle>Runtime Changed Manually</AlertTitle>
                  <AlertDescription fontSize="sm">
                    The runtime value differs from the preserved value. This manual change will be lost on restart unless you save a new override.
                  </AlertDescription>
                </Box>
              </Alert>
            )}

            {/* Info Alert */}
            <Alert status="info" borderRadius="md">
              <AlertIcon />
              <Box>
                <AlertTitle>Override Variable Value</AlertTitle>
                <AlertDescription fontSize="sm">
                  Set a custom value for this variable. The value will be preserved and used like a my.cnf setting.
                  {isComplexVariable && ' This variable supports merge mode for easier editing.'}
                </AlertDescription>
              </Box>
            </Alert>

            {/* Current Values Display */}
            <Box borderWidth={1} borderRadius="md" p={4} bg="gray.50">
              <Text fontWeight="bold" mb={3}>Current Values</Text>
              <VStack align="stretch" spacing={2} fontSize="sm">
                <HStack justify="space-between">
                  <Text fontWeight="semibold" minW="120px">Config:</Text>
                  <Code flex={1} p={2} fontSize="xs" whiteSpace="pre-wrap">{cfgValue || '(not set)'}</Code>
                </HStack>
                <HStack justify="space-between">
                  <Text fontWeight="semibold" minW="120px">Deployed:</Text>
                  <Code flex={1} p={2} fontSize="xs" whiteSpace="pre-wrap">{value || '(not set)'}</Code>
                </HStack>
                {!isPFSInstrument && (
                  <HStack justify="space-between">
                    <Text fontWeight="semibold" minW="120px">Runtime:</Text>
                    <Code flex={1} p={2} fontSize="xs" whiteSpace="pre-wrap" 
                          colorScheme={runtimeDiffersFromPreserved ? 'red' : undefined}>
                      {runtimeValue || '(not set)'}
                    </Code>
                  </HStack>
                )}
                {isPFSInstrument && (
                  <HStack justify="space-between">
                    <Text fontWeight="semibold" minW="120px">Runtime:</Text>
                    <Box flex={1} p={2} fontSize="xs">
                      <Badge colorScheme="purple">Managed via PFS Instruments Page</Badge>
                    </Box>
                  </HStack>
                )}
                {preservedValue && (
                  <HStack justify="space-between">
                    <Text fontWeight="semibold" minW="120px">Preserved:</Text>
                    <Code flex={1} p={2} fontSize="xs" whiteSpace="pre-wrap" colorScheme="blue">
                      {preservedValue}
                    </Code>
                  </HStack>
                )}
              </VStack>
            </Box>

            {/* Edit Tabs */}
            <Tabs variant="enclosed" onChange={(index) => setEditMode(index === 0 ? 'simple' : 'advanced')}>
              <TabList>
                <Tab>Simple Mode</Tab>
                {isComplexVariable && <Tab>Advanced Mode (Merge)</Tab>}
              </TabList>

              <TabPanels>
                {/* Simple Mode */}
                <TabPanel>
                  <VStack align="stretch" spacing={4}>
                    <FormControl>
                      <FormLabel>Custom Value</FormLabel>
                      <Textarea
                        value={customValue}
                        onChange={(e) => setCustomValue(e.target.value)}
                        placeholder="Enter custom value for this variable"
                        rows={6}
                        fontFamily="monospace"
                        fontSize="sm"
                      />
                      <FormHelperText>
                        Enter the exact value you want to set for this variable. It will be preserved like a my.cnf setting.
                      </FormHelperText>
                    </FormControl>

                    <HStack>
                      <Button size="sm" onClick={() => setCustomValue(cfgValue || '')}>
                        Use Config Value
                      </Button>
                      <Button size="sm" onClick={() => setCustomValue(value || '')}>
                        Use Deployed Value
                      </Button>
                      {!isPFSInstrument && (
                        <Button size="sm" onClick={() => setCustomValue(runtimeValue || '')}>
                          Use Runtime Value
                        </Button>
                      )}
                    </HStack>
                  </VStack>
                </TabPanel>

                {/* Advanced Mode (Merge) */}
                {isComplexVariable && (
                  <TabPanel>
                    <VStack align="stretch" spacing={4}>
                      <Alert status="info" fontSize="sm">
                        <AlertIcon />
                        <Box>
                          <AlertDescription>
                            Merge mode helps you override specific options in complex variables without rewriting the entire value.
                          </AlertDescription>
                        </Box>
                      </Alert>

                      <FormControl display="flex" alignItems="center">
                        <FormLabel mb="0">Enable Merge Mode</FormLabel>
                        <Switch
                          isChecked={mergeMode}
                          onChange={(e) => setMergeMode(e.target.checked)}
                        />
                      </FormControl>

                      {mergeMode && (
                        <>
                          <FormControl>
                            <FormLabel>Base Value</FormLabel>
                            <HStack>
                              <Button
                                size="sm"
                                colorScheme={baseValue === 'config' ? 'blue' : 'gray'}
                                onClick={() => setBaseValue('config')}
                              >
                                Config
                              </Button>
                              <Button
                                size="sm"
                                colorScheme={baseValue === 'deployed' ? 'blue' : 'gray'}
                                onClick={() => setBaseValue('deployed')}
                              >
                                Deployed
                              </Button>
                              <Button
                                size="sm"
                                colorScheme={baseValue === 'runtime' ? 'blue' : 'gray'}
                                onClick={() => setBaseValue('runtime')}
                              >
                                Runtime
                              </Button>
                            </HStack>
                            <FormHelperText>
                              Select which value to use as the base for merging
                            </FormHelperText>
                          </FormControl>

                          <Box borderWidth={1} borderRadius="md" p={4}>
                            <HStack justify="space-between" mb={3}>
                              <Text fontWeight="bold">Override Items</Text>
                              <Button size="sm" colorScheme="green" onClick={addOverrideItem}>
                                Add Item
                              </Button>
                            </HStack>

                            <VStack align="stretch" spacing={3}>
                              {overrideItems.map((item, index) => (
                                <HStack key={index} spacing={2} p={2} borderWidth={1} borderRadius="md" bg="white">
                                  <Input
                                    size="sm"
                                    placeholder="Key"
                                    value={item.key}
                                    onChange={(e) => updateOverrideItem(index, 'key', e.target.value)}
                                    flex={2}
                                  />
                                  <Text>=</Text>
                                  <Input
                                    size="sm"
                                    placeholder="Value"
                                    value={item.value}
                                    onChange={(e) => updateOverrideItem(index, 'value', e.target.value)}
                                    flex={2}
                                  />
                                  <Button
                                    size="sm"
                                    colorScheme="red"
                                    onClick={() => {
                                      if (item.action === 'add') {
                                        removeOverrideItem(index)
                                      } else {
                                        updateOverrideItem(index, 'action', item.action === 'remove' ? 'keep' : 'remove')
                                      }
                                    }}
                                  >
                                    {item.action === 'remove' ? 'Undo' : 'Remove'}
                                  </Button>
                                </HStack>
                              ))}
                            </VStack>
                          </Box>

                          <Box borderWidth={1} borderRadius="md" p={4} bg="gray.50">
                            <Text fontWeight="bold" mb={2}>Preview Merged Value</Text>
                            <Code p={2} display="block" whiteSpace="pre-wrap" fontSize="xs">
                              {buildMergedValue()}
                            </Code>
                          </Box>
                        </>
                      )}

                      {!mergeMode && (
                        <Text fontSize="sm" color="gray.600">
                          Enable merge mode to edit individual options
                        </Text>
                      )}
                    </VStack>
                  </TabPanel>
                )}
              </TabPanels>
            </Tabs>
          </VStack>
        </ModalBody>

        <ModalFooter>
          <HStack>
            <Button variant="ghost" onClick={onClose}>Cancel</Button>
            <Button 
              colorScheme="blue" 
              onClick={handleSave}
              isDisabled={!customValue && !mergeMode}
            >
              Save Override
            </Button>
          </HStack>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default EditVariableModal
