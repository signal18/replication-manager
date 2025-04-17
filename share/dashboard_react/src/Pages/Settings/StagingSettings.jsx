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
import { refreshStaging, reloadStagingScript } from '../../redux/clusterSlice'

function StagingSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()

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
          <TextForm
            value={selectedCluster?.config?.replicationMultisourceHeadClusters}
            confirmTitle={`Confirm staging replication-multisource-head-clusters to `}
            onSave={(value) => {
              dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'replication-multisource-head-clusters', value }))
            }}
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
        key: 'Reseed Staging From Parent',
        value: (
          <RMIconButton icon={TbDatabaseImport} onClick={() => {
            openConfirmModal(`Confirm reseed staging from parent? All cluster will be overwritten!`, () => () => {
              dispatch(
                reloadStagingScript({ clusterName: selectedCluster?.name })
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
