import { Box, Checkbox, Flex, HStack, Input, Text, VStack, Button, Alert, AlertIcon, AlertTitle, AlertDescription, CloseButton, Tooltip, Badge } from '@chakra-ui/react'
import React, { useEffect, useMemo, useState, useRef, useReducer } from 'react'

import styles from '../../styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { getDatabaseService, preserveVariable, setCustomVariableValue } from '../../../../redux/clusterSlice'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../../../../components/DataTable'
import { isEqual } from 'lodash'
import CopyToClipboard from '../../../../components/CopyToClipboard'
import RMIconButton from '../../../../components/RMIconButton'
import { TbShield, TbTrash, TbCheck, TbAlertCircle, TbZoomIn, TbExternalLink, TbEdit, TbInfoCircle, TbShieldCheck, TbShieldOff } from 'react-icons/tb'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import ComplexVariableModal from './ComplexVariableModal'
import EditVariableModal from './EditVariableModal'

import PreservedVariablesEditor from '../../../../components/PreservedVariablesEditor'
import AccordionComponent from '../../../../components/AccordionComponent'

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

// Helper function to parse size units (K, M, G, T) to bytes
// Uses BigInt to handle values larger than Number.MAX_SAFE_INTEGER
const parseSizeToBytes = (value) => {
  if (value === null || value === undefined || value === '') return null
  
  const strValue = String(value).trim().toUpperCase()
  
  // Check if it's just a plain number
  if (/^\d+$/.test(strValue)) {
    try {
      return BigInt(strValue)
    } catch (e) {
      return null
    }
  }
  
  // Match patterns like: 4G, 128M, 1024K, 2T, 4GB, 128MB, etc.
  const match = strValue.match(/^(\d+(?:\.\d+)?)\s*([KMGT])B?$/i)
  
  if (!match) {
    return null // Not a size value
  }
  
  const [, numStr, unit] = match
  const number = parseFloat(numStr)
  
  // Use BigInt for multipliers to handle large values
  const multipliers = {
    'K': 1024n,
    'M': 1024n * 1024n,
    'G': 1024n * 1024n * 1024n,
    'T': 1024n * 1024n * 1024n * 1024n
  }
  
  const multiplier = multipliers[unit.toUpperCase()]
  if (!multiplier) return null
  
  // Handle decimal values by converting to integer first
  const integerPart = Math.floor(number)
  const decimalPart = number - integerPart
  
  try {
    let result = BigInt(integerPart) * multiplier
    
    // Add decimal portion if present (e.g., 1.5G)
    if (decimalPart > 0) {
      result += BigInt(Math.floor(decimalPart * Number(multiplier)))
    }
    
    return result
  } catch (e) {
    return null
  }
}

// Helper function to check if two values are equal considering size units
const areSizeValuesEqual = (val1, val2) => {
  const bytes1 = parseSizeToBytes(val1)
  const bytes2 = parseSizeToBytes(val2)
  
  // If either couldn't be parsed as size, fall back to string comparison
  if (bytes1 === null || bytes2 === null) {
    return String(val1) === String(val2)
  }
  
  return bytes1 === bytes2
}

// Helper function to detect if a variable is likely a size-based variable
const isSizeVariable = (variableName) => {
  const sizePatterns = [
    /_size$/i,
    /_buffer$/i,
    /_memory$/i,
    /_cache$/i,
    /_length$/i,
    /_limit$/i,
    /^max_/i,
    /^min_/i,
    /innodb_/i,
    /buffer/i,
    /cache/i
  ]
  
  return sizePatterns.some(pattern => pattern.test(variableName))
}

// Helper function to check if values are equal (handles booleans, sizes, and regular values)
const areValuesEqual = (val1, val2, row) => {
  if (val1 === val2) return true
  if (val1 == null || val2 == null) return val1 === val2
  
  // Check for boolean variables
  if (isBooleanVariable(row)) {
    return areBooleanValuesEqual(val1, val2)
  }
  
  // Check for size variables
  if (isSizeVariable(row.variableName)) {
    return areSizeValuesEqual(val1, val2)
  }
  
  // Default string comparison
  return String(val1) === String(val2)
}

