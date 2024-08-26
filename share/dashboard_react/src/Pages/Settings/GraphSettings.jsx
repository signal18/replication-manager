import { Flex } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'

import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting, updateGraphiteBlackList, updateGraphiteWhiteList } from '../../redux/settingsSlice'
import Dropdown from '../../components/Dropdown'
import { convertObjectToArray } from '../../utility/common'
import RegexText from './RegexText'

function GraphSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()
  const [graphiteTemplateOptions, setGraphiteTemplateOptions] = useState()
  const {
    cluster: { monitor }
  } = useSelector((state) => state)
  useEffect(() => {
    if (monitor?.graphiteTemplateList) {
      setGraphiteTemplateOptions(convertObjectToArray(monitor.graphiteTemplateList))
    }
  }, [monitor?.graphiteTemplateList])

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
      value: [
        {
          key: 'Reset graphite template',
          value: (
            <Dropdown
              options={graphiteTemplateOptions}
              selectedValue={selectedCluster?.config?.graphiteWhitelistTemplate}
              confirmTitle={`Confirm reset graphite filterlist to `}
              onChange={(value) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'reset-graphite-filterlist',
                    value: value
                  })
                )
              }
            />
          )
        },
        {
          key: 'Graphite Whitelist',
          value: (
            <RegexText
              user={user}
              value={selectedCluster?.Whitelist.join('\n')}
              isSwitchChecked={selectedCluster?.config?.graphiteWhitelist}
              switchConfirmTitle={'Confirm switch settings for graphite-whitelist?'}
              onSwitchChange={() =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'graphite-whitelist' }))
              }
              confirmTitle={'Confirm update graphite whitelist?'}
              onSave={(value) =>
                dispatch(
                  updateGraphiteWhiteList({
                    clusterName: selectedCluster?.name,
                    value: value
                  })
                )
              }
            />
          )
        },
        {
          key: 'Graphite Blacklist',
          value: (
            <RegexText
              user={user}
              value={selectedCluster?.Blacklist.join('\n')}
              isSwitchChecked={selectedCluster?.config?.graphiteBlacklist}
              switchConfirmTitle={'Confirm switch settings for graphite-blacklist?'}
              onSwitchChange={() =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'graphite-blacklist' }))
              }
              confirmTitle={'Confirm update graphite Blacklist?'}
              onSave={(value) =>
                dispatch(
                  updateGraphiteBlackList({
                    clusterName: selectedCluster?.name,
                    value: value
                  })
                )
              }
            />
          )
        }
      ]
    }
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
}

export default GraphSettings
