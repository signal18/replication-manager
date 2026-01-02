import { Box, Checkbox, Flex, HStack, Input, Text, VStack, Button, Alert, AlertIcon, AlertTitle, AlertDescription, CloseButton } from '@chakra-ui/react'
import React, { useEffect, useMemo, useState, useRef, useReducer } from 'react'

import styles from '../../styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { getDatabaseService, preserveVariable, setCustomVariableValue } from '../../../../redux/clusterSlice'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../../../../components/DataTable'
import { isEqual } from 'lodash'
import CopyToClipboard from '../../../../components/CopyToClipboard'
import RMIconButton from '../../../../components/RMIconButton'
import { TbShield, TbTrash, TbCheck, TbAlertCircle, TbZoomIn, TbExternalLink, TbEdit, TbInfoCircle } from 'react-icons/tb'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import ComplexVariableModal from './ComplexVariableModal'
import EditVariableModal from './EditVariableModal'

const defaultState = {
  showCfg: true,
  showDeployed: true,
  showRuntime: true,
  showPreserve: true,
  showRowDiff: false,
  showRowPreserved: false,
  showInfoAlert: true,
  search: '',
  confirmState: {
    isOpen: false,
    type: '',
    title: '',
    action: null,
    payload: ''
  },
  complexVariableModal: {
    isOpen: false,
    data: null
  },
  editVariableModal: {
    isOpen: false,
    data: null
  }
}

const reducer = (state, action) => {
  switch (action.type) {
    case 'SET_SEARCH':
      return { ...state, search: action.payload }
    case 'SET_SHOW_CFG':
      return { ...state, showCfg: action.payload }
    case 'SET_SHOW_DEPLOYED':
      return { ...state, showDeployed: action.payload }
    case 'SET_SHOW_RUNTIME':
      return { ...state, showRuntime: action.payload }
    case 'SET_SHOW_PRESERVE':
      return { ...state, showPreserve: action.payload }
    case 'SET_SHOW_ROW_DIFF':
      return { ...state, showRowDiff: action.payload }
    case 'SET_SHOW_ROW_PRESERVED':
      return { ...state, showRowPreserved: action.payload }
    case 'SET_SHOW_INFO_ALERT':
      return { ...state, showInfoAlert: action.payload }
    case 'SET_CONFIRM_OPEN':
      return { ...state, confirmState: { ...state.confirmState, isOpen: action.payload } }
    case 'SET_CONFIRM_ACTION':
      return { ...state, confirmState: { ...state.confirmState, ...action.payload, isOpen: true} }
    case 'SET_COMPLEX_MODAL':
      return { ...state, complexVariableModal: action.payload }
    case 'SET_EDIT_MODAL':
      return { ...state, editVariableModal: action.payload }
    default:
      return state
  }
}

// Helper function to normalize boolean values for comparison only (not for display)
const normalizeBooleanValue = (value) => {
  if (value === null || value === undefined || value === '') return value
  
  const strValue = String(value).toUpperCase().trim()
  
  // Check for boolean representations
  if (['1', 'ON', 'TRUE', 'YES'].includes(strValue)) return 'ON'
  if (['0', 'OFF', 'FALSE', 'NO'].includes(strValue)) return 'OFF'
  
  return value
}

// Helper to check if two values are equivalent booleans (for comparison, not display)
const areBooleanValuesEqual = (val1, val2) => {
  const normalized1 = normalizeBooleanValue(val1)
  const normalized2 = normalizeBooleanValue(val2)
  
  return normalized1 === normalized2
}

// Helper to identify boolean variables by checking if runtime value is ON/OFF
const isBooleanVariable = (row) => {
  if (!row || !row.runtimeValue) return false
  
  const runtimeStr = String(row.runtimeValue).toUpperCase().trim()
  
  // If runtime value is ON or OFF, it's a boolean variable
  return runtimeStr === 'ON' || runtimeStr === 'OFF'
}

