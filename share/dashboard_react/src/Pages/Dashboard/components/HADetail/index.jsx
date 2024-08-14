import React, { useEffect, useState } from 'react'
import Card from '../../../../components/Card'
import { Box, Grid, GridItem, Text } from '@chakra-ui/react'
import TagPill from '../../../../components/TagPill'
import { useDispatch, useSelector } from 'react-redux'
import TableType1 from '../../../../components/TableType1'
import { failOverCluster, switchOverCluster } from '../../../../redux/clusterSlice'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import styles from './styles.module.scss'

function HADetail({ selectedCluster }) {
  const {
    cluster: {
      clusterMaster,
      loadingStates: { switchOver: switchOverLoading, failOver: failOverLoading }
    },
    common: { isDesktop }
  } = useSelector((state) => state)
  const dispatch = useDispatch()
  const [isModalOpen, setIsModalOpen] = useState(false)
  const [failOverData, setFailOverData] = useState([])
  const [SLAData, setSLAData] = useState([])

  useEffect(() => {
    if (selectedCluster) {
      setFailOverData([
        { key: 'Checks', value: selectedCluster.monitorSpin },
        { key: 'Failed', value: `${selectedCluster.failoverCounter} / ${selectedCluster.config.failoverLimit}` },
        { key: 'Last Time', value: selectedCluster.failoverLastTime }
      ])

      setSLAData([
        { key: 'Master Up', value: `${selectedCluster.uptime}%` },
        { key: 'Slaves Catch', value: `${selectedCluster.uptimeFailable}%` },
        { key: 'Slaves Sync', value: `${selectedCluster.uptimeSemisync}%` }
      ])
    }
  }, [selectedCluster])

  const openConfirmModal = (e) => {
    setIsModalOpen(true)
  }

  const closeModal = () => {
    setIsModalOpen(false)
  }

  const handleConfirm = () => {
    if (selectedCluster) {
      if (clusterMaster?.state === 'Failed') {
        dispatch(failOverCluster({ clusterName: selectedCluster.name }))
      } else if (clusterMaster?.state !== 'Failed') {
        dispatch(switchOverCluster({ clusterName: selectedCluster.name }))
      }
    }
    closeModal()
  }

  return (
    <>
      <Card
        width={isDesktop ? '50%' : '100%'}
        header={
          <>
            <Text>HA</Text>
            <Box ml='auto'>
              <TagPill colorScheme='green' text={selectedCluster.topology} />
            </Box>
          </>
        }
        body={
          <Grid
            {...(isDesktop ? { templateColumns: 'repeat(2, 1fr)' } : { templateRows: 'repeat(2, 1fr)' })}
            columnGap={1}>
            <GridItem>
              <Text className={styles.headerColumn}>Failover</Text>
              <TableType1 dataArray={failOverData} />
            </GridItem>
            <GridItem>
              <Text className={styles.headerColumn}>SLA</Text>
              <TableType1 dataArray={SLAData} />
            </GridItem>
          </Grid>
        }
        onClick={openConfirmModal}
        {...(clusterMaster?.state
          ? {
              headerAction: 'button',
              isLoading: switchOverLoading || failOverLoading,
              loadingText: 'Processing',
              isButtonBlinking: clusterMaster.state === 'Failed',
              buttonColorScheme: clusterMaster.state === 'Failed' ? 'red' : '',
              buttonText: clusterMaster.state === 'Failed' ? 'Failover' : 'Switchover'
            }
          : {})}
      />
      {isModalOpen && (
        <ConfirmModal
          title={'Confirm switchover?'}
          closeModal={closeModal}
          isOpen={isModalOpen}
          onConfirmClick={handleConfirm}
        />
      )}
    </>
  )
}

export default HADetail
