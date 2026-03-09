import { Flex, Text } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'
import NumberInput from '../../components/NumberInput'

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
      monProcessListLoadingInactive,
      monProcessListLoadingTransactions ,
      monProcessListLoadingInformationSchema ,
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
                setting: 'monitoring-ignore-errors',
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
      key: 'Monitoring Schema Columns',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-schema-columns?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-schema-columns' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringSchemaColumns}
          loading={monSchemaChangeLoading}
        />
      )
    },
     {
      key: 'Monitoring Schema Indexes',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-schema-indexes?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-schema-indexes' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringSchemaIndexes}
          loading={monSchemaChangeLoading}
        />
      )
    },
    {
      key: 'Monitoring Schema Ignore Tables',
      value: (
        <TextForm
          value={selectedCluster?.config?.monitoringSchemaIgnoreTables}
          confirmTitle={`Confirm change 'monitoring-schema-ignore-tables' to: `}
          onSave={(errorListValue) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'monitoring-schema-ignore-tables',
                value: errorListValue.length === 0 ? '&nbsp;' : errorListValue
              })
            )
          }
        />
      )
    },
    {
      key: 'Monitoring Schema Scan Timeout',
      value: (
        <Flex className={styles.valueWithInfo}>
          <Text className={styles.info}>
            Timeout in seconds for schema metadata scans (TABLES, COLUMNS, STATISTICS queries)
          </Text>
          <NumberInput
            value={selectedCluster?.config?.monitoringSchemaScanTimeout}
            showEditButton={true}
            showConfirmModal={true}
            confirmTitle={`Confirm change 'monitoring-schema-scan-timeout' to: `}
            onConfirm={(timeoutValue) =>
              dispatch(
                setSetting({
                  clusterName: selectedCluster?.name,
                  setting: 'monitoring-schema-scan-timeout',
                  value: timeoutValue.length === 0 ? '30' : timeoutValue
                })
              )
            }
          />
        </Flex>
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
    },
    {
      key: 'Monitoring Processlist Information Schema',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-processlist-information-schema?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-processlist-information-schema' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringProcesslistInformationSchema}
          loading={monProcessListLoading}
        />
      )
    },
    {
      key: 'Monitoring Processlist Inactive',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-processlist-inactive?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-processlist-inactive' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringProcesslistInactive}
          loading={monProcessListLoading}
        />
      )
    },
    {
      key: 'Monitoring Processlist Transactions',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-processlist-transactions?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-processlist-transactions' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringProcesslistTransactions}
          loading={monProcessListLoading}
        />
      )
    },
    {
      key: 'Monitoring Performance Schema Memory',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-performance-schema-memory?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-performance-schema-memory' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringPerformanceSchemaMemory}
          loading={monInnoDBLoading}
        />
      )
    },
    {
      key: 'Monitoring Performance Schema Instruments',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-performance-schema-instruments?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-performance-schema-instruments' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringPerformanceIntruments}
          loading={monInnoDBLoading}
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
      key: 'Monitoring InnoDB Mutex',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-performance-schema-mutex?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-performance-schema-mutex' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringPerformanceSchemaMutex}
          loading={monProcessListLoading}
        />
      )
    },
    {
      key: 'Monitoring InnoDB Latch',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-performance-schema-latch?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-performance-schema-latch' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringPerformanceSchemaLatch}
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
