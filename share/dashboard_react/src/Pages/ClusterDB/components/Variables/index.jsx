import { Box, Checkbox, Flex, HStack, Input, Text, VStack } from '@chakra-ui/react'
import React, { useEffect, useMemo, useState, useRef, useReducer } from 'react'

import styles from '../../styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { getDatabaseVariables, preserveVariable } from '../../../../redux/clusterSlice'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../../../../components/DataTable'
import { isEqual } from 'lodash'
import CopyToClipboard from '../../../../components/CopyToClipboard'
import RMIconButton from '../../../../components/RMIconButton'
import { TbShield, TbTrash } from 'react-icons/tb'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'

const defaultState = {
  showCfg: true,
  showDeployed: true,
  showRuntime: true,
  showPreserve: true,
  showRowDiff: false,
  showRowPreserved: false,
  search: '',
  confirmState: {
    isOpen: false,
    type: '',
    title: '',
    action: null,
    payload: ''
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
    case 'SET_CONFIRM_OPEN':
      return { ...state, confirmState: { ...state.confirmState, isOpen: action.payload } }
    case 'SET_CONFIRM_ACTION':
      return { ...state, confirmState: { ...state.confirmState, ...action.payload, isOpen: true} }
    default:
      return state
  }
}

function Variables({ clusterName, dbId, toggleVariableMode, variableMode }) {
  const [ vState, vDispatch ] = useReducer(reducer, defaultState)
  const dispatch = useDispatch()
  const {
    cluster: {
      database: { variables }
    }
  } = useSelector((state) => state)

  const [variablesData, setVariablesData] = useState(variables || [])
  const [variablesAllData, setvariablesAllData] = useState(variables || [])
  const prevVariablesRef = useRef(variables)

  const { showCfg, showDeployed, showRuntime, showPreserve, showRowDiff, showRowPreserved, search, confirmState } = vState
  const { isOpen, title, payload } = confirmState

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
      if (type === 'preserve-true') {
        dispatch(preserveVariable({ clusterName, preserve: true, variableName: payload }))
      } else if (type === 'preserve-false') {
        dispatch(preserveVariable({ clusterName, preserve: false, variableName: payload }))
      }
      closeConfirmModal()
    }

  useEffect(() => {
    dispatch(getDatabaseVariables({ clusterName, serviceName: 'variables', dbId, diff: showRowDiff }))
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

  const searchData = (data) => {
    const searchedData = data.filter((x) => {
      const searchValue = search.toLowerCase()
      if (x.variableName.toLowerCase().includes(searchValue) || (showCfg && x.cnfValue?.toLowerCase().includes(searchValue)) || (showDeployed && x.value?.toLowerCase().includes(searchValue)) || (showRuntime && x.runtimeValue?.toLowerCase().includes(searchValue))) {
        return x
      }
    })
    if (showRowPreserved) {
      return searchedData.filter((x) => x.preserveValue != null)
    }
    return searchedData
  }

  const handleSearch = (e) => {
    vDispatch({ type: 'SET_SEARCH', payload: e.target.value })
  }

  const columnHelper = createColumnHelper()

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.variableName, {
        header: 'Status',
        size: 100,
        maxSize: 200,
        minSize: 100,
      }),
      ...(showCfg ? [columnHelper.accessor((row) => row.cfgValue, {
        header: 'Configurator',
        size: 100,
        maxSize: 200,
        minSize: 100,
        cell: (info) => {
          const fullString = info.getValue()
          const fullLength = fullString?.length
          return (
            <>
              {fullLength > 15 ? (
                <CopyToClipboard copyIconPosition='start' className={styles.longVariable} text={info.getValue()} />
              ) : (
                <span>{info.getValue()}</span>
              )}
            </>
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
          return (
            <>
              {fullLength > 15 ? (
                <CopyToClipboard copyIconPosition='start' className={styles.longVariable} text={info.getValue()} />
              ) : (
                <span>{info.getValue()}</span>
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
          return (
            <>
              {fullLength > 15 ? (
                <CopyToClipboard copyIconPosition='start' className={styles.longVariable} text={info.getValue()} />
              ) : (
                <span>{info.getValue()}</span>
              )}
            </>
          )
        }
      })]: []),
      ...(showPreserve ? [columnHelper.accessor((row) => row.preserveValue, {
        header: 'Preserve',
        size: 100,
        maxSize: 200,
        minSize: 100,
        cell: (info) => {
          const fullString = info.getValue()
          const fullLength = fullString?.length
          return (
            <>
              {fullLength > 15 ? (
                <CopyToClipboard copyIconPosition='start' className={styles.longVariable} text={info.getValue()} />
              ) : (
                <span>{info.getValue()}</span>
              )}
            </>
          )
        }
      })]: []),
      columnHelper.accessor((row) => (
        <VStack align={"center"} justifyContent={"center"}>
          { row.preserveValue == null ? (
            <RMIconButton tooltip={"Preserve: True"} icon={TbShield} onClick={(e) => { e.stopPropagation(); vDispatch({ type: "SET_CONFIRM_ACTION", payload:{ type: "preserve-true", title: "Are you sure to preserve variable? This will prevent configurator to change the value for whole cluster", payload: row.variableName }}) }} />
          ) : (
            <RMIconButton tooltip={"Preserve: False"} icon={TbTrash} onClick={(e) => { e.stopPropagation(); vDispatch({ type: "SET_CONFIRM_ACTION", payload:{ type: "preserve-false", title: "Are you sure to remove variable's preservation? This will allow configurator to change the value for whole cluster", payload: row.variableName }}) }} />
          )}
        </VStack>
      ), {
        cell: (info) => info.getValue(),
        header: 'Actions',
        id: 'actions',
        size: 100,
        maxSize: 200,
        minSize: 50
      })
    ],
    [showCfg, showDeployed, showRuntime, showPreserve]
  )

  return (
    <VStack className={styles.contentContainer}>
      <Flex className={styles.actions}>
        <HStack gap='4'>
          <HStack className={styles.search}>
            <label htmlFor='search'>Search</label>
            <Input id='search' type='search' onChange={handleSearch} />
          </HStack>
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
        <DataTable data={variablesData} columns={columns} className={styles.table} enablePagination={true} />
      </Box>
      {isOpen && <ConfirmModal title={title} isOpen={isOpen} onConfirmClick={handleConfirm} closeModal={closeConfirmModal} />}
    </VStack>
  )
}

export default Variables
