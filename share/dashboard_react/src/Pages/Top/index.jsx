import { Flex, HStack, Text, Tooltip, VStack } from '@chakra-ui/react'
import React, { useEffect, useMemo, useState } from 'react'
import AccordionComponent from '../../components/AccordionComponent'
import ClusterWorkload from '../Dashboard/components/ClusterWorkload'
import styles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../../components/DataTable'
import { convertObjectToArrayForDropdown, getColorFromServerStatus, getReadableTime } from '../../utility/common'
import { Link } from 'react-router-dom'
import { getOpenSVCStats, getTopProcess } from '../../redux/clusterSlice'
import BarGraph from '../../components/BarGraph'
import Dropdown from '../../components/Dropdown'
import RunTests from '../Dashboard/components/RunTests'
import ServerStatus from '../../components/ServerStatus'
import ShowMoreText from '../../components/ShowMoreText'
import OpenSVCWorkload from '../Dashboard/components/OpenSVCWorkload/OpenSVCWorkload'

function Top({ selectedCluster }) {
  const dispatch = useDispatch()
  const [topProcessData, setTopProcessData] = useState([])
  const [numberOfRows, setNumberOfRows] = useState(convertObjectToArrayForDropdown([10, 15, 30, 40, 50]))
  const [selectedNumberOfRows, setSelectedNumberOfRows] = useState({ name: 10, value: 10 })
  const topProcess = useSelector((state) => state.cluster.topProcess)
  const clusterServers = useSelector((state) => state.cluster.clusterServers)
  const opensvcStats = useSelector((state) => state.cluster.opensvcStats)

  useEffect(() => {
    dispatch(getTopProcess({ clusterName: selectedCluster?.name }))
    dispatch(getOpenSVCStats({ clusterName: selectedCluster?.name }))
  }, [])

  useEffect(() => {
    if (topProcess?.length > 0) {
      const processes = topProcess.filter((process) => {
        const dbServer = clusterServers?.find((server) => server.id === process.id)
        return dbServer?.state?.toLowerCase() !== 'failed'
      })

      const updatedProcesses = processes.map((process) => {
        // Create a shallow copy of the current process object
        const processCopy = { ...process }

        // Create a shallow copy of the processlist array if it exists
        processCopy.processlist = process.processlist ? [...process.processlist] : []

        const emptyDataLength = selectedNumberOfRows.value - processCopy.processlist.length
        if (emptyDataLength > 0) {
          // Generate empty data to fill up the processlist
          const emptyData = Array(emptyDataLength).fill({
            id: '',
            user: '',
            host: '',
            db: { String: '' },
            command: '',
            time: { Float64: '' },
            timeMs: { Float64: '' },
            state: { String: '' },
            info: { String: '' },
            progress: { Float64: '' },
            rowsSent: '',
            rowsExamined: '',
            url: '',
            trxTime: 0,
            trxIsolationLevel: { String: '' },
            txrTablesInUse: 0,
            trxTablesLocked: 0,
            trxLockStructs: 0,
            trxLockMemoryBytes: 0,
            trxRowsModified: 0,
            trxRowsLocked: 0,
            trxIsReadOnly: 0
          })

          // Append the empty data to processlist
          processCopy.processlist = [...processCopy.processlist, ...emptyData]
        }

        return processCopy
      })

      setTopProcessData(updatedProcesses)
    }
  }, [topProcess, clusterServers, selectedNumberOfRows])

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
        maxWidth: '400px',
        cell: (info) => <ShowMoreText text={info.getValue()} />,
        enableSorting: false
      }),
      columnHelper.accessor((row) => row.trxTime, {
        header: 'Trx Time',
        maxWidth: '50px',
        cell: (info) => (
          <Tooltip label={getReadableTime(info.getValue())}>
            <span>{info.getValue()}</span>
          </Tooltip>
        ),
        enableSorting: true
      }),
      columnHelper.accessor((row) => row.trxIsolationLevel.String, {
        header: 'Isolation',
        maxWidth: '200px'
      })
    ],
    []
  )

  return (
    <VStack className={styles.topContainer}>
      <AccordionComponent
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        heading={'Tests'}
        body={<RunTests selectedCluster={selectedCluster} />}
      />
      {selectedCluster?.config?.provOrchestrator == "opensvc" && opensvcStats && (
        <AccordionComponent
          className={styles.accordion}
          heading={'OpenSVC Workload'}
          body={<OpenSVCWorkload workload={opensvcStats} />}
        />
      )}
      {selectedCluster?.workLoad && (
        <AccordionComponent
          className={styles.accordion}
          heading={'Cluster Workload'}
          body={<ClusterWorkload workload={selectedCluster?.workLoad} />}
        />
      )}
      <Dropdown
        label={'Select number of rows'}
        options={numberOfRows}
        selectedValue={selectedNumberOfRows.value}
        classNameFormContainer={styles.dropdownRows}
        onChange={(value) => setSelectedNumberOfRows(value)}
      />
      {topProcessData?.length > 0 &&
        topProcessData.map((topP) => {
          const dbServer = clusterServers?.find((server) => server.id === topP.id)
          const serverStatus = dbServer?.state || ''
          const color = getColorFromServerStatus(serverStatus)
          return serverStatus.toLowerCase() !== 'failed' ? (
            <AccordionComponent
              headerClassName={`${styles.accordionHeader} ${styles[color]}`}
              panelClassName={`${styles.accordionBody} ${styles[color]}`}
              className={styles.accordion}
              heading={
                <HStack>
                  <Text> {topP.url}</Text>
                  <ServerStatus state={serverStatus} isVirtualMaster={dbServer?.isVirtualMaster} isBlinking={true} />
                  <Link className={styles.morelink} to={`/clusters/${selectedCluster?.name}/${topP.id}`}>
                    show more
                  </Link>
                </HStack>
              }
              body={
                <>
                  <Flex wrap='wrap' justifyContent='space-evenly'>
                    {topP.header?.graphs?.length > 0 &&
                      topP.header.graphs.map((graph) => {
                        const graphData = graph.data.map((g) => ({
                          ...g,
                          name: g.name.replace(' ', '')
                        }))
                        return <BarGraph data={graphData} graphName={graph.name} className={styles.graph} />
                      })}
                  </Flex>
                  <DataTable key="top" data={topP.processlist} columns={columns} className={styles.table} />
                </>
              }
            />
          ) : null
        })}
    </VStack>
  )
}

export default Top
