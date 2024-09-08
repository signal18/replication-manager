import React, { useEffect, useMemo, useState } from 'react'
import styles from './styles.module.scss'
import { Checkbox, Flex, HStack, Input, Tooltip, VStack, Text } from '@chakra-ui/react'
import { createColumnHelper } from '@tanstack/react-table'
import { useSelector } from 'react-redux'
import { DataTable } from '../../../../components/DataTable'
import { getReadableTime } from '../../../../utility/common'
import ServerMenu from '../../../Dashboard/components/DBServers/ServerMenu'
import ServerStatus from '../../../../components/ServerStatus'

function ProcessList({ clusterName, selectedDBServer, user }) {
  const [data, setData] = useState([])
  const [allData, setAllData] = useState([])
  const [dataWithoutSleep, setDataWithoutSleep] = useState([])
  const [includeSleep, setIncludeSleep] = useState(false)
  const [search, setSearch] = useState('')

  const {
    cluster: {
      clusterMaster,
      clusterData,
      database: { processList }
    }
  } = useSelector((state) => state)

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
        header: 'Id'
      }),
      columnHelper.accessor((row) => row.user, {
        header: 'User'
      }),
      columnHelper.accessor((row) => row.host, {
        header: 'Host'
      }),
      columnHelper.accessor((row) => row.db.String, {
        header: 'Database'
      }),
      columnHelper.accessor((row) => row.command, {
        header: 'Command'
      }),

      columnHelper.accessor(
        (row) => (
          <Tooltip label={getReadableTime(row.time.Float64)}>
            <span>{row.time.Float64}</span>
          </Tooltip>
        ),
        {
          header: 'Time',
          cell: (info) => info.getValue()
        }
      ),
      columnHelper.accessor((row) => row.state.String, {
        header: 'State'
      }),
      columnHelper.accessor((row) => row.info.String, {
        header: 'Info'
      })
    ],
    []
  )

  return (
    <VStack className={styles.processlistContainer}>
      <Flex className={styles.actions}>
        <HStack>
          {selectedDBServer && (
            <>
              <ServerMenu
                clusterName={clusterName}
                clusterMasterId={clusterMaster?.id}
                backupLogicalType={clusterData?.config?.backupLogicalType}
                backupPhysicalType={clusterData?.config?.backupPhysicalType}
                row={selectedDBServer}
                user={user}
                showCompareWithOption={false}
              />
              <ServerStatus state={selectedDBServer?.state} />
              <Text className={styles.serverName}>{`${selectedDBServer?.host}:${selectedDBServer?.port}`}</Text>
            </>
          )}
        </HStack>
        <HStack gap='4'>
          <HStack className={styles.search}>
            <label htmlFor='search'>Search</label>
            <Input id='search' type='search' onChange={handleSearch} />
          </HStack>
        </HStack>
        <Checkbox size='lg' isChecked={includeSleep} onChange={handleIncludeSleep} className={styles.checkbox}>
          Include Sleep command
        </Checkbox>
      </Flex>
      <DataTable data={data} columns={columns} enablePagination={true} className={styles.table} />
    </VStack>
  )
}

export default ProcessList
