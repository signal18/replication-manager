import { Box, Checkbox, Flex, HStack, Input, Text, VStack, Badge, Button, Alert, AlertIcon, AlertTitle, AlertDescription } from '@chakra-ui/react'
import React, { useEffect, useMemo, useState } from 'react'
import styles from '../../styles.module.scss'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../../../../components/DataTable'
import { TbCheck, TbX, TbExternalLink, TbAlertCircle } from 'react-icons/tb'

function PFSInstruments({ selectedDBServer, pfsConfigValue, onNavigateToVariables }) {
  const [search, setSearch] = useState('')
  const [instrumentsData, setInstrumentsData] = useState([])
  const [showEnabledOnly, setShowEnabledOnly] = useState(false)
  const [showMismatchOnly, setShowMismatchOnly] = useState(false)

  // Parse the performance_schema_instrument config value
  const parsedConfig = useMemo(() => {
    if (!pfsConfigValue) return {}
    
    const config = {}
    const entries = pfsConfigValue.split(',').map(s => s.trim()).filter(Boolean)
    
    entries.forEach(entry => {
      const [pattern, value] = entry.split('=').map(s => s.trim())
      if (pattern && value) {
        config[pattern] = value
      }
    })
    
    return config
  }, [pfsConfigValue])

  // Function to check if an instrument matches a pattern
  const matchesPattern = (instrumentName, pattern) => {
    // Convert SQL wildcard pattern to regex
    const regexPattern = pattern
      .replace(/\\/g, '\\\\')
      .replace(/\./g, '\\.')
      .replace(/%/g, '.*')
      .replace(/_/g, '.')
    
    const regex = new RegExp(`^${regexPattern}$`, 'i')
    return regex.test(instrumentName)
  }

  // Function to get expected state from config
  const getExpectedState = (instrumentName) => {
    // Try exact match first
    if (parsedConfig[instrumentName]) {
      return parsedConfig[instrumentName]
    }
    
    // Try pattern matching
    for (const [pattern, value] of Object.entries(parsedConfig)) {
      if (pattern.includes('%') || pattern.includes('_')) {
        if (matchesPattern(instrumentName, pattern)) {
          return value
        }
      }
    }
    
    return null
  }

  useEffect(() => {
    if (selectedDBServer?.pfsInstruments) {
      const instruments = []
      for (const [name, value] of Object.entries(selectedDBServer.pfsInstruments)) {
        const enabled = value === 'YES' || value === '1' || value === 'ON'
        const expectedState = getExpectedState(name)
        const expectedEnabled = expectedState ? (expectedState === 'YES' || expectedState === '1' || expectedState === 'ON') : null
        const hasMismatch = expectedEnabled !== null && enabled !== expectedEnabled
        
        instruments.push({
          name: name,
          enabled: enabled,
          value: value,
          expectedState: expectedState,
          expectedEnabled: expectedEnabled,
          hasMismatch: hasMismatch
        })
      }
      setInstrumentsData(instruments)
    }
  }, [selectedDBServer, parsedConfig])

  const filteredData = useMemo(() => {
    let data = instrumentsData

    if (search) {
      const searchLower = search.toLowerCase()
      data = data.filter((item) => item.name.toLowerCase().includes(searchLower))
    }

    if (showEnabledOnly) {
      data = data.filter((item) => item.enabled)
    }

    if (showMismatchOnly) {
      data = data.filter((item) => item.hasMismatch)
    }

    return data
  }, [instrumentsData, search, showEnabledOnly, showMismatchOnly])

  const categorizedData = useMemo(() => {
    const categories = {}
    filteredData.forEach((item) => {
      const parts = item.name.split('/')
      const category = parts[0] || 'other'
      if (!categories[category]) {
        categories[category] = { total: 0, enabled: 0, mismatches: 0 }
      }
      categories[category].total++
      if (item.enabled) {
        categories[category].enabled++
      }
      if (item.hasMismatch) {
        categories[category].mismatches++
      }
    })
    return categories
  }, [filteredData])

  const totalMismatches = useMemo(() => {
    return instrumentsData.filter(item => item.hasMismatch).length
  }, [instrumentsData])

  const handleSearch = (e) => {
    setSearch(e.target.value)
  }

  const columnHelper = createColumnHelper()

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.name, {
        header: 'Instrument Name',
        size: 300,
        maxSize: 500,
        minSize: 200,
        cell: (info) => {
          const parts = info.getValue().split('/')
          const category = parts[0]
          const restName = parts.slice(1).join('/')
          const row = info.row.original
          
          return (
            <HStack spacing={2}>
              {row.hasMismatch && <TbAlertCircle color="orange" size={18} title="State doesn't match configuration" />}
              <Badge colorScheme="blue" fontSize="xs">{category}</Badge>
              <Text fontSize="sm">{restName}</Text>
            </HStack>
          )
        }
      }),
      columnHelper.accessor((row) => row.enabled, {
        header: 'Current Status',
        size: 120,
        maxSize: 150,
        minSize: 100,
        cell: (info) => {
          const enabled = info.getValue()
          const row = info.row.original
          const textColor = row.hasMismatch ? (enabled ? "green.600" : "red.600") : (enabled ? "green.600" : "gray.500")
          const iconColor = row.hasMismatch ? (enabled ? "orange" : "orange") : (enabled ? "green" : "red")
          
          return (
            <HStack spacing={2}>
              {enabled ? (
                <>
                  <TbCheck color={iconColor} size={18} />
                  <Text fontSize="sm" color={textColor} fontWeight={row.hasMismatch ? "bold" : "normal"}>Enabled</Text>
                </>
              ) : (
                <>
                  <TbX color={iconColor} size={18} />
                  <Text fontSize="sm" color={textColor} fontWeight={row.hasMismatch ? "bold" : "normal"}>Disabled</Text>
                </>
              )}
            </HStack>
          )
        }
      }),
      columnHelper.accessor((row) => row.expectedState, {
        header: 'Expected (Config)',
        size: 120,
        maxSize: 150,
        minSize: 100,
        cell: (info) => {
          const expectedState = info.getValue()
          const row = info.row.original
          
          if (!expectedState) {
            return <Text fontSize="sm" color="gray.400">Not configured</Text>
          }
          
          const expectedEnabled = row.expectedEnabled
          return (
            <HStack spacing={2}>
              {expectedEnabled ? (
                <>
                  <TbCheck color="green" size={18} />
                  <Text fontSize="sm" color="green.600">Should be ON</Text>
                </>
              ) : (
                <>
                  <TbX color="red" size={18} />
                  <Text fontSize="sm" color="gray.500">Should be OFF</Text>
                </>
              )}
            </HStack>
          )
        }
      }),
      columnHelper.accessor((row) => row.value, {
        header: 'Raw Value',
        size: 100,
        maxSize: 150,
        minSize: 80,
        cell: (info) => <Text fontSize="sm">{info.getValue()}</Text>
      })
    ],
    []
  )

  return (
    <VStack className={styles.contentContainer}>
      {totalMismatches > 0 && (
        <Alert status="warning" mb={4}>
          <AlertIcon />
          <Box flex="1">
            <AlertTitle>Configuration Mismatch Detected</AlertTitle>
            <AlertDescription>
              {totalMismatches} instrument{totalMismatches > 1 ? 's' : ''} {totalMismatches > 1 ? 'have' : 'has'} a different state than configured in <strong>performance_schema_instrument</strong> variable.
            </AlertDescription>
          </Box>
          {onNavigateToVariables && (
            <Button
              size="sm"
              colorScheme="orange"
              leftIcon={<TbExternalLink />}
              onClick={onNavigateToVariables}
            >
              View Variable Config
            </Button>
          )}
        </Alert>
      )}
      <Flex className={styles.actions}>
        <HStack gap='4'>
          {onNavigateToVariables && (
            <Button
              size="sm"
              colorScheme="blue"
              leftIcon={<TbExternalLink />}
              onClick={onNavigateToVariables}
              variant="outline"
            >
              Variables Page
            </Button>
          )}
          <HStack className={styles.search}>
            <label htmlFor='search'>Search</label>
            <Input id='search' type='search' value={search} onChange={handleSearch} placeholder="Filter instruments..." />
          </HStack>
        </HStack>
        <Box className={styles.divider} />
        <Checkbox 
          size='lg' 
          isChecked={showEnabledOnly} 
          onChange={(e) => setShowEnabledOnly(e.target.checked)} 
          className={styles.checkbox}
        >
          Show enabled only
        </Checkbox>
        <Box className={styles.divider} />
        <Checkbox 
          size='lg' 
          isChecked={showMismatchOnly} 
          onChange={(e) => setShowMismatchOnly(e.target.checked)} 
          className={styles.checkbox}
        >
          Show mismatches only {totalMismatches > 0 && `(${totalMismatches})`}
        </Checkbox>
        <Box className={styles.divider} />
        <HStack spacing={4}>
          <Text fontSize="sm" fontWeight="bold">Summary:</Text>
          {Object.entries(categorizedData).map(([category, stats]) => (
            <Badge key={category} colorScheme="purple" fontSize="xs">
              {category}: {stats.enabled}/{stats.total}
            </Badge>
          ))}
        </HStack>
      </Flex>
      <Box className={`${styles.tableContainer} ${styles.variableContainer}`} overflow={'auto'}>
        <DataTable 
          key="pfs-instruments" 
          data={filteredData} 
          columns={columns} 
          className={styles.table} 
          enablePagination={true} 
        />
      </Box>
    </VStack>
  )
}

export default PFSInstruments
