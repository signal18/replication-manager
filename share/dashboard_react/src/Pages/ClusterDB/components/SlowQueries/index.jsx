import React, { useEffect, useMemo, useState, useRef } from 'react'
import styles from '../../styles.module.scss'
import { Flex, HStack, Input, Tooltip, VStack, Text, Box } from '@chakra-ui/react'
import { createColumnHelper } from '@tanstack/react-table'
import { useDispatch, useSelector } from 'react-redux'
import { DataTable } from '../../../../components/DataTable'
import CopyToClipboard from '../../../../components/CopyToClipboard'
import { isEqual } from 'lodash'
import { getDatabaseService } from '../../../../redux/clusterSlice'
import Toolbar from '../Toolbar'

function SlowQueries({ clusterName, dbId, selectedDBServer }) {
  const dispatch = useDispatch()
  const [search, setSearch] = useState('')

  const {
    cluster: {
      database: { slowQueries }
    }
  } = useSelector((state) => state)
  const [data, setData] = useState(slowQueries || [])
  const [allData, setAllData] = useState(slowQueries || [])
  const prevSlowQueries = useRef(slowQueries)

  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'slow-queries', dbId }))
  }, [])

  useEffect(() => {
    if (slowQueries?.length > 0) {
      if (!isEqual(slowQueries, prevSlowQueries.current)) {
        setAllData(slowQueries)
        setData(searchData(slowQueries))

        // Update the previous slowQueries value
        prevSlowQueries.current = slowQueries
      }
    }
  }, [slowQueries])

  useEffect(() => {
    setData(searchData(allData))
  }, [search])

  const searchData = (serverData) => {
    const searchedData = serverData.filter((x) => {
      const searchValue = search.toLowerCase()
      if (x.query.toLowerCase().includes(searchValue)) {
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
      columnHelper.accessor((row, rowIndex) => rowIndex + 1, {
        header: '#',
        enableSorting: false
      }),
      columnHelper.accessor((row) => row.lastSeen, {
        header: 'Time',
        enableSorting: false
      }),
      columnHelper.accessor((row) => row.shemaName || '-', {
        header: 'Schema',
        enableSorting: false
      }),
      columnHelper.accessor((row) => row.query, {
        header: 'Query',
        cell: (info) => (
          <CopyToClipboard
            text={info.getValue()}
            className={styles.clipboardText}
            textType='Query'
            copyIconPosition='start'
          />
        ),
        enableSorting: false
      }),
      columnHelper.accessor((row) => row.execTimeTotal, {
        header: 'Exec Total time',
        enableSorting: false
      }),
      columnHelper.accessor((row) => row.rowsScanned, {
        header: 'Rows examined',
        enableSorting: false
      }),
      columnHelper.accessor((row) => row.rowsSent, {
        header: 'Rows sent',
        enableSorting: false
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
        <Toolbar clusterName={clusterName} tab='slowQueries' dbId={dbId} selectedDBServer={selectedDBServer} />
      </Flex>
      <Box className={styles.tableContainer}>
        <DataTable
          data={data}
          columns={columns}
          className={styles.table}
          enablePagination={true}
          enableSorting={true}
        />
      </Box>
    </VStack>
  )
}

export default SlowQueries
