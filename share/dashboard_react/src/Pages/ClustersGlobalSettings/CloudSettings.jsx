import { Flex } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { switchGlobalSetting } from '../../redux/globalClustersSlice'

function CloudSettings({ monitor }) {
  const dispatch = useDispatch()

  const dataObject = [
    {
      key: 'Cloud18',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch global settings for cloud18?'}
          onChange={() => dispatch(switchGlobalSetting({ setting: 'cloud18' }))}
          isChecked={monitor?.config?.cloud18}
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
