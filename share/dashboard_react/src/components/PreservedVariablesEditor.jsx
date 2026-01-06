import { Box, VStack, Text, Alert, AlertIcon, HStack, Button, useToast, Textarea, Input, CloseButton, IconButton } from '@chakra-ui/react'
import React, { useState, useEffect, useMemo } from 'react'
import { getPreservedVarsCnf, savePreservedVarsCnf } from '../redux/configSlice'
import { useDispatch } from 'react-redux'
import { TbTable, TbCode, TbPlus, TbTrash, TbDeviceFloppy, TbInfoCircle } from 'react-icons/tb'
import { DataTable } from './DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import RMIconButton from './RMIconButton'
import TextForm from './TextForm'
import ConfirmModal from './Modals/ConfirmModal'
import PropTypes from 'prop-types'

function PreservedVariablesEditor({ clusterName, user, className }) {
  const [viewMode, setViewMode] = useState('table') // 'table' or 'editor'
  const [content, setContent] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const [isLoaded, setIsLoaded] = useState(false)
  const [parsedData, setParsedData] = useState([])
  const [newVariable, setNewVariable] = useState({ name: '', value: '', exclusions: '' })
  const [isAddingNew, setIsAddingNew] = useState(false)
  const [confirmModal, setConfirmModal] = useState({ isOpen: false, title: '', action: null })
  const [showInfoAlert, setShowInfoAlert] = useState(true)
  
  const dispatch = useDispatch()
  const toast = useToast()
  const columnHelper = createColumnHelper()

  // Get localStorage key scoped to user
  const getInfoAlertKey = () => {
    const userId = user?.id || user?.username || 'default'
    return `preservedVarsEditor_hideInfo_${userId}`
  }

  // Load info alert visibility from localStorage
  useEffect(() => {
    const hideInfo = localStorage.getItem(getInfoAlertKey())
    if (hideInfo === 'true') {
      setShowInfoAlert(false)
    }
  }, [user])

  // Handle dismissing the info alert
  const handleDismissInfo = () => {
    setShowInfoAlert(false)
    localStorage.setItem(getInfoAlertKey(), 'true')
  }

  // Handle showing the info alert again
  const handleShowInfo = () => {
    setShowInfoAlert(true)
    localStorage.setItem(getInfoAlertKey(), 'false')
  }

  // Parse CNF content to table data
  const parseCnfToTable = (cnfContent) => {
    const lines = cnfContent.split('\n')
    const variables = []
    const variableMap = new Map()

    lines.forEach(line => {
      const trimmed = line.trim()
      if (!trimmed || trimmed.startsWith('#') || trimmed.startsWith('[')) return

      // Check for exclusion line
      if (trimmed.includes('.exclude')) {
        const [varWithSuffix, exclusionList] = trimmed.split('=').map(s => s.trim())
        const varName = varWithSuffix.replace('.exclude', '').trim()
        if (variableMap.has(varName)) {
          variableMap.get(varName).exclusions = exclusionList
        }
      } else if (trimmed.includes('=')) {
        const [varName, value] = trimmed.split('=').map(s => s.trim())
        if (!variableMap.has(varName)) {
          variableMap.set(varName, { variableName: varName, value: value || '', exclusions: '' })
        } else {
          variableMap.get(varName).value = value || ''
        }
      }
    })

    variableMap.forEach(value => variables.push(value))
    return variables
  }

  // Convert table data back to CNF format
  const tableDataToCnf = (data) => {
    let cnf = '[mysqld]\n'
    data.forEach(row => {
      if (row.variableName && row.variableName.trim()) {
        cnf += `${row.variableName} = ${row.value || ''}\n`
        if (row.exclusions && row.exclusions.trim()) {
          cnf += `${row.variableName}.exclude = ${row.exclusions}\n`
        }
      }
    })
    return cnf
  }

  // Load content
  const handleLoadContent = async () => {
    setIsLoading(true)
    try {
      const result = await dispatch(getPreservedVarsCnf({ clusterName })).unwrap()
      const cnfContent = result.content || '[mysqld]\n'
      setContent(cnfContent)
      setParsedData(parseCnfToTable(cnfContent))
      setIsLoaded(true)
      toast({
        title: 'Success',
        description: 'Preserved variables loaded successfully',
        status: 'success',
        duration: 3000,
        isClosable: true
      })
    } catch (error) {
      toast({
        title: 'Error',
        description: error.message || 'Failed to load preserved variables',
        status: 'error',
        duration: 5000,
        isClosable: true
      })
    } finally {
      setIsLoading(false)
    }
  }

  // Save content
  const handleSave = async () => {
    try {
      const contentToSave = viewMode === 'table' ? tableDataToCnf(parsedData) : content
      await dispatch(savePreservedVarsCnf({ clusterName, content: contentToSave })).unwrap()
      
      if (viewMode === 'table') {
        setContent(contentToSave)
      } else {
        setParsedData(parseCnfToTable(content))
      }
      
      toast({
        title: 'Success',
        description: 'Preserved variables saved successfully',
        status: 'success',
        duration: 3000,
        isClosable: true
      })
    } catch (error) {
      toast({
        title: 'Error',
        description: error.message || 'Failed to save preserved variables',
        status: 'error',
        duration: 5000,
        isClosable: true
      })
    }
  }

  // Auto-load on mount
  useEffect(() => {
    if (clusterName) {
      handleLoadContent()
    }
  }, [clusterName])

  // Update table when switching from editor to table mode
  const handleViewModeChange = (mode) => {
    if (mode === 'table' && viewMode === 'editor') {
      setParsedData(parseCnfToTable(content))
    } else if (mode === 'editor' && viewMode === 'table') {
      setContent(tableDataToCnf(parsedData))
    }
    setViewMode(mode)
  }

  // Handle adding new variable
  const handleAddVariable = () => {
    if (!newVariable.name.trim()) {
      toast({
        title: 'Error',
        description: 'Variable name is required',
        status: 'error',
        duration: 3000,
        isClosable: true
      })
      return
    }

    setParsedData(prev => [...prev, {
      variableName: newVariable.name.trim(),
      value: newVariable.value.trim(),
      exclusions: newVariable.exclusions.trim()
    }])
    setNewVariable({ name: '', value: '', exclusions: '' })
    setIsAddingNew(false)
  }

  // Handle removing variable
  const handleRemoveVariable = (varName) => {
    setParsedData(prev => prev.filter(v => v.variableName !== varName))
    setConfirmModal({ isOpen: false, title: '', action: null })
  }

  // Handle updating variable
  const handleUpdateVariable = (varName, field, value) => {
    setParsedData(prev => prev.map(v => 
      v.variableName === varName ? { ...v, [field]: value } : v
    ))
  }

  // Table columns
  const columns = useMemo(() => [
    columnHelper.accessor('variableName', {
      header: 'Variable Name',
      size: 150,
      cell: (info) => (
        <Text fontWeight="bold">{info.getValue()}</Text>
      )
    }),
    columnHelper.accessor('value', {
      header: 'Value',
      size: 150,
      cell: (info) => (
        <TextForm
          isDisabled={!user?.grants['cluster-settings']}
          value={info.getValue() || ''}
          confirmTitle="Update variable value?"
          maxLength={1024}
          onSave={(value) => handleUpdateVariable(info.row.original.variableName, 'value', value)}
        />
      )
    }),
    columnHelper.accessor('exclusions', {
      header: 'Exclusions (Server IDs)',
      size: 200,
      cell: (info) => (
        <TextForm
          isDisabled={!user?.grants['cluster-settings']}
          value={info.getValue() || ''}
          confirmTitle="Update exclusions?"
          placeholder="server1,server2,..."
          maxLength={1024}
          onSave={(value) => handleUpdateVariable(info.row.original.variableName, 'exclusions', value)}
        />
      )
    }),
    columnHelper.display({
      id: 'actions',
      header: 'Actions',
      size: 80,
      cell: (info) => (
        <RMIconButton
          isDisabled={!user?.grants['cluster-settings']}
          tooltip="Remove variable"
          icon={TbTrash}
          colorScheme="red"
          onClick={() => setConfirmModal({
            isOpen: true,
            title: `Remove preserved variable '${info.row.original.variableName}'?`,
            action: () => handleRemoveVariable(info.row.original.variableName)
          })}
        />
      )
    })
  ], [parsedData, user])

  if (isLoading) {
    return (
      <Box p={4}>
        <Text>Loading preserved variables...</Text>
      </Box>
    )
  }

  return (
    <VStack align="stretch" spacing={4} className={className}>
      {/* Info Alert - Dismissible */}
      {showInfoAlert && (
        <Alert status="info" variant="left-accent">
          <AlertIcon />
          <Box fontSize="sm" flex="1">
            <Text fontWeight="bold" mb={1}>📋 Cluster-Level Preserved Variables</Text>
            <Text>
              These variables maintain their values across configuration changes. Use <strong>Table View</strong> for easy editing
              or <strong>Editor View</strong> for direct CNF file editing. Variables can be excluded from specific servers using the exclusions column.
            </Text>
          </Box>
          <CloseButton
            alignSelf="flex-start"
            position="relative"
            right={-1}
            top={-1}
            onClick={handleDismissInfo}
          />
        </Alert>
      )}

      {/* View Mode Toggle and Actions */}
      <HStack justifyContent="space-between">
        <HStack>
          <Button
            size="sm"
            leftIcon={<TbTable />}
            colorScheme={viewMode === 'table' ? 'blue' : 'gray'}
            onClick={() => handleViewModeChange('table')}
          >
            Table View
          </Button>
          <Button
            size="sm"
            leftIcon={<TbCode />}
            colorScheme={viewMode === 'editor' ? 'blue' : 'gray'}
            onClick={() => handleViewModeChange('editor')}
          >
            Editor View
          </Button>
          {!showInfoAlert && (
            <IconButton
              size="sm"
              icon={<TbInfoCircle />}
              variant="ghost"
              colorScheme="blue"
              aria-label="Show info"
              title="Show information"
              onClick={handleShowInfo}
            />
          )}
        </HStack>
        
        <HStack>
          {viewMode === 'table' && (
            <Button
              size="sm"
              leftIcon={<TbPlus />}
              colorScheme="green"
              isDisabled={!user?.grants['cluster-settings']}
              onClick={() => setIsAddingNew(!isAddingNew)}
            >
              {isAddingNew ? 'Cancel' : 'Add Variable'}
            </Button>
          )}
          <Button
            size="sm"
            leftIcon={<TbDeviceFloppy />}
            colorScheme="purple"
            isDisabled={!user?.grants['cluster-settings']}
            onClick={handleSave}
          >
            Save
          </Button>
        </HStack>
      </HStack>

      {/* Table View */}
      {viewMode === 'table' && (
        <>
          {/* Add New Variable Form */}
          {isAddingNew && (
            <Box p={4} borderWidth={1} borderRadius="md" borderColor="green.300" bg="green.50">
              <VStack align="stretch" spacing={3}>
                <Text fontWeight="bold">Add New Variable</Text>
                <HStack>
                  <Input
                    placeholder="Variable name (e.g., max_connections)"
                    value={newVariable.name}
                    onChange={(e) => setNewVariable(prev => ({ ...prev, name: e.target.value }))}
                    size="sm"
                  />
                  <Input
                    placeholder="Value (e.g., 500)"
                    value={newVariable.value}
                    onChange={(e) => setNewVariable(prev => ({ ...prev, value: e.target.value }))}
                    size="sm"
                  />
                  <Input
                    placeholder="Exclusions (e.g., server1,server2)"
                    value={newVariable.exclusions}
                    onChange={(e) => setNewVariable(prev => ({ ...prev, exclusions: e.target.value }))}
                    size="sm"
                  />
                </HStack>
                <HStack>
                  <Button size="sm" colorScheme="green" onClick={handleAddVariable}>
                    Add
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => {
                    setIsAddingNew(false)
                    setNewVariable({ name: '', value: '', exclusions: '' })
                  }}>
                    Cancel
                  </Button>
                </HStack>
              </VStack>
            </Box>
          )}

          {/* Variables Table */}
          <Box>
            {parsedData.length === 0 ? (
              <Alert status="info">
                <AlertIcon />
                No preserved variables defined. Click "Add Variable" to create one.
              </Alert>
            ) : (
              <DataTable
                data={parsedData}
                columns={columns}
                enablePagination={true}
              />
            )}
          </Box>

          {/* Priority Information */}
          <Box fontSize="xs" color="gray.600" p={3} borderWidth={1} borderRadius="md" bg="gray.50">
            <Text fontWeight="bold" mb={2}>📌 Priority System:</Text>
            <Text>• <strong>Priority 1 (Highest):</strong> Server-specific (01_preserved.cnf) - always wins</Text>
            <Text>• <strong>Priority 2 (Middle):</strong> Cluster-level (this file) - applies unless excluded</Text>
            <Text>• <strong>Priority 3 (Lowest):</strong> Configurator/defaults</Text>
            <Text mt={2}>
              <strong>Exclusions:</strong> List server IDs (comma-separated) to exclude from cluster-level defaults.
              Excluded servers will use Priority 3 unless they have Priority 1 overrides.
            </Text>
          </Box>
        </>
      )}

      {/* Editor View */}
      {viewMode === 'editor' && (
        <VStack align="stretch" spacing={2}>
          <Text fontSize="sm" color="gray.600">
            File: <strong>{clusterName}/preserved_variables.cnf</strong>
          </Text>
          <Textarea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            placeholder="[mysqld]\nmax_connections = 500\nmax_connections.exclude = server1,server2"
            fontFamily="monospace"
            fontSize="sm"
            minH="400px"
            isDisabled={!user?.grants['cluster-settings']}
          />
          <Box fontSize="xs" color="gray.600" p={3} borderWidth={1} borderRadius="md" bg="gray.50">
            <Text fontWeight="bold" mb={1}>💡 Syntax:</Text>
            <Text>• <code>variable_name = value</code> - Set cluster-level default</Text>
            <Text>• <code>variable_name.exclude = server1,server2</code> - Exclude servers from cluster default</Text>
          </Box>
        </VStack>
      )}

      {/* Confirm Modal */}
      {confirmModal.isOpen && (
        <ConfirmModal
          isOpen={confirmModal.isOpen}
          title={confirmModal.title}
          onConfirmClick={() => {
            if (confirmModal.action) confirmModal.action()
          }}
          closeModal={() => setConfirmModal({ isOpen: false, title: '', action: null })}
        />
      )}
    </VStack>
  )
}

PreservedVariablesEditor.propTypes = {
  clusterName: PropTypes.string.isRequired,
  user: PropTypes.object,
  className: PropTypes.string
}

export default PreservedVariablesEditor
