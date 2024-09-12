import { Box, HStack, Spinner, Text, Tooltip, VStack } from '@chakra-ui/react'
import React, { useEffect, useMemo, useRef, useState } from 'react'
import AccordionComponent from '../../components/AccordionComponent'
import ClusterWorkload from '../Dashboard/components/ClusterWorkload'
import styles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { isEqual } from 'lodash'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../../components/DataTable'
import { getReadableTime } from '../../utility/common'
import { Link } from 'react-router-dom'
import { getTopProcess } from '../../redux/clusterSlice'

function Top({ selectedCluster }) {
  const dispatch = useDispatch()
  const {
    cluster: { topProcess }
  } = useSelector((state) => state)

  const [groupedData, setGroupedData] = useState(null)

  const prevTopProcesses = useRef(null)

  useEffect(() => {
    dispatch(getTopProcess({ clusterName: selectedCluster?.name }))
  }, [])

  useEffect(() => {
    if (topProcess?.length > 0) {
      if (topProcess.length !== prevTopProcesses.current?.length) {
        const grouped = topProcess.reduce((acc, item) => {
          const url = item.url

          // If the url doesn't exist as a key in the accumulator, create it
          if (!acc[url]) {
            acc[url] = []
          }

          // Push the current item into the array corresponding to its url
          acc[url].push(item)

          return acc
        }, {})
        setGroupedData(grouped)
        prevTopProcesses.current = topProcess
      } else {
        console.log('xxxx no rearrange')
      }
    }
  }, [topProcess])

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

  const renderTables = () => {
    const tables = []
    if (groupedData) {
      console.log('groupedData::', groupedData)
      for (let [key, value] of Object.entries(groupedData)) {
        tables.push(
          <AccordionComponent
            className={styles.accordion}
            heading={
              <HStack>
                <Text> {key}</Text>
                <Link className={styles.morelink} to={`/clusters/${selectedCluster?.name}/`}>
                  show more
                </Link>
              </HStack>
            }
            body={<DataTable data={value} columns={columns} className={styles.table} />}
          />
        )
      }
    }
    return tables
  }

  return (
    <VStack className={styles.topContainer}>
      <AccordionComponent
        className={styles.accordion}
        heading={'Cluster Workload'}
        body={<ClusterWorkload workload={selectedCluster?.workLoad} />}
      />
      {renderTables()}
    </VStack>
  )
}

export default Top