function Variables({ clusterName, dbId, toggleVariableMode, variableMode, onNavigateToPFSInstruments, searchFilter }) {
  const [ vState, vDispatch ] = useReducer(reducer, defaultState)
  const dispatch = useDispatch()
  const variables = useSelector((state) => state.cluster.database.variables)

  const [variablesData, setVariablesData] = useState(variables || [])
  const [variablesAllData, setvariablesAllData] = useState(variables || [])
  const prevVariablesRef = useRef(variables)

  const { showCfg, showDeployed, showRuntime, showPreserve, showRowDiff, showRowPreserved, showInfoAlert, search, confirmState, complexVariableModal, editVariableModal } = vState
  const { isOpen, title, payload } = confirmState

  // Get username for user-specific localStorage key
  const username = localStorage.getItem('username') || 'default'
  const alertStorageKey = `variables_info_alert_dismissed_${username}`

  // Check localStorage on mount to see if user dismissed the alert
  useEffect(() => {
    const dontShowAgain = localStorage.getItem(alertStorageKey)
    if (dontShowAgain === 'true') {
      vDispatch({ type: 'SET_SHOW_INFO_ALERT', payload: false })
    }
  }, [alertStorageKey])

  // Set search filter when navigating from PFS Instruments page
  useEffect(() => {
    if (searchFilter) {
      vDispatch({ type: 'SET_SEARCH', payload: searchFilter })
    }
  }, [searchFilter])

  useEffect(() => {
      vDispatch({ type: 'SET_SHOW_ROW_DIFF', payload: (variableMode === 'diff') })
  }, [variableMode])
      
  const closeConfirmModal = () => {
    vDispatch({ type: 'SET_CONFIRM_OPEN', payload: false})
  }

  const setVariableMode = (e) => {
    const value = e.target.checked ? "diff" : "all"
    vDispatch({ type: 'SET_SHOW_ROW_DIFF', payload: e.target.checked })
    toggleVariableMode(value)
  }

  const handleConfirm = () => {
    const { type } = confirmState
    
    if (type === 'preserve') {
      dispatch(preserveVariable({ clusterName, dbId, variableName: payload, action: 'preserve' }))
        .unwrap()
        .then(() => {
          // Refresh variables after successful preserve
          dispatch(getDatabaseService({ clusterName, serviceName: 'variables', dbId, queryParams: { diff: showRowDiff } }))
        })
    } else if (type === 'accept') {
      dispatch(preserveVariable({ clusterName, dbId, variableName: payload, action: 'accept' }))
        .unwrap()
        .then(() => {
          // Refresh variables after successful accept
          dispatch(getDatabaseService({ clusterName, serviceName: 'variables', dbId, queryParams: { diff: showRowDiff } }))
        })
    } else if (type === 'clear') {
      dispatch(preserveVariable({ clusterName, dbId, variableName: payload, action: 'clear' }))
        .unwrap()
        .then(() => {
          // Refresh variables after successful clear
          dispatch(getDatabaseService({ clusterName, serviceName: 'variables', dbId, queryParams: { diff: showRowDiff } }))
        })
    }
    closeConfirmModal()
  }

  const handleSaveCustomValue = (variableName, customValue) => {
    dispatch(setCustomVariableValue({ clusterName, dbId, variableName, customValue }))
      .unwrap()
      .then(() => {
        // Refresh variables after successful custom value set
        dispatch(getDatabaseService({ clusterName, serviceName: 'variables', dbId, queryParams: { diff: showRowDiff } }))
      })
  }

  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'variables', dbId, queryParams: { diff: showRowDiff } }))
  }, [])

  useEffect(() => {
      if (!isEqual(variables, prevVariablesRef.current)) {
        setVariablesData(searchData(variables))
        setvariablesAllData(variables)
        prevVariablesRef.current = variables
      }
  }, [variables])

  useEffect(() => {
    setVariablesData(searchData(variablesAllData))
  }, [search, showRowPreserved])

  const searchData = (data = []) => {
    const searchedData = data?.filter((x) => {
      const searchValue = search.toLowerCase()
      if (x.variableName.toLowerCase().includes(searchValue) || (showCfg && x.cnfValue?.toLowerCase().includes(searchValue)) || (showDeployed && x.value?.toLowerCase().includes(searchValue)) || (showRuntime && x.runtimeValue?.toLowerCase().includes(searchValue))) {
        return x
      }
    }) || []
    if (showRowPreserved) {
      return searchedData.filter((x) => x.preservedValue != null)
    }
    return searchedData
  }

  const handleSearch = (e) => {
    vDispatch({ type: 'SET_SEARCH', payload: e.target.value })
  }

  const handleDismissAlert = () => {
    localStorage.setItem(alertStorageKey, 'true')
    vDispatch({ type: 'SET_SHOW_INFO_ALERT', payload: false })
  }

  const handleShowInfo = () => {
    vDispatch({ type: 'SET_SHOW_INFO_ALERT', payload: true })
  }

  const columnHelper = createColumnHelper()

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.variableName, {
        header: 'Variable Name',
        size: 150,
        maxSize: 250,
        minSize: 150,
        cell: (info) => {
          const row = info.row.original
          const isBoolean = isBooleanVariable(row)
          const hasDiff = isBoolean 
            ? !areBooleanValuesEqual(row.cfgValue, row.value)
            : row.cfgValue !== row.value
          const hasPreserve = row.preservedValue != null
          const isPFSInstrument = row.variableName === 'performance_schema_instrument'
          const runtimeDiffersFromPreserved = hasPreserve && 
            (isBoolean 
              ? !areBooleanValuesEqual(row.runtimeValue, row.preservedValue)
              : row.runtimeValue !== row.preservedValue) && 
            !isPFSInstrument
          
          return (
            <HStack spacing={2}>
              {runtimeDiffersFromPreserved && <TbAlertCircle color="red" title="Runtime changed manually!" />}
              {hasDiff && !runtimeDiffersFromPreserved && <TbAlertCircle color="orange" title="Has difference" />}
              {hasPreserve && <TbShield color="blue" title="Preservation set" />}
              <Text>{info.getValue()}</Text>
            </HStack>
          )
        }
      }),
      ...(showCfg ? [columnHelper.accessor((row) => row.cfgValue, {
        header: 'Configurator',
        size: 100,
        maxSize: 200,
        minSize: 100,
        cell: (info) => {
          const fullString = info.getValue()
          const fullLength = fullString?.length
          const row = info.row.original
          
          // Check if this is a boolean variable
          const isBoolean = isBooleanVariable(row)
          
          // Complex variable detection: either long strings OR known complex variables
          const isLongVariable = fullLength > 100 || 
                                  (row.value?.length > 100) || 
                                  (row.runtimeValue?.length > 100)
          const knownComplexVars = [
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
          const isKnownComplex = knownComplexVars.includes(row.variableName)
          const isComplex = isLongVariable || isKnownComplex
          
          return (
            <HStack spacing={2}>
              {isComplex && (
                <RMIconButton 
                  tooltip="View details" 
                  icon={TbZoomIn} 
                  size="xs"
                  onClick={(e) => { 
                    e.stopPropagation()
                    vDispatch({ 
                      type: "SET_COMPLEX_MODAL", 
                      payload: { isOpen: true, data: row }
                    })
                  }} 
                />
              )}
              {fullLength > 15 ? (
                <CopyToClipboard copyIconPosition='start' className={styles.longVariable} text={fullString} />
              ) : (
                <span style={isBoolean ? { fontWeight: 'bold' } : {}}>{fullString}</span>
              )}
            </HStack>
          )
        }
      })]: []),
      ...(showDeployed ? [columnHelper.accessor((row) => row.value, {
        header: 'Deployed',
        size: 100,
        maxSize: 200,
        minSize: 100,
        cell: (info) => {
          const fullString = info.getValue()
          const fullLength = fullString?.length
          const row = info.row.original
          
          // Check if this is a boolean variable
          const isBoolean = isBooleanVariable(row)
          
          return (
            <>
              {fullLength > 15 ? (
                <CopyToClipboard copyIconPosition='start' className={styles.longVariable} text={fullString} />
              ) : (
                <span style={isBoolean ? { fontWeight: 'bold' } : {}}>{fullString}</span>
              )}
            </>
          )
        }
      })]:[]),
      ...(showRuntime ? [columnHelper.accessor((row) => row.runtimeValue, {
        header: 'Runtime',
        size: 100,
        maxSize: 200,
        minSize: 100,
        cell: (info) => {
          const fullString = info.getValue()
          const fullLength = fullString?.length
          const row = info.row.original
          
          // Check if this is the performance_schema_instrument variable
          const isPFSInstrument = row.variableName === 'performance_schema_instrument'
          
          // Show PFS Instruments button for performance_schema_instrument
          if (isPFSInstrument) {
            return (
              <Button
                size="sm"
                colorScheme="purple"
                leftIcon={<TbExternalLink />}
                onClick={(e) => {
                  e.stopPropagation()
                  if (onNavigateToPFSInstruments) {
                    onNavigateToPFSInstruments()
                  }
                }}
              >
                View PFS Instruments
              </Button>
            )
          }
          
          // Check if this is a boolean variable
          const isBoolean = isBooleanVariable(row)
          
          // Check if runtime differs from preserved value (not for PFS instrument)
          const hasPreserve = row.preservedValue != null
          const runtimeDiffersFromPreserved = hasPreserve && 
            (isBoolean 
              ? !areBooleanValuesEqual(row.runtimeValue, row.preservedValue)
              : row.runtimeValue !== row.preservedValue)
          
          return (
            <HStack spacing={2}>
              {runtimeDiffersFromPreserved && (
                <TbAlertCircle 
                  color="red" 
                  size={18}
                  title="Runtime value differs from preserved! Manual change detected - will be lost on restart unless re-preserved" 
                />
              )}
              {fullLength > 15 ? (
                <CopyToClipboard 
                  copyIconPosition='start' 
                  className={styles.longVariable} 
                  text={fullString} 
                />
              ) : (
                <span style={{ 
                  ...(runtimeDiffersFromPreserved ? { color: 'red', fontWeight: 'bold' } : {}),
                  ...(isBoolean && !runtimeDiffersFromPreserved ? { fontWeight: 'bold' } : {})
                }}>
                  {fullString}
                </span>
              )}
            </HStack>
          )
        }
      })]: []),
      ...(showPreserve ? [columnHelper.accessor((row) => row.preservedValue, {
        header: 'Preserve',
        size: 100,
        maxSize: 200,
        minSize: 100,
        cell: (info) => {
          const fullString = info.getValue()
          const fullLength = fullString?.length
          const row = info.row.original
          
          // Check if this is a boolean variable
          const isBoolean = isBooleanVariable(row)
          
          return (
            <>
              {fullLength > 15 ? (
                <CopyToClipboard copyIconPosition='start' className={styles.longVariable} text={fullString} />
              ) : (
                <span style={isBoolean ? { fontWeight: 'bold' } : {}}>{fullString}</span>
              )}
            </>
          )
        }
      })]: []),
      columnHelper.accessor((row) => {
        const hasPreserve = row.preservedValue != null
        const isBoolean = isBooleanVariable(row)
        const hasDiff = isBoolean 
          ? !areBooleanValuesEqual(row.cfgValue, row.value)
          : row.cfgValue !== row.value
        const isPreserved = hasPreserve && 
          (isBoolean 
            ? areBooleanValuesEqual(row.preservedValue, row.value)
            : row.preservedValue === row.value)
        const isPFSInstrument = row.variableName === 'performance_schema_instrument'
        const runtimeDiffersFromPreserved = hasPreserve && 
          (isBoolean 
            ? !areBooleanValuesEqual(row.runtimeValue, row.preservedValue)
            : row.runtimeValue !== row.preservedValue) && 
          !isPFSInstrument
        
        return (
          <VStack align={"center"} justifyContent={"center"} spacing={2}>
            {runtimeDiffersFromPreserved && (
              <HStack spacing={1} bg="red.50" p={2} borderRadius="md" border="1px solid" borderColor="red.300">
                <TbAlertCircle color="red" size={16} />
                <Text fontSize="xs" color="red.600" fontWeight="bold">
                  Manual change detected!
                </Text>
              </HStack>
            )}
            <HStack align={"center"} justifyContent={"center"} spacing={2}>
                {hasDiff && !hasPreserve && (
              <>
                <RMIconButton 
                  tooltip="Preserve deployed value (keep current database value)" 
                  icon={TbShield} 
                  colorScheme="blue"
                  onClick={(e) => { 
                    e.stopPropagation()
                    vDispatch({ 
                      type: "SET_CONFIRM_ACTION", 
                      payload: { 
                        type: "preserve", 
                        title: `Preserve deployed value for '${row.variableName}'?`,
                        payload: row.variableName 
                      }
                    })
                  }} 
                />
                <RMIconButton 
                  tooltip="Accept config value (use configurator value)" 
                  icon={TbCheck} 
                  colorScheme="green"
                  onClick={(e) => { 
                    e.stopPropagation()
                    vDispatch({ 
                      type: "SET_CONFIRM_ACTION", 
                      payload: { 
                        type: "accept", 
                        title: `Accept config value for '${row.variableName}'?`,
                        payload: row.variableName 
                      }
                    })
                  }} 
                />
              </>
            )}
            {hasPreserve && !runtimeDiffersFromPreserved && (
              <>
                <Text fontSize="xs" color={isPreserved ? "blue.500" : "green.500"} fontWeight="bold">
                  {isPreserved ? "Preserved" : "Accepted"}
                </Text>
                <RMIconButton 
                  tooltip="Clear preservation status" 
                  icon={TbTrash} 
                  colorScheme="red"
                  size="sm"
                  onClick={(e) => { 
                    e.stopPropagation()
                    vDispatch({ 
                      type: "SET_CONFIRM_ACTION", 
                      payload: { 
                        type: "clear", 
                        title: `Clear preservation for '${row.variableName}'? This will allow automatic configuration.`,
                        payload: row.variableName 
                      }
                    })
                  }} 
                />
              </>
            )}
            {runtimeDiffersFromPreserved && (
              <>
                <RMIconButton 
                  tooltip="Re-preserve with current runtime value (update preserved value to match runtime)" 
                  icon={TbShield} 
                  colorScheme="orange"
                  onClick={(e) => { 
                    e.stopPropagation()
                    vDispatch({ 
                      type: "SET_CONFIRM_ACTION", 
                      payload: { 
                        type: "preserve", 
                        title: `Re-preserve '${row.variableName}' with current runtime value? This will update the preserved value to ${row.runtimeValue}`,
                        payload: row.variableName 
                      }
                    })
                  }} 
                />
                <RMIconButton 
                  tooltip="Restart required to restore preserved value" 
                  icon={TbTrash} 
                  colorScheme="red"
                  size="sm"
                  onClick={(e) => { 
                    e.stopPropagation()
                    vDispatch({ 
                      type: "SET_CONFIRM_ACTION", 
                      payload: { 
                        type: "clear", 
                        title: `Clear preservation for '${row.variableName}'? Runtime change will be lost on next restart.`,
                        payload: row.variableName 
                      }
                    })
                  }} 
                />
              </>
            )}
            {!hasDiff && !hasPreserve && (
              <Text fontSize="xs" color="gray.500">No diff</Text>
            )}
            </HStack>
            <HStack align={"center"} justifyContent={"center"} spacing={2} mt={2}>
              <RMIconButton 
                tooltip="Edit/Override variable value (set custom value)" 
                icon={TbEdit} 
                colorScheme="purple"
                size="sm"
                onClick={(e) => { 
                  e.stopPropagation()
                  vDispatch({ 
                    type: "SET_EDIT_MODAL", 
                    payload: { isOpen: true, data: row }
                  })
                }} 
              />
            </HStack>
          </VStack>
        )
      }, {
        cell: (info) => info.getValue(),
        header: 'Actions',
        id: 'actions',
        size: 100,
        maxSize: 200,
        minSize: 50
      })
    ],
    [showCfg, showDeployed, showRuntime, showPreserve, onNavigateToPFSInstruments, vDispatch]
  )

  return (
    <VStack className={styles.contentContainer}>
      {showInfoAlert && (
        <Alert status="info" variant="left-accent" mb={4}>
          <AlertIcon />
          <Box flex="1">
            <AlertTitle fontSize="sm" fontWeight="bold">Important Note for DBAs</AlertTitle>
            <AlertDescription fontSize="xs">
              If deployed values don't match configurator values after applying changes, please check the database nodes for:
              <br />
              • Custom configuration files in <strong>custom.d/</strong> or <strong>conf.d/</strong> directories
              <br />
              • Manual variable overrides in <strong>my.cnf</strong> or <strong>my.ini</strong>
              <br />
              • Runtime SET GLOBAL/PERSIST commands that may override configuration
              <br />
              These external configurations may take precedence over Replication Manager settings.
              <br />
              <Button 
                size="xs" 
                colorScheme="blue" 
                variant="link" 
                mt={2}
                onClick={handleDismissAlert}
              >
                Don't show this again
              </Button>
            </AlertDescription>
          </Box>
          <CloseButton
            alignSelf="flex-start"
            position="relative"
            right={-1}
            top={-1}
            onClick={handleDismissAlert}
          />
        </Alert>
      )}
      <Flex className={styles.actions}>
        <HStack gap='4'>
          <HStack className={styles.search}>
            <label htmlFor='search'>Search</label>
            <Input id='search' type='search' onChange={handleSearch} />
          </HStack>
          {!showInfoAlert && (
            <RMIconButton
              tooltip="Show important information for DBAs"
              icon={TbInfoCircle}
              size="sm"
              colorScheme="blue"
              onClick={handleShowInfo}
            />
          )}
        </HStack>
        <Box className={styles.divider} />
        <Checkbox size='lg' isChecked={showRowDiff} onChange={setVariableMode} className={styles.checkbox}>
          Show diff only
        </Checkbox>
        <Box className={styles.divider} />
        <Checkbox size='lg' isChecked={showRowPreserved} onChange={(e) => { vDispatch({ type: "SET_SHOW_ROW_PRESERVED", payload: e.target.checked}) }} className={styles.checkbox}>
          Only preserved options
        </Checkbox>
        <Box className={styles.divider} />
        <Text>Show columns:</Text>
        <Checkbox size='lg' isChecked={showCfg} onChange={(e) => { vDispatch({ type: "SET_SHOW_CFG", payload: e.target.checked}) }} className={styles.checkbox}>
          Configurator
        </Checkbox>
        <Checkbox size='lg' isChecked={showDeployed} onChange={(e) => { vDispatch({ type: "SET_SHOW_DEPLOYED", payload: e.target.checked}) }} className={styles.checkbox}>
          Deployed
        </Checkbox>
        <Checkbox size='lg' isChecked={showRuntime} onChange={(e) => { vDispatch({ type: "SET_SHOW_RUNTIME", payload: e.target.checked}) }} className={styles.checkbox}>
          Runtime
        </Checkbox>
        <Checkbox size='lg' isChecked={showPreserve} onChange={(e) => { vDispatch({ type: "SET_SHOW_PRESERVE", payload: e.target.checked}) }} className={styles.checkbox}>
          Preserve
        </Checkbox>
      </Flex>
      <Box className={`${styles.tableContainer} ${styles.variableContainer}`} overflow={'auto'}>
        <DataTable key="variables" data={variablesData} columns={columns} className={styles.table} enablePagination={true} />
      </Box>
      {isOpen && <ConfirmModal title={title} isOpen={isOpen} onConfirmClick={handleConfirm} closeModal={closeConfirmModal} />}
      {complexVariableModal.isOpen && (
        <ComplexVariableModal 
          isOpen={complexVariableModal.isOpen} 
          onClose={() => vDispatch({ type: "SET_COMPLEX_MODAL", payload: { isOpen: false, data: null }})}
          variableData={complexVariableModal.data}
        />
      )}
      {editVariableModal.isOpen && (
        <EditVariableModal 
          isOpen={editVariableModal.isOpen} 
          onClose={() => vDispatch({ type: "SET_EDIT_MODAL", payload: { isOpen: false, data: null }})}
          variableData={editVariableModal.data}
          onSave={handleSaveCustomValue}
        />
      )}
    </VStack>
  )
}

export default Variables
