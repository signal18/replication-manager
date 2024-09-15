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
import { getTopProcess } from '../../redux/clusterSlice'
import BarGraph from '../../components/BarGraph'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import CopyToClipboard from '../../components/CopyToClipboard'

function Top({ selectedCluster }) {
  const dispatch = useDispatch()
  const [isModalOpen, setIsModalOpen] = useState(false)
  const [fullInfoValue, setFullInfoValue] = useState('')
  const {
    cluster: { topProcess }
  } = useSelector((state) => state)

  useEffect(() => {
    dispatch(getTopProcess({ clusterName: selectedCluster?.name }))
  }, [])

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
        cell: (info) => {
          const fullString = info.getValue()
          const fullLength = fullString.length
          const slicedValue = fullString.slice(0, 30)

          return (
            <>
              <span>{fullLength > 30 ? `${slicedValue}...` : fullString}</span>
              {fullLength > 30 && (
                <button onClick={() => openModal(fullString)} className={styles.showmore}>
                  more
                </button>
              )}
            </>
          )
        },
        enableSorting: false
      })
    ],
    []
  )

  return (
    <VStack className={styles.topContainer}>
      {selectedCluster?.workLoad && (
        <AccordionComponent
          className={styles.accordion}
          heading={'Cluster Workload'}
          body={<ClusterWorkload workload={selectedCluster?.workLoad} />}
        />
      )}
      {topProcess?.length > 0 &&
        topProcess.map((topP) => {
          return (
            <AccordionComponent
              className={styles.accordion}
              heading={
                <HStack>
                  <Text> {topP.url}</Text>
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
          )
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
