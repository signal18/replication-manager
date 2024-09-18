import React, { useEffect, useMemo, useState, useRef } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { getDatabaseService } from '../../../../redux/clusterSlice'
import { createColumnHelper } from '@tanstack/react-table'
import { Box, Flex, HStack, Input, VStack } from '@chakra-ui/react'
import styles from '../../styles.module.scss'
import { DataTable } from '../../../../components/DataTable'
import { isEqual } from 'lodash'
import Toolbar from '../Toolbar'

function MetadataLocks({ clusterName, dbId, selectedDBServer }) {
  const dispatch = useDispatch()

  const {
    cluster: {
      database: { metadataLocks }
    }
  } = useSelector((state) => state)

  const [search, setSearch] = useState('')
  const [data, setData] = useState(metadataLocks || [])
  const [allData, setAllData] = useState(metadataLocks || [])
  const prevMetadataLocksRef = useRef(metadataLocks)

  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'meta-data-locks', dbId }))
  }, [])

  useEffect(() => {
    if (metadataLocks?.length > 0) {
      if (!isEqual(metadataLocks, prevMetadataLocksRef.current)) {
        setAllData(metadataLocks)
        setData(searchData(metadataLocks))

        prevMetadataLocksRef.current = metadataLocks
      }
    }
  }, [metadataLocks])

  useEffect(() => {
    setData(searchData(allData))
  }, [search])

  const searchData = (data) => {
    const searchedData = data.filter((x) => {
      const searchValue = search.toLowerCase()
      if (
        x.lockMode?.String.toLowerCase().includes(searchValue) ||
        x.shemaName?.String.toLowerCase().includes(searchValue) ||
        x.shemaName?.String.toLowerCase().includes(searchValue)
      ) {
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
        header: '#'
      }),
      columnHelper.accessor((row) => row.lastSeen, {
        header: 'Time'
      }),
      columnHelper.accessor((row) => row.lockMode.String, {
        header: 'Lock Mode'
      }),
      columnHelper.accessor((row) => row.lockDuration.String, {
        header: 'Duration'
      }),
      columnHelper.accessor((row) => row.lockTimeMs.Int64, {
        header: 'Lock Time(ms)'
      }),
      columnHelper.accessor((row) => row.lockType.String, {
        header: 'Lock Type'
      }),
      columnHelper.accessor((row) => row.lockCatalog.String, {
        header: 'Catalog'
      }),
      columnHelper.accessor((row) => row.lockSchema.String, {
        header: 'Schema'
      }),
      columnHelper.accessor((row) => row.lockName.String, {
        header: 'Table'
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
        <Toolbar clusterName={clusterName} tab='metadataLocks' dbId={dbId} selectedDBServer={selectedDBServer} />
      </Flex>
      <Box className={styles.tableContainer}>
        <DataTable data={data} columns={columns} className={styles.table} />
      </Box>
    </VStack>
  )
}

export default MetadataLocks
