import { Box, Flex, HStack, Input, VStack } from '@chakra-ui/react'
import React, { useEffect, useMemo, useState, useRef } from 'react'

import styles from '../../styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { getDatabaseService } from '../../../../redux/clusterSlice'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../../../../components/DataTable'
import { isEqual } from 'lodash'
import CopyToClipboard from '../../../../components/CopyToClipboard'

function Variables({ clusterName, dbId }) {
  const dispatch = useDispatch()
  const {
    cluster: {
      database: { variables }
    }
  } = useSelector((state) => state)

  const [search, setSearch] = useState('')

  const [variablesData, setVariablesData] = useState(variables || [])
  const [variablesAllData, setvariablesAllData] = useState(variables || [])
  const prevVariablesRef = useRef(variables)

  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'variables', dbId }))
  }, [])

  useEffect(() => {
    if (variables?.length > 0) {
      if (!isEqual(variables, prevVariablesRef.current)) {
        setVariablesData(searchData(variables))
        setvariablesAllData(variables)
        prevVariablesRef.current = variables
      }
    }
  }, [variables])

  useEffect(() => {
    setVariablesData(searchData(variablesAllData))
  }, [search])

  const searchData = (data) => {
    const searchedData = data.filter((x) => {
      const searchValue = search.toLowerCase()
      if (x.variableName.toLowerCase().includes(searchValue) || x.value.toLowerCase().includes(searchValue)) {
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
        width: '50%'
      }),
      columnHelper.accessor((row) => row.value, {
        header: 'Value',
        width: '50%',
        cell: (info) => {
          const fullString = info.getValue()
          const fullLength = fullString.length
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
      </Flex>
      <Box className={`${styles.tableContainer} ${styles.variableContainer}`}>
        <DataTable data={variablesData} columns={columns} className={styles.table} />
      </Box>
    </VStack>
  )
}

export default Variables
