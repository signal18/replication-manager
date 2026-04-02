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
  return result.map((size) => ({ name: formatBytes(size, 0), value: size }))
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
  const defaultOption = { name: formatBytes(defaultDecompressBufferSize, 0), value: defaultDecompressBufferSize }
  if (!updatedOptions.some((option) => option.value === defaultOption.value)) updatedOptions.push(defaultOption)
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
  const [action, setAction] = useState({ title: '', type: '', body: <></> })
  const { title, type } = action
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const { globalClusters: { monitor } } = useSelector((state) => state)

  const openCommonModal = () => setIsCommonModalOpen(true)
  const closeCommonModal = () => setIsCommonModalOpen(false)

  const renderInfoModalBody = (content) => (
    <Box className={joinClasses(modalStyles.infoTooltip, styles.infoTooltip)}>
      <Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown>
    </Box>
  )

  const openInfoModal = (titleText, content) => {
    setAction({ title: titleText, type: '', body: renderInfoModalBody(content) })
    openCommonModal()
  }


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

  // Consistent help icon style matching other settings tabs
  const h = (content, title) => (
    <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)}
      iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />
  )

  const handleBackupSnapshotsToggle = () => {
    setIsBackupSnapshotsOpen((prev) => !prev)
    requestAnimationFrame(() => {
      backupSnapshotsToggleRef.current?.scrollIntoView({ block: 'center', behavior: 'smooth' })
    })
  }

  const handleResticRepoToggle = () => setIsResticRepoConfigOpen((prev) => !prev)

  const backupEncryptionKeyringValidation = useMemo(
    () => validateKeyringJson(backupEncryptionKeyring),
    [backupEncryptionKeyring]
  )

  const closeKeyringModal = () => {
    setIsKeyringConfirmModalOpen(false)
    setIsKeyringModalOpen(false)
    setBackupEncryptionKeyring('')
  }

  useEffect(() => {
    if (selectedCluster?.config?.binlogCopyMode) setselectedBinlogBackupType(selectedCluster.config.binlogCopyMode)
  }, [selectedCluster?.config?.binlogCopyMode])

  useEffect(() => {
    if (monitor?.backupBinlogList) setBinlogBackupOptions(convertObjectToArrayForDropdown(monitor.backupBinlogList))
    if (monitor?.backupLogicalList) setLogicalBackupOptions(convertObjectToArrayForDropdown(monitor.backupLogicalList))
    if (monitor?.backupPhysicalList) setPhysicalBackupOptions(convertObjectToArrayForDropdown(monitor.backupPhysicalList))
    if (monitor?.binlogParseList) setBinlogParseOptions(convertObjectToArrayForDropdown(monitor.binlogParseList))
  }, [monitor?.backupBinlogList, monitor?.backupLogicalList, monitor?.backupPhysicalList, monitor?.binlogParseList])

  useEffect(() => {
    setBackupEncryptionSecretMode('passphrase')
    setBackupEncryptionPassphrase('')
    setBackupEncryptionKeyring('')
    setIsKeyringModalOpen(false)
    setIsKeyringConfirmModalOpen(false)
  }, [selectedCluster?.name])

  const isUsingScript = selectedCluster?.config?.backupSaveScript.length > 0

  // Help content
  const hSaveScript = `**Custom Backup Save Script**\n\nExecutes a custom backup script instead of the built-in logical backup tools. When set, other logical backup options are ignored.\n\nThe script receives these parameters:\n1. DB Server Host\n2. Master Host\n3. DB Server Port\n4. Master Port\n5. DB User\n6. DB Password\n7. Cluster Name\n\nConfig: \`backup-save-script\``
  const hLoadScript = `**Custom Load Script**\n\nExecutes a custom script for loading/restoring backups.\n\nThe script receives these parameters:\n1. DB Server Host\n2. Master Host\n3. DB Server Port\n4. Master Port\n5. DB User\n6. DB Password\n7. Cluster Name\n\nConfig: \`backup-load-script\``
  const hLogicalBackup = `**Logical Backup**\n\nSelects the tool used for logical backups (mysqldump, mydumper, etc.).\nLogical backups produce SQL or structured data files that are portable across versions.\n\nConfig: \`backup-logical-type\``
  const hLogicalPostScript = `**Logical Backup Post-Script**\n\nScript executed after a logical backup completes.\n\nThe script receives:\n1. Cluster name\n2. DB Server Host\n3. DB Server Port\n4. Backup Path\n\nConfig: \`backup-logical-post-script\``
  const hDbClientOptions = `**DB Client Options**\n\nExtra command-line options passed to the MySQL client binary used during backup operations.\n\nConfig: \`backup-mysqlclient-options\``
  const hMysqldumpOptions = `**Mysqldump Options**\n\nAdditional command-line flags passed to mysqldump.\nExample: \`--single-transaction --triggers --routines\`\n\nConfig: \`backup-mysqldump-options\``
  const hSplitDump = `**Mysqldump Splitdump Output**\n\nSends mysqldump output through replication-manager-cli splitdump instead of a single .sql.gz file.\nOutput is written to a splitdump directory (uncompressed).\nRequires the CLI to be available in PATH or configured below.\n\nConfig: \`backup-mysqldump-splitdump\``
  const hSplitDumpSize = `**Splitdump Shard Size**\n\nMaximum shard size for splitdump output. Default: 1G. Set to 0 to disable sharding.\n\nConfig: \`backup-splitdump-file-size\``
  const hCliPath = `**Replication Manager CLI Path**\n\nPath to the replication-manager-cli binary used for splitdump processing.\nLeave empty to use PATH lookup.\n\nConfig: \`replication-manager-cli-path\``
  const hMydumperOptions = `**Mydumper Options**\n\nAdditional command-line flags passed to mydumper.\n\nConfig: \`backup-mydumper-options\``
  const hMydumperRegex = `**Mydumper Regex**\n\nRegular expression to filter which tables mydumper includes in the backup.\n\nConfig: \`backup-mydumper-regex\``
  const hMyloaderOptions = `**Myloader Options**\n\nAdditional command-line flags passed to myloader during restore.\n\nConfig: \`backup-myloader-options\``
  const hSplitUser = `**Split Logical Dump with DB Credentials**\n\nWhen enabled, the logical dump is split into per-database files using the database credentials for each.\n\nConfig: \`backup-split-mysql-user\``
  const hRestoreUser = `**Restore User When Reseed**\n\nWhen enabled, MySQL user accounts and grants are restored as part of the reseed process.\n\nConfig: \`backup-restore-mysql-user\``
  const hPhysicalBackup = `**Physical Backup**\n\nSelects the tool used for physical backups (xtrabackup, mariabackup, etc.).\nPhysical backups copy raw data files and are faster to restore for large datasets.\n\nConfig: \`backup-physical-type\``
  const hPhysicalPostScript = `**Physical Backup Post-Script**\n\nScript executed after a physical backup completes.\n\nThe script receives:\n1. Cluster name\n2. DB Server Host\n3. DB Server Port\n4. Backup Path\n\nConfig: \`backup-physical-post-script\``
  const hBinlogBackup = `**Binlog Backup**\n\nSelects the method used to back up binary logs (mysqlbinlog, script, etc.).\nBinary log backups enable point-in-time recovery between full backup snapshots.\n\nConfig: \`binlog-copy-mode\``
  const hBinlogParseMode = `**Binlog Parse Mode**\n\nSelects the parser used to read binary logs during recovery or flashback operations.\n\nConfig: \`binlog-parse-mode\``
  const hCompression = `**Use Compression**\n\nEnables compression of backup files using pgzip.\nReduces storage space at the cost of additional CPU during backup and restore.\n\nConfig: \`compress-backups\``
  const hCompressionLevel = `**Compression Level**\n\nCompression level from 1 (fastest, largest files) to 9 (slowest, smallest files).\nDefault: 6. For most workloads, 1–3 provides a good speed/size tradeoff.\n\nConfig: \`compress-backups-compression-level\``
  const hParallelBlocks = `**Parallel Blocks**\n\nNumber of parallel blocks used during decompression (restore).\nHigher values speed up restore at the cost of more CPU and memory.\n\nConfig: \`compress-backups-parallel-blocks\``
  const hDecompressBuffer = `**Decompress Buffer Size**\n\nBlock size used by pgzip during decompression. Must match the size used during compression.\nDefault: ~244 KiB.\n\nConfig: \`compress-backups-decompress-buffer-size\``
  const hBackupBuffer = `**Backup Buffer Size**\n\nNetwork buffer size used when streaming backup data between nodes (SST).\nLarger values can improve throughput on high-bandwidth networks.\n\nConfig: \`sst-send-buffer\``
  const hBackupBinlogs = `**Backup Binlogs**\n\nEnables automatic backup of binary log files alongside the main backup.\nRequired for point-in-time recovery.\n\nConfig: \`backup-binlogs\``
  const hBackupBinlogsKeep = `**Backup Binlogs Keep Files**\n\nNumber of binary log backup files to retain. Older files are automatically deleted.\n\nConfig: \`backup-binlogs-keep\``
  const hEnforcePurge = `**Enforce Binlog Purge**\n\nWhen enabled, replication-manager automatically purges binary logs from all servers once they have been replicated and backed up.\n\nConfig: \`force-binlog-purge\``
  const hMaxBinlogSize = `**Max Binlog Total Size**\n\nMaximum total size (in GB) of binary logs retained across all servers before purging begins.\n\nConfig: \`force-binlog-purge-total-size\``
  const hMinReplicas = `**Minimum Replicas Needed for Purging**\n\nMinimum number of replicas that must have received a binary log event before it is eligible for purging on the master.\n\nConfig: \`force-binlog-purge-min-replica\``
  const hPurgeOnRestore = `**Enforce Binlog Purge on Restore**\n\nWhen enabled, binary logs are purged on the restored node immediately after a reseed completes.\n\nConfig: \`force-binlog-purge-on-restore\``
  const hPurgeOnReplicas = `**Enforce Binlog Purge on Replicas**\n\nWhen enabled, binary log purge is enforced on all replica servers in addition to the master.\n\nConfig: \`force-binlog-purge-replicas\``
  const hStreamingEndpoint = `**Backup Streaming Endpoint**\n\nS3-compatible endpoint URL for streaming backup storage.\nExample: \`https://s3.amazonaws.com\`\n\nConfig: \`backup-streaming-endpoint\``
  const hStreamingRegion = `**Backup Streaming Region**\n\nCloud region for the streaming backup bucket.\nExample: \`eu-west-1\`\n\nConfig: \`backup-streaming-region\``
  const hStreamingBucket = `**Backup Streaming Bucket**\n\nS3 bucket name used for streaming backup storage.\n\nConfig: \`backup-streaming-bucket\``
  const hCheckFreeSpace = `**Check Free Space Before Backup**\n\nVerifies that sufficient disk space is available before starting a backup.\nPrevents partial backups that could corrupt the backup directory.\n\nConfig: \`backup-check-free-space\``
  const hDiskWarn = `**Disk Usage Warning Threshold**\n\nDisk usage percentage at which a warning alert is triggered.\n\nConfig: \`backup-disk-treshold-warn\``
  const hDiskCrit = `**Disk Usage Critical Threshold**\n\nDisk usage percentage at which backup is blocked and a critical alert is triggered.\n\nConfig: \`backup-disk-treshold-crit\``
  const hResticPurgeThreshold = `**Custom Threshold for Purging Old Restic Backups**\n\nDisk usage percentage at which the oldest Restic backups are automatically purged.\nSet to 0 to follow the critical threshold.\n\nConfig: \`backup-restic-purge-oldest-on-disk-threshold\``
  const hResticPurgeOnDisk = `**Purge Oldest Restic Backups if Disk Usage Exceeds Threshold**\n\nWhen enabled, automatically removes the oldest Restic backup snapshots when disk usage exceeds the configured threshold.\n\nConfig: \`backup-restic-purge-oldest-on-disk-space\``
  const hEstimateSize = `**Estimate Backup Size**\n\nEstimates the expected backup size before starting, using the last backup size or information_schema data.\nUsed to verify sufficient free space is available.\n\nConfig: \`backup-estimate-size\``
  const hGrowthPercentage = `**Last Backup Growth Percentage**\n\nExpected growth percentage applied to the last backup size when estimating the next backup size.\nSet to 0 to use the last backup size without adjustment.\n\nConfig: \`backup-growth-percentage\``
  const hEstimatePercentage = `**Backup Estimation Percentage from information_schema**\n\nPercentage of the information_schema size used as the backup size estimate when no previous backup exists.\n\nConfig: \`backup-estimate-size-percentage\``
  const hBackupEncryptionEnabled = `**Backup Encryption**\n\nEnable encryption for backup artifacts.\nWhen enabled, backup encryption controls are shown to configure mode, format, and secret material.\n\nConfig: \`backup-encryption-enabled\``
  const hBackupEncryptionDirectoryMode = `**Encryption Directory Mode**\n\nControls how backup files are organized:\n- **archive**: Whole backup packed as a single archive, then encrypted.\n- **per-file**: Each backup file encrypted individually.\n\nConfig: \`backup-encryption-directory-mode\``
  const hBackupEncryptionDirectoryFormat = `**Encryption Archive Format**\n\nArchive format used when directory mode is 'archive'.\n\nConfig: \`backup-encryption-directory-format\``
  const hBackupEncryptionKeepPlainDir = `**Keep Plain Directory Copy**\n\nKeep a plain (unencrypted) copy of the backup directory alongside the encrypted version.\n\nConfig: \`backup-encryption-keep-plain-dir\``
  const hBackupEncryptionExplicitPassphrase = `**Require Explicit Passphrase**\n\nRequire explicit passphrase usage for backup encryption operations.\nUse this when you want restores or encryption workflows to refuse implicit/default credentials.\n\nConfig: \`backup-encryption-require-explicit-passphrase\``
  const hBackupEncryptionPassphrase = `**Encryption Passphrase (write-only)**\n\nWrite-only passphrase entry for backup encryption.\nCurrent value is never displayed in UI.\nSaving updates the passphrase secret for this cluster.\n\nConfig: \`backup-encryption-passphrase\``
  const hBackupEncryptionKeyring = `**Encryption Keyring (write-only)**\n\nWrite-only JSON keyring editor for backup encryption keys.\nCurrent keyring value is never displayed in UI.\nPaste JSON and save to update keyring secret.\n\nConfig: \`backup-encryption-keyring\``
  const hBackupEncryptionUnsafePerFileRestore = `**Unsafe Per-file Restore Mode**\n\nAllow unsafe per-file restore behavior.\nUse only if you understand the data safety implications of restoring partially encrypted per-file backups.\n\nConfig: \`backup-encryption-unsafe-per-file-restore\``

  const canEditSettings = user?.grants['cluster-settings'] !== false

  const dataObject = [
    {
      key: 'Custom Backup Script',
      help: h(hSaveScript, 'Custom Backup Save Script'),
      value: (
        <TextForm value={selectedCluster?.config?.backupSaveScript} confirmTitle={`Confirm backup-save-script to `}
          maxLength={1024} className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-save-script', value: btoa(value) }))} />
      )
    },
    {
      key: 'Custom Load Script',
      help: h(hLoadScript, 'Custom Load Script'),
      value: (
        <TextForm value={selectedCluster?.config?.backupLoadScript} confirmTitle={`Confirm backup-load-script to `}
          maxLength={1024} className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-load-script', value: btoa(value) }))} />
      )
    },
    {
      key: 'Logical Backup',
      help: h(hLogicalBackup, 'Logical Backup'),
      value: (
        <Flex className={styles.dropdownContainer}>
          <Dropdown options={logicalBackupOptions} className={styles.dropdownButton}
            selectedValue={selectedCluster?.config?.backupLogicalType} confirmTitle={`Confirm logical backup to`}
            isDisabled={isUsingScript}
            onChange={(backupType) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-logical-type', value: backupType }))} />
        </Flex>
      )
    },
    {
      key: 'Logical Backup Post-Script',
      help: h(hLogicalPostScript, 'Logical Backup Post-Script'),
      value: (
        <TextForm value={selectedCluster?.config?.backupLogicalPostScript} confirmTitle={`Confirm backup-logical-post-script to `}
          maxLength={1024} className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-logical-post-script', value: btoa(value) }))} />
      )
    },
    {
      key: 'DB Client Options',
      help: h(hDbClientOptions, 'DB Client Options'),
      value: (
        <TextForm value={selectedCluster?.config?.backupMysqlclientOptions} confirmTitle={`Confirm backup-mysqlclient-options to `}
          maxLength={1024} className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-mysqlclient-options', value: btoa(value) }))} />
      )
    },
    {
      key: 'Mysqldump Options',
      help: h(hMysqldumpOptions, 'Mysqldump Options'),
      value: (
        <TextForm value={selectedCluster?.config?.backupMysqldumpOptions} confirmTitle={`Confirm backup-mysqldump-options to `}
          maxLength={1024} className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-mysqldump-options', value: btoa(value) }))} />
      )
    },
    {
      key: 'Mysqldump Splitdump Output',
      help: h(hSplitDump, 'Mysqldump Splitdump Output'),
      value: (
        <RMSwitch isChecked={selectedCluster?.config?.backupMysqldumpSplitDump}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for backup-mysqldump-splitdump?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-mysqldump-splitdump' }))} />
      )
    },
    {
      key: 'Splitdump Shard Size',
      help: h(hSplitDumpSize, 'Splitdump Shard Size'),
      value: (
        <Dropdown options={splitdumpSizeOptions} className={styles.dropdownButton}
          selectedValue={selectedCluster?.config?.backupSplitdumpFileSize || '1G'}
          confirmTitle={`Confirm backup-splitdump-file-size to `}
          onChange={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-splitdump-file-size', value }))} />
      )
    },
    {
      key: 'Replication Manager CLI Path',
      help: h(hCliPath, 'Replication Manager CLI Path'),
      value: (
        <TextForm value={selectedCluster?.config?.replicationManagerCliPath} confirmTitle={`Confirm replication-manager-cli-path to `}
          maxLength={1024} className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'replication-manager-cli-path', value }))} />
      )
    },
    {
      key: 'Mydumper Options',
      help: h(hMydumperOptions, 'Mydumper Options'),
      value: (
        <TextForm value={selectedCluster?.config?.backupMyDumperOptions} confirmTitle={`Confirm backup-mydumper-options to `}
          maxLength={1024} className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-mydumper-options', value: btoa(value) }))} />
      )
    },
    {
      key: 'Mydumper Regex',
      help: h(hMydumperRegex, 'Mydumper Regex'),
      value: (
        <TextForm value={selectedCluster?.config?.backupMyDumperRegex} confirmTitle={`Confirm backup-mydumper-regex to `}
          maxLength={1024} className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-mydumper-regex', value: btoa(value) }))} />
      )
    },
    {
      key: 'Myloader Options',
      help: h(hMyloaderOptions, 'Myloader Options'),
      value: (
        <TextForm value={selectedCluster?.config?.backupMyLoaderOptions} confirmTitle={`Confirm backup-myloader-options to `}
          maxLength={1024} className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-myloader-options', value: btoa(value) }))} />
      )
    },
    {
      key: 'Split Logical Dump with DB Credentials',
      help: h(hSplitUser, 'Split Logical Dump with DB Credentials'),
      value: (
        <RMSwitch isChecked={selectedCluster?.config?.backupSplitMysqlUser}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for backup-split-mysql-user?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-split-mysql-user' }))} />
      )
    },
    {
      key: 'Restore User When Reseed',
      help: h(hRestoreUser, 'Restore User When Reseed'),
      value: (
        <RMSwitch isChecked={selectedCluster?.config?.backupRestoreMysqlUser}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for backup-restore-mysql-user?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-restore-mysql-user' }))} />
      )
    },
    {
      key: 'Physical Backup',
      help: h(hPhysicalBackup, 'Physical Backup'),
      value: (
        <Flex className={styles.dropdownContainer}>
          <Dropdown options={physicalBackupOptions} className={styles.dropdownButton}
            selectedValue={selectedCluster?.config?.backupPhysicalType} confirmTitle={`Confirm physical backup to`}
            onChange={(backupType) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-physical-type', value: backupType }))} />
        </Flex>
      )
    },
    {
      key: 'Physical Backup Post-Script',
      help: h(hPhysicalPostScript, 'Physical Backup Post-Script'),
      value: (
        <TextForm value={selectedCluster?.config?.backupPhysicalPostScript} confirmTitle={`Confirm backup-physical-post-script to `}
          maxLength={1024} className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-physical-post-script', value: btoa(value) }))} />
      )
    },
    {
      key: 'Binlog Backup',
      help: h(hBinlogBackup, 'Binlog Backup'),
      value: (
        <Flex className={`${styles.dropdownContainer} ${styles.dropdownContainerColumn}`} direction='column' align='flex-start'>
          <Dropdown options={binlogBackupOptions} className={styles.dropdownButton}
            selectedValue={selectedCluster?.config?.binlogCopyMode} confirmTitle={`Confirm Binlog backup to`}
            onChange={(backupType) => {
              setselectedBinlogBackupType(backupType)
              if (backupType !== 'script') dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-binlog-type', value: backupType }))
            }} />
          {selectedBinlogBackupType === 'script' && (
            <TextForm label={'Backup Binlog Script Path'} direction='column' className={styles.scriptTextContainer}
              value={selectedCluster?.config?.binlogCopyScript} confirmTitle='Confirm Binlog backup to script with value '
              onSave={(scriptValue) => {
                dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-binlog-script', value: scriptValue }))
                dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-binlog-type', value: 'script' }))
              }} />
          )}
        </Flex>
      )
    },
    {
      key: 'Binlog Parse Mode',
      help: h(hBinlogParseMode, 'Binlog Parse Mode'),
      value: (
        <Flex className={styles.dropdownContainer}>
          <Dropdown options={binlogParseOptions} className={styles.dropdownButton}
            selectedValue={selectedCluster?.config?.binlogParseMode} confirmTitle={`Confirm binlog parse mode to`}
            onChange={(mode) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'binlog-parse-mode', value: mode }))} />
        </Flex>
      )
    },
    {
      key: 'Use Compression',
      help: h(hCompression, 'Use Compression'),
      value: (
        <RMSwitch isChecked={selectedCluster?.config?.compressBackups}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for compress-backups?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'compress-backups' }))} />
      )
    },
    {
      key: 'Compression Level',
      help: h(hCompressionLevel, 'Compression Level'),
      value: (
        <NumberInput min={1} max={9} value={selectedCluster?.config?.compressBackupsCompressionLevel}
          showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change compression level to: `}
          onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'compress-backups-compression-level', value }))} />
      )
    },
    {
      key: 'Parallel Blocks',
      help: h(hParallelBlocks, 'Parallel Blocks'),
      value: (
        <NumberInput min={1} max={32} value={selectedCluster?.config?.compressBackupsParallelBlocks}
          showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change parallel blocks to: `}
          onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'compress-backups-parallel-blocks', value }))} />
      )
    },
    {
      key: 'Decompress Buffer Size',
      help: h(hDecompressBuffer, 'Decompress Buffer Size'),
      value: (
        <Dropdown options={decompressBufferOptions} selectedValue={selectedCluster?.config?.compressBackupsDecompressBufferSize}
          confirmTitle={`Confirm change 'compress-backups-decompress-buffer-size' to `}
          onChange={(size) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'compress-backups-decompress-buffer-size', value: size }))} />
      )
    },
    {
      key: 'Backup Buffer Size',
      help: h(hBackupBuffer, 'Backup Buffer Size'),
      value: (
        <Dropdown options={sizeOptions} selectedValue={selectedCluster?.config?.sstSendBuffer}
          confirmTitle={`Confirm change 'sst-send-buffer' to `}
          onChange={(size) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'sst-send-buffer', value: size }))} />
      )
    },
    {
      key: 'Backup Binlogs',
      help: h(hBackupBinlogs, 'Backup Binlogs'),
      value: (
        <RMSwitch isChecked={selectedCluster?.config?.autorejoinBackupBinlog}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for backup-binlogs?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-binlogs' }))} />
      )
    },
    {
      key: 'Backup Binlogs Keep Files',
      help: h(hBackupBinlogsKeep, 'Backup Binlogs Keep Files'),
      value: (
        <RMSlider value={selectedCluster?.config?.backupBinlogsKeep}
          confirmTitle='Confirm change keep binlogs files to: '
          onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-binlogs-keep', value: val }))} />
      )
    },
    {
      key: 'Enforce Binlog Purge',
      help: h(hEnforcePurge, 'Enforce Binlog Purge'),
      value: (
        <RMSwitch isChecked={selectedCluster?.config?.forceBinlogPurge}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for force-binlog-purge?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-binlog-purge' }))} />
      )
    },
    {
      key: 'Max Binlog Total Size (GB)',
      help: h(hMaxBinlogSize, 'Max Binlog Total Size'),
      value: (
        <RMSlider value={selectedCluster?.config?.forceBinlogPurgeTotalSize} max={256} showMarkAtInterval={64}
          confirmTitle='Confirm change force-binlog-purge-total-size to: '
          onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'force-binlog-purge-total-size', value: val }))} />
      )
    },
    {
      key: 'Minimum Replicas for Purging',
      help: h(hMinReplicas, 'Minimum Replicas Needed for Purging'),
      value: (
        <RMSlider value={selectedCluster?.config?.forceBinlogPurgeMinReplica} max={12}
          confirmTitle='Confirm change force-binlog-purge-min-replica to: '
          onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'force-binlog-purge-min-replica', value: val }))} />
      )
    },
    {
      key: 'Enforce Binlog Purge on Restore',
      help: h(hPurgeOnRestore, 'Enforce Binlog Purge on Restore'),
      value: (
        <RMSwitch isChecked={selectedCluster?.config?.forceBinlogPurgeOnRestore}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for force-binlog-purge-on-restore?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-binlog-purge-on-restore' }))} />
      )
    },
    {
      key: 'Enforce Binlog Purge on Replicas',
      help: h(hPurgeOnReplicas, 'Enforce Binlog Purge on Replicas'),
      value: (
        <RMSwitch isChecked={selectedCluster?.config?.forceBinlogPurgeReplicas}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for force-binlog-purge-replicas?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'force-binlog-purge-replicas' }))} />
      )
    },
    {
      key: 'Backup Streaming Endpoint',
      help: h(hStreamingEndpoint, 'Backup Streaming Endpoint'),
      value: (
        <TextForm value={selectedCluster?.config?.backupStreamingEndpoint} confirmTitle={`Confirm backup-streaming-endpoint to `}
          className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-streaming-endpoint', value }))} />
      )
    },
    {
      key: 'Backup Streaming Region',
      help: h(hStreamingRegion, 'Backup Streaming Region'),
      value: (
        <TextForm value={selectedCluster?.config?.backupStreamingRegion} confirmTitle={`Confirm backup-streaming-region to `}
          className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-streaming-region', value }))} />
      )
    },
    {
      key: 'Backup Streaming Bucket',
      help: h(hStreamingBucket, 'Backup Streaming Bucket'),
      value: (
        <TextForm value={selectedCluster?.config?.backupStreamingBucket} confirmTitle={`Confirm backup-streaming-bucket to `}
          className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-streaming-bucket', value }))} />
      )
    },
    {
      key: 'Backup Encryption',
      value: [
        {
          key: 'Enable backup encryption',
          help: h(hBackupEncryptionEnabled, 'Backup Encryption'),
          value: (
            <RMSwitch
              isChecked={selectedCluster?.config?.backupEncryptionEnabled}
              isDisabled={!canEditSettings}
              confirmTitle={'Confirm switch settings for backup-encryption-enabled?'}
              onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-encryption-enabled' }))}
            />
          )
        },
        ...(selectedCluster?.config?.backupEncryptionEnabled ? [
          {
            key: 'Encryption directory mode',
            help: h(hBackupEncryptionDirectoryMode, 'Encryption Directory Mode'),
            value: (
              <Dropdown
                options={backupEncryptionDirectoryModeOptions}
                className={styles.dropdownButton}
                selectedValue={selectedCluster?.config?.backupEncryptionDirectoryMode || 'archive'}
                confirmTitle={'Confirm backup-encryption-directory-mode to '}
                isDisabled={!canEditSettings}
                onChange={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-encryption-directory-mode', value }))}
              />
            )
          },
          ...(selectedCluster?.config?.backupEncryptionDirectoryMode !== 'per-file' ? [
            {
              key: 'Encryption archive format',
              help: h(hBackupEncryptionDirectoryFormat, 'Encryption Archive Format'),
              value: (
                <Dropdown
                  options={backupEncryptionDirectoryFormatOptions}
                  className={styles.dropdownButton}
                  selectedValue={selectedCluster?.config?.backupEncryptionDirectoryFormat || 'tar.gz'}
                  confirmTitle={'Confirm backup-encryption-directory-format to '}
                  isDisabled={!canEditSettings}
                  onChange={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-encryption-directory-format', value }))}
                />
              )
            }
          ] : []),
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
            help: h(hBackupEncryptionKeepPlainDir, 'Keep Plain Directory Copy'),
            value: (
              <RMSwitch
                isChecked={selectedCluster?.config?.backupEncryptionKeepPlainDir}
                isDisabled={!canEditSettings}
                confirmTitle={'Confirm switch settings for backup-encryption-keep-plain-dir?'}
                onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-encryption-keep-plain-dir' }))}
              />
            )
          },
          {
            key: 'Require explicit passphrase',
            help: h(hBackupEncryptionExplicitPassphrase, 'Require Explicit Passphrase'),
            value: (
              <RMSwitch
                isChecked={selectedCluster?.config?.backupEncryptionRequireExplicitPassphrase}
                isDisabled={!canEditSettings}
                confirmTitle={'Confirm switch settings for backup-encryption-require-explicit-passphrase?'}
                onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-encryption-require-explicit-passphrase' }))}
              />
            )
          },
          ...(backupEncryptionSecretMode === 'passphrase' ? [
            {
              key: 'Passphrase (write-only)',
              help: h(hBackupEncryptionPassphrase, 'Encryption Passphrase'),
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
                      dispatch(setSecretSetting({ clusterName: selectedCluster?.name, setting: 'backup-encryption-passphrase', value: backupEncryptionPassphrase }))
                      setBackupEncryptionPassphrase('')
                    }}
                  />
                </HStack>
              )
            }
          ] : []),
          ...(backupEncryptionSecretMode === 'keyring' ? [
            {
              key: 'Keyring JSON (write-only)',
              help: h(hBackupEncryptionKeyring, 'Encryption Keyring'),
              value: (
                <HStack width={'100%'}>
                  <Text fontSize='sm' color='gray.500'>Current keyring is hidden</Text>
                  <RMButton size='small' isDisabled={!canEditSettings} onClick={() => setIsKeyringModalOpen(true)}>
                    Edit keyring JSON
                  </RMButton>
                </HStack>
              )
            }
          ] : []),
          ...(selectedCluster?.config?.backupEncryptionDirectoryMode === 'per-file' ? [
            {
              key: 'Unsafe per-file restore mode',
              help: h(hBackupEncryptionUnsafePerFileRestore, 'Unsafe Per-file Restore Mode'),
              value: (
                <RMSwitch
                  isChecked={selectedCluster?.config?.backupEncryptionUnsafePerFileRestore}
                  isDisabled={!canEditSettings}
                  confirmTitle={'Confirm switch settings for backup-encryption-unsafe-per-file-restore?'}
                  onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-encryption-unsafe-per-file-restore' }))}
                />
              )
            }
          ] : [])
        ] : [])
      ]
    },
    {
      // Backup Snapshots uses a different fullWidth layout — keep icon inline in the key
      key: (
        <HStack spacing={2} className={styles.sectionHeader}>
          <Text>Backup Snapshots</Text>
          <Box as="button" type="button" ref={backupSnapshotsToggleRef}
            className={styles.sectionToggle} aria-expanded={isBackupSnapshotsOpen}
            onClick={handleBackupSnapshotsToggle}>
            {isBackupSnapshotsOpen ? 'Hide' : 'Show'}
          </Box>
        </HStack>
      ),
      value: isBackupSnapshotsOpen
        ? BackupSnapshotsSettings({ selectedCluster, user, dispatch, onOpenInfoModal: openInfoModal, isResticRepoConfigOpen, onToggleResticRepoConfig: handleResticRepoToggle })
        : null
    },
    {
      key: 'Check Free Space',
      value: [
        { key: 'Check Free Space Before Backup', help: h(hCheckFreeSpace, 'Check Free Space Before Backup'), value: (<RMSwitch isChecked={selectedCluster?.config?.backupCheckFreeSpace} isDisabled={user?.grants['cluster-settings'] == false} confirmTitle={'Confirm switch settings for backup-check-free-space?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-check-free-space' }))} />) },
        ...(selectedCluster?.config?.backupCheckFreeSpace ? [
          { key: 'Disk Usage Warning Threshold', help: h(hDiskWarn, 'Disk Usage Warning Threshold'), value: (<NumberInput min={1} max={100} value={selectedCluster?.config?.backupDiskTresholdWarn} showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'backup-disk-treshold-warn' to: `} onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-disk-treshold-warn', value }))} />) },
          { key: 'Disk Usage Critical Threshold', help: h(hDiskCrit, 'Disk Usage Critical Threshold'), value: (<NumberInput min={1} max={100} value={selectedCluster?.config?.backupDiskTresholdCrit} showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'backup-disk-treshold-crit' to: `} onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-disk-treshold-crit', value }))} />) },
          { key: 'Restic Purge Threshold', help: h(hResticPurgeThreshold, 'Custom Threshold for Purging Old Restic Backups'), value: (<NumberInput min={0} max={100} value={selectedCluster?.config?.backupResticPurgeOldestOnDiskThreshold} showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change restic threshold to: `} onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-restic-purge-oldest-on-disk-threshold', value }))} />) },
          { key: 'Purge Oldest Restic on Low Disk', help: h(hResticPurgeOnDisk, 'Purge Oldest Restic Backups if Disk Usage Exceeds Threshold'), value: (<RMSwitch isChecked={selectedCluster?.config?.backupResticPurgeOldestOnDiskSpace} isDisabled={user?.grants['cluster-settings'] == false} confirmTitle={'Confirm switch settings for backup-restic-purge-oldest-on-disk-space?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-restic-purge-oldest-on-disk-space' }))} />) },
          { key: 'Estimate Backup Size', help: h(hEstimateSize, 'Estimate Backup Size'), value: (<RMSwitch isChecked={selectedCluster?.config?.backupEstimateSize} isDisabled={user?.grants['cluster-settings'] == false} confirmTitle={'Confirm switch settings for backup-estimate-size?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-estimate-size' }))} />) },
          ...(selectedCluster?.config?.backupEstimateSize ? [
            { key: 'Backup Growth Percentage', help: h(hGrowthPercentage, 'Last Backup Growth Percentage'), value: (<NumberInput min={1} value={selectedCluster?.config?.backupGrowthPercentage} showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'backup-growth-percentage' to: `} onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-growth-percentage', value }))} />) },
            { key: 'Estimation from information_schema (%)', help: h(hEstimatePercentage, 'Backup Estimation Percentage from information_schema'), value: (<NumberInput min={1} value={selectedCluster?.config?.backupEstimateSizePercentage} showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'backup-estimate-size-percentage' to: `} onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-estimate-size-percentage', value }))} />) },
          ] : [])
        ] : [])
      ]
    },
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn={true} />
      {isCommonModalOpen && (
        <CommonModal isOpen={isCommonModalOpen} size='lg' title={title} body={action.body}
          contentClassName={joinClasses(modalStyles.infoModalContent, styles.infoModalContent)}
          headerClassName={joinClasses(modalStyles.infoModalHeader, styles.infoModalHeader)}
          bodyClassName={joinClasses(modalStyles.infoModalBody, styles.infoModalBody)}
          closeModal={closeCommonModal} />
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
                <RMButton variant='outline' colorScheme='white' size='small' onClick={closeKeyringModal}>
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
            dispatch(setSecretSetting({ clusterName: selectedCluster?.name, setting: 'backup-encryption-keyring', value: backupEncryptionKeyring }))
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
