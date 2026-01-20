import { useDispatch, useSelector } from 'react-redux'
import {
  FormControl,
  FormLabel,
  HStack,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  NumberInput,
  NumberInputField,
  Radio,
  RadioGroup,
  Select,
  Stack,
  Switch,
  Text
} from '@chakra-ui/react'
import MenuOptions from '../../../../components/MenuOptions'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import RMButton from '../../../../components/RMButton'
import {
  dropServer,
  flushLogs,
  getBackups,
  jobsUpgrade,
  logicalBackup,
  optimizeServer,
  physicalBackupMaster,
  pitrRestore,
  promoteToLeader,
  provisionDatabase,
  getResticSnapshot,
  reseedLogicalFromBackup,
  reseedLogicalFromMaster,
  reseedPhysicalFromBackup,
  reseedFromRestic,
  resetMaster,
  resetSlaveAll,
  runRemoteJobs,
  setAsIgnored,
  setAsPreferred,
  setAsUnrated,
  setMaintenanceMode,
  skipReplicationEvent,
  startDatabase,
  restartDatabase,
  startSlave,
  stopDatabase,
  stopSlave,
  toggleInnodbMonitor,
  toggleReadOnly,
  toggleSlowQueryCapture,
  unprovisionDatabase
} from '../../../../redux/clusterSlice'
import { useState, useEffect, useCallback, useMemo } from 'react'
import { useHref } from 'react-router-dom'
import { generateConfig } from '../../../../redux/configSlice'
import { useTheme } from '../../../../ThemeProvider'
import parentStyles from '../../../../components/Modals/styles.module.scss'

// Constants
const JOBS_CONTAINER_RID = 'container#jobs'
const RESTIC_BACKUP_TYPE_TAG = 'backup-type'

const normalizeResticTags = (tags) => {
  if (Array.isArray(tags)) {
    return tags
  }
  if (typeof tags === 'string') {
    return tags.split(',').map((tag) => tag.trim()).filter(Boolean)
  }
  return []
}

const normalizeResticTagCategory = (value) => {
  if (!value || typeof value !== 'string') {
    return ''
  }
  return value.trim().toLowerCase().replace(/[_\s]+/g, '-')
}

const getResticBackupType = (tags) => {
  const normalizedTags = normalizeResticTags(tags)
  for (const tag of normalizedTags) {
    if (typeof tag !== 'string') {
      continue
    }
    const trimmed = tag.trim()
    if (!trimmed) {
      continue
    }
    const separatorIndex = trimmed.indexOf(':')
    if (separatorIndex === -1) {
      continue
    }
    const category = normalizeResticTagCategory(trimmed.slice(0, separatorIndex))
    if (category !== RESTIC_BACKUP_TYPE_TAG) {
      continue
    }
    const value = trimmed.slice(separatorIndex + 1).trim().toLowerCase()
    if (value) {
      return value
    }
  }
  return ''
}

const getSnapshotTimeValue = (snapshot) => {
  if (!snapshot || typeof snapshot.time !== 'string') {
    return 0
  }
  const parsed = Date.parse(snapshot.time)
  return Number.isNaN(parsed) ? 0 : parsed
}

const isSupportedReseedBackupType = (backupType) => {
  return backupType === '' || backupType === 'logical' || backupType === 'physical'
}

const normalizeSnapshotPath = (value) => {
  if (!value || typeof value !== 'string') {
    return ''
  }
  const trimmed = value.trim()
  if (!trimmed) {
    return ''
  }
  const normalized = trimmed.replace(/\/+$/, '')
  return normalized === '' ? '/' : normalized
}

const escapeRegExp = (value) => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

const joinSnapshotPath = (basePath, suffix) => {
  if (!basePath || !suffix) {
    return basePath || ''
  }
  if (basePath.endsWith(`/${suffix}`) || basePath.endsWith(suffix)) {
    return basePath
  }
  return `${basePath}/${suffix}`
}

const getResticSnapshotPrimaryPath = (snapshot) => {
  if (!snapshot) {
    return ''
  }
  const paths = Array.isArray(snapshot.paths) ? snapshot.paths : []
  for (const path of paths) {
    if (typeof path === 'string') {
      const trimmed = normalizeSnapshotPath(path)
      if (trimmed) {
        return trimmed
      }
    }
  }
  return ''
}

const getResticSnapshotBackupPath = ({
  snapshot,
  reseedType,
  backupLogicalType,
  backupPhysicalType,
  compressBackups
}) => {
  const basePath = getResticSnapshotPrimaryPath(snapshot)
  if (!basePath) {
    return ''
  }

  const normalizedReseedType = typeof reseedType === 'string' ? reseedType.toLowerCase() : ''
  if (normalizedReseedType === 'logical') {
    const tool = typeof backupLogicalType === 'string' ? backupLogicalType.toLowerCase() : ''
    switch (tool) {
      case 'dumpling': {
        const adhocPattern = /\/dumpling\.\d+$/
        if (adhocPattern.test(basePath) || basePath.endsWith('/dumpling')) {
          return basePath
        }
        return joinSnapshotPath(basePath, 'dumpling')
      }
      case 'mydumper': {
        const adhocPattern = /\/mydumper\.\d+$/
        if (adhocPattern.test(basePath) || basePath.endsWith('/mydumper')) {
          return basePath
        }
        return joinSnapshotPath(basePath, 'mydumper')
      }
      case 'mysqldump':
      default: {
        const adhocPattern = /\/mysqldump\.\d+\.sql\.gz$/
        if (adhocPattern.test(basePath) || basePath.endsWith('/mysqldump.sql.gz')) {
          return basePath
        }
        return joinSnapshotPath(basePath, 'mysqldump.sql.gz')
      }
    }
  }

  if (normalizedReseedType === 'physical') {
    const tool = typeof backupPhysicalType === 'string' ? backupPhysicalType : ''
    if (!tool) {
      return basePath
    }
    const ext = compressBackups ? '.xbtream.gz' : '.xbtream'
    const fileName = `${tool}${ext}`
    const adhocPattern = new RegExp(`/${escapeRegExp(tool)}\\.\\d+${escapeRegExp(ext)}$`)
    if (adhocPattern.test(basePath) || basePath.endsWith(`/${fileName}`)) {
      return basePath
    }
    return joinSnapshotPath(basePath, fileName)
  }

  return basePath
}

const normalizeBackupValue = (value) => {
  if (value === null || value === undefined) {
    return ''
  }
  if (typeof value === 'string') {
    return value.trim()
  }
  return String(value)
}

const getBackupLineValue = (backup) => {
  return normalizeBackupValue(backup?.backup_line ?? backup?.backupLine).toLowerCase()
}

const getBackupRetentionDays = (backup) => {
  const raw = backup?.retention_days ?? backup?.retentionDays ?? 0
  const parsed = Number(raw)
  return Number.isFinite(parsed) ? parsed : 0
}

const getBackupToolValue = (backup) => {
  return normalizeBackupValue(backup?.backup_tool ?? backup?.backupTool)
}

