import { Box, Flex, HStack, Input, Spinner, Stack, Text, Textarea } from '@chakra-ui/react'
import React, { useRef, useState, useEffect, useMemo } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'

import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSecretSetting, setSetting, switchSetting } from '../../redux/settingsSlice'
import RMSlider from '../../components/Sliders/RMSlider'
import Dropdown from '../../components/Dropdown'
import { convertObjectToArrayForDropdown, formatBytes } from '../../utility/common'
import TextForm from '../../components/TextForm'
import CommonModal from '../../components/Modals/CommonModal'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import { HiCheck, HiQuestionMarkCircle, HiX } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'
import RMButton from '../../components/RMButton'
import remarkGfm from 'remark-gfm'
import BackupSnapshotsSettings from './BackupSnapshotsSettings'
import NumberInput from '../../components/NumberInput'

const sizeGenerator = () => {
  const result = []
  let i = 1024;
  while (i <= 1024 * 1024 * 1024) {
    result.push(i)
    i = i * 2
  }

  return result.map((size) => {
    return { name: formatBytes(size, 0), value: size }
  })
}

const defaultDecompressBufferSize = 250000

const backupEncryptionDirectoryModeOptions = [
  { name: 'Archive', value: 'archive' },
  { name: 'Per-file', value: 'per-file' }
]

const backupEncryptionDirectoryFormatOptions = [
  { name: 'tar.gz', value: 'tar.gz' },
  { name: 'tar', value: 'tar' }
]

const backupEncryptionSecretModeOptions = [
  { name: 'Single passphrase', value: 'passphrase' },
  { name: 'Rotation keyring', value: 'keyring' }
]

const validateKeyringJson = (rawValue) => {
  if (!rawValue?.trim()) {
    return {
      isValid: false,
      message: 'Keyring JSON is required'
    }
  }

  try {
    const parsed = JSON.parse(rawValue)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {
        isValid: false,
        message: 'Keyring JSON must be a JSON object'
      }
    }

    const activeKeyId = typeof parsed.activeKeyId === 'string' ? parsed.activeKeyId.trim() : ''
    if (!activeKeyId) {
      return {
        isValid: false,
        message: 'activeKeyId is required'
      }
    }

    if (!Array.isArray(parsed.keys) || parsed.keys.length === 0) {
      return {
        isValid: false,
        message: 'keys must be a non-empty array'
      }
    }

    const seen = new Set()
    let activeCount = 0
    let activeMatches = false

    for (const entry of parsed.keys) {
      if (!entry || typeof entry !== 'object' || Array.isArray(entry)) {
        return {
          isValid: false,
          message: 'Each key entry must be an object'
        }
      }

      const id = typeof entry.id === 'string' ? entry.id.trim() : ''
      const passphrase = typeof entry.passphrase === 'string' ? entry.passphrase.trim() : ''
      const state = typeof entry.state === 'string' ? entry.state.trim().toLowerCase() : ''

      if (!id) {
        return {
          isValid: false,
          message: 'Each key entry requires a non-empty id'
        }
      }
      if (seen.has(id)) {
        return {
          isValid: false,
          message: `Duplicate key id: ${id}`
        }
      }
      seen.add(id)

      if (!passphrase) {
        return {
          isValid: false,
          message: `Key \"${id}\" requires a non-empty passphrase`
        }
      }
      if (!['active', 'decrypt-only'].includes(state)) {
        return {
          isValid: false,
          message: `Key \"${id}\" has invalid state. Allowed: active|decrypt-only`
        }
      }
      if (state === 'active') {
        activeCount += 1
        if (id === activeKeyId) {
          activeMatches = true
        }
      }
    }

    if (activeCount !== 1) {
      return {
        isValid: false,
        message: 'Exactly one key must have state=active'
      }
    }
    if (!activeMatches) {
      return {
        isValid: false,
        message: 'activeKeyId must reference the key with state=active'
      }
    }

    if (parsed.legacyDefaultKeyId !== undefined) {
      const legacyId = typeof parsed.legacyDefaultKeyId === 'string' ? parsed.legacyDefaultKeyId.trim() : ''
      if (!legacyId) {
        return {
          isValid: false,
          message: 'legacyDefaultKeyId must be a non-empty string when provided'
        }
      }
      if (!seen.has(legacyId)) {
        return {
          isValid: false,
          message: 'legacyDefaultKeyId must reference a key in keys[]'
        }
      }
    }

    return {
      isValid: true,
      message: ''
    }
  } catch {
    return {
      isValid: false,
      message: 'Invalid JSON format'
    }
  }
}

