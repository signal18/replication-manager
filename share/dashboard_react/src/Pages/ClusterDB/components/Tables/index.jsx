import React, { useEffect, useMemo, useState, useRef } from 'react'
import styles from '../../styles.module.scss'
import { Flex, HStack, Input, Tooltip, VStack, Text, Box } from '@chakra-ui/react'
import { createColumnHelper } from '@tanstack/react-table'
import { useDispatch, useSelector } from 'react-redux'
import { DataTable } from '../../../../components/DataTable'
import CopyToClipboard from '../../../../components/CopyToClipboard'
import { isEqual } from 'lodash'
import { checksumTable, getDatabaseService } from '../../../../redux/clusterSlice'
import Toolbar from '../Toolbar'
import { getTablePct } from '../../../../utility/common'
import RMButton from '../../../../components/RMButton'
import Gauge from '../../../../components/Gauge'

function Tables({ clusterName, dbId, selectedDBServer, tableSize }) {
  const dispatch = useDispatch()
  const [search, setSearch] = useState('')
  console.log('tableSize::', tableSize)

  const {
    cluster: {
      database: { tables }
    }
  } = useSelector((state) => state)
  const [data, setData] = useState(tables || [])
  const [allData, setAllData] = useState(tables || [])
  const prevTables = useRef(tables)

  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'tables', dbId }))
  }, [])

  useEffect(() => {
    if (tables?.length > 0) {
      if (!isEqual(tables, prevTables.current)) {
        setAllData(tables)
        setData(searchData(tables))

        // Update the previous slowQueries value
        prevTables.current = tables
      }
    }
  }, [tables])

  useEffect(() => {
    setData(searchData(allData))
  }, [search])

  const searchData = (serverData) => {
    const searchedData = serverData.filter((x) => {
      const searchValue = search.toLowerCase()
      if (
        x.table_schema.toLowerCase().includes(searchValue) ||
        x.table_name.toLowerCase().includes(searchValue) ||
        x.engine.toLowerCase().includes(searchValue)
      ) {
        return x
      }
    })
    return searchedData
  }

  const handleSearch = (e) => {
    setSearch(e.target.value)
  }

  const handleChecksum = (schema, table) => {
    console.log('schema', schema)
    dispatch(checksumTable({ clusterName, schema, table }))
  }

  const columnHelper = createColumnHelper()

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.table_schema, {
        header: 'Schema',
        cell: (info) => (
          <Flex className={styles.tablesSchemaCol}>
            <RMButton
              className={styles.btnChecksum}
              onClick={() => handleChecksum(info.row.original.table_schema, info.row.original.table_name)}>
              CHECKSUM
            </RMButton>
            <span>{info.getValue()}</span>
          </Flex>
        )
      }),
      columnHelper.accessor((row) => row.table_name, {
        header: 'Name'
      }),
      columnHelper.accessor((row) => row.engine, {
        header: 'Engine'
      }),
      columnHelper.accessor((row) => row.table_rows, {
        header: 'Rows'
      }),
      columnHelper.accessor((row) => row.data_length, {
        header: 'Data'
      }),
      columnHelper.accessor((row) => row.index_length, {
        header: 'Index'
      }),
      columnHelper.accessor((row) => getTablePct(row.data_length, row.index_length, tableSize), {
        header: '%',
        cell: (info) => {
          if (isNaN(info.getValue())) {
            return ''
          }
          return (
            <Gauge
              className={styles.gauge}
              minValue={0}
              maxValue={100}
              value={info.getValue()}
              width={210}
              height={90}
            />
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
      <Box className={styles.tableContainer}>
        <DataTable data={data} columns={columns} className={styles.table} />
      </Box>
    </VStack>
  )
}

export default Tables
