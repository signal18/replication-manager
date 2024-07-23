import { Flex, Spinner } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import Dropdown from '../../components/Dropdown'
import { convertObjectToArray } from '../../utility/common'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { changeTopology, switchSetting } from '../../redux/settingsSlice'

function GeneralSettings({ selectedCluster, user, openConfirmModal }) {
  const [topologyOptions, setTopologyOptions] = useState([])
  const dispatch = useDispatch()

  const {
    settings: {
      failoverLoading,
      targetTopologyLoading,
      allowUnsafeClusterLoading,
      allowMultitierSlaveLoading,
      testLoading
    }
  } = useSelector((state) => state)

  useEffect(() => {
    if (selectedCluster?.topologyType) {
      setTopologyOptions(convertObjectToArray(selectedCluster.topologyType))
    }
  }, [selectedCluster?.topologyType])

  const dataObject = [
    {
      key: 'Failover Mode (interactive)',
      value: (
        <RMSwitch
          onText='On-call (manual)'
          offText='On-leave (auto)'
          confirmTitle={'Confirm switch settings for failover-mode?'}
          onConfirm={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'failover-mode' }))}
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.interactive}
          loading={failoverLoading}
        />
      )
    },
    {
      key: 'Target Topology',
      value: (
        <Flex align='center' gap='2'>
          <Dropdown
            options={topologyOptions}
            buttonClassName={styles.dropdownButton}
            // width='200px'
            selectedValue={selectedCluster?.config?.topologyTarget}
            askConfirmation={true}
            onChange={(topology) => {
              openConfirmModal(`This will set preferred topology to ${topology.value}. Confirm?`, () => () => {
                dispatch(changeTopology({ clusterName: selectedCluster?.name, topology: topology.value }))
              })
            }}
          />
          {targetTopologyLoading && <Spinner />}
        </Flex>
      )
    },
    {
      key: 'Allow multi-master-ring topology on unsafe cluster',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.replicationMultiMasterRingUnsafe}
          isDisabled={user?.grants['cluster-settings'] == false}
          loading={allowUnsafeClusterLoading}
          confirmTitle={'Confirm switch settings for multi-master-ring-unsafe?'}
          onConfirm={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'multi-master-ring-unsafe' }))
          }
        />
      )
    },
    {
      key: 'Allow Multi-Tier Slave',
      value: (
        <RMSwitch
          isChecked={!selectedCluster?.config?.replicationMasterSlaveNeverRelay}
          isDisabled={user?.grants['cluster-settings'] == false}
          loading={allowMultitierSlaveLoading}
          confirmTitle={'Confirm switch settings for replication-no-relay?'}
          onConfirm={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'replication-no-relay' }))
          }
        />
      )
    },
    {
      key: 'Test Mode',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.test}
          isDisabled={user?.grants['cluster-settings'] == false}
          loading={testLoading}
          confirmTitle={'Confirm switch settings for test?'}
          onConfirm={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'test' }))}
        />
      )
    }
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2
        dataArray={dataObject}
        className={styles.table}
        labelClassName={styles.label}
        valueClassName={styles.value}
        rowDivider={true}
        rowClassName={styles.row}
      />
    </Flex>
    // <Grid className={styles.grid}>
    //   <GridItemContainer title='Failover Mode (interactive)'>
    //     <RMSwitch
    //       onText='On-call (manual)'
    //       offText='On-leave (auto)'
    //       onChange={() =>
    //         openConfirmModal(
    //           'Confirm switch settings for failover-mode?',
    //           () => () => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'failover-mode' }))
    //         )
    //       }
    //       isDisabled={user?.grants['cluster-settings'] == false}
    //       isChecked={selectedCluster?.config?.interactive}
    //     />
    //   </GridItemContainer>
    // </Grid>
  )
}

export default GeneralSettings