const buildDecompressBufferOptions = (options) => {
  const updatedOptions = [...options]
  const defaultOption = {
    name: formatBytes(defaultDecompressBufferSize, 0),
    value: defaultDecompressBufferSize
  }

  if (!updatedOptions.some((option) => option.value === defaultOption.value)) {
    updatedOptions.push(defaultOption)
  }

  return updatedOptions.sort((a, b) => a.value - b.value)
}

function BackupSettings({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const joinClasses = (...classes) => classes.filter(Boolean).join(' ')
  const [logicalBackupOptions, setLogicalBackupOptions] = useState([])
  const [physicalBackupOptions, setPhysicalBackupOptions] = useState([])
  const [binlogBackupOptions, setBinlogBackupOptions] = useState([])
  const [binlogParseOptions, setBinlogParseOptions] = useState([])
  const [sizeOptions, setSizeOptions] = useState(sizeGenerator())
  const decompressBufferOptions = useMemo(() => buildDecompressBufferOptions(sizeOptions), [sizeOptions])
  const [selectedBinlogBackupType, setselectedBinlogBackupType] = useState('')
  const [isBackupSnapshotsOpen, setIsBackupSnapshotsOpen] = useState(true)
  const [isResticRepoConfigOpen, setIsResticRepoConfigOpen] = useState(true)
  const [backupEncryptionSecretMode, setBackupEncryptionSecretMode] = useState('passphrase')
  const [backupEncryptionPassphrase, setBackupEncryptionPassphrase] = useState('')
  const [backupEncryptionKeyring, setBackupEncryptionKeyring] = useState('')
  const [isKeyringModalOpen, setIsKeyringModalOpen] = useState(false)
  const [isKeyringConfirmModalOpen, setIsKeyringConfirmModalOpen] = useState(false)
  const backupSnapshotsToggleRef = useRef(null)
  const [action, setAction] = useState({
    title: '',
    type: '',
    body: <></>
  })
  const { title, type } = action
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)

  const BackupSaveScriptRequirement = `Backup save script execute a backup script and will not execute other logical backup tools.  
The script must be able to handle the following parameters:  
1. DB Server Host
2. Master Host
3. DB Server Port
4. Master Port
5. DB User
6. DB Password
7. Cluster Name
`

  const BackupLoadScriptRequirement = `Backup load script will execute a script.  
The script will be executed with the following parameters:  
1. DB Server Host
2. Master Host
3. DB Server Port
4. Master Port
5. DB User
6. DB Password
7. Cluster Name
`

  const BackupPostScriptRequirement = `Post-backup script will execute a script.  
The script will be executed with the following parameters:  
1. Cluster name
2. DB Server Host
3. DB Server Port
4. Backup Path
`

  const SplitDumpRequirement = `Splitdump sends mysqldump output through replication-manager-cli splitdump.  
When enabled, mysqldump output is written to a splitdump directory (uncompressed) instead of a single .sql.gz file.  
Ensure the CLI is available in PATH or set a custom CLI path below.`

  const ReplicationManagerCliRequirement = `Path to replication-manager-cli used for splitdump processing.  
Leave empty to use PATH lookup (replication-manager-cli).`

  const SplitDumpStreamSizeRequirement = `Splitdump shard size limit for mysqldump splitdump output.  
Default: 1G. Select from list; 0 disables sharding.`

  const BackupEncryptionRequirement = `Enable encryption for backup artifacts.  
When enabled, backup encryption controls are shown to configure mode, format, and secret material.`

  const BackupEncryptionExplicitPassphraseRequirement = `Require explicit passphrase usage for backup encryption operations.  
Use this when you want restores or encryption workflows to refuse implicit/default credentials.`

  const BackupEncryptionPassphraseRequirement = `Write-only passphrase entry for backup encryption.  
Current value is never displayed in UI.  
Saving updates the passphrase secret for this cluster.`

  const BackupEncryptionKeyringRequirement = `Write-only JSON keyring editor for backup encryption keys.  
Current keyring value is never displayed in UI.  
Paste JSON and save to update keyring secret.`

  const BackupEncryptionUnsafePerFileRestoreRequirement = `Allow unsafe per-file restore behavior.  
Use only if you understand the data safety implications of restoring partially encrypted per-file backups.`

  const splitdumpSizeOptions = [
    { name: '16 MiB', value: '16MiB' },
    { name: '32 MiB', value: '32MiB' },
    { name: '64 MiB', value: '64MiB' },
    { name: '128 MiB', value: '128MiB' },
    { name: '256 MiB', value: '256MiB' },
    { name: '512 MiB', value: '512MiB' },
    { name: '1 G', value: '1G' },
    { name: '2 G', value: '2G' },
    { name: '4 G', value: '4G' },
    { name: 'No sharding', value: '0' }
  ]

  const backupEncryptionKeyringValidation = useMemo(
    () => validateKeyringJson(backupEncryptionKeyring),
    [backupEncryptionKeyring]
  )

  const canEditSettings = user?.grants['cluster-settings'] !== false

  const openCommonModal = () => {
    setIsCommonModalOpen(true)
  }

  const closeCommonModal = () => {
    setIsCommonModalOpen(false)
  }

  const renderInfoModalBody = (content) => (
    <Box className={joinClasses(modalStyles.infoTooltip, styles.infoTooltip)}>
      <Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown>
    </Box>
  )

  const openInfoModal = (titleText, content) => {
    setAction({
      title: titleText,
      type: '',
      body: renderInfoModalBody(content)
    })
    openCommonModal()
  }

  const handleBackupSnapshotsToggle = () => {
    setIsBackupSnapshotsOpen((prev) => !prev)
    requestAnimationFrame(() => {
      backupSnapshotsToggleRef.current?.scrollIntoView({
        block: 'center',
        behavior: 'smooth'
      })
    })
  }

  const handleResticRepoToggle = () => {
    setIsResticRepoConfigOpen((prev) => !prev)
  }

  const closeKeyringModal = () => {
    setIsKeyringConfirmModalOpen(false)
    setIsKeyringModalOpen(false)
    setBackupEncryptionKeyring('')
  }

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

  useEffect(() => {
    setBackupEncryptionSecretMode('passphrase')
    setBackupEncryptionPassphrase('')
    setBackupEncryptionKeyring('')
    setIsKeyringModalOpen(false)
    setIsKeyringConfirmModalOpen(false)
  }, [selectedCluster?.name])

  const isUsingScript = selectedCluster?.config?.backupSaveScript.length > 0

  const dataObject = [
    {
      key: (
        <Stack>
          <Text as="span">Custom Backup Script</Text>
          <Text as="span" fontSize='sm' color='gray.500'>(Will not use other logical backup options if set)</Text>
        </Stack>
      ),
      value: (
        <HStack width={'100%'}>
          <TextForm
            value={selectedCluster?.config?.backupSaveScript}
            confirmTitle={`Confirm backup-save-script to `}
            maxLength={1024}
            className={styles.textbox}
            onSave={(value) =>
              dispatch(
                setSetting({
                  clusterName: selectedCluster?.name,
                  setting: 'backup-save-script',
                  value: btoa(value)
                })
              )
            }
          />
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => { setAction({ title: 'Custom Backup Save Script', type: '', body: renderInfoModalBody(BackupSaveScriptRequirement) }); openCommonModal() }} />
        </HStack>
      )
    },
    {
      key: (
        <Stack>
          <Text>Custom Load Script</Text>
        </Stack>
      ),
      value: (
        <HStack width={'100%'}>
          <TextForm
            value={selectedCluster?.config?.backupLoadScript}
            confirmTitle={`Confirm backup-load-script to `}
            maxLength={1024}
            className={styles.textbox}
            onSave={(value) =>
              dispatch(
                setSetting({
                  clusterName: selectedCluster?.name,
                  setting: 'backup-load-script',
                  value: btoa(value)
                })
              )
            }
          />
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => { setAction({ title: 'Custom Backup Load Script', type: '', body: renderInfoModalBody(BackupLoadScriptRequirement) }); openCommonModal() }} />
        </HStack>
      )
    },
    {
      key: 'Logical Backup',
      value: (
        <Flex className={styles.dropdownContainer}>
          <Dropdown
            options={logicalBackupOptions}
            className={styles.dropdownButton}
            selectedValue={selectedCluster?.config?.backupLogicalType}
            confirmTitle={`Confirm logical backup to`}
            isDisabled={isUsingScript}
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
      key: (
        <Stack>
          <Text>Logical Backup Post-Script</Text>
        </Stack>
      ),
      value: (
        <HStack width={'100%'}>
          <TextForm
            value={selectedCluster?.config?.backupLogicalPostScript}
            confirmTitle={`Confirm backup-logical-post-script to `}
            maxLength={1024}
            className={styles.textbox}
            onSave={(value) =>
              dispatch(
                setSetting({
                  clusterName: selectedCluster?.name,
                  setting: 'backup-logical-post-script',
                  value: btoa(value)
                })
              )
            }
          />
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => { setAction({ title: 'Backup Logical Post-Script', type: '', body: renderInfoModalBody(BackupPostScriptRequirement) }); openCommonModal() }} />
        </HStack>
      )
    },
    {
      key: 'DB Client options',
      value: (
        <TextForm
          value={selectedCluster?.config?.backupMysqlclientOptions}
          confirmTitle={`Confirm backup-mysqlclient-options to `}
          maxLength={1024}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'backup-mysqlclient-options',
                value: btoa(value)
              })
            )
          }
        />
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
      key: (
        <HStack spacing={2}>
          <Text>Mysqldump splitdump output</Text>
          <RMIconButton
            icon={HiQuestionMarkCircle}
            onClick={() => openInfoModal('Mysqldump splitdump', SplitDumpRequirement)}
          />
        </HStack>
      ),
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.backupMysqldumpSplitDump}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for backup-mysqldump-splitdump?'}
          onChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'backup-mysqldump-splitdump'
              })
            )
          }
        />
      )
    },
    {
      key: (
        <HStack spacing={2}>
          <Text>Splitdump shard size</Text>
          <RMIconButton
            icon={HiQuestionMarkCircle}
            onClick={() => openInfoModal('Splitdump shard size', SplitDumpStreamSizeRequirement)}
          />
        </HStack>
      ),
      value: (
        <Dropdown
          options={splitdumpSizeOptions}
          className={styles.dropdownButton}
          selectedValue={selectedCluster?.config?.backupSplitdumpFileSize || '1G'}
          confirmTitle={`Confirm backup-splitdump-file-size to `}
          onChange={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'backup-splitdump-file-size',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: (
        <HStack spacing={2}>
          <Text>Replication Manager CLI path</Text>
          <RMIconButton
            icon={HiQuestionMarkCircle}
            onClick={() => openInfoModal('Replication Manager CLI path', ReplicationManagerCliRequirement)}
          />
        </HStack>
      ),
      value: (
        <TextForm
          value={selectedCluster?.config?.replicationManagerCliPath}
          confirmTitle={`Confirm replication-manager-cli-path to `}
          maxLength={1024}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'replication-manager-cli-path',
                value: value
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
      key: 'Mydumper Regex',
      value: (
        <TextForm
          value={selectedCluster?.config?.backupMyDumperRegex}
          confirmTitle={`Confirm backup-mydumper-regex to `}
          maxLength={1024}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'backup-mydumper-regex',
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
      key: 'Split Logical Dump with DB Credentials',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.backupSplitMysqlUser}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for backup-split-mysql-user?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-split-mysql-user' }))}
        />
      )
    },
    {
      key: 'Restore User When Reseed',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.backupRestoreMysqlUser}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for backup-restore-mysql-user?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-restore-mysql-user' }))}
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
      key: (
        <Stack>
          <Text>Physical Backup Post-Script</Text>
        </Stack>
      ),
      value: (
        <HStack width={'100%'}>
          <TextForm
            value={selectedCluster?.config?.backupPhysicalPostScript}
            confirmTitle={`Confirm backup-physical-post-script to `}
            maxLength={1024}
            className={styles.textbox}
            onSave={(value) =>
              dispatch(
                setSetting({
                  clusterName: selectedCluster?.name,
                  setting: 'backup-physical-post-script',
                  value: btoa(value)
                })
              )
            }
          />
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => { setAction({ title: 'Backup Physical Post-Script', type: '', body: renderInfoModalBody(BackupPostScriptRequirement) }); openCommonModal() }} />
        </HStack>
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
      key: (
        <Stack>
          <Text>Compression Level (1=fastest, 9=best)</Text>
        </Stack>
      ),
      value: (
        <NumberInput
          min={1}
          max={9}
          value={selectedCluster?.config?.compressBackupsCompressionLevel}
          showEditButton={true}
          showConfirmModal={true}
          confirmTitle={`Confirm change compression level to: `}
          onConfirm={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'compress-backups-compression-level',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: (
        <Stack>
          <Text>Parallel Blocks (higher=faster restore)</Text>
        </Stack>
      ),
      value: (
        <NumberInput
          min={1}
          max={32}
          value={selectedCluster?.config?.compressBackupsParallelBlocks}
          showEditButton={true}
          showConfirmModal={true}
          confirmTitle={`Confirm change parallel blocks to: `}
          onConfirm={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'compress-backups-parallel-blocks',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: (
        <Stack>
          <Text>Decompress Buffer Size (pgzip block size)</Text>
        </Stack>
      ),
      value: (
        <Dropdown
          options={decompressBufferOptions}
          selectedValue={selectedCluster?.config?.compressBackupsDecompressBufferSize}
          confirmTitle={`Confirm change 'compress-backups-decompress-buffer-size' to `}
          onChange={(size) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'compress-backups-decompress-buffer-size',
                value: size
              })
            )
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
      key: (
        <HStack spacing={2} className={styles.sectionHeader}>
          <Text>Backup encryption</Text>
          <RMIconButton
            icon={HiQuestionMarkCircle}
            onClick={() => openInfoModal('Backup encryption', BackupEncryptionRequirement)}
          />
        </HStack>
      ),
      value: [
        {
          key: 'Enable backup encryption',
          value: (
            <RMSwitch
              isChecked={selectedCluster?.config?.backupEncryptionEnabled}
              isDisabled={!canEditSettings}
              confirmTitle={'Confirm switch settings for backup-encryption-enabled?'}
              onChange={() =>
                dispatch(
                  switchSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'backup-encryption-enabled'
                  })
                )
              }
            />
          )
        },
        ...(selectedCluster?.config?.backupEncryptionEnabled
          ? [
            {
              key: 'Encryption directory mode',
              value: (
                <Dropdown
                  options={backupEncryptionDirectoryModeOptions}
                  className={styles.dropdownButton}
                  selectedValue={selectedCluster?.config?.backupEncryptionDirectoryMode || 'archive'}
                  confirmTitle={'Confirm backup-encryption-directory-mode to '}
                  isDisabled={!canEditSettings}
                  onChange={(value) =>
                    dispatch(
                      setSetting({
                        clusterName: selectedCluster?.name,
                        setting: 'backup-encryption-directory-mode',
                        value
                      })
                    )
                  }
                />
              )
            },
            ...(selectedCluster?.config?.backupEncryptionDirectoryMode !== 'per-file'
              ? [
                {
                  key: 'Encryption archive format',
                  value: (
                    <Dropdown
                      options={backupEncryptionDirectoryFormatOptions}
                      className={styles.dropdownButton}
                      selectedValue={selectedCluster?.config?.backupEncryptionDirectoryFormat || 'tar.gz'}
                      confirmTitle={'Confirm backup-encryption-directory-format to '}
                      isDisabled={!canEditSettings}
                      onChange={(value) =>
                        dispatch(
                          setSetting({
                            clusterName: selectedCluster?.name,
                            setting: 'backup-encryption-directory-format',
                            value
                          })
                        )
                      }
                    />
                  )
                }
              ]
              : []),
            {
              key: 'Secret input mode',
              value: (
                <Dropdown
                  options={backupEncryptionSecretModeOptions}
                  className={styles.dropdownButton}
                  selectedValue={backupEncryptionSecretMode}
                  isDisabled={!canEditSettings}
                  onChange={(value) => setBackupEncryptionSecretMode(value)}
                />
              )
            },
            {
              key: 'Keep plain directory copy',
              value: (
                <RMSwitch
                  isChecked={selectedCluster?.config?.backupEncryptionKeepPlainDir}
                  isDisabled={!canEditSettings}
                  confirmTitle={'Confirm switch settings for backup-encryption-keep-plain-dir?'}
                  onChange={() =>
                    dispatch(
                      switchSetting({
                        clusterName: selectedCluster?.name,
                        setting: 'backup-encryption-keep-plain-dir'
                      })
                    )
                  }
                />
              )
            },
            {
              key: (
                <HStack spacing={2}>
                  <Text as='span'>Require explicit passphrase</Text>
                  <RMIconButton
                    icon={HiQuestionMarkCircle}
                    onClick={() =>
                      openInfoModal(
                        'Require explicit passphrase',
                        BackupEncryptionExplicitPassphraseRequirement
                      )
                    }
                  />
                </HStack>
              ),
              value: (
                <RMSwitch
                  isChecked={selectedCluster?.config?.backupEncryptionRequireExplicitPassphrase}
                  isDisabled={!canEditSettings}
                  confirmTitle={'Confirm switch settings for backup-encryption-require-explicit-passphrase?'}
                  onChange={() =>
                    dispatch(
                      switchSetting({
                        clusterName: selectedCluster?.name,
                        setting: 'backup-encryption-require-explicit-passphrase'
                      })
                    )
                  }
                />
              )
            },
            ...(backupEncryptionSecretMode === 'passphrase'
              ? [{
              key: (
                <HStack spacing={2}>
                  <Text as='span'>Passphrase (write-only)</Text>
                  <RMIconButton
                    icon={HiQuestionMarkCircle}
                    onClick={() =>
                      openInfoModal('Encryption passphrase', BackupEncryptionPassphraseRequirement)
                    }
                  />
                </HStack>
              ),
              value: (
                <HStack width={'100%'}>
                  <Input
                    type='password'
                    value={backupEncryptionPassphrase}
                    onChange={(e) => setBackupEncryptionPassphrase(e.target.value)}
                    placeholder='Stored value is not shown. Enter new passphrase'
                    isDisabled={!canEditSettings}
                  />
                  <RMIconButton
                    icon={HiX}
                    tooltip='Clear'
                    colorScheme='red'
                    isDisabled={!backupEncryptionPassphrase || !canEditSettings}
                    onClick={() => setBackupEncryptionPassphrase('')}
                  />
                  <RMIconButton
                    icon={HiCheck}
                    tooltip='Save passphrase'
                    colorScheme='green'
                    isDisabled={!backupEncryptionPassphrase || !canEditSettings}
                    confirm={true}
                    confirmTitle='Confirm backup encryption passphrase update?'
                    confirmBody='This will store the new passphrase. Existing secret values are never displayed.'
                    onClick={() => {
                      dispatch(
                        setSecretSetting({
                          clusterName: selectedCluster?.name,
                          setting: 'backup-encryption-passphrase',
                          value: backupEncryptionPassphrase
                        })
                      )
                      setBackupEncryptionPassphrase('')
                    }}
                  />
                </HStack>
              )
            }]
              : []),
            ...(backupEncryptionSecretMode === 'keyring'
              ? [{
              key: (
                <HStack spacing={2}>
                  <Text as='span'>Keyring JSON (write-only)</Text>
                  <RMIconButton
                    icon={HiQuestionMarkCircle}
                    onClick={() => openInfoModal('Encryption keyring', BackupEncryptionKeyringRequirement)}
                  />
                </HStack>
              ),
              value: (
                <HStack width={'100%'}>
                  <Text fontSize='sm' color='gray.500'>Current keyring is hidden</Text>
                  <RMButton
                    size='small'
                    isDisabled={!canEditSettings}
                    onClick={() => setIsKeyringModalOpen(true)}>
                    Edit keyring JSON
                  </RMButton>
                </HStack>
              )
            }]
              : []),
            ...(selectedCluster?.config?.backupEncryptionDirectoryMode === 'per-file'
              ? [
                {
                  key: (
                    <HStack spacing={2}>
                      <Text as='span'>Unsafe per-file restore mode</Text>
                      <RMIconButton
                        icon={HiQuestionMarkCircle}
                        onClick={() =>
                          openInfoModal(
                            'Unsafe per-file restore mode',
                            BackupEncryptionUnsafePerFileRestoreRequirement
                          )
                        }
                      />
                    </HStack>
                  ),
                  value: (
                    <RMSwitch
                      isChecked={selectedCluster?.config?.backupEncryptionUnsafePerFileRestore}
                      isDisabled={!canEditSettings}
                      confirmTitle={'Confirm switch settings for backup-encryption-unsafe-per-file-restore?'}
                      onChange={() =>
                        dispatch(
                          switchSetting({
                            clusterName: selectedCluster?.name,
                            setting: 'backup-encryption-unsafe-per-file-restore'
                          })
                        )
                      }
                    />
                  )
                }
              ]
              : [])
          ]
          : [])
      ]
    },
    {
      key: (
        <HStack spacing={2} className={styles.sectionHeader}>
          <Text>Backup snapshots</Text>
          <Box
            as="button"
            type="button"
            ref={backupSnapshotsToggleRef}
            className={styles.sectionToggle}
            aria-expanded={isBackupSnapshotsOpen}
            onClick={handleBackupSnapshotsToggle}
          >
            {isBackupSnapshotsOpen ? 'Hide' : 'Show'}
          </Box>
        </HStack>
      ),
      value: isBackupSnapshotsOpen
        ? BackupSnapshotsSettings({
          selectedCluster,
          user,
          dispatch,
          onOpenInfoModal: openInfoModal,
          isResticRepoConfigOpen,
          onToggleResticRepoConfig: handleResticRepoToggle
        })
        : null
    },
    {
      key: 'Check free space',
      value: [
        {
          key: 'Check free space before backup',
          value: (
            <RMSwitch
              isChecked={selectedCluster?.config?.backupCheckFreeSpace}
              isDisabled={user?.grants['cluster-settings'] == false}
              confirmTitle={'Confirm switch settings for backup-check-free-space?'}
              onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-check-free-space' }))}
            />
          )
        },
        ...(selectedCluster?.config?.backupCheckFreeSpace
          ? [
            {
              key: 'Disk Usage Warning Threshold',
              value: (
                <NumberInput
                  min={1}
                  max={100}
                  value={selectedCluster?.config?.backupDiskTresholdWarn}
                  showEditButton={true}
                  showConfirmModal={true}
                  confirmTitle={`Confirm change 'backup-disk-treshold-warn' to: `}
                  onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-disk-treshold-warn', value: value }))}
                />
              )
            },
            {
              key: 'Disk Usage Critical Threshold',
              value: (
                <NumberInput
                  min={1}
                  max={100}
                  value={selectedCluster?.config?.backupDiskTresholdCrit}
                  showEditButton={true}
                  showConfirmModal={true}
                  confirmTitle={`Confirm change 'backup-disk-treshold-crit' to: `}
                  onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-disk-treshold-crit', value: value }))}
                />
              )
            },
            {
              key: 'Custom Threshold for Purging Old Restic Backups (0 means follow critical threshold)',
              value: (
                <NumberInput
                  min={0}
                  max={100}
                  value={selectedCluster?.config?.backupResticPurgeOldestOnDiskThreshold}
                  showEditButton={true}
                  showConfirmModal={true}
                  confirmTitle={`Confirm change restic threshold to: `}
                  onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-restic-purge-oldest-on-disk-threshold', value: value }))}
                />
              )
            },
            {
              key: "Purge oldest restic backups if disk usage exceed threshold",
              value: (
                <RMSwitch
                  isChecked={selectedCluster?.config?.backupResticPurgeOldestOnDiskSpace}
                  isDisabled={user?.grants['cluster-settings'] == false}
                  confirmTitle={'Confirm switch settings for backup-restic-purge-oldest-on-disk-space?'}
                  onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-restic-purge-oldest-on-disk-space' }))}
                />
              )
            },
            {
              key: 'Estimate backup size',
              value: (
                <RMSwitch
                  isChecked={selectedCluster?.config?.backupEstimateSize}
                  isDisabled={user?.grants['cluster-settings'] == false}
                  confirmTitle={'Confirm switch settings for backup-estimate-size?'}
                  onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-estimate-size' }))}
                />
              )
            },
            ...(selectedCluster?.config?.backupEstimateSize
              ? [
                {
                  key: 'Last Backup Growth Percentage (0 means same with last backup)',
                  value: (
                    <NumberInput
                      min={1}
                      value={selectedCluster?.config?.backupGrowthPercentage}
                      showEditButton={true}
                      showConfirmModal={true}
                      confirmTitle={`Confirm change 'backup-growth-percentage' to: `}
                      onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-growth-percentage', value: value }))}
                    />
                  )
                },
                {
                  key: 'Backup estimation percentage ratio from information_schema (if last backup not exist)',
                  value: (
                    <NumberInput
                      min={1}
                      value={selectedCluster?.config?.backupEstimateSizePercentage}
                      showEditButton={true}
                      showConfirmModal={true}
                      confirmTitle={`Confirm change 'backup-estimate-size-percentage' to: `}
                      onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-estimate-size-percentage', value: value }))}
                    />
                  )
                },
              ] : [])
          ] : [])
      ]
    },
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.table} />
      {isCommonModalOpen && (
        <CommonModal
          isOpen={isCommonModalOpen}
          size='lg'
          title={title}
          body={action.body}
          contentClassName={joinClasses(modalStyles.infoModalContent, styles.infoModalContent)}
          headerClassName={joinClasses(modalStyles.infoModalHeader, styles.infoModalHeader)}
          bodyClassName={joinClasses(modalStyles.infoModalBody, styles.infoModalBody)}
          closeModal={() => {
            closeCommonModal()
          }}
        />
      )}
      {isKeyringModalOpen && (
        <CommonModal
          isOpen={isKeyringModalOpen}
          size='xl'
          title='Backup encryption keyring JSON'
          body={(
            <Stack spacing={4}>
              <Text fontSize='sm' color='gray.500'>
                Write-only editor. Existing keyring value is not shown.
              </Text>
              <Box
                as='pre'
                p={3}
                borderRadius='md'
                border='1px solid'
                borderColor='gray.200'
                bg='blackAlpha.100'
                overflow='auto'
                fontSize='xs'>
{`{
  "activeKeyId": "2026-03",
  "legacyDefaultKeyId": "pre-keyring",
  "keys": [
    {"id": "pre-keyring", "passphrase": "old-pass", "state": "decrypt-only"},
    {"id": "2026-03", "passphrase": "new-pass", "state": "active"}
  ]
}`}
              </Box>
              <Textarea
                value={backupEncryptionKeyring}
                onChange={(e) => setBackupEncryptionKeyring(e.target.value)}
                placeholder='Paste keyring JSON here'
                minHeight='280px'
                fontFamily='monospace'
                fontSize='sm'
                isDisabled={!canEditSettings}
              />
              {!backupEncryptionKeyringValidation.isValid && (
                <Text fontSize='sm' color='red.400'>
                  {backupEncryptionKeyringValidation.message}
                </Text>
              )}
              <HStack justify='flex-end'>
                <RMButton
                  variant='outline'
                  colorScheme='white'
                  size='small'
                  onClick={closeKeyringModal}>
                  Cancel
                </RMButton>
                <RMButton
                  colorScheme='blue'
                  size='small'
                  isDisabled={!backupEncryptionKeyringValidation.isValid || !canEditSettings}
                  onClick={() => setIsKeyringConfirmModalOpen(true)}>
                  Save keyring
                </RMButton>
              </HStack>
            </Stack>
          )}
          contentClassName={joinClasses(modalStyles.infoModalContent, styles.infoModalContent)}
          headerClassName={joinClasses(modalStyles.infoModalHeader, styles.infoModalHeader)}
          bodyClassName={joinClasses(modalStyles.infoModalBody, styles.infoModalBody)}
          closeModal={closeKeyringModal}
        />
      )}
      {isKeyringConfirmModalOpen && (
        <ConfirmModal
          isOpen={isKeyringConfirmModalOpen}
          closeModal={() => setIsKeyringConfirmModalOpen(false)}
          title='Confirm backup encryption keyring update?'
          body='This will store the provided keyring JSON. Existing secret values are never displayed.'
          onConfirmClick={() => {
            dispatch(
              setSecretSetting({
                clusterName: selectedCluster?.name,
                setting: 'backup-encryption-keyring',
                value: backupEncryptionKeyring
              })
            )
            setIsKeyringConfirmModalOpen(false)
            setIsKeyringModalOpen(false)
            setBackupEncryptionKeyring('')
          }}
        />
      )}
    </Flex>
  )
}

export default BackupSettings
