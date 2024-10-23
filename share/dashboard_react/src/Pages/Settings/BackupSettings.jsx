import { Flex, Spinner } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'

import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import RMSlider from '../../components/Sliders/RMSlider'
import Dropdown from '../../components/Dropdown'
import { convertObjectToArrayForDropdown, formatBytes } from '../../utility/common'
import TextForm from '../../components/TextForm'

function BackupSettings({ selectedCluster, user }) {
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
    globalClusters: { monitor }
  } = useSelector((state) => state)

  useEffect(() => {
    if (selectedCluster?.config?.binlogCopyMode) {
      setselectedBinlogBackupType(selectedCluster.config.binlogCopyMode)
    }
  }, [selectedCluster?.config?.binlogCopyMode])

  useEffect(() => {
    if (monitor?.backupBinlogList) {
      setBinlogBackupOptions(convertObjectToArrayForDropdown(monitor.backupBinlogList))
    }
    if (monitor?.backupLogicalList) {
      setLogicalBackupOptions(convertObjectToArrayForDropdown(monitor.backupLogicalList))
    }
    if (monitor?.backupPhysicalList) {
      setPhysicalBackupOptions(convertObjectToArrayForDropdown(monitor.backupPhysicalList))
    }
    if (monitor?.binlogParseList) {
      setBinlogParseOptions(convertObjectToArrayForDropdown(monitor.binlogParseList))
    }
  }, [monitor?.backupBinlogList, monitor?.backupLogicalList, monitor?.backupPhysicalList, monitor?.binlogParseList])

  const dataObject = [
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
      key: 'Mysqldump options',
      value: (
        <TextForm
          value={selectedCluster?.config?.backupMysqldumpOptions}
          confirmTitle={`Confirm backup-mysqldump-options to `}
          maxLength={1024}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'backup-mysqldump-options',
                value: btoa(value)
              })
            )
          }
        />
      )
    },
    {
      key: 'Mydumper options',
      value: (
        <TextForm
          value={selectedCluster?.config?.backupMyDumperOptions}
          confirmTitle={`Confirm backup-mydumper-options to `}
          maxLength={1024}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'backup-mydumper-options',
                value: btoa(value)
              })
            )
          }
        />
      )
    },
    {
      key: 'Myloader options',
      value: (
        <TextForm
          value={selectedCluster?.config?.backupMyLoaderOptions}
          confirmTitle={`Confirm backup-myloader-options to `}
          maxLength={1024}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'backup-myloader-options',
                value: btoa(value)
              })
            )
          }
        />
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
              value={selectedCluster?.config?.binlogCopyScript}
              confirmTitle='Confirm Binlog backup to script with value '
              onSave={(scriptValue) => {
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
              }}
            />
          )}
        </Flex>
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
      key: 'Use Compression',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.compressBackups}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for compress-backups?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'compress-backups' }))}
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
    },
    {
      key: 'Backup Streaming Endpoint',
      value: (
        <TextForm
          value={selectedCluster?.config?.backupStreamingEndpoint}
          confirmTitle={`Confirm backup-streaming-endpoint to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'backup-streaming-endpoint',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Backup Streaming Region',
      value: (
        <TextForm
          value={selectedCluster?.config?.backupStreamingRegion}
          confirmTitle={`Confirm backup-streaming-region to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'backup-streaming-region',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Backup Streaming Bucket',
      value: (
        <TextForm
          value={selectedCluster?.config?.backupStreamingBucket}
          confirmTitle={`Confirm backup-streaming-bucket to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'backup-streaming-bucket',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Backup snapshots',
      value: [
        {
          key: 'Use Restic For Backup',
          value: (
            <RMSwitch
              isChecked={selectedCluster?.config?.backupRestic}
              isDisabled={user?.grants['cluster-settings'] == false}
              confirmTitle={'Confirm switch settings for backup-restic?'}
              onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-restic' }))}
            />
          )
        },
        ...(selectedCluster?.config?.backupRestic
          ? [
            {
              key: 'Backup restic binary path',
              value: (
                <TextForm
                  value={selectedCluster?.config?.backupResticBinaryPath}
                  confirmTitle={`Confirm backup-restic-binary-path to `}
                  className={styles.textbox}
                  onSave={(value) =>
                    dispatch(
                      setSetting({
                        clusterName: selectedCluster?.name,
                        setting: 'backup-restic-binary-path',
                        value: value
                      })
                    )
                  }
                />
              )
            },
            {
              key: 'Backup restic password',
              value: (
                <TextForm
                  value={selectedCluster?.config?.backupResticPassword}
                  confirmTitle={`Confirm backup-restic-password to `}
                  className={styles.textbox}
                  onSave={(value) =>
                    dispatch(
                      setSetting({
                        clusterName: selectedCluster?.name,
                        setting: 'backup-restic-password',
                        value: btoa(value)
                      })
                    )
                  }
                />
              )
            },
            {
              key: 'Backup restic aws',
              value: (
                <RMSwitch
                  isChecked={selectedCluster?.config?.backupResticAws}
                  isDisabled={user?.grants['cluster-settings'] == false}
                  confirmTitle={'Confirm switch settings for backup-restic-aws?'}
                  onChange={() =>
                    dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-restic-aws' }))
                  }
                />
              )
            },
            ...(selectedCluster?.config?.backupResticAws
              ? [
                {
                  key: 'Backup restic access key id',
                  value: (
                    <TextForm
                      value={selectedCluster?.config?.backupResticAwsAccessKeyId}
                      confirmTitle={`Confirm backup-restic-binary-path to `}
                      className={styles.textbox}
                      onSave={(value) =>
                        dispatch(
                          setSetting({
                            clusterName: selectedCluster?.name,
                            setting: 'backup-restic-aws-access-key-id',
                            value: value
                          })
                        )
                      }
                    />
                  )
                },
                {
                  key: 'Backup restic aws access secret',
                  value: (
                    <TextForm
                      value={selectedCluster?.config?.backupResticAwsAccessSecret}
                      confirmTitle={`Confirm backup-restic-aws-access-secret to `}
                      className={styles.textbox}
                      onSave={(value) =>
                        dispatch(
                          setSetting({
                            clusterName: selectedCluster?.name,
                            setting: 'backup-restic-aws-access-secret',
                            value: btoa(value)
                          })
                        )
                      }
                    />
                  )
                },
                {
                  key: 'Backup restic aws bucket',
                  value: (
                    <TextForm
                      value={selectedCluster?.config?.backupResticRepository}
                      confirmTitle={`Confirm backup-restic-repository to `}
                      className={styles.textbox}
                      onSave={(value) =>
                        dispatch(
                          setSetting({
                            clusterName: selectedCluster?.name,
                            setting: 'backup-restic-repository',
                            value: btoa(value)
                          })
                        )
                      }
                    />
                  )
                }
              ] : [])
          ]
          : [])
      ]
    }
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
}

export default BackupSettings
