import React, { useEffect, useMemo, useState } from 'react'
import styles from '../../styles.module.scss'
import { Box, Checkbox, Flex, HStack, Input, Tooltip, VStack } from '@chakra-ui/react'
import { createColumnHelper } from '@tanstack/react-table'
import { useDispatch, useSelector } from 'react-redux'
import { DataTable } from '../../../../components/DataTable'
import { getReadableTime } from '../../../../utility/common'
import DropdownSysbench from '../../../../components/DropdownSysbench'
import { getDatabaseService } from '../../../../redux/clusterSlice'

function ProcessList({ clusterName, dbId }) {
  const dispatch = useDispatch()
  const [data, setData] = useState([])

  const [includeSleep, setIncludeSleep] = useState(false)
  const [search, setSearch] = useState('')

  const {
    cluster: {
      database: { processList }
    }
  } = useSelector((state) => state)

  const [allData, setAllData] = useState(processList || [])
  const [dataWithoutSleep, setDataWithoutSleep] = useState(processList || [])

  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'processlist', dbId }))
  }, [])

  useEffect(() => {
    if (processList?.length > 0) {
      setAllData(processList)
      if (includeSleep) {
        setData(searchData(processList))
      } else {
        const dataWithoutSleep = processList.filter((x) => x.command !== 'Sleep')
        setDataWithoutSleep(dataWithoutSleep)
        setData(searchData(dataWithoutSleep))
      }
    }
  }, [processList])

  useEffect(() => {
    let updatedData = []
    if (includeSleep) {
      updatedData = allData
    } else {
      updatedData = dataWithoutSleep
    }

    if (search) {
      updatedData = searchData(updatedData)
    }
    setData(updatedData)
  }, [includeSleep, search])

  const searchData = (serverData) => {
    const searchedData = serverData.filter((x) => {
      const searchValue = search.toLowerCase()
      if (
        x.user.toLowerCase().includes(searchValue) ||
        x.command.toLowerCase().includes(searchValue) ||
        x.state.String.toLowerCase().includes(searchValue) ||
        x.info.String.toLowerCase().includes(searchValue) ||
        x.host.toLowerCase().includes(searchValue)
      ) {
        return x
      }
    })
    return searchedData
  }

  const handleIncludeSleep = (e) => {
    setIncludeSleep(e.target.checked)
  }

  const handleSearch = (e) => {
    setSearch(e.target.value)
  }
  const columnHelper = createColumnHelper()

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.id, {
        header: 'Id',
        enableSorting: false
      }),
      columnHelper.accessor((row) => row.user, {
        header: 'User',
        enableSorting: false
      }),
      columnHelper.accessor((row) => row.host, {
        header: 'Host',
        enableSorting: false
      }),
      columnHelper.accessor((row) => row.db.String, {
        header: 'Database',
        enableSorting: false
      }),
      columnHelper.accessor((row) => row.command, {
        header: 'Command',
        enableSorting: false
      }),

      columnHelper.accessor((row) => row.time.Float64, {
        header: 'Time',
        cell: (info) => (
          <Tooltip label={getReadableTime(info.getValue())}>
            <span>{info.getValue()}</span>
          </Tooltip>
        ),
        enableSorting: true,
        sortingFn: 'basic'
      }),
      columnHelper.accessor((row) => row.state.String, {
        header: 'State',
        enableSorting: false
      }),
      columnHelper.accessor((row) => row.info.String, {
        header: 'Info',
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
        <Box className={styles.divider} />
        <Checkbox size='lg' isChecked={includeSleep} onChange={handleIncludeSleep} className={styles.checkbox}>
          Include Sleep command
        </Checkbox>
        <Box className={styles.divider} />
        <DropdownSysbench clusterName={clusterName} />
      </Flex>
      <Box className={styles.tableContainer}>
        <DataTable data={data} columns={columns} className={styles.table} enableSorting={true} />
      </Box>
    </VStack>
  )
}

export default ProcessList
