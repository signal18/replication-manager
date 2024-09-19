import React, { useEffect, useMemo, useState, useRef } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { getDatabaseService } from '../../../../redux/clusterSlice'
import { createColumnHelper } from '@tanstack/react-table'
import { Box, Flex, HStack, Input, VStack } from '@chakra-ui/react'
import styles from '../../styles.module.scss'
import { DataTable } from '../../../../components/DataTable'
import { isEqual } from 'lodash'
import Toolbar from '../Toolbar'

function ResponseTime({ clusterName, dbId, selectedDBServer }) {
  const dispatch = useDispatch()

  const {
    cluster: {
      database: { responsetime }
    }
  } = useSelector((state) => state)

  // const [search, setSearch] = useState('')
  const [data, setData] = useState(responsetime || [])
  //const [allData, setAllData] = useState(responsetime || [])
  const prevRespTimeRef = useRef(responsetime)

  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'query-response-time', dbId }))
  }, [])

  useEffect(() => {
    if (responsetime?.length > 0) {
      if (!isEqual(responsetime, prevRespTimeRef.current)) {
        //setAllData(responsetime)
        //setData(searchData(responsetime))
        setData(responsetime)
        prevRespTimeRef.current = responsetime
      }
    }
  }, [responsetime])

  // useEffect(() => {
  //   setData(searchData(allData))
  // }, [search])

  // const searchData = (data) => {
  //   const searchedData = data.filter((x) => {
  //     const searchValue = search.toLowerCase()
  //     if (
  //       x.lockMode?.String.toLowerCase().includes(searchValue) ||
  //       x.shemaName?.String.toLowerCase().includes(searchValue) ||
  //       x.shemaName?.String.toLowerCase().includes(searchValue)
  //     ) {
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
        header: '#'
      }),
      columnHelper.accessor((row) => row.time, {
        header: 'Time'
      }),
      columnHelper.accessor((row) => row.count, {
        header: 'Count'
      }),
      columnHelper.accessor((row) => row.total, {
        header: 'Total'
      })
    ],
    []
  )
  return (
    <VStack className={styles.contentContainer}>
      <Flex className={styles.actions}>
        <Toolbar clusterName={clusterName} tab='responseTime' dbId={dbId} selectedDBServer={selectedDBServer} />
      </Flex>
      <Box className={styles.tableContainer}>
        <DataTable data={data} columns={columns} className={styles.table} />
      </Box>
    </VStack>
  )
}

export default ResponseTime
