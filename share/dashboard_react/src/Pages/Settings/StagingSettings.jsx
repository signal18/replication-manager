import { Flex, HStack } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'
import RMSwitch from '../../components/RMSwitch'

function StagingSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()

  const dataObject = [
    {
      key: 'Topology staging',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for cloud18-shared?'}
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
            dispatch(setSetting({ clusterName: selectedCluster?.name , setting: 'topology-staging-refresh-script', value }))
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
            dispatch(setSetting({ clusterName: selectedCluster?.name , setting: 'topology-staging-post-detach-script', value }))
          }}
        />
      )
    },
  ]
  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
}

export default StagingSettings
