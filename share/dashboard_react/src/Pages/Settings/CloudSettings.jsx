import { Flex } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { switchSetting } from '../../redux/settingsSlice'

function CloudSettings({ selectedCluster, user }) {
  const dispatch = useDispatch()

  const dataObject = [
    {
      key: 'Cloud18',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for cloud18?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'cloud18' }))}
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.cloud18}
        />
      )
    },
    {
      key: 'Share the cluster on the Cloud18',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for cloud18-shared?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'cloud18-shared' }))}
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.cloud18Shared}
        />
      )
    }
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
}

export default CloudSettings
