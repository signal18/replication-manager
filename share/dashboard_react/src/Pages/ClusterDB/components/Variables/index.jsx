import { Box, Checkbox, Flex, HStack, Input, Text, VStack } from '@chakra-ui/react'
import React, { useEffect, useMemo, useState, useRef } from 'react'

import styles from '../../styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { getDatabaseVariables } from '../../../../redux/clusterSlice'
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
  const [showDiff, setShowDiff] = useState(variableMode === 'diff')

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
  }

  const handleConfirm = (value) => {
      if (type === 'preserve-true') {
        dispatch(preserveDbVariable({ clusterName: selectedCluster.name, serverName: selectedDBServer.name, preserve: true, variableName: payload }))
      } else if (type === 'preserve-false') {
        dispatch(preserveDbVariable({ clusterName: selectedCluster.name, serverName: selectedDBServer.name, preserve: false, variableName: payload }))
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
  }, [search])

  const searchData = (data) => {
    const searchedData = data.filter((x) => {
      const searchValue = search.toLowerCase()
      if (x.variableName.toLowerCase().includes(searchValue) || x.cnfValue?.toLowerCase().includes(searchValue)|| x.value?.toLowerCase().includes(searchValue)) {
        return x
      }
    })
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
      columnHelper.accessor((row) => row.cfgValue, {
        header: 'Configurator Value',
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
      }),
      columnHelper.accessor((row) => row.value, {
        header: 'Server Value',
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
      }),
      columnHelper.accessor((row) => (
        <VStack align={"center"} justifyContent={"center"}>
          { row?.preserve ? (
            <>
              <Text>Preserved</Text>
              <RMIconButton tooltip={"Preserve: False"} icon={TbTrash} onClick={(e) => { e.stopPropagation(); setAction({ type: "preserve-false", title: "Are you sure to remove variable's preservation?", payload: row.variableName }); openConfirmModal() }} />
            </>
          ) : (
            <>
              <Text>Not Preserved</Text>
              <RMIconButton tooltip={"Preserve: True"} icon={TbShield} onClick={(e) => { e.stopPropagation(); setAction({ type: "preserve-true", title: "Are you sure to preserve variable?", payload: row.variableName }); openConfirmModal() }} />
            </>

          )}
        </VStack>
      ), {
        cell: (info) => info.getValue(),
        header: 'Actions',
        id: 'actions'
      })
    ],
    []
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
        <Checkbox size='lg' isChecked={showDiff} onChange={toggleVariableMode} className={styles.checkbox}>
          Show diff only
        </Checkbox>
      </Flex>
      <Box className={`${styles.tableContainer} ${styles.variableContainer}`}>
        <DataTable data={variablesData} columns={columns} className={styles.table} />
      </Box>
      {isConfirmModalOpen && <ConfirmModal title={title} isOpen={isConfirmModalOpen} onConfirmClick={handleConfirm} closeModal={closeConfirmModal} />}
    </VStack>
  )
}

export default Variables
