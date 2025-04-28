import { Box, Checkbox, Flex, HStack, Input, Text, VStack } from '@chakra-ui/react'
import React, { useEffect, useMemo, useState, useRef } from 'react'

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

function Variables({ clusterName, dbId, toggleVariableMode, variableMode }) {
  const dispatch = useDispatch()
  const {
    cluster: {
      database: { variables }
    }
  } = useSelector((state) => state)

  const [search, setSearch] = useState('')
  const [action, setAction] = useState({ type: '', title: '', payload: '' })
  const [variablesData, setVariablesData] = useState(variables || [])
  const [variablesAllData, setvariablesAllData] = useState(variables || [])
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(null)
  const prevVariablesRef = useRef(variables)
  const { type, title, payload } = action
  const [showDiff, setShowDiff] = useState(variableMode === 'diff' ? true : false)
  const [showPreserved, setShowPreserved] = useState(false)
  const [showCfg, setShowCfg] = useState(true)
  const [showDeployed, setShowDeployed] = useState(true)
  const [showRuntime, setShowRuntime] = useState(true)

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
  }

  const setVariableMode = (e) => {
    const value = e.target.checked ? "diff" : "all"
    setShowDiff(e.target.checked)
    toggleVariableMode(value)
  }

  const handleConfirm = (value) => {
      if (type === 'preserve-true') {
        dispatch(preserveVariable({ clusterName, preserve: true, variableName: payload }))
      } else if (type === 'preserve-false') {
        dispatch(preserveVariable({ clusterName, preserve: false, variableName: payload }))
      }
      closeConfirmModal()
    }

  useEffect(() => {
    dispatch(getDatabaseVariables({ clusterName, serviceName: 'variables', dbId, diff: showDiff }))
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
  }, [search, showPreserved])

  const searchData = (data) => {
    const searchedData = data.filter((x) => {
      const searchValue = search.toLowerCase()
      if (x.variableName.toLowerCase().includes(searchValue) || (showCfg && x.cnfValue?.toLowerCase().includes(searchValue)) || (showDeployed && x.value?.toLowerCase().includes(searchValue)) || (showRuntime && x.runtimeValue?.toLowerCase().includes(searchValue))) {
        return x
      }
    })
    if (showPreserved) {
      return searchedData.filter((x) => x.preserve === true)
    }
    return searchedData
  }

  const handleSearch = (e) => {
    setSearch(e.target.value)
  }

  const columnHelper = createColumnHelper()

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.variableName, {
        header: 'Status',
        width: '30%'
      }),
      ...(showCfg ? [columnHelper.accessor((row) => row.cfgValue, {
        header: 'Configurator',
        width: '30%',
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
        width: '30%',
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
        width: '30%',
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
          { row?.preserve ? (
            <>
              <Text>Preserved</Text>
              <RMIconButton tooltip={"Preserve: False"} icon={TbTrash} onClick={(e) => { e.stopPropagation(); setAction({ type: "preserve-false", title: "Are you sure to remove variable's preservation? This will allow configurator to change the value for whole cluster", payload: row.variableName }); openConfirmModal() }} />
            </>
          ) : (
            <>
              <Text>Not Preserved</Text>
              <RMIconButton tooltip={"Preserve: True"} icon={TbShield} onClick={(e) => { e.stopPropagation(); setAction({ type: "preserve-true", title: "Are you sure to preserve variable? This will prevent configurator to change the value for whole cluster", payload: row.variableName }); openConfirmModal() }} />
            </>

          )}
        </VStack>
      ), {
        cell: (info) => info.getValue(),
        header: 'Actions',
        id: 'actions'
      })
    ],
    [showCfg, showDeployed, showRuntime]
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
        <Checkbox size='lg' isChecked={showDiff} onChange={setVariableMode} className={styles.checkbox}>
          Show diff only
        </Checkbox>
        <Box className={styles.divider} />
        <Checkbox size='lg' isChecked={showPreserved} onChange={(e) => { setShowPreserved(e.target.checked) }} className={styles.checkbox}>
          Only preserved options
        </Checkbox>
        <Box className={styles.divider} />
        <Text>Show columns:</Text>
        <Checkbox size='lg' isChecked={showCfg} onChange={(e) => { setShowCfg(e.target.checked) }} className={styles.checkbox}>
          Configurator
        </Checkbox>
        <Checkbox size='lg' isChecked={showDeployed} onChange={(e) => { setShowDeployed(e.target.checked) }} className={styles.checkbox}>
          Deployed
        </Checkbox>
        <Checkbox size='lg' isChecked={showRuntime} onChange={(e) => { setShowRuntime(e.target.checked) }} className={styles.checkbox}>
          Runtime
        </Checkbox>
      </Flex>
      <Box className={`${styles.tableContainer} ${styles.variableContainer}`} overflow={'auto'}>
        <DataTable data={variablesData} columns={columns} className={styles.table} enablePagination={true} />
      </Box>
      {isConfirmModalOpen && <ConfirmModal title={title} isOpen={isConfirmModalOpen} onConfirmClick={handleConfirm} closeModal={closeConfirmModal} />}
    </VStack>
  )
}

export default Variables
