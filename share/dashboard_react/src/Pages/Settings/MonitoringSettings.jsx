import { Flex, Text } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'

function MonitoringSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()

  const {
    settings: {
      monSaveConfigLoading,
      monPauseLoading,
      monCaptureLoading,
      monSchemaChangeLoading,
      monInnoDBLoading,
      monVarDiffLoading,
      monProcessListLoading,
      captureTriggerLoading,
      monIgnoreErrLoading
    }
  } = useSelector((state) => state)

  const dataObject = [
    {
      key: 'Monitoring Save Config',
      value: [
        {
          key: 'Monitoring Save Config',
          value: (
            <RMSwitch
              confirmTitle={'Confirm switch settings for monitoring-save-config?'}
              onChange={() =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-save-config' }))
              }
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={selectedCluster?.config?.monitoringSaveConfig}
              loading={monSaveConfigLoading}
            />
          )
        },
        {
          key: 'Monitoring Pause',
          value: (
            <RMSwitch
              confirmTitle={'Confirm switch settings for monitoring-pause?'}
              onChange={() =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-pause' }))
              }
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={selectedCluster?.config?.monitoringPause}
              loading={monPauseLoading}
            />
          )
        }
      ]
    },
    {
      key: 'Capture',
      value: (
        <Flex className={styles.valueWithInfo}>
          <Text className={styles.info}>
            Stack trace contain show processlist, engine status, slave and master status for
          </Text>
          <RMSwitch
            confirmTitle={'Confirm switch settings for monitoring-capture?'}
            onChange={() =>
              dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-capture' }))
            }
            isDisabled={user?.grants['cluster-settings'] == false}
            isChecked={selectedCluster?.config?.monitoringCapture}
            loading={monCaptureLoading}
          />
        </Flex>
      )
    },
    {
      key: 'Capture Trigger',
      value: (
        <TextForm
          value={selectedCluster?.config?.monitoringCaptureTrigger}
          confirmTitle={`Confirm change 'monitoring-capture-trigger' to `}
          onSave={(captureTriggerValue) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'monitoring-capture-trigger',
                value: captureTriggerValue.length === 0 ? '{undefined}' : captureTriggerValue
              })
            )
          }
        />
      )
    },
    {
      key: 'Monitoring Ignore Error List',
      value: (
        <TextForm
          value={selectedCluster?.config?.monitoringIgnoreErrors}
          confirmTitle={`Confirm change 'monitoring-ignore-errors' to: `}
          onSave={(errorListValue) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'monitşring-ignore-errors',
                value: errorListValue.length === 0 ? '{undefined}' : errorListValue
              })
            )
          }
        />
      )
    },
    {
      key: 'Monitoring Schema',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-schema-change?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-schema-change' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringSchemaChange}
          loading={monSchemaChangeLoading}
        />
      )
    },
    {
      key: 'Monitoring InnoDB Status',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-innodb-status?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-innodb-status' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringInnoDBStatus}
          loading={monInnoDBLoading}
        />
      )
    },
    {
      key: 'Monitoring Variable Diff',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-variable-diff?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-variable-diff' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringVariableDiff}
          loading={monVarDiffLoading}
        />
      )
    },
    {
      key: 'Monitoring Processlist',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-processlist?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-processlist' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringProcesslist}
          loading={monProcessListLoading}
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

export default MonitoringSettings
