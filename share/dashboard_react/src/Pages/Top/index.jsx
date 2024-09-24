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
import { getTopProcess } from '../../redux/clusterSlice'
import BarGraph from '../../components/BarGraph'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import CopyToClipboard from '../../components/CopyToClipboard'
import Dropdown from '../../components/Dropdown'
import RunTests from '../Dashboard/components/RunTests'
import ServerStatus from '../../components/ServerStatus'
import ShowMoreText from '../../components/ShowMoreText'

function Top({ selectedCluster }) {
  const dispatch = useDispatch()
  const [isModalOpen, setIsModalOpen] = useState(false)
  const [fullInfoValue, setFullInfoValue] = useState('')
  const [topProcessData, setTopProcessData] = useState([])
  const [numberOfRows, setNumberOfRows] = useState(convertObjectToArrayForDropdown([10, 15, 30, 40, 50]))
  const [selectedNumberOfRows, setSelectedNumberOfRows] = useState({ name: 10, value: 10 })
  const {
    cluster: { topProcess, clusterServers }
  } = useSelector((state) => state)

  useEffect(() => {
    dispatch(getTopProcess({ clusterName: selectedCluster?.name }))
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
            url: ''
          })

          // Append the empty data to processlist
          processCopy.processlist = [...processCopy.processlist, ...emptyData]
        }

        return processCopy
      })

      setTopProcessData(updatedProcesses)
    }
  }, [topProcess, clusterServers, selectedNumberOfRows])

  const openModal = (fullValue) => {
    setIsModalOpen(true)
    setFullInfoValue(fullValue)
  }

  const closeModal = () => {
    setIsModalOpen(false)
    setFullInfoValue('')
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
      })
    ],
    []
  )

  return (
    <VStack className={styles.topContainer}>
      <AccordionComponent
        className={styles.accordion}
        heading={'Tests'}
        body={<RunTests selectedCluster={selectedCluster} />}
      />
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
                  <DataTable data={topP.processlist} columns={columns} className={styles.table} />
                </>
              }
            />
          ) : null
        })}
      {isModalOpen && (
        <ConfirmModal
          isOpen={isModalOpen}
          closeModal={closeModal}
          title='Info'
          body={<CopyToClipboard text={fullInfoValue} className={styles.modalbodyText} keepOpen={true} />}
          showCancelButton={false}
          showConfirmButton={false}
        />
      )}
    </VStack>
  )
}

export default Top
