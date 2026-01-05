import React, { useMemo } from 'react'
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
  Code,
} from '@chakra-ui/react'

/**
 * Modal for viewing complex variables with better formatting
 * Useful for variables like optimizer_switch, performance_schema_instrument
 */
function ComplexVariableModal({ isOpen, onClose, variableData }) {
  if (!variableData) return null

  const { variableName, cfgValue, value, runtimeValue, preservedValue } = variableData

  // Parse complex variable into parts
  const parseParts = (str) => {
    if (!str) return []
    
    // Handle different formats:
    // - comma-separated: "option1=val1,option2=val2" (optimizer_switch, MapValue.String())
    // - newline-separated: "option1=val1\noption2=val2" (some display formats)
    // - semicolon-separated: "option1=val1;option2=val2"
    // - space-separated: "option1=val1 option2=val2"
    const separators = ['\n', ',', ';', ' ']
    
    for (const sep of separators) {
      if (str.includes(sep)) {
        const parts = str.split(sep).map(part => part.trim()).filter(part => part.length > 0)
        // Only return if we got multiple parts or a single valid part
        if (parts.length > 0) {
          return parts
        }
      }
    }
    
    // Single value
    return [str]
  }

  const configParts = useMemo(() => parseParts(cfgValue), [cfgValue])
  const deployedParts = useMemo(() => parseParts(value), [value])
  const runtimeParts = useMemo(() => parseParts(runtimeValue), [runtimeValue])

  // Find differences between config and deployed
  const differences = useMemo(() => {
    const diffs = []
    const configMap = new Map()
    const deployedMap = new Map()

    configParts.forEach(part => {
      const [key, val] = part.split('=')
      if (key) configMap.set(key.trim(), val?.trim() || '')
    })

    deployedParts.forEach(part => {
      const [key, val] = part.split('=')
      if (key) deployedMap.set(key.trim(), val?.trim() || '')
    })

    // Check all keys
    const allKeys = new Set([...configMap.keys(), ...deployedMap.keys()])
    
    allKeys.forEach(key => {
      const configVal = configMap.get(key)
      const deployedVal = deployedMap.get(key)
      
      if (configVal !== deployedVal) {
        diffs.push({
          key,
          config: configVal || '(not set)',
          deployed: deployedVal || '(not set)',
          isDifferent: true
        })
      } else {
        diffs.push({
          key,
          config: configVal,
          deployed: deployedVal,
          isDifferent: false
        })
      }
    })

    return diffs
  }, [configParts, deployedParts])

  const hasDifferences = differences.some(d => d.isDifferent)

  return (
    <Modal isOpen={isOpen} onClose={onClose} size="4xl" scrollBehavior="inside">
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>
          <VStack align="start" spacing={2}>
            <HStack>
              <Text>Variable Details: {variableName}</Text>
              {hasDifferences && (
                <Badge colorScheme="orange">Has Differences</Badge>
              )}
              {preservedValue && (
                <Badge colorScheme="blue">Preserved</Badge>
              )}
            </HStack>
          </VStack>
        </ModalHeader>
        <ModalCloseButton />
        
        <ModalBody>
          <VStack align="stretch" spacing={6}>
            {/* Summary */}
            <Box>
              <Text fontWeight="bold" mb={2}>Summary</Text>
              <VStack align="stretch" spacing={1} fontSize="sm">
                <Text>Total options: {differences.length}</Text>
                <Text>Differences: {differences.filter(d => d.isDifferent).length}</Text>
                <Text>Matches: {differences.filter(d => !d.isDifferent).length}</Text>
              </VStack>
            </Box>

            {/* Detailed Comparison */}
            <Box>
              <Text fontWeight="bold" mb={2}>Detailed Comparison</Text>
              <VStack align="stretch" spacing={2}>
                {differences.map((diff, idx) => (
                  <Box
                    key={idx}
                    p={3}
                    borderWidth={1}
                    borderRadius="md"
                    bg={diff.isDifferent ? 'orange.50' : 'gray.50'}
                    borderColor={diff.isDifferent ? 'orange.200' : 'gray.200'}
                  >
                    <HStack justify="space-between" mb={2}>
                      <Text fontWeight="bold" fontSize="sm">
                        {diff.key}
                      </Text>
                      {diff.isDifferent && (
                        <Badge colorScheme="orange" fontSize="xs">DIFFERENT</Badge>
                      )}
                    </HStack>
                    
                    <VStack align="stretch" spacing={1} fontSize="sm">
                      <HStack>
                        <Text fontWeight="semibold" minW="100px">Config:</Text>
                        <Code colorScheme={diff.isDifferent ? 'orange' : 'gray'}>
                          {diff.config}
                        </Code>
                      </HStack>
                      <HStack>
                        <Text fontWeight="semibold" minW="100px">Deployed:</Text>
                        <Code colorScheme={diff.isDifferent ? 'orange' : 'gray'}>
                          {diff.deployed}
                        </Code>
                      </HStack>
                    </VStack>
                  </Box>
                ))}
              </VStack>
            </Box>

            {/* Raw Values */}
            <Box>
              <Text fontWeight="bold" mb={2}>Raw Values</Text>
              <VStack align="stretch" spacing={3}>
                <Box>
                  <Text fontSize="sm" fontWeight="semibold" mb={1}>Config:</Text>
                  <Code p={2} display="block" whiteSpace="pre-wrap" fontSize="xs">
                    {cfgValue || '(empty)'}
                  </Code>
                </Box>
                <Box>
                  <Text fontSize="sm" fontWeight="semibold" mb={1}>Deployed:</Text>
                  <Code p={2} display="block" whiteSpace="pre-wrap" fontSize="xs">
                    {value || '(empty)'}
                  </Code>
                </Box>
                {runtimeValue && (
                  <Box>
                    <Text fontSize="sm" fontWeight="semibold" mb={1}>Runtime:</Text>
                    <Code p={2} display="block" whiteSpace="pre-wrap" fontSize="xs">
                      {runtimeValue}
                    </Code>
                  </Box>
                )}
                {preservedValue && (
                  <Box>
                    <Text fontSize="sm" fontWeight="semibold" mb={1}>Preserved:</Text>
                    <Code p={2} display="block" whiteSpace="pre-wrap" fontSize="xs" colorScheme="blue">
                      {preservedValue}
                    </Code>
                  </Box>
                )}
              </VStack>
            </Box>
          </VStack>
        </ModalBody>

        <ModalFooter>
          <Button onClick={onClose}>Close</Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default ComplexVariableModal