const getBackupMethodType = (backup) => {
  const rawMethod = backup?.backup_method ?? backup?.backupMethod
  if (rawMethod !== null && rawMethod !== undefined) {
    if (typeof rawMethod === 'number') {
      if (rawMethod === 1) {
        return 'logical'
      }
      if (rawMethod === 2) {
        return 'physical'
      }
    }
    const normalized = normalizeBackupValue(rawMethod).toLowerCase()
    if (normalized === '1' || normalized === 'logical') {
      return 'logical'
    }
    if (normalized === '2' || normalized === 'physical') {
      return 'physical'
    }
  }

  const tool = getBackupToolValue(backup).toLowerCase()
  if (tool === 'xtrabackup' || tool === 'mariabackup') {
    return 'physical'
  }
  if (tool === 'mysqldump' || tool === 'mydumper' || tool === 'dumpling' || tool === 'script') {
    return 'logical'
  }
  return ''
}

const getBackupStartTimestampMs = (backup) => {
  const raw = backup?.start_time ?? backup?.startTime
  if (typeof raw === 'number') {
    return raw > 1e12 ? raw : raw * 1000
  }
  if (typeof raw === 'string') {
    const parsed = Date.parse(raw)
    return Number.isNaN(parsed) ? 0 : parsed
  }
  const idValue = Number(backup?.id ?? backup?.Id ?? 0)
  if (Number.isFinite(idValue) && idValue > 0) {
    return idValue > 1e12 ? idValue : idValue * 1000
  }
  return 0
}

const backupMatchesServer = (backup, row) => {
  if (!backup || !row) {
    return true
  }
  const backupServerId = backup?.server_id ?? backup?.serverId
  if (backupServerId !== null && backupServerId !== undefined && row?.id !== null && row?.id !== undefined) {
    return String(backupServerId) === String(row.id)
  }
  const source = normalizeBackupValue(backup?.source ?? backup?.Source)
  if (source && row?.host && row?.port) {
    return source === `${row.host}:${row.port}`
  }
  return true
}

/**
 * ServerMenu - Context menu for database server operations
 * 
 * Provides a hierarchical menu of database operations organized by category (Maintenance, 
 * Backup, Provision, DB Utils). All operations are permission-based and require user confirmation
 * for destructive actions.
 * 
 * Configuration-Based Database Management:
 * - Uses configuration files (01_preserved.cnf, 02_delta.cnf, 03_agreed.cnf) for database startup
 * - 01_preserved.cnf: User-accepted differences (highest precedence)
 * - 02_delta.cnf: Detected differences between deployed and config
 * - 03_agreed.cnf: Variables that should match between systems
 * 
 * @param {Object} props - Component properties
 * @param {string} props.clusterName - Name of the database cluster
 * @param {string} props.clusterMasterId - ID of the master server in the cluster
 * @param {string} props.backupPhysicalType - Physical backup type (e.g., 'xtrabackup', 'mariabackup')
 * @param {string} props.backupLogicalType - Logical backup type (e.g., 'mysqldump', 'mydumper')
 * @param {boolean} props.backupRestic - Whether restic backups are enabled
 * @param {string} props.orchestrator - Orchestrator type (e.g., 'opensvc', 'kubernetes')
 * @param {Object} props.row - Server data object
 * @param {string} props.row.id - Server ID
 * @param {string} props.row.host - Server hostname/IP
 * @param {number} props.row.port - Server port
 * @param {boolean} props.row.isSlave - Whether server is a slave
 * @param {boolean} props.row.prefered - Whether server is marked as preferred for failover
 * @param {boolean} props.row.preferedBackup - Whether server is the preferred backup server
 * @param {boolean} props.row.ignored - Whether server is ignored for failover
 * @param {Object} props.user - User object with permission grants
 * @param {Object} props.user.grants - User permission grants object
 * @param {boolean} props.isDesktop - Whether rendering on desktop (affects menu placement)
 * @param {string} [props.from='tableView'] - Origin context ('tableView' or other)
 * @param {Function} props.openCompareModal - Callback to open server comparison modal
 * @param {string} [props.colorScheme] - Color scheme for the menu
 * @param {string} [props.className] - Additional CSS classes
 * @param {boolean} [props.showCompareWithOption=true] - Whether to show compare option
 * @param {boolean} [props.showTerminal=false] - Whether to show terminal options
 * 
 * @returns {JSX.Element} ServerMenu component with context menu and confirmation modal
 */
