import { Box, Flex, HStack, Spinner, Stack, Text } from '@chakra-ui/react'
import React, { useState, useEffect, useMemo } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import RMSlider from '../../components/Sliders/RMSlider'
import Dropdown from '../../components/Dropdown'
import { convertObjectToArrayForDropdown, formatBytes } from '../../utility/common'
import TextForm from '../../components/TextForm'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'
import remarkGfm from 'remark-gfm'
import NumberInput from '../../components/NumberInput'
import RMButton from '../../components/RMButton'
import SetCredentialsModal from '../../components/Modals/SetCredentialsModal'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { getBackupVerificationServer, markBackupVerificationServer, setAsIgnored } from '../../redux/clusterSlice'

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


  const verificationLevelOptions = [
    { name: 'Integrity', value: 'integrity' },
    { name: 'Test restore', value: 'restore' }
  ]

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


  useEffect(() => {
    if (selectedCluster?.config?.binlogCopyMode) setselectedBinlogBackupType(selectedCluster.config.binlogCopyMode)
  }, [selectedCluster?.config?.binlogCopyMode])

  useEffect(() => {
    if (monitor?.backupBinlogList) setBinlogBackupOptions(convertObjectToArrayForDropdown(monitor.backupBinlogList))
    if (monitor?.backupLogicalList) setLogicalBackupOptions(convertObjectToArrayForDropdown(monitor.backupLogicalList))
    if (monitor?.backupPhysicalList) setPhysicalBackupOptions(convertObjectToArrayForDropdown(monitor.backupPhysicalList))
    if (monitor?.binlogParseList) setBinlogParseOptions(convertObjectToArrayForDropdown(monitor.binlogParseList))
  }, [monitor?.backupBinlogList, monitor?.backupLogicalList, monitor?.backupPhysicalList, monitor?.binlogParseList])

  const isUsingScript = selectedCluster?.config?.backupSaveScript.length > 0
  const [isVerificationCredentialOpen, setIsVerificationCredentialOpen] = useState(false)
  const clusterServers = useSelector((state) => state.cluster.clusterServers)
  // backup-verification-host: a server outside the cluster, or a replica of the cluster listed
  // in db-servers-ignored-hosts, neither the primary nor a relay (the test restore reseeds it)
  const [verificationExternal, setVerificationExternal] = useState(false)
  const [verificationPick, setVerificationPick] = useState(null) // a replica waiting for its confirmation
  const [verificationDropdownKey, setVerificationDropdownKey] = useState(0) // remount: back to the saved value
  const verificationHost = selectedCluster?.config?.backupVerificationHost || ''
  const verificationReplicaOptions = useMemo(
    () =>
      (clusterServers || []).map((s) => {
        const why = s.state === 'Master' ? 'primary' : s.isRelay ? 'relay' : ''
        return { name: `${s.url} (${why || 'replica'})`, value: s.url, isDisabled: why !== '', server: s }
      }),
    [clusterServers]
  )
  const verificationOnReplica = verificationReplicaOptions.some((o) => o.value === verificationHost)
  const verificationTarget = verificationExternal || !verificationOnReplica ? 'external' : verificationHost
  const verificationTargetOptions = [{ name: 'External server', value: 'external' }, ...verificationReplicaOptions]
  // the failover candidates left once the picked replica is ignored: not the primary, not
  // ignored, not failed
  const verificationPickLastCandidate =
    verificationPick &&
    !verificationPick.ignored &&
    !(clusterServers || []).some(
      (s) => s.id !== verificationPick.id && s.state !== 'Master' && !s.ignored && !['Failed', 'Suspect', 'ErrorAuth'].includes(s.state)
    )
  // an external server: marked as a verification server, or empty (marked on its first test restore)
  const [verificationServer, setVerificationServer] = useState({ loading: false, state: null, error: '' })
  const [isVerificationMarkOpen, setIsVerificationMarkOpen] = useState(false)
  const loadVerificationServer = async () => {
    if (!selectedCluster?.name || verificationTarget !== 'external' || !verificationHost) {
      setVerificationServer({ loading: false, state: null, error: '' })
      return
    }
    setVerificationServer({ loading: true, state: null, error: '' })
    try {
      const { data, status } = await dispatch(getBackupVerificationServer({ clusterName: selectedCluster.name })).unwrap()
      setVerificationServer(status === 200 ? { loading: false, state: data, error: '' } : { loading: false, state: null, error: String(data || status) })
    } catch (e) {
      setVerificationServer({ loading: false, state: null, error: e?.errorMessage || 'not reachable' })
    }
  }
  useEffect(() => {
    if (!isVerificationCredentialOpen) loadVerificationServer()
  }, [selectedCluster?.name, verificationTarget, verificationHost, isVerificationCredentialOpen])
  const confirmVerificationMark = async () => {
    setIsVerificationMarkOpen(false)
    try {
      await dispatch(markBackupVerificationServer({ clusterName: selectedCluster?.name })).unwrap()
    } catch (e) {
      // the banner tells why
    }
    loadVerificationServer()
  }
  const cancelVerificationPick = () => {
    setVerificationPick(null)
    setVerificationDropdownKey((k) => k + 1)
  }
  const confirmVerificationPick = () => {
    const s = verificationPick
    if (!s.ignored) {
      dispatch(setAsIgnored({ clusterName: selectedCluster?.name, serverId: s.id }))
    }
    setVerificationExternal(false)
    dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-verification-host', value: btoa(s.url) }))
    setVerificationPick(null)
  }

  // Help content
  const hBackupEncryption = `**Backup Encryption**\n\nEncrypts newly created local backup artifacts (physical, logical, mydumper/dumpling, binlog) with the database root password configured in \`db-servers-credential\`, using the standard OpenSSL AES-256-CBC format.\n\nBackups can be decrypted without replication-manager:\n\n\`openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -md sha256 -in backup.enc -out backup\`\n\nA backup needs the root password that was valid when it was taken. Existing plaintext backups are unaffected; only backups created after enabling this switch are encrypted. Disabling it restores plaintext behavior.\n\nConfig: \`backup-encryption\``
  const hSaveScript = `**Custom Backup Save Script**\n\nExecutes a custom backup script instead of the built-in logical backup tools. When set, other logical backup options are ignored.\n\nThe script receives these parameters:\n1. DB Server Host\n2. Master Host\n3. DB Server Port\n4. Master Port\n5. DB User\n6. DB Password\n7. Cluster Name\n\nConfig: \`backup-save-script\``
  const hLoadScript = `**Custom Load Script**\n\nExecutes a custom script for loading/restoring backups.\n\nThe script receives these parameters:\n1. DB Server Host\n2. Master Host\n3. DB Server Port\n4. Master Port\n5. DB User\n6. DB Password\n7. Cluster Name\n\nConfig: \`backup-load-script\``
  const hLogicalBackup = `**Logical Backup**\n\nSelects the tool used for logical backups (mysqldump, mydumper, etc.).\nLogical backups produce SQL or structured data files that are portable across versions.\n\nConfig: \`backup-logical-type\``
  const hLogicalPostScript = `**Logical Backup Post-Script**\n\nScript executed after a logical backup completes.\n\nThe script receives:\n1. Cluster name\n2. DB Server Host\n3. DB Server Port\n4. Backup Path\n\nConfig: \`backup-logical-post-script\``
  const hDbClientOptions = `**DB Client Options**\n\nExtra command-line options passed to the MySQL client binary used during backup operations.\n\nConfig: \`backup-mysqlclient-options\``
  const hMysqldumpOptions = `**Mysqldump Options**\n\nAdditional command-line flags passed to mysqldump.\nExample: \`--single-transaction --triggers --routines\`\n\nConfig: \`backup-mysqldump-options\``
  const hMysqldumpPath = `**Mysqldump Path**\n\nThe dump client of this cluster's logical backups and reseeds. A MySQL/Percona cluster needs a MySQL client (the default is the MariaDB client, which can not dump MySQL 8.4). Empty: the default. The client version is read again when it changes.\n\nConfig: \`backup-mysqldump-path\``
  const hMysqlclientPath = `**MySQL Client Path**\n\nThe client that loads a logical backup into this cluster's servers. Empty: the default.\n\nConfig: \`backup-mysqlclient-path\``
  const hMysqlbinlogPath = `**Mysqlbinlog Path**\n\nThe binary log client of this cluster. Empty: the default.\n\nConfig: \`backup-mysqlbinlog-path\``
  const hDiscovery = `**Backup Discovery**\n\nAt the start of each server monitor, lists the logical backups placed in the backup folders by hand or by another tool (mysqldump files, splitdump and mydumper folders) that no metadata describes, with what their check found, in **Found in backup folders** (Maintenance). Nothing is registered: a backup is used only once the operator registers it, and never by an automatic restore.\n\nThe **Discover** button runs a discovery whatever this setting, and the registered backups are checked for changes (WARN0249) whatever it.\n\nConfig: \`backup-discovery\``
  const hDiscoveryCandidateMax = `**Backup Discovery Candidates**\n\nThe artifacts a discovery lists per server: beyond it, the oldest are not listed and WARN0250 is raised. 0: the default (100).\n\nConfig: \`backup-discovery-candidate-max\``
  const hVerification = `**Backup Verification**\n\nVerifies each logical (mysqldump) backup once it completes, at the verification level below. A backup can also be verified on demand from the backup list (Maintenance).\n\nThe result is shown in the backup list; a failed verification of a server's last logical backup raises ERR00117.\n\nConfig: \`backup-verification\``
  const hVerificationLevel = `**Backup Verification Level**\n\n- **Integrity**: reads the backup as a restore would, without a database: gzip to its end, the dump's \`-- Dump completed\` line, the position against the backup metadata, every file of a splitdump. An encrypted backup has its HMAC checked first.\n- **Test restore**: the integrity checks, then the backup is loaded into the verification server below, and each table's rows and each schema's tables, views, routines, triggers and events are compared with the dump.\n\nConfig: \`backup-verification-level\``
  const hVerificationHost = `**Backup Verification Server**\n\nWhere the test restore loads the backup:\n\n- **External server** (host:port): outside the cluster, of the same flavor and version as the cluster, with the credentials below. **It is disposable**: the whole backup is loaded, the mysql schema (accounts) included, and the backup's other schemas are dropped there before the load and after the comparison. It must be marked as a verification server (the table \`replication_manager_schema.backup_verification_server\`): a server without schemas of its own is marked on its first test restore, one that holds schemas is refused until marked with **Mark as verification server**. Never mark a server that holds data of its own.\n- **A replica of the cluster** (not the primary nor a relay): the test restore reseeds it: maintenance, replication stopped, load and comparison, then replication restarted from the backup's position. A replica not yet in ignored hosts is added to them when picked (never a failover candidate).\n\nConfig: \`backup-verification-host\``
  const hVerificationTimeout = `**Backup Verification Timeout**\n\nSeconds a test restore may run, its load and its comparison, before it is canceled and fails. A replica of the cluster whose test restore is canceled stays in maintenance, its replication stopped: reseed it. 0 disables the limit.\n\nConfig: \`backup-verification-timeout\``
  const hVerificationRegisteredProven = `**Reseed From Registered Backups Once Proven**\n\nA registered backup (registered from a backup folder) is a file replication-manager did not take. When on, **Reseed from this backup** is refused for it until a test restore of its file passed (the verification server above); the automatic rejoin already requires the same with a \`Proven\` backup selector.\n\nWhen off, a registered backup is reseeded from unless its last verification of its current file failed (WARN0253), which always refuses it.\n\nConfig: \`backup-verification-registered-proven\``
  const hVerificationCredential = `**Backup Verification Credentials**\n\nThe user:password replication-manager connects to the verification server with. The test restore loads the backup's accounts: use an account the backups hold, such as the cluster's database credentials.\n\nConfig: \`backup-verification-credential\``
  const hSplitDump = `**Mysqldump Splitdump Output**\n\nSends mysqldump output through replication-manager-cli splitdump instead of a single .sql.gz file.\nOutput is written to a splitdump directory (uncompressed).\nRequires the CLI to be available in PATH or configured below.\n\nConfig: \`backup-mysqldump-splitdump\``
  const hSplitDumpSize = `**Splitdump Shard Size**\n\nMaximum shard size for splitdump output. Default: 1G. Set to 0 to disable sharding.\n\nConfig: \`backup-splitdump-file-size\``
  const hSplitDumpCreateDatabases = `**Auto-create databases for splitdump restore**\n\nWhen enabled, missing databases are automatically created before applying splitdump schema files during restore.\n\nConfig: \`backup-splitdump-create-databases\``
  const hRestoreDefinerStrict = `**Strict DEFINER enforcement on restore**\n\nWhen enabled, restore fails if an incompatible DEFINER clause is detected (e.g. the definer user does not exist on the target). When disabled (default), the file is re-imported with DEFINER clauses stripped and a warning is logged.\n\nConfig: \`backup-restore-definer-strict\``
  const hCliPath = `**Replication Manager CLI Path**\n\nPath to the replication-manager-cli binary used for splitdump processing.\nLeave empty to use PATH lookup.\n\nConfig: \`replication-manager-cli-path\``
  const hMydumperOptions = `**Mydumper Options**\n\nAdditional command-line flags passed to mydumper.\n\nConfig: \`backup-mydumper-options\``
  const hMydumperRegex = `**Mydumper Regex**\n\nRegular expression to filter which tables mydumper includes in the backup.\n\nConfig: \`backup-mydumper-regex\``
  const hMyloaderOptions = `**Myloader Options**\n\nAdditional command-line flags passed to myloader during restore.\n\nConfig: \`backup-myloader-options\``
  const hLoadThreads = `**Logical Restore Threads**\n\nNumber of parallel threads used when restoring a logical/splitdump backup (passed as \`--threads\` to myloader/splitdump).\nHigher values speed up restore at the cost of more CPU and I/O on the target. Default: 2.\n\nConfig: \`backup-logical-load-threads\``
  const hRemotePickup = `**Remote Dump Pickup Timeout**\n\nWhen \`mysqldump\` is listed in Force Remote Execution (Scheduler settings), the dump runs on the database node through dbjobs and streams back to replication-manager.\nThis is how long, in seconds, a backup waits for dbjobs to take the dump before it fails. 0 uses the default: 300.\n\nConfig: \`backup-logical-remote-pickup-timeout\``
  const hRemoteReport = `**Remote Dump Report Timeout**\n\nFor a dump run on the database node (Force Remote Execution lists \`mysqldump\`): how long, in seconds, the backup waits after the end of the stream for dbjobs to report the exit status of the dump client. 0 uses the default: 120.\n\nConfig: \`backup-logical-remote-report-timeout\``
  const hRemoteArm = `**Remote Dump Arm Timeout**\n\nFor a dump run on the database node (Force Remote Execution lists \`mysqldump\`): how long, in seconds, dbjobs' request for a receiver waits for the backup to be ready for its stream. 0 uses the default: 10.\n\nConfig: \`backup-logical-remote-arm-timeout\``
  const hDumpThreads = `**Logical Dump Threads**\n\nNumber of parallel threads used when taking a logical/splitdump backup.\nHigher values speed up the dump at the cost of more load on the source. Default: 2.\n\nConfig: \`backup-logical-dump-threads\``
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
  const hCheckFreeSpace = `**Check Free Space Before Backup**\n\nVerifies that sufficient disk space is available before starting a backup.\nPrevents partial backups that could corrupt the backup directory.\n\nConfig: \`backup-check-free-space\``
  const hDiskWarn = `**Disk Usage Warning Threshold**\n\nDisk usage percentage at which a warning alert is triggered.\n\nConfig: \`backup-disk-treshold-warn\``
  const hDiskCrit = `**Disk Usage Critical Threshold**\n\nDisk usage percentage at which backup is blocked and a critical alert is triggered.\n\nConfig: \`backup-disk-treshold-crit\``
  const hEstimateSize = `**Estimate Backup Size**\n\nEstimates the expected backup size before starting, using the last backup size or information_schema data.\nUsed to verify sufficient free space is available.\n\nConfig: \`backup-estimate-size\``
  const hGrowthPercentage = `**Last Backup Growth Percentage**\n\nExpected growth percentage applied to the last backup size when estimating the next backup size.\nSet to 0 to use the last backup size without adjustment.\n\nConfig: \`backup-growth-percentage\``
  const hEstimatePercentage = `**Backup Estimation Percentage from information_schema**\n\nPercentage of the information_schema size used as the backup size estimate when no previous backup exists.\n\nConfig: \`backup-estimate-size-percentage\``

  const dataObject = [
    {
      key: 'Backup Encryption',
      help: h(hBackupEncryption, 'Backup Encryption'),
      value: (
        <RMSwitch isChecked={selectedCluster?.config?.backupEncryption}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for backup-encryption? Local backups will be encrypted with the database root password (db-servers-credential); restoring them needs that password.'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-encryption' }))} />
      )
    },
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
      key: 'Mysqldump Path',
      help: h(hMysqldumpPath, 'Mysqldump Path'),
      value: (
        <TextForm value={selectedCluster?.config?.backupMysqldumpPath} confirmTitle={`Confirm backup-mysqldump-path to `}
          maxLength={1024} className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-mysqldump-path', value }))} />
      )
    },
    {
      key: 'MySQL Client Path',
      help: h(hMysqlclientPath, 'MySQL Client Path'),
      value: (
        <TextForm value={selectedCluster?.config?.backupMysqlclientPath} confirmTitle={`Confirm backup-mysqlclient-path to `}
          maxLength={1024} className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-mysqlclient-path', value }))} />
      )
    },
    {
      key: 'Mysqlbinlog Path',
      help: h(hMysqlbinlogPath, 'Mysqlbinlog Path'),
      value: (
        <TextForm value={selectedCluster?.config?.backupMysqlbinlogPath} confirmTitle={`Confirm backup-mysqlbinlog-path to `}
          maxLength={1024} className={styles.textbox}
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-mysqlbinlog-path', value }))} />
      )
    },
    {
      key: 'Backup Discovery',
      help: h(hDiscovery, 'Backup Discovery'),
      value: (
        <RMSwitch isChecked={selectedCluster?.config?.backupDiscovery}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for backup-discovery?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-discovery' }))} />
      )
    },
    {
      key: 'Backup Discovery Candidates',
      help: h(hDiscoveryCandidateMax, 'Backup Discovery Candidates'),
      value: (
        <NumberInput min={0} value={selectedCluster?.config?.backupDiscoveryCandidateMax}
          isDisabled={user?.grants['cluster-settings'] == false}
          showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'backup-discovery-candidate-max' to: `}
          onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-discovery-candidate-max', value }))} />
      )
    },
    {
      key: 'Backup Verification',
      help: h(hVerification, 'Backup Verification'),
      value: (
        <RMSwitch isChecked={selectedCluster?.config?.backupVerification}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for backup-verification?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-verification' }))} />
      )
    },
    {
      key: 'Backup Verification Level',
      help: h(hVerificationLevel, 'Backup Verification Level'),
      value: (
        <Dropdown options={verificationLevelOptions} className={styles.dropdownButton}
          selectedValue={selectedCluster?.config?.backupVerificationLevel || 'integrity'}
          confirmTitle={`Confirm backup-verification-level to`}
          isDisabled={user?.grants['cluster-settings'] == false}
          onChange={(level) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-verification-level', value: level }))} />
      )
    },
    {
      key: 'Backup Verification Timeout',
      help: h(hVerificationTimeout, 'Backup Verification Timeout'),
      value: (
        <NumberInput min={0} value={selectedCluster?.config?.backupVerificationTimeout}
          isDisabled={user?.grants['cluster-settings'] == false}
          showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'backup-verification-timeout' to: `}
          onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-verification-timeout', value }))} />
      )
    },
    {
      key: 'Backup Verification Server',
      help: h(hVerificationHost, 'Backup Verification Server'),
      value: (
        <Flex direction='column' gap={2} align='flex-start'>
          <Dropdown key={verificationDropdownKey} options={verificationTargetOptions} className={styles.dropdownButton}
            selectedValue={verificationTarget}
            isDisabled={user?.grants['cluster-settings'] == false}
            onChange={(option) => {
              if (option.value === 'external') {
                setVerificationExternal(true)
                return
              }
              setVerificationPick(option.server)
            }} />
          {verificationTarget === 'external' && (
            <TextForm value={verificationOnReplica ? '' : verificationHost} confirmTitle={`Confirm backup-verification-host to `}
              maxLength={255} className={styles.textbox}
              onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-verification-host', value: btoa(value) }))} />
          )}
          {verificationTarget === 'external' && verificationHost && (
            <HStack gap={2} wrap='wrap'>
              {verificationServer.loading ? (
                <>
                  <Spinner size='xs' />
                  <Text fontSize='sm'>Checking the server</Text>
                </>
              ) : verificationServer.error ? (
                <Text fontSize='sm' color='red.500'>Not reachable: {verificationServer.error}</Text>
              ) : verificationServer.state?.marked ? (
                <Text fontSize='sm'>Marked as a verification server</Text>
              ) : verificationServer.state?.empty ? (
                <Text fontSize='sm'>No schema of its own: marked on its first test restore</Text>
              ) : verificationServer.state ? (
                <>
                  <Text fontSize='sm' color='orange.500'>
                    Not marked, holds {verificationServer.state.schemas.join(', ')}: the test restore refuses it
                  </Text>
                  <RMButton isDisabled={user?.grants['db-restore'] == false} onClick={() => setIsVerificationMarkOpen(true)}>
                    Mark as verification server
                  </RMButton>
                </>
              ) : null}
            </HStack>
          )}
        </Flex>
      )
    },
    ...(verificationTarget === 'external'
      ? [
          {
            key: 'Backup Verification Credentials',
            help: h(hVerificationCredential, 'Backup Verification Credentials'),
            value: (
              <RMButton isDisabled={user?.grants['cluster-settings'] == false} onClick={() => setIsVerificationCredentialOpen(true)}>
                Set credentials
              </RMButton>
            )
          }
        ]
      : []),
    {
      key: 'Reseed From Registered Backups Once Proven',
      help: h(hVerificationRegisteredProven, 'Reseed From Registered Backups Once Proven'),
      value: (
        <RMSwitch isChecked={selectedCluster?.config?.backupVerificationRegisteredProven}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for backup-verification-registered-proven?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-verification-registered-proven' }))} />
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
      key: 'Auto-create databases for splitdump restore',
      help: h(hSplitDumpCreateDatabases, 'Auto-create databases for splitdump restore'),
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.backupSplitdumpCreateDatabases}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for backup-splitdump-create-databases?'}
          onChange={() =>
            dispatch(
              switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-splitdump-create-databases' })
            )
          }
        />
      )
    },
    {
      key: 'Strict DEFINER enforcement on restore',
      help: h(hRestoreDefinerStrict, 'Strict DEFINER enforcement on restore'),
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.backupRestoreDefinerStrict}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for backup-restore-definer-strict?'}
          onChange={() =>
            dispatch(
              switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-restore-definer-strict' })
            )
          }
        />
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
      key: 'Logical Restore Threads',
      help: h(hLoadThreads, 'Logical Restore Threads'),
      value: (
        <NumberInput min={1} max={64} value={selectedCluster?.config?.backupLogicalLoadThreads}
          showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change logical restore threads to: `}
          onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-logical-load-threads', value }))} />
      )
    },
    {
      key: 'Logical Dump Threads',
      help: h(hDumpThreads, 'Logical Dump Threads'),
      value: (
        <NumberInput min={1} max={64} value={selectedCluster?.config?.backupLogicalDumpThreads}
          showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change logical dump threads to: `}
          onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-logical-dump-threads', value }))} />
      )
    },
    {
      key: 'Remote Dump Pickup Timeout',
      help: h(hRemotePickup, 'Remote Dump Pickup Timeout'),
      value: (
        <NumberInput min={0} value={selectedCluster?.config?.backupLogicalRemotePickupTimeout}
          showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'backup-logical-remote-pickup-timeout' to: `}
          onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-logical-remote-pickup-timeout', value }))} />
      )
    },
    {
      key: 'Remote Dump Report Timeout',
      help: h(hRemoteReport, 'Remote Dump Report Timeout'),
      value: (
        <NumberInput min={0} value={selectedCluster?.config?.backupLogicalRemoteReportTimeout}
          showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'backup-logical-remote-report-timeout' to: `}
          onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-logical-remote-report-timeout', value }))} />
      )
    },
    {
      key: 'Remote Dump Arm Timeout',
      help: h(hRemoteArm, 'Remote Dump Arm Timeout'),
      value: (
        <NumberInput min={0} value={selectedCluster?.config?.backupLogicalRemoteArmTimeout}
          showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'backup-logical-remote-arm-timeout' to: `}
          onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-logical-remote-arm-timeout', value }))} />
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
      key: 'Check Free Space',
      value: [
        { key: 'Check Free Space Before Backup', help: h(hCheckFreeSpace, 'Check Free Space Before Backup'), value: (<RMSwitch isChecked={selectedCluster?.config?.backupCheckFreeSpace} isDisabled={user?.grants['cluster-settings'] == false} confirmTitle={'Confirm switch settings for backup-check-free-space?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'backup-check-free-space' }))} />) },
        ...(selectedCluster?.config?.backupCheckFreeSpace ? [
          { key: 'Disk Usage Warning Threshold', help: h(hDiskWarn, 'Disk Usage Warning Threshold'), value: (<NumberInput min={1} max={100} value={selectedCluster?.config?.backupDiskTresholdWarn} showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'backup-disk-treshold-warn' to: `} onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-disk-treshold-warn', value }))} />) },
          { key: 'Disk Usage Critical Threshold', help: h(hDiskCrit, 'Disk Usage Critical Threshold'), value: (<NumberInput min={1} max={100} value={selectedCluster?.config?.backupDiskTresholdCrit} showEditButton={true} showConfirmModal={true} confirmTitle={`Confirm change 'backup-disk-treshold-crit' to: `} onConfirm={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'backup-disk-treshold-crit', value }))} />) },
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
      {verificationPick && (
        <ConfirmModal isOpen={!!verificationPick} closeModal={cancelVerificationPick}
          title={`Use ${verificationPick.url} as the backup verification server?`}
          body={
            <>
              <p>
                Each test restore reseeds it: maintenance, replication stopped, the backup loaded and compared, then
                replication restarted from the backup's position.
                {verificationPick.ignored ? '' : ' It is added to the ignored hosts: an ignored server is never a failover candidate.'}
              </p>
              {verificationPickLastCandidate && (
                <p>
                  <strong>It is the last replica available for failover:</strong> once it is ignored, a failover of the
                  primary has no candidate.
                </p>
              )}
            </>
          }
          onConfirmClick={confirmVerificationPick} />
      )}
      {isVerificationMarkOpen && verificationServer.state && (
        <ConfirmModal isOpen={isVerificationMarkOpen} closeModal={() => setIsVerificationMarkOpen(false)}
          title={`Mark ${verificationServer.state.host} as the backup verification server?`}
          body={
            <>
              <p>
                It holds {verificationServer.state.schemas.join(', ')}. Each test restore loads the whole backup there,
                the mysql schema (accounts) included, and drops the schemas the backup holds before the load and after
                the comparison: a schema of the same name is lost.
              </p>
              <p>
                <strong>Never mark a server that holds data of its own</strong> (a primary, a replica, a VIP or a proxy).
              </p>
            </>
          }
          onConfirmClick={confirmVerificationMark} />
      )}
      {isVerificationCredentialOpen && (
        <SetCredentialsModal clusterName={selectedCluster?.name} isOpen={isVerificationCredentialOpen}
          type='backup-verification-credential' closeModal={() => setIsVerificationCredentialOpen(false)} />
      )}
    </Flex>
  )
}

export default BackupSettings
