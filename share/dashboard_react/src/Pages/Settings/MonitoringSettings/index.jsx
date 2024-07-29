import { Grid } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../../components/RMSwitch'
import GridItemContainer from '../GridItemContainer'
import Dropdown from '../../../components/Dropdown'
import { convertObjectToArray } from '../../../utility/common'
import { useDispatch } from 'react-redux'
import { changeTopology, switchSetting } from '../../../redux/clusterSlice'

function MonitoringSettings({ selectedCluster, user, openConfirmModal }) {
  const [topologyOptions, setTopologyOptions] = useState([])
  const dispatch = useDispatch()

  useEffect(() => {
    if (selectedCluster?.topologyType) {
      setTopologyOptions(convertObjectToArray(selectedCluster.topologyType))
    }
  }, [selectedCluster?.topologyType])

  return (
    <Grid className={styles.grid} templateColumns='repeat(2, 1fr)'>
      <GridItemContainer title='Monitoring Save Config'>
        {/* <RMSwitch
          onText='On-call (manual)'
          offText='On-leave (auto)'
          onChange={() =>
            openConfirmModal(
              'Confirm switch settings for failover-mode?',
              () => () => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'failover-mode' }))
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.interactive}
        /> */}
      </GridItemContainer>
      <GridItemContainer title='Capture'>
        {/* <Dropdown
          options={topologyOptions}
          width='50%'
          selectedValue={selectedCluster?.config?.topologyTarget}
          askConfirmation={true}
          onChange={(topology) => {
            openConfirmModal(`This will set preferred topology to ${topology.value}. Confirm?`, () => () => {
              dispatch(changeTopology({ clusterName: selectedCluster?.name, topology: topology.value }))
            })
          }}
        /> */}
      </GridItemContainer>
      <GridItemContainer title='Capture Trigger'>
        {/* <RMSwitch
          isChecked={selectedCluster?.config?.replicationMultiMasterRingUnsafe}
          isDisabled={user?.grants['cluster-settings'] == false}
          onChange={() =>
            openConfirmModal(
              'Confirm switch settings for multi-master-ring-unsafe?',
              () => () =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'multi-master-ring-unsafe' }))
            )
          }
        /> */}
      </GridItemContainer>
      <GridItemContainer title='Monitoring Schema'>
        {/* <RMSwitch
          isChecked={!selectedCluster?.config?.replicationMasterSlaveNeverRelay}
          isDisabled={user?.grants['cluster-settings'] == false}
          onChange={() =>
            openConfirmModal(
              'Confirm switch settings for replication-no-relay?',
              () => () =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'replication-no-relay' }))
            )
          }
        /> */}
      </GridItemContainer>
      <GridItemContainer title='Monitoring InnoDB Status'>
        {/* <RMSwitch
          isChecked={selectedCluster?.config?.test}
          isDisabled={user?.grants['cluster-settings'] == false}
          onChange={() =>
            openConfirmModal(
              'Confirm switch settings for test?',
              () => () => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'test' }))
            )
          }
        /> */}
      </GridItemContainer>
      <GridItemContainer title='Monitoring Variable Diff'>
        {/* <RMSwitch
          isChecked={selectedCluster?.config?.verbose}
          isDisabled={user?.grants['cluster-settings'] == false}
          onChange={() =>
            openConfirmModal(
              'Confirm switch settings for verbose?',
              () => () => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'verbose' }))
            )
          }
        /> */}
      </GridItemContainer>
      <GridItemContainer title='Monitoring Processlist'>
        {/* <RMSwitch
          isChecked={selectedCluster?.config?.verbose}
          isDisabled={user?.grants['cluster-settings'] == false}
          onChange={() =>
            openConfirmModal(
              'Confirm switch settings for verbose?',
              () => () => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'verbose' }))
            )
          }
        /> */}
      </GridItemContainer>
    </Grid>
  )
}

export default MonitoringSettings
