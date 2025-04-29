import { Flex, HStack } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'
import RMSwitch from '../../components/RMSwitch'
import RMIconButton from '../../components/RMIconButton'
import { TbDatabaseExport, TbDatabaseImport, TbReload } from 'react-icons/tb'
import { refreshStaging, reseedStagingFromParent } from '../../redux/clusterSlice'
import Dropdown from '../../components/Dropdown'

function StagingSettings({ selectedCluster, user, openConfirmModal, monitor }) {
  const dispatch = useDispatch()
  const [clusters, setClusters] = useState([])

  useEffect(() => {
    if (monitor?.clusters) {
      setClusters(monitor?.clusters?.filter((cl) => cl != selectedCluster.name).map((cluster) => ({ name: cluster, value: cluster })))
    }
  },[monitor?.clusters?.join(',')])

  const dataObject = [
    {
      key: 'Topology staging',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for topology-staging?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'topology-staging' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.topologyStaging}
        />
      )
    },
    {
      key: 'Staging refresh Script',
      value: (
        <TextForm
          value={selectedCluster?.config?.topologyStagingRefreshScript}
          confirmTitle={`Confirm staging refresh script to `}
          onSave={(value) => {
            dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'topology-staging-refresh-script', value }))
          }}
        />
      )
    },
    {
      key: 'Staging post-detach script',
      value: (
        <TextForm
          value={selectedCluster?.config?.topologyStagingPostDetachScript}
          confirmTitle={`Confirm staging post-detach script to `}
          onSave={(value) => {
            dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'topology-staging-post-detach-script', value }))
          }}
        />
      )
    },
    ...(selectedCluster?.config?.topologyStaging ? [
      {
        key: 'Staging multisource head cluster',
        value: (
          <Dropdown
                id='replication-multisource-head-clusters'
                confirmTitle={`Confirm staging replication-multisource-head-clusters to `}
                onChange={(value) => {
                  dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'replication-multisource-head-clusters', value }))
                }}
                selectedValue={selectedCluster?.config?.replicationMultisourceHeadClusters}
                options={clusters}
                className={styles.fullWidth}
              />
        )
      },
      {
        key: 'Refresh Staging',
        value: (
          <RMIconButton icon={TbDatabaseExport} onClick={() => {
            openConfirmModal(`Confirm refresh-staging? This action can not be undone!`, () => () => {
              dispatch(
                refreshStaging({ clusterName: selectedCluster?.name })
              )
            })
          }} />
        )
      },
      {
        key: 'Bootstrap Staging From Parent',
        value: (
          <RMIconButton icon={TbDatabaseImport} onClick={() => {
            openConfirmModal(`Confirm bootstrap staging from parent? Data will be overwritten by parent cluster!`, () => () => {
              dispatch(
                reseedStagingFromParent({ clusterName: selectedCluster?.name })
              )
            })
          }} />
        )
      },
    ] : []),
  ]
  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
}

export default StagingSettings