function Variables({ clusterName, dbId, toggleVariableMode, variableMode, onNavigateToPFSInstruments, searchFilter, user }) {
  const [ vState, vDispatch ] = useReducer(reducer, defaultState)
  const dispatch = useDispatch()
  const variables = useSelector((state) => state.cluster.database.variables)

  const [variablesData, setVariablesData] = useState(variables || [])
  const [variablesAllData, setvariablesAllData] = useState(variables || [])
  const prevVariablesRef = useRef(variables)

  const { showCfg, showDeployed, showRuntime, showPreserve, showRowDiff, showRowPreserved, showInfoAlert, search, confirmState, complexVariableModal, editVariableModal } = vState
  const { isOpen, title, payload } = confirmState

  // Truncate length for table cell values
  const TRUNCATE_LENGTH = 100

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
          const hasDiff = !areValuesEqual(row.cfgValue, row.value, row)
          const hasPreserve = row.preservedValue != null
          const isPFSInstrument = row.variableName === 'performance_schema_instrument'
          const runtimeDiffersFromPreserved = hasPreserve && 
            !areValuesEqual(row.runtimeValue, row.preservedValue, row) && 
            !isPFSInstrument
          
          // Preservation metadata
          const preservedSource = row.preservedSource // "server-specific" or "cluster-level"
          const preservedPriority = row.preservedPriority // 1, 2, or 3
          const isExcludedFromCluster = row.isExcludedFromCluster // true if excluded
          
          // Priority badges
          const getPriorityBadge = () => {
            if (!hasPreserve) return null
            
            if (preservedPriority === 1) {
              return (
                <Tooltip label="Priority 1: Server-specific override (01_preserved.cnf)" placement="top">
                  <Badge colorScheme="purple" fontSize="xs" px={1}>P1</Badge>
                </Tooltip>
              )
            } else if (preservedPriority === 2) {
              return (
                <Tooltip label="Priority 2: Cluster-level default (preserved_variables.cnf)" placement="top">
                  <Badge colorScheme="blue" fontSize="xs" px={1}>P2</Badge>
                </Tooltip>
              )
            }
            return null
          }
          
          return (
            <HStack spacing={2}>
              {runtimeDiffersFromPreserved && <TbAlertCircle color="red" title="Runtime changed manually!" />}
              {hasDiff && !runtimeDiffersFromPreserved && <TbAlertCircle color="orange" title="Has difference" />}
              {hasPreserve && preservedPriority === 1 && (
                <Tooltip label="Server-specific preservation (Priority 1)" placement="top">
                  <span><TbShieldCheck color="purple" /></span>
                </Tooltip>
              )}
              {hasPreserve && preservedPriority === 2 && (
                <Tooltip label="Cluster-level preservation (Priority 2)" placement="top">
                  <span><TbShield color="blue" /></span>
                </Tooltip>
              )}
              {isExcludedFromCluster && (
                <Tooltip label="Server excluded from cluster-level preservation" placement="top">
                  <span><TbShieldOff color="gray" /></span>
                </Tooltip>
              )}
              {getPriorityBadge()}
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
          
          // Truncate for display and add space after commas for better wrapping
          const displayString = fullString?.replace(/,/g, ', ')
          const truncatedString = fullLength > TRUNCATE_LENGTH ? displayString.substring(0, TRUNCATE_LENGTH) + '...' : displayString
          
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
              <Text 
                title={fullLength > TRUNCATE_LENGTH ? fullString : undefined} 
                whiteSpace="normal" 
                wordBreak="break-word"
                fontWeight={isBoolean ? 'bold' : 'normal'}
              >
                {truncatedString}
              </Text>
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
          
          // Truncate for display and add space after commas for better wrapping
          const displayString = fullString?.replace(/,/g, ', ')
          const truncatedString = fullLength > TRUNCATE_LENGTH ? displayString.substring(0, TRUNCATE_LENGTH) + '...' : displayString
          
          return (
            <Text 
              title={fullLength > TRUNCATE_LENGTH ? fullString : undefined}
              whiteSpace="normal" 
              wordBreak="break-word"
              fontWeight={isBoolean ? 'bold' : 'normal'}
            >
              {truncatedString}
            </Text>
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
            !areValuesEqual(row.runtimeValue, row.preservedValue, row)
          
          // Truncate for display and add space after commas for better wrapping
          const displayString = fullString?.replace(/,/g, ', ')
          const truncatedString = fullLength > TRUNCATE_LENGTH ? displayString.substring(0, TRUNCATE_LENGTH) + '...' : displayString
          
          return (
            <HStack spacing={2}>
              {runtimeDiffersFromPreserved && (
                <TbAlertCircle 
                  color="red" 
                  size={18}
                  title="Runtime value differs from preserved! Manual change detected - will be lost on restart unless re-preserved" 
                />
              )}
              <Text 
                title={fullLength > TRUNCATE_LENGTH ? fullString : undefined}
                whiteSpace="normal"
                wordBreak="break-word"
                color={runtimeDiffersFromPreserved ? 'red' : undefined}
                fontWeight={runtimeDiffersFromPreserved || isBoolean ? 'bold' : 'normal'}
              >
                {truncatedString}
              </Text>
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
          
          // Preservation metadata
          const preservedSource = row.preservedSource
          const preservedPriority = row.preservedPriority
          const isExcludedFromCluster = row.isExcludedFromCluster
          
          // Truncate for display and add space after commas for better wrapping
          const displayString = fullString?.replace(/,/g, ', ')
          const truncatedString = fullLength > TRUNCATE_LENGTH ? displayString.substring(0, TRUNCATE_LENGTH) + '...' : displayString
          
          // Build source info
          let sourceInfo = ''
          if (fullString) {
            if (preservedPriority === 1) {
              sourceInfo = '📋 Server-specific (Priority 1)'
            } else if (preservedPriority === 2) {
              sourceInfo = '🌐 Cluster-level (Priority 2)'
            }
          }
          
          return (
            <VStack align="flex-start" spacing={0}>
              <HStack spacing={1}>
                {preservedPriority === 1 && (
                  <Tooltip label="Server-specific preservation (Priority 1 - Highest)" placement="top">
                    <Badge colorScheme="purple" fontSize="xs">Server</Badge>
                  </Tooltip>
                )}
                {preservedPriority === 2 && (
                  <Tooltip label="Cluster-level preservation (Priority 2)" placement="top">
                    <Badge colorScheme="blue" fontSize="xs">Cluster</Badge>
                  </Tooltip>
                )}
                <Text 
                  title={fullLength > TRUNCATE_LENGTH ? `${sourceInfo}\n\nValue: ${fullString}` : sourceInfo}
                  whiteSpace="normal" 
                  wordBreak="break-word"
                  fontWeight={isBoolean ? 'bold' : 'normal'}
                >
                  {truncatedString}
                </Text>
              </HStack>
              {isExcludedFromCluster && preservedPriority === 1 && (
                <Tooltip label="This server has a server-specific override but is excluded from cluster-level preservation" placement="top">
                  <Text fontSize="xs" color="gray.500" fontStyle="italic">
                    (cluster excluded)
                  </Text>
                </Tooltip>
              )}
            </VStack>
          )
        }
      })]: []),
      columnHelper.accessor((row) => {
        const hasPreserve = row.preservedValue != null
        const hasDiff = !areValuesEqual(row.cfgValue, row.value, row)
        const isPreserved = hasPreserve && areValuesEqual(row.preservedValue, row.value, row)
        const isPFSInstrument = row.variableName === 'performance_schema_instrument'
        const runtimeDiffersFromPreserved = hasPreserve && 
          !areValuesEqual(row.runtimeValue, row.preservedValue, row) && 
          !isPFSInstrument
        
        return (
          <HStack align={"center"} justifyContent={"center"} spacing={2} wrap={"nowrap"}>
            {hasDiff && !hasPreserve && (
              <>
                <RMIconButton 
                  tooltip="Preserve deployed value (keep current database value)" 
                  icon={TbShield} 
                  colorScheme="blue"
                  size="sm"
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
                  size="sm"
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
                  size="sm"
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
              <HStack spacing={1}>
                <TbCheck color="green" size={16} />
                <Text fontSize="xs" color="green.600" fontWeight="medium">Synced</Text>
              </HStack>
            )}
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
        )
      }, {
        cell: (info) => info.getValue(),
        header: 'Actions',
        id: 'actions',
        size: 150,
        maxSize: 250,
        minSize: 150
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
              <strong>Three-Tier Preserved Variables System:</strong>
              <br />
              • <strong>Priority 1 (Server-specific)</strong>: Variables in server's <code>01_preserved.cnf</code> - highest priority, shown with purple badge
              <br />
              • <strong>Priority 2 (Cluster-level)</strong>: Variables in cluster's <code>preserved_variables.cnf</code> - applies to all servers unless excluded, shown with blue badge
              <br />
              • <strong>Priority 3 (Excluded/None)</strong>: Servers can be excluded from cluster-level variables using <code>.exclude</code> suffix
              <br />
              <br />
              <strong>Icons Legend:</strong>
              <br />
              • <TbShieldCheck style={{display: 'inline', verticalAlign: 'middle'}} color="purple" /> Server-specific preservation (Priority 1)
              <br />
              • <TbShield style={{display: 'inline', verticalAlign: 'middle'}} color="blue" /> Cluster-level preservation (Priority 2)
              <br />
              • <TbShieldOff style={{display: 'inline', verticalAlign: 'middle'}} color="gray" /> Excluded from cluster-level
              <br />
              <br />
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
      
      {user?.grants['cluster-settings'] && (
        <Box width="100%" mt={4}>
          <AccordionComponent
            heading={'Cluster Preserved Variables Configuration'}
            className={styles.accordion}
            body={<PreservedVariablesEditor clusterName={clusterName} user={user} />}
          />
        </Box>
      )}
      
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
