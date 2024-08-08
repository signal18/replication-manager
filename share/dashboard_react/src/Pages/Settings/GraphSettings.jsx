import { Flex } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'

import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import RMSlider from '../../components/Sliders/RMSlider'
import Dropdown from '../../components/Dropdown'

function GraphSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()

  const dataObject = [
    {
      key: 'GRAPHITE CONFIG',
      value: [
        {
          key: 'Graphite Metrics',
          value: (
            <RMSwitch
              confirmTitle={'Confirm switch settings for graphite-metrics?'}
              onChange={() =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'graphite-metrics' }))
              }
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={selectedCluster?.config?.graphiteMetrics}
            />
          )
        },
        {
          key: 'Graphite Embedded',
          value: (
            <RMSwitch
              confirmTitle={'Confirm switch settings for graphite-embedded?'}
              onChange={() =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'graphite-embedded' }))
              }
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={selectedCluster?.config?.graphiteEmbedded}
            />
          )
        }
      ]
    },
    {
      key: 'METRICS CONFIGURATION',
      value: ''
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
  )
}

export default GraphSettings