function ServerMenu({
  clusterName,
  clusterMasterId,
  backupPhysicalType,
  backupLogicalType,
  backupRestic,
  orchestrator,
  row,
  user,
  isDesktop,
  from = 'tableView',
  openCompareModal,
  colorScheme,
  className,
  showCompareWithOption = true,
  showTerminal = false
}) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const backupsList = useSelector((state) => state.cluster?.backups?.list)
  const resticSnapshots = useSelector((state) => state.cluster?.restic?.snapshots)
  const compressBackups = useSelector((state) => state.cluster?.clusterData?.config?.compressBackups)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [serverName, setServerName] = useState('')
  const [isAdvancedBackupOpen, setIsAdvancedBackupOpen] = useState(false)
  const [advancedBackupType, setAdvancedBackupType] = useState('logical')
  const [advancedBackupLine, setAdvancedBackupLine] = useState('default')
  const [advancedRetentionDays, setAdvancedRetentionDays] = useState(0)
  const [advancedResticEnabled, setAdvancedResticEnabled] = useState(Boolean(backupRestic))
  const [isAdvancedReseedOpen, setIsAdvancedReseedOpen] = useState(false)
  const [advancedReseedType, setAdvancedReseedType] = useState('logical')
  const [advancedReseedSource, setAdvancedReseedSource] = useState('backup')
  const [advancedReseedLine, setAdvancedReseedLine] = useState('default')
  const [advancedReseedBackupId, setAdvancedReseedBackupId] = useState('')
  const [advancedReseedResticSnapshot, setAdvancedReseedResticSnapshot] = useState('')
  const [advancedReseedResticFilePath, setAdvancedReseedResticFilePath] = useState('')
  const [advancedReseedResticMode, setAdvancedReseedResticMode] = useState('dump')
  const [advancedReseedResticInPlace, setAdvancedReseedResticInPlace] = useState(false)
  const [advancedReseedResticUseSourcePath, setAdvancedReseedResticUseSourcePath] = useState(false)
  const [pitrBackupId, setPitrBackupId] = useState('')
  const [pitrRestoreTime, setPitrRestoreTime] = useState('')
  const [pitrUseBinlog, setPitrUseBinlog] = useState(false)

  const getHref = useHref('/').replace(/\/+$/, '');

  const normalizedBackupsList = useMemo(() => {
    if (!backupsList) {
      return []
    }
    return Array.isArray(backupsList) ? backupsList : Object.values(backupsList)
  }, [backupsList])

  const normalizedBackupLogicalType = useMemo(() => {
    return typeof backupLogicalType === 'string' ? backupLogicalType.toLowerCase() : ''
  }, [backupLogicalType])

  const isResticMysqldump = normalizedBackupLogicalType === 'mysqldump'
  const isResticMydumper = normalizedBackupLogicalType === 'mydumper'
  const isResticPhysical = advancedReseedType === 'physical'
  const defaultResticMode = useMemo(() => {
    if (isResticPhysical) {
      return 'restore'
    }
    return isResticMysqldump ? 'dump' : 'restore'
  }, [isResticPhysical, isResticMysqldump])

  const normalizedReseedTool = useMemo(() => {
    if (advancedReseedType === 'physical') {
      return typeof backupPhysicalType === 'string' ? backupPhysicalType.toLowerCase() : ''
    }
    return normalizedBackupLogicalType
  }, [advancedReseedType, backupPhysicalType, normalizedBackupLogicalType])

  const resticSnapshotsForReseed = useMemo(() => {
    if (!isAdvancedReseedOpen || !backupRestic || !Array.isArray(resticSnapshots)) {
      return []
    }
    const backupType = typeof advancedReseedType === 'string' ? advancedReseedType.toLowerCase() : ''

    const groups = new Map()
    resticSnapshots.forEach((snapshot, index) => {
      if (!snapshot) {
        return
      }
      const snapshotType = getResticBackupType(snapshot.tags)
      if (!isSupportedReseedBackupType(snapshotType)) {
        return
      }
      const treeKey = snapshot.tree || snapshot.id
      if (!treeKey) {
        return
      }
      const entry = {
        snapshot,
        index,
        timeValue: getSnapshotTimeValue(snapshot),
        backupType: snapshotType
      }
      const existing = groups.get(treeKey)
      if (existing) {
        existing.push(entry)
      } else {
        groups.set(treeKey, [entry])
      }
    })

    const selections = []
    groups.forEach((entries) => {
      const matching = entries.filter((entry) => entry.backupType === backupType)
      const candidates = matching.length > 0 ? matching : entries
      let chosen = candidates[0]
      for (const candidate of candidates.slice(1)) {
        if (candidate.timeValue > chosen.timeValue) {
          chosen = candidate
        }
      }
      selections.push(chosen)
    })

    selections.sort((a, b) => a.index - b.index)
    return selections.map((entry) => entry.snapshot)
  }, [resticSnapshots, advancedReseedType, isAdvancedReseedOpen, backupRestic])

  const adhocBackupsForReseed = useMemo(() => {
    if (!normalizedBackupsList.length) {
      return []
    }
    const expectedType = advancedReseedType === 'physical' ? 'physical' : 'logical'
    const filtered = normalizedBackupsList.filter((backup) => {
      if (!backup) {
        return false
      }
      const line = getBackupLineValue(backup)
      const isAdhoc = line === 'adhoc' || getBackupRetentionDays(backup) > 0
      if (!isAdhoc) {
        return false
      }
      const methodType = getBackupMethodType(backup)
      if (methodType && methodType !== expectedType) {
        return false
      }
      return true
    })
    const matching = filtered.filter((backup) => backupMatchesServer(backup, row))
    const candidates = matching.length > 0 ? matching : filtered
    return candidates.sort((a, b) => getBackupStartTimestampMs(b) - getBackupStartTimestampMs(a))
  }, [normalizedBackupsList, advancedReseedType, row])

  const defaultBackupsForReseed = useMemo(() => {
    if (!normalizedBackupsList.length) {
      return []
    }
    const expectedType = advancedReseedType === 'physical' ? 'physical' : 'logical'
    const filtered = normalizedBackupsList.filter((backup) => {
      if (!backup) {
        return false
      }
      const line = getBackupLineValue(backup)
      const isAdhoc = line === 'adhoc' || getBackupRetentionDays(backup) > 0
      if (isAdhoc) {
        return false
      }
      const methodType = getBackupMethodType(backup)
      if (methodType && methodType !== expectedType) {
        return false
      }
      const backupTool = getBackupToolValue(backup).toLowerCase()
      if (normalizedReseedTool && backupTool && backupTool !== normalizedReseedTool) {
        return false
      }
      return true
    })
    const matching = filtered.filter((backup) => backupMatchesServer(backup, row))
    const candidates = matching.length > 0 ? matching : filtered
    return candidates.sort((a, b) => getBackupStartTimestampMs(b) - getBackupStartTimestampMs(a))
  }, [normalizedBackupsList, advancedReseedType, normalizedReseedTool, row])

  const selectedReseedBackup = useMemo(() => {
    if (advancedReseedLine === 'adhoc') {
      return adhocBackupsForReseed.find(
        (backup) => String(backup?.id ?? backup?.Id ?? '') === String(advancedReseedBackupId)
      ) || null
    }
    return defaultBackupsForReseed[0] || null
  }, [advancedReseedLine, adhocBackupsForReseed, advancedReseedBackupId, defaultBackupsForReseed])

  const selectedReseedBackupLabel = useMemo(() => {
    if (!selectedReseedBackup) {
      return ''
    }
    const backupId = selectedReseedBackup?.id ?? selectedReseedBackup?.Id
    const backupTool = getBackupToolValue(selectedReseedBackup) || 'backup'
    const backupLine = getBackupLineValue(selectedReseedBackup)
    const backupTime = getBackupStartTimestampMs(selectedReseedBackup)
    const backupSource = normalizeBackupValue(selectedReseedBackup?.source ?? selectedReseedBackup?.Source)
    const timeLabel = backupTime ? new Date(backupTime).toLocaleString() : 'Unknown time'
    return `${backupTool} - ${timeLabel}${backupLine ? ` (${backupLine})` : ''}${backupSource ? ` [${backupSource}]` : ''}${backupId ? ` #${backupId}` : ''}`
  }, [selectedReseedBackup])

  const selectedResticSnapshot = useMemo(() => {
    if (!advancedReseedResticSnapshot) {
      return null
    }
    return resticSnapshotsForReseed.find((snapshot) => snapshot.id === advancedReseedResticSnapshot) || null
  }, [resticSnapshotsForReseed, advancedReseedResticSnapshot])

  const selectedResticSnapshotPath = useMemo(() => {
    return getResticSnapshotBackupPath({
      snapshot: selectedResticSnapshot,
      reseedType: advancedReseedType,
      backupLogicalType,
      backupPhysicalType,
      compressBackups: Boolean(compressBackups)
    })
  }, [selectedResticSnapshot, advancedReseedType, backupLogicalType, backupPhysicalType, compressBackups])

  const resticRestoreLabel = isResticPhysical
    ? 'Restore to disk (physical)'
    : 'Restore to disk (mysqldump/mydumper)'
  const resticMountLabel = isResticPhysical
    ? 'Mount repo (physical)'
    : 'Mount repo (mysqldump/mydumper)'

  const resticReseedModeHelp = useMemo(() => {
    switch (advancedReseedResticMode) {
      case 'restore':
        if (isResticPhysical) {
          return 'Restores the physical backup file to local disk, then runs SST. Uses disk space but works without FUSE.'
        }
        if (isResticMysqldump) {
          return 'Restores the dump file to local disk, then loads it with mysql client. Uses disk space and works with mysqldump files.'
        }
        return 'Restores the snapshot to local disk, then runs myloader. Uses disk space but works with mydumper directories.'
      case 'mount':
        if (isResticPhysical) {
          return 'Mounts the restic repo via FUSE and streams the physical backup file via SST. Uses less disk but needs FUSE and a single active mount.'
        }
        if (isResticMysqldump) {
          return 'Mounts the restic repo via FUSE and loads the dump file with mysql client. Uses less disk but needs FUSE and a single active mount.'
        }
        return 'Mounts the restic repo via FUSE and runs myloader from the mount. Uses less disk but needs FUSE and a single active mount.'
      case 'dump':
      default:
        return 'Streams the dump directly from restic into MySQL. Fast and low disk usage, but only works with mysqldump files.'
    }
  }, [advancedReseedResticMode, isResticMysqldump, isResticPhysical])

  const resticReseedModeSupported = useMemo(() => {
    if (advancedReseedSource !== 'restic') {
      return true
    }
    if (isResticPhysical) {
      return advancedReseedResticMode === 'restore' || advancedReseedResticMode === 'mount'
    }
    if (advancedReseedResticMode === 'dump') {
      return isResticMysqldump
    }
    if (advancedReseedResticMode === 'restore' || advancedReseedResticMode === 'mount') {
      return isResticMydumper || isResticMysqldump
    }
    return false
  }, [advancedReseedSource, advancedReseedResticMode, isResticMysqldump, isResticMydumper, isResticPhysical])

  const openTerminalPage = useCallback((clusterName, srvId, commandType = '') => {
    const terminalURL = getHref.concat(`/terminal/clusters/${clusterName}/servers/${srvId}/${commandType}`).replace(/\/+$/, '')
    window.open(terminalURL, '_blank')
  }, [getHref])

  useEffect(() => {
    if (row?.id) {
      setServerName(`server ${row.host}:${row.port} (${row.id})`)
    }
  }, [row])

  useEffect(() => {
    if (!backupRestic && advancedReseedSource === 'restic') {
      setAdvancedReseedSource('backup')
    }
  }, [backupRestic, advancedReseedSource])

  useEffect(() => {
    if (advancedReseedSource !== 'restic' && advancedReseedResticInPlace) {
      setAdvancedReseedResticInPlace(false)
    }
  }, [advancedReseedSource, advancedReseedResticInPlace])

  useEffect(() => {
    if (!isResticPhysical && advancedReseedResticInPlace) {
      setAdvancedReseedResticInPlace(false)
    }
  }, [isResticPhysical, advancedReseedResticInPlace])

  useEffect(() => {
    if (advancedReseedSource !== 'restic' && advancedReseedResticUseSourcePath) {
      setAdvancedReseedResticUseSourcePath(false)
    }
  }, [advancedReseedSource, advancedReseedResticUseSourcePath])

  useEffect(() => {
    if (!isResticPhysical && advancedReseedResticUseSourcePath) {
      setAdvancedReseedResticUseSourcePath(false)
    }
  }, [isResticPhysical, advancedReseedResticUseSourcePath])

  useEffect(() => {
    if (advancedReseedResticInPlace && advancedReseedResticUseSourcePath) {
      setAdvancedReseedResticUseSourcePath(false)
    }
  }, [advancedReseedResticInPlace, advancedReseedResticUseSourcePath])

  useEffect(() => {
    if (!isAdvancedReseedOpen || !backupRestic || !clusterName) {
      return
    }
    dispatch(getResticSnapshot({ clusterName }))
  }, [isAdvancedReseedOpen, backupRestic, clusterName, dispatch])

  useEffect(() => {
    if (!isAdvancedReseedOpen || advancedReseedSource !== 'backup' || !clusterName) {
      return
    }
    if (advancedReseedLine === 'adhoc' || pitrUseBinlog) {
      dispatch(getBackups({ clusterName }))
    }
  }, [isAdvancedReseedOpen, advancedReseedSource, advancedReseedLine, pitrUseBinlog, clusterName, dispatch])

  useEffect(() => {
    if (!isAdvancedReseedOpen || advancedReseedSource !== 'restic') {
      return
    }
    if (resticSnapshotsForReseed.length === 0) {
      if (advancedReseedResticSnapshot !== '') {
        setAdvancedReseedResticSnapshot('')
      }
      return
    }
    const snapshotExists = resticSnapshotsForReseed.some((snapshot) => snapshot.id === advancedReseedResticSnapshot)
    if (!snapshotExists) {
      const serverHost = row?.host
      const serverId = row?.id
      const preferredSnapshot = resticSnapshotsForReseed.find((snapshot) => {
        const snapshotTags = normalizeResticTags(snapshot.tags)
        const matchesHost = serverHost && snapshot.hostname === serverHost
        const matchesTag = serverId && snapshotTags.some((tag) => typeof tag === 'string' && tag.includes(serverId))
        return matchesHost || matchesTag
      })
      setAdvancedReseedResticSnapshot(preferredSnapshot?.id || resticSnapshotsForReseed[0]?.id || '')
    }
  }, [
    isAdvancedReseedOpen,
    advancedReseedSource,
    resticSnapshotsForReseed,
    advancedReseedResticSnapshot,
    row
  ])

  useEffect(() => {
    if (!isAdvancedReseedOpen || advancedReseedSource !== 'restic') {
      return
    }
    if (isResticPhysical) {
      if (advancedReseedResticMode === 'dump') {
        setAdvancedReseedResticMode(defaultResticMode)
      }
      if (advancedReseedResticMode !== 'restore' && advancedReseedResticInPlace) {
        setAdvancedReseedResticInPlace(false)
      }
      if (advancedReseedResticMode === 'dump' && advancedReseedResticUseSourcePath) {
        setAdvancedReseedResticUseSourcePath(false)
      }
      return
    }
    if (!isResticMysqldump && !isResticMydumper) {
      return
    }
    if (advancedReseedResticMode === 'dump' && !isResticMysqldump) {
      setAdvancedReseedResticMode(defaultResticMode)
    }
    if ((advancedReseedResticMode === 'restore' || advancedReseedResticMode === 'mount')
      && !isResticMydumper
      && !isResticMysqldump) {
      setAdvancedReseedResticMode(defaultResticMode)
    }
  }, [
    isAdvancedReseedOpen,
    advancedReseedSource,
    advancedReseedResticMode,
    isResticMysqldump,
    isResticMydumper,
    isResticPhysical,
    advancedReseedResticInPlace,
    advancedReseedResticUseSourcePath,
    defaultResticMode
  ])

  useEffect(() => {
    if (!isAdvancedReseedOpen || advancedReseedSource !== 'restic') {
      return
    }
    if (selectedResticSnapshotPath) {
      if (selectedResticSnapshotPath !== advancedReseedResticFilePath) {
        setAdvancedReseedResticFilePath(selectedResticSnapshotPath)
      }
      return
    }
    if (advancedReseedResticFilePath !== '') {
      setAdvancedReseedResticFilePath('')
    }
  }, [
    isAdvancedReseedOpen,
    advancedReseedSource,
    selectedResticSnapshotPath,
    advancedReseedResticFilePath
  ])

  useEffect(() => {
    if (advancedReseedSource !== 'backup' || advancedReseedLine !== 'adhoc' || advancedReseedType !== 'logical') {
      if (advancedReseedBackupId !== '') {
        setAdvancedReseedBackupId('')
      }
      return
    }
    if (adhocBackupsForReseed.length === 0) {
      if (advancedReseedBackupId !== '') {
        setAdvancedReseedBackupId('')
      }
      return
    }
    const currentExists = adhocBackupsForReseed.some((backup) => String(backup?.id ?? backup?.Id ?? '') === advancedReseedBackupId)
    if (!currentExists) {
      const nextId = String(adhocBackupsForReseed[0]?.id ?? adhocBackupsForReseed[0]?.Id ?? '')
      setAdvancedReseedBackupId(nextId)
    }
  }, [
    advancedReseedSource,
    advancedReseedLine,
    advancedReseedType,
    adhocBackupsForReseed,
    advancedReseedBackupId
  ])

  useEffect(() => {
    if (advancedReseedSource !== 'backup' || !pitrUseBinlog) {
      if (pitrBackupId !== '') {
        setPitrBackupId('')
      }
      return
    }
    const nextId = selectedReseedBackup ? String(selectedReseedBackup?.id ?? selectedReseedBackup?.Id ?? '') : ''
    if (!nextId) {
      if (pitrBackupId !== '') {
        setPitrBackupId('')
      }
      return
    }
    if (pitrBackupId !== nextId) {
      setPitrBackupId(nextId)
    }
  }, [advancedReseedSource, pitrUseBinlog, selectedReseedBackup, pitrBackupId])

  useEffect(() => {
    if (advancedReseedSource === 'backup' && pitrUseBinlog) {
      return
    }
    if (pitrRestoreTime !== '') {
      setPitrRestoreTime('')
    }
  }, [advancedReseedSource, pitrUseBinlog, pitrRestoreTime])

  useEffect(() => {
    if (!pitrUseBinlog && pitrRestoreTime !== '') {
      setPitrRestoreTime('')
    }
  }, [pitrUseBinlog, pitrRestoreTime])

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setConfirmHandler(null)
    setConfirmTitle('')
  }

  const openAdvancedBackupModal = () => {
    const canUseDefaultLine = row?.preferedBackup || clusterMasterId === row?.id
    setAdvancedBackupType('logical')
    setAdvancedBackupLine(canUseDefaultLine ? 'default' : 'adhoc')
    setAdvancedRetentionDays(0)
    setAdvancedResticEnabled(Boolean(backupRestic))
    setIsAdvancedBackupOpen(true)
  }

  const closeAdvancedBackupModal = () => {
    setIsAdvancedBackupOpen(false)
  }

  const handleAdvancedBackup = () => {
    const options = { line: advancedBackupLine }
    if (advancedBackupLine === 'adhoc' && advancedRetentionDays > 0) {
      options.retentionDays = advancedRetentionDays
    }
    if (advancedBackupLine === 'adhoc' && backupRestic) {
      options.restic = advancedResticEnabled
    }

    if (advancedBackupType === 'physical') {
      dispatch(physicalBackupMaster({ clusterName, serverId: row.id, options }))
    } else {
      dispatch(logicalBackup({ clusterName, serverId: row.id, options }))
    }
    closeAdvancedBackupModal()
  }

  const openAdvancedReseedModal = (initialSource = 'backup', enablePitr = false) => {
    setAdvancedReseedType('logical')
    setAdvancedReseedSource(initialSource)
    setAdvancedReseedLine('default')
    setAdvancedReseedBackupId('')
    setAdvancedReseedResticMode(defaultResticMode)
    setAdvancedReseedResticSnapshot('')
    setAdvancedReseedResticFilePath('')
    setAdvancedReseedResticInPlace(false)
    setAdvancedReseedResticUseSourcePath(false)
    setPitrBackupId('')
    setPitrRestoreTime('')
    setPitrUseBinlog(enablePitr)
    setIsAdvancedReseedOpen(true)
  }

  const closeAdvancedReseedModal = () => {
    setIsAdvancedReseedOpen(false)
  }

  const handleAdvancedReseed = () => {
    const options = { line: advancedReseedLine }
    if (advancedReseedLine === 'adhoc' && advancedReseedType === 'logical' && advancedReseedBackupId) {
      options.backupId = advancedReseedBackupId
    }

    if (advancedReseedSource === 'backup' && pitrUseBinlog) {
      handlePitrRestore()
    } else if (advancedReseedSource === 'master') {
      // Reseed from master (only for logical)
      dispatch(reseedLogicalFromMaster({ clusterName, serverId: row.id }))
    } else if (advancedReseedSource === 'restic') {
      // Reseed from restic snapshot
      const resticBackupTool = advancedReseedType === 'physical' ? backupPhysicalType : normalizedBackupLogicalType
      dispatch(reseedFromRestic({ 
        clusterName, 
        serverId: row.id, 
        snapshotId: advancedReseedResticSnapshot,
        filePath: advancedReseedResticFilePath,
        mode: advancedReseedResticMode,
        backupTool: resticBackupTool,
        backupType: advancedReseedType,
        inPlace: isResticPhysical
          && advancedReseedResticMode === 'restore'
          && advancedReseedResticInPlace,
        useSourcePath: isResticPhysical
          && (advancedReseedResticMode === 'restore' || advancedReseedResticMode === 'mount')
          && advancedReseedResticUseSourcePath
      }))
    } else {
      // Reseed from backup
      if (advancedReseedType === 'physical') {
        dispatch(reseedPhysicalFromBackup({ clusterName, serverId: row.id, options }))
      } else {
        dispatch(reseedLogicalFromBackup({ clusterName, serverId: row.id, options }))
      }
    }
    closeAdvancedReseedModal()
  }

  const handlePitrRestore = () => {
    if (!pitrBackupId) {
      return
    }
    const pitrData = {
      Backup: parseInt(pitrBackupId, 10) || 0,
      RestoreTime: pitrRestoreTime ? new Date(pitrRestoreTime).getTime() / 1000 : 0,
      UseBinlog: pitrUseBinlog
    }
    dispatch(pitrRestore({ clusterName, serverId: row.id, pitrData }))
  }

  return (
    <>
      <MenuOptions
        className={className}
        colorScheme={colorScheme}
        placement={from === 'tableView' ? 'right-end' : 'left-end'}
        subMenuPlacement={isDesktop ? (from === 'tableView' ? 'right-end' : 'left-end') : 'bottom'}
        options={[
          ...(showCompareWithOption
            ? [
              {
                name: 'Compare With',
                onClick: () => openCompareModal(row)
              }
            ]
            : []),
          {
            name: 'Maintenance Mode',
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm maintenance for ${serverName}?`)
              setConfirmHandler(() => () => dispatch(setMaintenanceMode({ clusterName, serverId: row.id })))
            }
          },
          ...(user?.grants['terminal-db'] && showTerminal ? [
            {
              name: 'Web Terminal',
              subMenu: [
                {
                  name: 'MySQL Terminal',
                  onClick: () => openTerminalPage(clusterName, row.id, 'mysql')
                },
                {
                  name: 'MyTop Terminal',
                  onClick: () => openTerminalPage(clusterName, row.id, 'mytop')
                },
                ...(user?.grants['terminal-global'] ? [
                  {
                    name: 'Shell Terminal',
                    onClick: () => openTerminalPage(clusterName, row.id)
                  }
                ] : []),
              ]
            }
          ] : []),
          ...(user?.grants['cluster-switchover'] && row.isSlave
            ? [
              {
                name: 'Promote To Leader',
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm promotion for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(promoteToLeader({ clusterName, serverId: row.id })))
                }
              }
            ]
            : []),
          {
            name: 'Failover Candidate',
            subMenu: [
              ...(user?.grants['cluster-failover'] && !row.prefered && !row.ignored
                ? [
                  {
                    name: 'Set as Preferred',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm set as preferred for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(setAsPreferred({ clusterName, serverId: row.id })))
                    }
                  },
                  {
                    name: 'Set as Ignored',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm set as ignored for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(setAsIgnored({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              ...(user?.grants['cluster-failover'] && (row.prefered || row.ignored)
                ? [
                  {
                    name: 'Set as unrated',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm set as unrated for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(setAsUnrated({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : [])
            ]
          },
          {
            name: 'Backup',
            subMenu: [
              ...(clusterMasterId === row.id && user?.grants['db-backup']
                ? [
                  {
                    name: 'Physical Backup',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm master physical (${backupPhysicalType}) backup?`)
                      setConfirmHandler(() => () => dispatch(physicalBackupMaster({ clusterName, serverId: row.id })))
                    }
                  },
                  {
                    name: 'Logical Backup',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm sending logical backup (${backupLogicalType}) for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(logicalBackup({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              ...(clusterMasterId !== row.id && user?.grants['db-restore']
                ? [
                  {
                    name: 'Advanced Reseed',
                    onClick: () => openAdvancedReseedModal()
                  },
                  {
                    name: 'Point-In-Time Recovery',
                    onClick: () => openAdvancedReseedModal('backup', true)
                  }
                ]
                : []),
              ...(user?.grants['db-backup']
                ? [
                  {
                    name: 'Advanced Backup',
                    onClick: () => openAdvancedBackupModal()
                  },
                  {
                    name: 'Flush logs',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm flush logs for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(flushLogs({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              {
                name: 'Run Remote Jobs',
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm running remote jobs for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(runRemoteJobs({ clusterName, serverId: row.id })))
                }
              }
            ]
          },
          {
            name: 'Provision',
            subMenu: [
              ...(user?.grants['db-maintenance']
                ? [
                  {
                    name: 'Jobs Upgrade',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm jobs upgrade for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(jobsUpgrade({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              ...(user?.grants['db-stop']
                ? [
                  {
                    name: 'Stop Database',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm stop for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(stopDatabase({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              ...(user?.grants['db-start']
                ? [
                  {
                    name: 'Start Database',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm start for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(startDatabase({ clusterName, serverId: row.id })))
                    }
                  },
                  ...(orchestrator === 'opensvc' ? [
                    {
                      name: 'Restart Jobs Container',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm restart jobs container for ${serverName}?`)
                        setConfirmHandler(() => () => dispatch(restartDatabase({ clusterName, serverId: row.id, rid: JOBS_CONTAINER_RID })))
                      }
                    }
                  ] : [])
                ]
                : []),
              ...(user?.grants['prov-db-provision']
                ? [
                  {
                    name: 'Provision Database',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm provision ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(provisionDatabase({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              ...(user?.grants['prov-db-unprovision']
                ? [
                  {
                    name: 'Unprovision Database',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm unprovision for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(unprovisionDatabase({ clusterName, serverId: row.id })))
                    }
                  },
                ]
                : []),
              ...(user?.grants['db-config-flag']
                ? [
                  {
                    name: 'Refresh Variables and Generate Config',
                    onClick: () => {
                      setConfirmTitle(`Confirm generate db config for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(generateConfig({ clusterName, host: row.host, port: row.port })))
                      openConfirmModal()
                    }
                  }
                ]
                : []),
              {
                name: 'Remove Monitor',
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm removing monitor for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(dropServer({ clusterName, host: row.host, port: row.port })))
                }
              },
            ]
          },
          {
            name: 'DB Utils',
            subMenu: [
              ...(user?.grants['db-optimize']
                ? [
                  {
                    name: 'Optimize',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm optimize for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(optimizeServer({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              ...(user?.grants['db-replication']
                ? [
                  {
                    name: 'Skip 1 Replication Event',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm skip replication event for ${serverName}?`)
                      setConfirmHandler(
                        () => () => dispatch(skipReplicationEvent({ clusterName, serverId: row.id }))
                      )
                    }
                  }
                ]
                : []),
              ...(user?.grants['db-logs']
                ? [
                  {
                    name: 'Toggle InnoDB Monitor',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm toggle innodb monitor ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(toggleInnodbMonitor({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),

              ...(user?.grants['db-capture']
                ? [
                  {
                    name: 'Toggle Slow Query Capture',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm toggle slow query capture ${serverName}?`)
                      setConfirmHandler(
                        () => () => dispatch(toggleSlowQueryCapture({ clusterName, serverId: row.id }))
                      )
                    }
                  }
                ]
                : []),
              ...(user?.grants['db-replication']
                ? [
                  {
                    name: 'Start Slave',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm start slave on ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(startSlave({ clusterName, serverId: row.id })))
                    }
                  },
                  {
                    name: 'Stop Slave',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm stop slave on ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(stopSlave({ clusterName, serverId: row.id })))
                    }
                  },
                  {
                    name: 'Reset Master',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(
                        `Confirm reset master this may break replication when done on master, ${serverName}?`
                      )
                      setConfirmHandler(() => () => dispatch(resetMaster({ clusterName, serverId: row.id })))
                    }
                  },
                  {
                    name: 'Reset Slave',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm reset slave this will break replication on, ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(resetSlaveAll({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              ...(user?.grants['db-readonly']
                ? [
                  {
                    name: 'Toggle Readonly',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm toggle read only on ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(toggleReadOnly({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : [])
            ]
          }
        ]}
      />
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={confirmTitle}
          onConfirmClick={() => {
            confirmHandler()
            closeConfirmModal()
          }}
        />
      )}
      <Modal isOpen={isAdvancedBackupOpen} onClose={closeAdvancedBackupModal} size='lg'>
        <ModalOverlay />
        <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
          <ModalHeader>Advanced Backup</ModalHeader>
          <ModalCloseButton />
          <ModalBody>
            <Stack spacing={4}>
              <FormControl>
                <FormLabel>Backup type</FormLabel>
                <RadioGroup value={advancedBackupType} onChange={setAdvancedBackupType}>
                  <HStack spacing={4}>
                    <Radio value='logical'>Logical ({backupLogicalType})</Radio>
                    <Radio value='physical'>Physical ({backupPhysicalType})</Radio>
                  </HStack>
                </RadioGroup>
              </FormControl>
              <FormControl>
                <FormLabel>Backup line</FormLabel>
                {(row?.preferedBackup || clusterMasterId === row?.id) ? (
                  <>
                    <RadioGroup value={advancedBackupLine} onChange={setAdvancedBackupLine}>
                      <HStack spacing={4}>
                        <Radio value='default'>Default line</Radio>
                        <Radio value='adhoc'>Ad-hoc</Radio>
                      </HStack>
                    </RadioGroup>
                    {advancedBackupLine === 'default' ? (
                      <Text fontSize='sm' color='gray.500'>
                        Default line backup replaces the previous default backup file.
                      </Text>
                    ) : (
                      <Text fontSize='sm' color='blue.500'>
                        Ad-hoc backups are stored independently with timestamped filenames and can use per-backup retention.
                      </Text>
                    )}
                  </>
                ) : (
                  <>
                    <RadioGroup value='adhoc' isDisabled>
                      <HStack spacing={4}>
                        <Radio value='adhoc' isChecked>Ad-hoc (forced)</Radio>
                      </HStack>
                    </RadioGroup>
                    <Text fontSize='sm' color='orange.500' mt={2}>
                      Only preferred backup server or master can use default line. Ad-hoc backups are stored independently with timestamped filenames.
                    </Text>
                  </>
                )}
              </FormControl>
              {advancedBackupLine === 'adhoc' && (
                <>
                  <FormControl>
                    <FormLabel>Retention days</FormLabel>
                    <NumberInput
                      min={0}
                      step={1}
                      precision={0}
                      value={advancedRetentionDays}
                      onChange={(_, valueNumber) => setAdvancedRetentionDays(Number.isNaN(valueNumber) ? 0 : valueNumber)}
                    >
                      <NumberInputField placeholder='0 = keep indefinitely' />
                    </NumberInput>
                  </FormControl>
                  {backupRestic && (
                    <FormControl display='flex' alignItems='center' justifyContent='space-between'>
                      <FormLabel mb='0'>Backup to Restic</FormLabel>
                      <Switch isChecked={advancedResticEnabled} onChange={(event) => setAdvancedResticEnabled(event.target.checked)} />
                    </FormControl>
                  )}
                </>
              )}
            </Stack>
          </ModalBody>
          <ModalFooter gap={3}>
            <RMButton variant='outline' colorScheme='white' size='medium' onClick={closeAdvancedBackupModal}>
              Cancel
            </RMButton>
            <RMButton colorScheme='blue' size='medium' onClick={handleAdvancedBackup}>
              Start Backup
            </RMButton>
          </ModalFooter>
        </ModalContent>
      </Modal>
      <Modal isOpen={isAdvancedReseedOpen} onClose={closeAdvancedReseedModal} size='lg'>
        <ModalOverlay />
        <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
          <ModalHeader>Advanced Reseed</ModalHeader>
          <ModalCloseButton />
          <ModalBody>
            <Stack spacing={4}>
              <FormControl>
                <FormLabel>Reseed type</FormLabel>
                <RadioGroup value={advancedReseedType} onChange={(value) => {
                  setAdvancedReseedType(value)
                  // Physical reseed cannot use master source; keep restic/backup if already selected.
                  if (value === 'physical' && advancedReseedSource === 'master') {
                    setAdvancedReseedSource('backup')
                  }
                }}>
                  <HStack spacing={4}>
                    <Radio value='logical'>Logical ({backupLogicalType})</Radio>
                    <Radio value='physical'>Physical ({backupPhysicalType})</Radio>
                  </HStack>
                </RadioGroup>
              </FormControl>
              <FormControl>
                <FormLabel>Reseed source</FormLabel>
                <RadioGroup value={advancedReseedSource} onChange={setAdvancedReseedSource}>
                  <Stack spacing={2}>
                    <Radio value='backup'>From Backup</Radio>
                    <Radio value='master' isDisabled={advancedReseedType === 'physical'}>
                      From Master {advancedReseedType === 'physical' && '(not available for physical)'}
                    </Radio>
                    <Radio value='restic' isDisabled={!backupRestic}>
                      From Restic Snapshot
                    </Radio>
                  </Stack>
                </RadioGroup>
                {advancedReseedSource === 'backup' && (
                  <Text fontSize='sm' color='gray.500' mt={2}>
                    Restore from a previously created backup.
                  </Text>
                )}
                {advancedReseedSource === 'master' && (
                  <Text fontSize='sm' color='gray.500' mt={2}>
                    Create a fresh dump from the current master and restore it.
                  </Text>
                )}
                {advancedReseedSource === 'restic' && (
                  <Text fontSize='sm' color='gray.500' mt={2}>
                    Restore from a restic snapshot stored in the restic repository.
                  </Text>
                )}
              </FormControl>
              {advancedReseedSource === 'backup' && (
                <FormControl>
                  <FormLabel>Backup line</FormLabel>
                  <RadioGroup value={advancedReseedLine} onChange={setAdvancedReseedLine}>
                    <HStack spacing={4}>
                      <Radio value='default'>Default line</Radio>
                      <Radio value='adhoc'>Ad-hoc</Radio>
                    </HStack>
                  </RadioGroup>
                  {advancedReseedLine === 'default' ? (
                    <Text fontSize='sm' color='gray.500' mt={2}>
                      Restore from the default backup line (latest default backup).
                    </Text>
                  ) : (
                    <Text fontSize='sm' color='blue.500' mt={2}>
                      Select an ad-hoc backup from the list below.
                    </Text>
                  )}
                  {advancedReseedLine === 'default' && !pitrUseBinlog && (
                    selectedReseedBackup ? (
                      <Text fontSize='sm' color='gray.500' mt={2}>
                        Base backup: {selectedReseedBackupLabel}
                      </Text>
                    ) : (
                      <Text fontSize='sm' color='orange.500' mt={2}>
                        No default backup available. Create a backup before reseeding.
                      </Text>
                    )
                  )}
                </FormControl>
              )}
              {advancedReseedSource === 'backup' && advancedReseedLine === 'adhoc' && (
                <FormControl>
                  <FormLabel>Ad-hoc backup</FormLabel>
                  <Select
                    placeholder={adhocBackupsForReseed.length > 0 ? 'Select an ad-hoc backup' : 'No ad-hoc backups'}
                    value={advancedReseedBackupId}
                    onChange={(event) => setAdvancedReseedBackupId(event.target.value)}
                    isDisabled={adhocBackupsForReseed.length === 0 || advancedReseedType !== 'logical'}
                  >
                    {adhocBackupsForReseed.map((backup) => {
                      const backupId = backup?.id ?? backup?.Id
                      const backupTool = getBackupToolValue(backup) || 'backup'
                      const backupLine = getBackupLineValue(backup)
                      const backupTime = getBackupStartTimestampMs(backup)
                      const backupSource = normalizeBackupValue(backup?.source ?? backup?.Source)
                      return (
                        <option key={backupId || backupTime} value={backupId || ''}>
                          {backupTool} - {backupTime ? new Date(backupTime).toLocaleString() : 'Unknown time'}
                          {backupLine ? ` (${backupLine})` : ''}
                          {backupSource ? ` [${backupSource}]` : ''}
                          {backupId ? ` #${backupId}` : ''}
                        </option>
                      )
                    })}
                  </Select>
                  {adhocBackupsForReseed.length > 0 ? (
                    <Text fontSize='sm' color='gray.500' mt={2}>
                      Only logical ad-hoc backups are selectable here.
                    </Text>
                  ) : (
                    <Text fontSize='sm' color='orange.500' mt={2}>
                      Create an ad-hoc backup to populate this list.
                    </Text>
                  )}
                </FormControl>
              )}
              {advancedReseedSource === 'backup' && (
                <>
                  <FormControl display='flex' alignItems='center' justifyContent='space-between'>
                    <FormLabel mb='0'>Apply Binary Logs (PITR)</FormLabel>
                    <Switch
                      isChecked={pitrUseBinlog}
                      onChange={(e) => setPitrUseBinlog(e.target.checked)}
                    />
                  </FormControl>
                  {pitrUseBinlog ? (
                    <>
                      {selectedReseedBackup ? (
                        <Text fontSize='sm' color='gray.500'>
                          Base backup: {selectedReseedBackupLabel}
                        </Text>
                      ) : (
                        <Text fontSize='sm' color='orange.500'>
                          No base backup available for PITR. Select a backup first.
                        </Text>
                      )}
                      <FormControl>
                        <FormLabel>Restore to Time (optional)</FormLabel>
                        <Input
                          type='datetime-local'
                          value={pitrRestoreTime}
                          onChange={(e) => setPitrRestoreTime(e.target.value)}
                          placeholder='Leave empty to restore to latest'
                        />
                        <Text fontSize='sm' color='gray.500' mt={2}>
                          Specify a point in time to restore to. Leave empty to restore to the latest point available.
                        </Text>
                      </FormControl>
                      <Text fontSize='sm' color='blue.500'>
                        PITR restores the selected backup and applies binary logs up to the chosen time (or latest). Replication is not restarted.
                      </Text>
                    </>
                  ) : (
                    <Text fontSize='sm' color='gray.500'>
                      This will restore the base backup and rejoin replication.
                    </Text>
                  )}
                </>
              )}
              {advancedReseedSource === 'restic' && (
                <>
                  <FormControl>
                    <FormLabel>Restic restore mode</FormLabel>
                    <RadioGroup value={advancedReseedResticMode} onChange={setAdvancedReseedResticMode}>
                      <Stack spacing={2}>
                        <Radio value='dump' isDisabled={isResticPhysical || !isResticMysqldump}>
                          Stream dump (mysqldump)
                        </Radio>
                        <Radio
                          value='restore'
                          isDisabled={!isResticPhysical && !isResticMydumper && !isResticMysqldump}
                        >
                          {resticRestoreLabel}
                        </Radio>
                        <Radio
                          value='mount'
                          isDisabled={!isResticPhysical && !isResticMydumper && !isResticMysqldump}
                        >
                          {resticMountLabel}
                        </Radio>
                      </Stack>
                    </RadioGroup>
                    <Text fontSize='sm' color='gray.500' mt={2}>
                      {resticReseedModeHelp}
                    </Text>
                    {!isResticPhysical && !isResticMysqldump && !isResticMydumper && (
                      <Text fontSize='sm' color='orange.500' mt={2}>
                        Restic reseed supports mysqldump (stream/restore/mount) or mydumper (restore/mount) backups.
                      </Text>
                    )}
                  </FormControl>
                  {isResticPhysical && advancedReseedResticMode === 'restore' && (
                    <FormControl display='flex' alignItems='center' justifyContent='space-between'>
                      <FormLabel mb='0'>Restore in-place (replace backup file)</FormLabel>
                      <Switch
                        isChecked={advancedReseedResticInPlace}
                        onChange={(event) => setAdvancedReseedResticInPlace(event.target.checked)}
                      />
                    </FormControl>
                  )}
                  {isResticPhysical && advancedReseedResticMode === 'restore' && (
                    <Text fontSize='sm' color='orange.500'>
                      In-place restore overwrites the current physical backup file. Make sure the latest backup is saved to restic before running it.
                    </Text>
                  )}
                  {isResticPhysical && (advancedReseedResticMode === 'restore' || advancedReseedResticMode === 'mount') && (
                    <FormControl display='flex' alignItems='center' justifyContent='space-between'>
                      <FormLabel mb='0'>Use restic source path directly</FormLabel>
                      <Switch
                        isChecked={advancedReseedResticUseSourcePath}
                        onChange={(event) => setAdvancedReseedResticUseSourcePath(event.target.checked)}
                        isDisabled={advancedReseedResticInPlace}
                      />
                    </FormControl>
                  )}
                  {isResticPhysical && advancedReseedResticUseSourcePath && (
                    <Text fontSize='sm' color='gray.500'>
                      Streams the restored or mounted file directly for SST and skips the extra copy step. The restic restore directory or mount stays active until reseed completes.
                    </Text>
                  )}
                  <FormControl isRequired>
                    <FormLabel>Restic Snapshot</FormLabel>
                    <Select 
                      placeholder='Select a restic snapshot' 
                      value={advancedReseedResticSnapshot}
                      onChange={(e) => {
                        const snapshotId = e.target.value
                        setAdvancedReseedResticSnapshot(snapshotId)
                        const snapshot = resticSnapshotsForReseed.find((item) => item.id === snapshotId)
                        setAdvancedReseedResticFilePath(getResticSnapshotBackupPath({
                          snapshot,
                          reseedType: advancedReseedType,
                          backupLogicalType,
                          backupPhysicalType,
                          compressBackups: Boolean(compressBackups)
                        }))
                      }}
                    >
                      {resticSnapshotsForReseed.map(snapshot => (
                        <option key={snapshot.id} value={snapshot.id}>
                          {snapshot.short_id} - {snapshot.time} {snapshot.hostname ? `(${snapshot.hostname})` : ''}
                          {snapshot.tags && snapshot.tags.length > 0 ? ` [${snapshot.tags.join(', ')}]` : ''}
                        </option>
                      ))}
                    </Select>
                    {resticSnapshotsForReseed.length === 0 && (
                      <Text fontSize='sm' color='orange.500' mt={2}>
                        No restic snapshots available for reseed.
                      </Text>
                    )}
                    <Text fontSize='sm' color='gray.500' mt={2}>
                      Select the restic snapshot to restore from.
                    </Text>
                  </FormControl>
                  {selectedResticSnapshotPath ? (
                    <Text fontSize='sm' color='gray.500'>
                      Using snapshot path: {selectedResticSnapshotPath}
                    </Text>
                  ) : (
                    <Text fontSize='sm' color='orange.500'>
                      Selected snapshot does not include a file path to restore.
                    </Text>
                  )}
                </>
              )}
            </Stack>
          </ModalBody>
          <ModalFooter gap={3}>
            <RMButton variant='outline' colorScheme='white' size='medium' onClick={closeAdvancedReseedModal}>
              Cancel
            </RMButton>
            <RMButton
              colorScheme='blue'
              size='medium'
              onClick={handleAdvancedReseed}
              isDisabled={(advancedReseedSource === 'restic' && (!advancedReseedResticSnapshot || !selectedResticSnapshotPath || !resticReseedModeSupported))
                || (advancedReseedSource === 'backup' && advancedReseedLine === 'adhoc' && advancedReseedType === 'logical' && !advancedReseedBackupId)
                || (advancedReseedSource === 'backup' && pitrUseBinlog && !pitrBackupId)}
            >
              Start Reseed
            </RMButton>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </>
  )
}

export default ServerMenu
