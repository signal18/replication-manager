import { Flex, HStack, Text, Tooltip, VStack } from '@chakra-ui/react'
import React, { useEffect, useMemo, useState } from 'react'
import AccordionComponent from '../../components/AccordionComponent'
import ClusterWorkload from '../Dashboard/components/ClusterWorkload'
import styles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../../components/DataTable'
import { getReadableTime } from '../../utility/common'
import { Link } from 'react-router-dom'
import { getDatabaseService, getTopProcess } from '../../redux/clusterSlice'
import BarGraph from '../../components/BarGraph'

function Top({ selectedCluster }) {
  const dispatch = useDispatch()
  const {
    cluster: {
      topProcess,
      database: { statusDelta }
    }
  } = useSelector((state) => state)

  const [graphData, setGraphData] = useState({})

  useEffect(() => {
    dispatch(getTopProcess({ clusterName: selectedCluster?.name }))
  }, [])

  useEffect(() => {
    if (topProcess) {
      for (let key of Object.keys(topProcess)) {
        dispatch(getDatabaseService({ clusterName: selectedCluster?.name, serviceName: 'status-delta', dbId: key }))
      }
    }
  }, [topProcess])

  const addMetric = (variableObj, metricName, friendlyMetricName, arr, groupName) => {
    const isMetricExists = arr.find((x) => x.variableName === metricName)
    if (!isMetricExists) {
      if (variableObj.variableName === metricName) {
        arr.push({ ...variableObj, name: friendlyMetricName, groupName })
      } else {
        arr.push({ variableName: metricName, value: 0, name: friendlyMetricName, groupName })
      }
    }
  }

  useEffect(() => {
    if (statusDelta) {
      let graph = {}
      for (let [key, value] of Object.entries(statusDelta)) {
        if (value) {
          const graphArr = []
          let read = 0

          const updatedValueArray = value.filter(
            (x) =>
              x.variableName === 'QUESTIONS' ||
              x.variableName === 'COM_SELECT' ||
              x.variableName === 'COM_UPDATE' ||
              x.variableName === 'COM_INSERT' ||
              x.variableName === 'COM_DELETE' ||
              x.variableName === 'COM_REPLACE' ||
              x.variableName === 'CREATED_TMP_DISK_TABLES' ||
              x.variableName === 'BINLOG_STMT_CACHE_DISK_USE' ||
              x.variableName === 'BINLOG_CACHE_DISK_USE' ||
              x.variableName === 'BINLOG_COMMITS' ||
              x.variableName === 'BINLOG_GROUP_COMMITS' ||
              x.variableName === 'HANDLER_COMMIT' ||
              x.variableName === 'HANDLER_READ_FIRST' ||
              x.variableName === 'HANDLER_READ_KEY' ||
              x.variableName === 'HANDLER_READ_NEXT' ||
              x.variableName === 'HANDLER_READ_PREV' ||
              x.variableName === 'HANDLER_READ_RND' ||
              x.variableName === 'HANDLER_READ_RND_NEXT' ||
              x.variableName === 'HANDLER_UPDATE' ||
              x.variableName === 'HANDLER_DELETE' ||
              x.variableName === 'HANDLER_WRITE'
          )

          for (let variable of updatedValueArray) {
            addMetric(variable, 'QUESTIONS', 'qps', graphArr, 'common')
            addMetric(variable, 'COM_SELECT', 'select', graphArr, 'general')
            addMetric(variable, 'COM_UPDATE', 'update', graphArr, 'general')
            addMetric(variable, 'COM_INSERT', 'insert', graphArr, 'general')
            addMetric(variable, 'COM_DELETE', 'delete', graphArr, 'general')
            addMetric(variable, 'COM_REPLACE', 'replace', graphArr, 'general')
            addMetric(variable, 'CREATED_TMP_DISK_TABLES', 'created tmp disk tables', graphArr, 'disk')
            addMetric(variable, 'BINLOG_STMT_CACHE_DISK_USE', 'binlog stmt cache disk use', graphArr, 'disk')
            addMetric(variable, 'BINLOG_CACHE_DISK_USE', 'binlog cache disk use', graphArr, 'disk')
            addMetric(variable, 'BINLOG_COMMITS', 'binlog commits', graphArr, 'commit')
            addMetric(variable, 'BINLOG_GROUP_COMMITS', 'binlog group commits', graphArr, 'commit')
            addMetric(variable, 'HANDLER_COMMIT', 'handler commit', graphArr, 'commit')
            addMetric(variable, 'HANDLER_WRITE', 'write', graphArr, 'row')
            addMetric(variable, 'HANDLER_DELETE', 'delete', graphArr, 'row')
            addMetric(variable, 'HANDLER_UPDATE', 'update', graphArr, 'row')
            if (
              variable.variableName === 'HANDLER_READ_FIRST' ||
              variable.variableName === 'HANDLER_READ_KEY' ||
              variable.variableName === 'HANDLER_READ_NEXT' ||
              variable.variableName === 'HANDLER_READ_PREV' ||
              variable.variableName === 'HANDLER_READ_RND' ||
              variable.variableName === 'HANDLER_READ_RND_NEXT'
            ) {
              read += parseInt(variable.value)
            }
          }
          graphArr.push({ variableName: 'HANDLER_WRITE', name: 'read', value: read, groupName: 'row' })
          graph[key] = graphArr
        }
      }
      setGraphData(graph)
    }
  }, [statusDelta])

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
    if (topProcess) {
      for (let [key, value] of Object.entries(topProcess)) {
        const graphArr = graphData[key]
        const qpsGraph = graphArr?.length > 0 ? [graphArr[0]] : []
        const generalGraph = graphArr?.filter((x) => x.groupName === 'general')
        const commitGraph = graphArr?.filter((x) => x.groupName === 'commit')
        const diskGraph = graphArr?.filter((x) => x.groupName === 'disk')
        const rowGraph = graphArr?.filter((x) => x.groupName === 'row')

        tables.push(
          <AccordionComponent
            className={styles.accordion}
            heading={
              <HStack>
                <Text> {value[0].url}</Text>
                <Link className={styles.morelink} to={`/clusters/${selectedCluster?.name}/${key}`}>
                  show more
                </Link>
              </HStack>
            }
            body={
              <>
                <Flex wrap='wrap' justifyContent='space-evenly'>
                  {generalGraph?.length > 0 && (
                    <BarGraph data={[...qpsGraph, ...generalGraph]} className={styles.graph} />
                  )}
                  {commitGraph?.length > 0 && (
                    <BarGraph data={[...qpsGraph, ...commitGraph]} className={styles.graph} />
                  )}
                  {diskGraph?.length > 0 && <BarGraph data={[...qpsGraph, ...diskGraph]} className={styles.graph} />}
                  {rowGraph?.length > 0 && <BarGraph data={[...qpsGraph, ...rowGraph]} className={styles.graph} />}
                </Flex>
                <DataTable data={value} columns={columns} className={styles.table} />
              </>
            }
          />
        )
      }
    }
    return tables
  }

  return (
    <VStack className={styles.topContainer}>
      {selectedCluster?.workLoad && (
        <AccordionComponent
          className={styles.accordion}
          heading={'Cluster Workload'}
          body={<ClusterWorkload workload={selectedCluster?.workLoad} />}
        />
      )}

      {renderTables()}
    </VStack>
  )
}

export default Top
