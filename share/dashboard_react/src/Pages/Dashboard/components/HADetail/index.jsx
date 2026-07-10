import { useState, useMemo } from 'react'
import Card from '../../../../components/Card'
import { Box, Grid, GridItem, Text } from '@chakra-ui/react'
import TagPill from '../../../../components/TagPill'
import { useDispatch, useSelector } from 'react-redux'
import TableType1 from '../../../../components/TableType1'
import { failOverCluster, switchOverCluster } from '../../../../redux/clusterSlice'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import CrashesModal from '../../../../components/Modals/CrashesModal'
import styles from './styles.module.scss'
import RMIconButton from '../../../../components/RMIconButton'
import { HiCog } from 'react-icons/hi'
import { MdHistory } from 'react-icons/md'

function HADetail({ selectedCluster, user, readOnly = false, onOpenSettings }) {
  const clusterMaster = useSelector((state) => state.cluster.clusterMaster)
  const { switchOverLoading, failOverLoading } = useSelector((state) => state.cluster.loadingStates)
  const isDesktop = useSelector((state) => state.common.isDesktop)
      
  const dispatch = useDispatch()
  const [isModalOpen, setIsModalOpen] = useState(false)
  const [isCrashesOpen, setIsCrashesOpen] = useState(false)
  const crashes = selectedCluster?.dbServersCrashes || []
  const failOverData = useMemo(() => selectedCluster ? [
    { key: 'Checks', value: selectedCluster.monitorSpin },
    { key: 'Failed', value: `${selectedCluster.failoverCounter} / ${selectedCluster?.config?.failoverLimit}` },
    { key: 'Last Time', value: selectedCluster.failoverLastTime }
  ] : [], [selectedCluster])

  const SLAData = useMemo(() => selectedCluster ? [
    { key: 'Master Up', value: `${selectedCluster.uptime}%` },
    { key: 'Slaves Catch Up', value: `${selectedCluster.uptimeFailable}%` },
    { key: 'Slaves Sync', value: `${selectedCluster.uptimeSemisync}%` }
  ] : [], [selectedCluster])

  const openConfirmModal = () => {
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
        {...(clusterMaster?.state && !readOnly && !user?.roles?.['visitor'] && (user?.grants?.['cluster-switchover'] || user?.grants?.['cluster-failover'])
          ? {
              headerAction: 'button',
              isLoading: switchOverLoading || failOverLoading,
              loadingText: 'Processing',
              isButtonBlinking: clusterMaster.state === 'Failed',
              buttonColorScheme: clusterMaster.state === 'Failed' ? 'red' : '',
              buttonText: clusterMaster.state === 'Failed' ? 'Failover' : 'Switchover'
            }
          : {})}
        extraHeaderActions={
          <>
            <RMIconButton
              icon={MdHistory}
              tooltip={crashes.length > 0 ? `Crashes / last divergence (${crashes.length})` : 'Crashes / last divergence'}
              onClick={() => setIsCrashesOpen(true)}
              size='xs'
              variant='ghost'
              colorScheme={crashes.some((c) => c.deltaAnalyzed && !c.deltaFlashable) ? 'red' : crashes.length > 0 ? 'purple' : 'gray'}
            />
            {onOpenSettings && <RMIconButton icon={HiCog} tooltip='Failover Settings' onClick={onOpenSettings} size='xs' variant='ghost' />}
          </>
        }
      />
      {isModalOpen && (
        <ConfirmModal
          title={'Confirm switchover?'}
          closeModal={closeModal}
          isOpen={isModalOpen}
          onConfirmClick={handleConfirm}
        />
      )}
      {isCrashesOpen && (
        <CrashesModal
          isOpen={isCrashesOpen}
          closeModal={() => setIsCrashesOpen(false)}
          clusterName={selectedCluster?.name}
          crashes={crashes}
        />
      )}
    </>
  )
}

export default HADetail
