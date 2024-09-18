import { Flex, HStack, Input, Text, VStack } from '@chakra-ui/react'
import React, { useEffect, useMemo, useState, useRef } from 'react'
import AccordionComponent from '../../../../components/AccordionComponent'
import styles from '../../styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { getDatabaseService } from '../../../../redux/clusterSlice'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../../../../components/DataTable'
import { isEqual } from 'lodash'

function Status({ clusterName, dbId }) {
  const dispatch = useDispatch()
  const {
    cluster: {
      database: {
        status: { statusDelta, statusInnoDB }
      }
    }
  } = useSelector((state) => state)
  const [searchStatusDelta, setSearchStatusDelta] = useState('')

  const [statusDeltaData, setStatusDeltaData] = useState(statusDelta || [])
  const [statusDeltaAllData, setStatusDeltaAllData] = useState(statusDelta || [])
  const prevStatusDeltaRef = useRef(statusDelta)

  const [statusInnoDBData, setStatusInnoDBData] = useState(statusInnoDB || [])
  const prevStatusInnoDBRef = useRef(statusInnoDB)

  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'status-delta', dbId }))
    dispatch(getDatabaseService({ clusterName, serviceName: 'status-innodb', dbId }))
  }, [])

  useEffect(() => {
    if (statusDelta?.length > 0) {
      if (!isEqual(statusDelta, prevStatusDeltaRef.current)) {
        setStatusDeltaData(searchData(statusDelta, searchStatusDelta))
        setStatusDeltaAllData(statusDelta)
        prevStatusDeltaRef.current = statusDelta
      }
    }
  }, [statusDelta])

  useEffect(() => {
    if (statusInnoDB?.length > 0) {
      if (!isEqual(statusInnoDB, prevStatusInnoDBRef.current)) {
        setStatusInnoDBData(searchData(statusInnoDB))
        prevStatusInnoDBRef.current = statusInnoDB
      }
    }
  }, [statusInnoDB])

  useEffect(() => {
    setStatusDeltaData(searchData(statusDeltaAllData, searchStatusDelta))
  }, [searchStatusDelta])

  const searchData = (data, search = '') => {
    const searchedData = data.filter((x) => {
      const searchValue = search.toLowerCase()
      if (x.variableName.toLowerCase().includes(searchValue)) {
        return x
      }
    })
    return searchedData
  }

  const handleSearchStatusDelta = (e) => {
    setSearchStatusDelta(e.target.value)
  }

  const columnHelper = createColumnHelper()

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.variableName, {
        header: 'Status',
        width: '80%'
      }),
      columnHelper.accessor((row) => row.value, {
        header: 'Value',
        cell: (info) => {
          return <Text className={styles.longColumnValue}>{info.getValue()}</Text>
        }
      })
    ],
    []
  )

  return (
    <VStack className={styles.contentContainer}>
      <Flex className={styles.statusContainer}>
        <VStack className={styles.statusInnerContainer}>
          <HStack className={styles.search}>
            <label htmlFor='search'>Search</label>
            <Input id='search' type='search' onChange={handleSearchStatusDelta} />
          </HStack>
          <AccordionComponent
            allowToggle={false}
            heading={'Status Delta'}
            className={styles.accordion}
            body={<DataTable data={statusDeltaData} columns={columns} className={styles.table} />}
          />
        </VStack>
        <VStack className={styles.statusInnerContainer}>
          <AccordionComponent
            heading={'Status InnoDB'}
            className={styles.accordion}
            allowToggle={false}
            body={<DataTable data={statusInnoDBData} columns={columns} className={styles.table} />}
          />
        </VStack>
      </Flex>
    </VStack>
  )
}

export default Status
