import { Flex, Spinner } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'

import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import RMSlider from '../../components/Sliders/RMSlider'
import Dropdown from '../../components/Dropdown'
import { convertObjectToArray, formatBytes } from '../../utility/common'
import TextForm from '../../components/TextForm'

function BackupSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()
  const [logicalBackupOptions, setLogicalBackupOptions] = useState([])
  const [physicalBackupOptions, setPhysicalBackupOptions] = useState([])
  const [binlogBackupOptions, setBinlogBackupOptions] = useState([])
  const [binlogParseOptions, setBinlogParseOptions] = useState([])
  const [sizeOptions, setSizeOptions] = useState(
    [1024, 2048, 4096, 8192, 16384, 32768, 65536, 1048576].map((size) => {
      return { name: formatBytes(size, 0), value: size }
    })
  )
  const [selectedBinlogBackupType, setselectedBinlogBackupType] = useState('')

  const {
    cluster: { monitor }
  } = useSelector((state) => state)

  useEffect(() => {
    if (selectedCluster?.config?.binlogCopyMode) {
      setselectedBinlogBackupType(selectedCluster.config.binlogCopyMode)
    }
  }, [selectedCluster?.config?.binlogCopyMode])

  useEffect(() => {
    if (monitor?.backupBinlogList) {
      setBinlogBackupOptions(convertObjectToArray(monitor.backupBinlogList))
    }
    if (monitor?.backupLogicalList) {
      setLogicalBackupOptions(convertObjectToArray(monitor.backupLogicalList))
    }
    if (monitor?.backupPhysicalList) {
      setPhysicalBackupOptions(convertObjectToArray(monitor.backupPhysicalList))
    }
    if (monitor?.binlogParseList) {
      setBinlogParseOptions(convertObjectToArray(monitor.binlogParseList))
    }
  }, [monitor?.backupBinlogList, monitor?.backupLogicalList, monitor?.backupPhysicalList, monitor?.binlogParseList])

  const dataObject = [
    {
      key: 'Database Backups',
      value: [
        {
          key: 'Logical Backup',
          value: (
            <Flex className={styles.dropdownContainer}>
              <Dropdown
                options={logicalBackupOptions}
                className={styles.dropdownButton}
                selectedValue={selectedCluster?.config?.backupLogicalType}
                confirmTitle={`Confirm logical backup to`}
                onChange={(backupType) => {
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'backup-logical-type',
                      value: backupType
                    })
                  )
                }}
              />
            </Flex>
          )
        },
        {
          key: 'Physical Backup',
          value: (
            <Flex className={styles.dropdownContainer}>
              <Dropdown
                options={physicalBackupOptions}
                className={styles.dropdownButton}
                selectedValue={selectedCluster?.config?.backupPhysicalType}
                confirmTitle={`Confirm physical backup to`}
                onChange={(backupType) =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'backup-physical-type',
                      value: backupType
                    })
                  )
                }
              />
            </Flex>
          )
        },
        {
          key: 'Binlog Backup',
          value: (
            <Flex
              className={`${styles.dropdownContainer} ${styles.dropdownContainerColumn}`}
              direction='column'
              align='flex-start'>
              <Dropdown
                options={binlogBackupOptions}
                className={styles.dropdownButton}
                selectedValue={selectedCluster?.config?.binlogCopyMode}
                confirmTitle={`Confirm Binlog backup to`}
                onChange={(backupType) => {
                  setselectedBinlogBackupType(backupType)
                  if (backupType !== 'script') {
                    dispatch(
                      setSetting({
                        clusterName: selectedCluster?.name,
                        setting: 'backup-binlog-type',
                        value: backupType
                      })
                    )
                  }
                }}
              />
              {selectedBinlogBackupType === 'script' && (
                <TextForm
                  label={'Backup Binlog Script Path'}
                  direction='column'
                  className={styles.scriptTextContainer}
                  originalValue={selectedCluster?.config?.binlogCopyScript}
                  onConfirm={(scriptValue) =>
                    openConfirmModal(`Confirm Binlog backup to script with value ${scriptValue} `, () => () => {
                      dispatch(
                        setSetting({
                          clusterName: selectedCluster?.name,
                          setting: 'backup-binlog-script',
                          value: scriptValue
                        })
                      )
                      dispatch(
                        setSetting({
                          clusterName: selectedCluster?.name,
                          setting: 'backup-binlog-type',
                          value: 'script'
                        })
                      )
                    })
                  }
                />
              )}
            </Flex>
          )
        }
      ]
    },
    {
      key: 'Use Compression For Backup',
      value: [
        {
          key: 'Use Compression',
          value: (
            <RMSwitch
              isChecked={selectedCluster?.config?.compressBackups}
              isDisabled={user?.grants['cluster-settings'] == false}
              confirmTitle={'Confirm switch settings for compress-backups?'}
              onChange={() =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'compress-backups' }))
              }
            />
          )
        },
        {
          key: 'Backup Buffer Size',
          value: (
            <Dropdown
              options={sizeOptions}
              selectedValue={selectedCluster?.config?.sstSendBuffer}
              confirmTitle={`Confirm change 'sst-send-buffer' to `}
              onChange={(size) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'sst-send-buffer',
                    value: size
                  })
                )
              }
            />
          )
        }
      ]
    },
    {
      key: 'Backup Binlogs',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.autorejoinBackupBinlog}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for backup-binlogs?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-binlogs' }))}
        />
      )
    },
    {
      key: 'Backup Binlogs Keep files',
      value: (
        <RMSlider
          value={selectedCluster?.config?.backupBinlogsKeep}
          confirmTitle='Confirm change keep binlogs files to: '
          onChange={(val) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'backup-binlogs-keep',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Binlog Parse Mode',
      value: (
        <Flex className={styles.dropdownContainer}>
          <Dropdown
            options={binlogParseOptions}
            className={styles.dropdownButton}
            selectedValue={selectedCluster?.config?.binlogParseMode}
            confirmTitle={`Confirm binlog parse mode to`}
            onChange={(mode) =>
              dispatch(
                setSetting({
                  clusterName: selectedCluster?.name,
                  setting: 'binlog-parse-mode',
                  value: mode
                })
              )
            }
          />
        </Flex>
      )
    },
    {
      key: 'Enforce Binlog Purge',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.forceBinlogPurge}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for force-binlog-purge?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-binlog-purge' }))
          }
        />
      )
    },
    {
      key: 'Max Binlog Total Size in GB',
      value: (
        <RMSlider
          value={selectedCluster?.config?.forceBinlogPurgeTotalSize}
          max={256}
          showMarkAtInterval={64}
          confirmTitle='Confirm change force-binlog-purge-total-size to: '
          onChange={(val) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-binlog-purge-total-size',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Minimum Replica(s) needed for purging',
      value: (
        <RMSlider
          value={selectedCluster?.config?.forceBinlogPurgeMinReplica}
          max={12}
          confirmTitle='Confirm change force-binlog-purge-min-replica to: '
          onChange={(val) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'force-binlog-purge-min-replica',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Enforce Binlog Purge on Restore',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.forceBinlogPurgeOnRestore}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for force-binlog-purge-on-restore?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-binlog-purge-on-restore' }))
          }
        />
      )
    },
    {
      key: 'Enforce Binlog Purge On Replicas',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.forceBinlogPurgeReplicas}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for force-binlog-purge-replicas?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-binlog-purge-replicas' }))
          }
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
  )
}

export default BackupSettings
