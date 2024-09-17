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

function DigestQueries({ clusterName, dbId, selectedDBServer, digestMode, toggleDigestMode }) {
  const dispatch = useDispatch()
  // const [search, setSearch] = useState('')

  const {
    cluster: {
      database: { digestQueries }
    }
  } = useSelector((state) => state)
  const [data, setData] = useState(digestQueries || [])
  // const [allData, setAllData] = useState(digestQueries || [])
  const prevDigestQueries = useRef(digestQueries)

  useEffect(() => {
    if (digestMode === 'pfs') {
      dispatch(getDatabaseService({ clusterName, serviceName: 'digest-statements-pfs', dbId }))
    } else {
      dispatch(getDatabaseService({ clusterName, serviceName: 'digest-statements-slow', dbId }))
    }
  }, [])

  useEffect(() => {
    if (digestQueries?.length > 0) {
      if (!isEqual(digestQueries, prevDigestQueries.current)) {
        // setAllData(digestQueries)
        setData(digestQueries)

        // Update the previous digestQueries value
        prevDigestQueries.current = digestQueries
      }
    }
  }, [digestQueries])

  // useEffect(() => {
  //   setData(searchData(allData))
  // }, [search])

  // const searchData = (serverData) => {
  //   const searchedData = serverData.filter((x) => {
  //     const searchValue = search.toLowerCase()
  //     if (x.query.toLowerCase().includes(searchValue)) {
  //       return x
  //     }
  //   })
  //   return searchedData
  // }

  // const handleSearch = (e) => {
  //   setSearch(e.target.value)
  // }
  const columnHelper = createColumnHelper()

  const columns = useMemo(
    () => [
      columnHelper.accessor((row, rowIndex) => rowIndex + 1, {
        header: 'Id'
      }),
      columnHelper.accessor((row) => row.lastSeen, {
        header: 'Last seen at',
        id: 'lastseen'
      }),
      columnHelper.accessor((row) => row.shemaName || '-', {
        header: 'Schema',
        id: 'schema'
      }),
      columnHelper.accessor((row) => row.query, {
        header: 'Query',
        id: 'query',
        cell: (info) => (
          <CopyToClipboard
            text={info.getValue()}
            className={styles.clipboardText}
            textType='Query'
            copyIconPosition='start'
          />
        )
      }),
      columnHelper.accessor((row) => row.execCount, {
        header: 'Count',
        id: 'count'
      }),

      columnHelper.group({
        header: 'Times',
        columns: [
          columnHelper.accessor((row) => row.execTimeTotal, {
            header: 'Total',
            id: 'total'
          }),
          columnHelper.accessor((row) => row.execTimeMax.Float64, {
            header: 'Max',
            id: 'max'
          }),
          columnHelper.accessor((row) => row.execTimeAvgMs.Float64, {
            header: 'Avg',
            id: 'avg'
          })
        ]
      }),
      columnHelper.group({
        header: 'Rows',
        id: 'row',
        columns: [
          columnHelper.accessor((row) => row.rowsSent, {
            header: 'Sent',
            id: 'sent'
          }),
          columnHelper.accessor((row) => row.rowsSentAvg, {
            header: 'Avg',
            id: 'row_avg'
          }),
          columnHelper.accessor((row) => row.rowsScanned, {
            header: 'Scan',
            id: 'scan'
          })
        ]
      }),
      columnHelper.group({
        header: 'Plans',
        id: 'plans',
        columns: [
          columnHelper.accessor((row) => row.planFullScan, {
            header: 'Full',
            id: 'full'
          }),
          columnHelper.accessor((row) => row.planTmpDisk, {
            header: 'Disk',
            id: 'disk'
          }),
          columnHelper.accessor((row) => row.planTmpMem, {
            header: 'Mem',
            id: 'mem'
          })
        ]
      })
    ],
    []
  )

  return (
    <VStack className={styles.contentContainer}>
      <Flex className={styles.actions}>
        {/* <HStack gap='4'>
          <HStack className={styles.search}>
            <label htmlFor='search'>Search</label>
            <Input id='search' type='search' onChange={handleSearch} />
          </HStack>
        </HStack> */}
        <Toolbar
          clusterName={clusterName}
          tab='digestQueries'
          dbId={dbId}
          selectedDBServer={selectedDBServer}
          digestMode={digestMode}
          toggleDigestMode={toggleDigestMode}
        />
      </Flex>
      <Box className={styles.tableContainer}>
        <DataTable data={data} columns={columns} className={styles.table} />
      </Box>
    </VStack>
  )
}

export default DigestQueries
