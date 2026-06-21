import { Box, Flex, FormControl, FormLabel, Grid, GridItem, HStack, Icon, Input, Select, Stack, Text, VStack, Checkbox, Alert, AlertIcon, Divider, useDisclosure } from '@chakra-ui/react'
import React, { useState, useEffect, useRef } from 'react'
import { HiChevronDown, HiChevronUp, HiQuestionMarkCircle, HiRefresh, HiCheckCircle, HiDatabase, HiArrowCircleRight, HiShieldExclamation, HiTrash, HiLockClosed } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'
import RMButton from '../../components/RMButton'
import RMSwitch from '../../components/RMSwitch'
import TextForm from '../../components/TextForm'
import Dropdown from '../../components/Dropdown'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import { resticInitRepo, resticCheckConfig, resticCopyRepo, resticWipeRepo, getResticCurrentTask } from '../../redux/clusterSlice'
import styles from './styles.module.scss'
import tableStyles from '../../components/TableType2/styles.module.scss'

const ResticTagsHelp = `backup-restic-tags defines the tag templates sent to restic backups.
Enter a comma-separated list. Whitespace around items is trimmed.

Supported keys/placeholders:
- {tenant}: tenant/organization identifier.
- {cluster}: cluster name.
- {engine}: database engine (e.g. MariaDB/MySQL).
- {version}: engine version.
- {backup-type}: logical/physical/binlog or other backup type label.
- {backup-tool}: tool used to run the backup.
- {line}: "default" or "adhoc".
- {method}: same value as backup-type.

Template forms:
- Bare key (cluster) emits the value only (e.g. "mycluster").
- Key with empty value (cluster:) emits key:value (e.g. "cluster:mycluster").
- Use {key} placeholders in mixed tags (team:{tenant}, env:{cluster}).
- Tags that already contain ":" and no placeholders are used as-is (env:prod).

Quote an item to keep it literal and skip template expansion. Single quotes are literal; double quotes allow backslash escapes.

Examples:
- tenant,cluster,engine,version,backup-type,backup-tool,line
- cluster:,backup-type:,line:
- team:{tenant},env:{cluster}
- env:prod,role:primary
- "cluster", "role:primary,critical"`

const ResticHostHelp = `backup-restic-host overrides the restic --host value used for snapshots.  
Set a value to use a consistent alias across backups.  
Leave it empty to use restic's default hostname (no alias).`

const ResticRepoAppendClusterHelp = `backup-restic-repo-append-cluster controls whether the cluster name is appended to the repository path (local and S3).

Example:
- base: /var/lib/repman/backups/archive
- cluster: prod
- on  -> /var/lib/repman/backups/archive/prod
- off -> /var/lib/repman/backups/archive (only if you set backup-restic-local-repository outside the default archive dir)

Auto-skip rules when enabled:
- If the last path segment already equals the cluster name, it is not appended again.
- If the S3 bucket name equals the cluster name, it is not appended to the prefix.

Note: custom local repo paths inside the default archive directory are ignored.`

const ResticSftpHelp = `SFTP repositories are configured via backup-restic-local-repository using restic's sftp syntax:
sftp:user@host:/path/to/repo
sftp:user@host:path/to/repo  (relative to user's home directory)

Authentication (SSH login to the remote host):
- The "user" in the path above is the SSH login, NOT the restic repository password.
- Only key-based SSH auth is supported: the account running the restic process needs a private key
  (e.g. ~/.ssh/id_ed25519 with no passphrase, or loaded in ssh-agent) whose matching public key is listed
  in that user's ~/.ssh/authorized_keys on the remote host.
- Password-based SSH login is NOT supported - restic runs non-interactively and cannot prompt for one.
- Non-default SSH ports or other per-host options are not part of the repository path; add a Host entry
  to that user's ~/.ssh/config instead, e.g.:
    Host myhost
      HostName host
      Port 2222
      User user
  ...then reference the Host alias (myhost) in the repository path (sftp:myhost:/path/to/repo).

Repository encryption password:
- "Backup restic password" (backup-restic-password, in Connection & credentials above) is unrelated to SSH.
  It is the restic repository encryption passphrase (RESTIC_PASSWORD) and applies to every backend type.

Other notes:
- backup-restic-aws stays disabled while SFTP is selected.
- Force re-init is not supported for SFTP repositories; remove the remote repository path manually over SSH
  before re-initializing.`

const ResticRepositoryTypeHelp = `**Restic Repository Type**

Selects the Restic storage backend for this cluster's backups.

- **None** disables Restic backups entirely.
- **Local filesystem** and **SFTP** both use the path configured in "Backup restic local repository" (they differ only in path syntax: a plain filesystem path vs. \`sftp:user@host:/path\`).
- **S3 / Object storage** uses the AWS/S3 settings in the Storage section below.

Switching types does not erase the values stored for the other types, so you can switch back without re-entering them.

Config: \`backup-archive-mode\``

const ResticS3ModeHelp = `**S3 Source-of-Truth Selector**

Controls which set of S3 configuration fields is used as the authoritative source for the Restic repository path and credentials.

- **Auto** (default): at startup the server probes both the new (bucket/prefix/endpoint) config and the legacy repository URL, picks whichever is reachable first (new is tried first), and persists the winner as \`new\` or \`legacy\` in config. If neither is reachable, it falls back to presence-based selection and retries on the next restart. The UI preview for auto mode is approximate — it reflects field presence, not the actual runtime probe result.
- **New**: uses only \`backup-restic-aws-bucket\`, \`backup-restic-aws-prefix\`, \`backup-restic-aws-endpoint\`, and related credential fields.
- **Legacy**: uses only \`backup-restic-repository\` as the repository URL.

This selector applies to the active destination, startup checks, init behavior, and saved-source copy/migration so all operations see the same S3 source of truth.

Config: \`backup-restic-s3-mode\``

const ResticBinaryPathHelp = `**Backup Restic Binary Path**

Filesystem path to the \`restic\` executable used for all backup, restore, and maintenance operations on this cluster.

Leave empty to use \`restic\` resolved from the system PATH. The binary must exist and be executable by the replication-manager process.

Config: \`backup-restic-binary-path\``

const ResticPasswordHelp = `**Backup Restic Password**

Encryption passphrase for the Restic repository (passed to restic as \`RESTIC_PASSWORD\`).

Applies to every backend (local, SFTP, and S3) - the repository cannot be initialized, read, or written to without it. The value is stored encrypted.

**Losing this password makes all existing snapshots permanently unreadable** - there is no recovery.

Config: \`backup-restic-password\``

const ResticLocalRepositoryHelp = `**Backup Restic Local Repository**

Path to the Restic repository.

- For **Local filesystem**, an absolute filesystem path (e.g. \`/var/lib/repman/backups/archive\`).
- For **SFTP**, an \`sftp:user@host:/path\` URI - see the SFTP (?) help for authentication details.

When "Backup restic repo append cluster" is enabled, the cluster name is appended to this path (unless it is already the last path segment).

Config: \`backup-restic-local-repository\``

const ResticAwsAccessKeyIdHelp = `**Backup Restic AWS Access Key ID**

Access key ID used to authenticate to the S3 (or S3-compatible) bucket configured below.

Use the access key assigned to this S3 repository configuration.

Config: \`backup-restic-aws-access-key-id\``

const ResticAwsAccessSecretHelp = `**Backup Restic AWS Access Secret**

Secret access key paired with "Backup restic aws access key id". Stored encrypted.

Use the secret key paired with the access key above. Stored encrypted.

Config: \`backup-restic-aws-access-secret\``

const ResticAwsRegionHelp = `**Backup Restic AWS Region**

AWS region of the S3 bucket (e.g. \`us-east-1\`, \`eu-west-1\`).

Some S3-compatible services require this even when a custom endpoint is set. Leave empty only if the runtime environment already provides a usable AWS region (for example \`AWS_REGION\` or \`AWS_DEFAULT_REGION\`).

Config: \`backup-restic-aws-region\``

const ResticAwsEndpointHelp = `**Backup Restic AWS Endpoint**

Custom S3-compatible endpoint URL (e.g. \`https://minio.example.com\`).

Leave empty to use AWS's default S3 endpoints. Must be an \`http://\` or \`https://\` URL with a host - do not use the \`s3://\` scheme here.

Config: \`backup-restic-aws-endpoint\``

const ResticAwsBucketHelp = `**Backup Restic AWS Bucket**

Name of the S3 (or S3-compatible) bucket that stores the Restic repository.

Must not contain a slash or backslash character. Combined with "Backup restic repo append cluster" to decide whether the cluster name is appended to the prefix instead of the bucket name.

Config: \`backup-restic-aws-bucket\``

const ResticAwsPrefixHelp = `**Backup Restic AWS Prefix**

Optional path prefix within the bucket under which the Restic repository is stored (no leading slash).

Combined with "Backup restic repo append cluster": if enabled and the last prefix segment does not already equal the cluster name, the cluster name is appended.

Config: \`backup-restic-aws-prefix\``

const ResticEffectiveS3RepoHelp = `**Effective S3 Repository (preview)**

Read-only preview of the full \`s3:\` repository path that restic will use, built from the endpoint, bucket, and prefix above plus the append-cluster rule.

**When mode is \`auto\`:** this preview is approximate — it resolves based on which fields are populated in the UI, not the actual runtime probe. At startup the server probes both new and legacy configs and uses whichever is reachable; the runtime path may differ from what is shown here. Set the mode to \`new\` or \`legacy\` explicitly for an authoritative preview.

This field is computed - it cannot be edited directly.`

const ResticLegacyRepoUrlHelp = `**Legacy Repository URL (fallback)**

Older single-field repository path, used only when S3 / Object storage is enabled and "Backup restic aws bucket" is empty.

For custom S3-compatible services, you can set this directly to \`s3:https://server:port/bucket\`. New configurations should prefer the dedicated bucket/prefix/endpoint/region fields above.

Config: \`backup-restic-repository\``

const ResticAdditionalEnvHelp = `**Backup Restic Additional Env**

Extra environment variables passed to the \`restic\` process, as a comma- or space-separated list of \`KEY\` or \`KEY=VALUE\` entries.

Quote values that contain commas, e.g. \`NO_PROXY="host1,host2"\`. Useful for things like \`AWS_SESSION_TOKEN\` or proxy settings that restic/AWS SDK recognize.

Config: \`backup-restic-additional-env\``

const RESTIC_REPOSITORY_TYPE_OPTIONS = [
  { value: 'none', name: 'None' },
  { value: 'restic-local', name: 'Local filesystem' },
  { value: 'restic-aws', name: 'S3 / Object storage' },
  { value: 'restic-sftp', name: 'SFTP' }
]

function ResticRepositorySettings({
  clusterName,
  config,
  user,
  dispatch,
  onOpenInfoModal
}) {
  const h = (content, title) => (
    <RMIconButton
      icon={HiQuestionMarkCircle}
      iconFontsize='1rem'
      variant='ghost'
      style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }}
      onClick={() => onOpenInfoModal(title, content)}
    />
  )

  const isBrowser = typeof window !== 'undefined'
  const storagePrefix = 'settings:restic-repository-settings'
  const getStorageKey = (section) => `${storagePrefix}:${section}`
  const readStoredState = (section, defaultValue) => {
    if (!isBrowser) return defaultValue
    try {
      const storedValue = window.localStorage.getItem(getStorageKey(section))
      if (storedValue === null) return defaultValue
      return storedValue === 'true'
    } catch (error) {
      return defaultValue
    }
  }
  const persistStoredState = (section, value) => {
    if (!isBrowser) return
    try {
      window.localStorage.setItem(getStorageKey(section), String(value))
    } catch (error) {
    }
  }
  const [isConnectionOpen, setIsConnectionOpen] = useState(() =>
    readStoredState('connection-credentials', false)
  )
  const [isStorageOpen, setIsStorageOpen] = useState(() => readStoredState('storage', false))
  const [isTaggingOpen, setIsTaggingOpen] = useState(() => readStoredState('tagging', false))
  const [isLegacyOpen, setIsLegacyOpen] = useState(!(config?.backupResticAwsBucket || '').trim())
  const areAllSectionsOpen = isConnectionOpen && isStorageOpen && isTaggingOpen

  // Restic init modal state
  const { 
    isOpen: isInitModalOpen, 
    onOpen: onOpenInitModal, 
    onClose: onCloseInitModal 
  } = useDisclosure()
  const [initForce, setInitForce] = useState(false)
  const [confirmEmptyPrefix, setConfirmEmptyPrefix] = useState(false)
  const archiveMode = config?.backupArchiveMode || 'none'
  const isAws = archiveMode === 'restic-aws'
  const isSftp = archiveMode === 'restic-sftp'
  const s3Mode = config?.backupResticS3Mode || 'auto'
  const awsBucket = (config?.backupResticAwsBucket || '').trim()
  const awsPrefix = (config?.backupResticAwsPrefix || '').trim()
  const awsEndpoint = (config?.backupResticAwsEndpoint || '').trim()
  const appendCluster = Boolean(config?.backupResticRepoAppendCluster)
  const clusterSuffix = clusterName ? `/${clusterName}` : ''
  const trimmedEndpoint = awsEndpoint.replace(/\/+$/, '')
  const normalizedPrefix = awsPrefix.replace(/^\/+|\/+$/g, '')
  const lastPrefixSegment = normalizedPrefix ? normalizedPrefix.split('/').slice(-1)[0] : ''
  const shouldAppendAws = appendCluster && clusterName && awsBucket && awsBucket !== clusterName && lastPrefixSegment !== clusterName
  const effectivePrefix = shouldAppendAws
    ? (normalizedPrefix ? `${normalizedPrefix}/${clusterName}` : clusterName)
    : normalizedPrefix
  const legacyRepoPath = (config?.backupResticRepository || '').replace(/\/+$/, '')
  const legacyHasClusterSuffix = clusterName && legacyRepoPath.endsWith(clusterSuffix)
  const effectiveLegacyRepoPath = appendCluster && clusterName && legacyRepoPath && !legacyHasClusterSuffix
    ? `${legacyRepoPath}${clusterSuffix}`
    : legacyRepoPath
  // Compute effective repo path aligned with the backend s3Mode selector.
  const resolvedS3Mode = s3Mode === 'new' ? 'new' : (s3Mode === 'legacy' ? 'legacy' : (awsBucket ? 'new' : 'legacy'))
  const awsRepoPath = resolvedS3Mode === 'new'
    ? (awsBucket ? `s3:${trimmedEndpoint ? `${trimmedEndpoint}/` : ''}${awsBucket}${effectivePrefix ? `/${effectivePrefix}` : ''}` : '')
    : effectiveLegacyRepoPath
  const isAwsPrefixEmpty = isAws && awsBucket && !awsPrefix
  const isForceInitBlocked = initForce && ((isAwsPrefixEmpty && !confirmEmptyPrefix) || isSftp)

  useEffect(() => {
    setIsLegacyOpen(!awsBucket)
  }, [awsBucket])

  useEffect(() => {
    setCheckResult(null)
  }, [archiveMode])

  const handleSettingChange = (setting, value, encodeValue = false) =>
    dispatch(
      setSetting({
        clusterName: clusterName,
        setting,
        value: encodeValue ? btoa(value) : value
      })
    )

  const handleSwitchChange = (setting) =>
    dispatch(
      switchSetting({
        clusterName: clusterName,
        setting
      })
    )

  const handleArchiveModeChange = (nextMode) => {
    if (nextMode === archiveMode) return
    handleSettingChange('backup-archive-mode', nextMode)
  }

  const handleResticInit = () => {
    setInitForce(false) // Reset force flag
    setConfirmEmptyPrefix(false)
    onOpenInitModal()
  }

  const handleConfirmInit = async () => {
    try {
      await dispatch(resticInitRepo({ 
        clusterName, 
        force: initForce,
        allowEmptyPrefix: confirmEmptyPrefix
      })).unwrap()
      
      onCloseInitModal()
    } catch (error) {
      console.error('Failed to initialize repository:', error)
      onCloseInitModal()
    }
  }

  // Check repository state
  const [isCheckLoading, setIsCheckLoading] = useState(false)
  const [checkResult, setCheckResult] = useState(null)
  const {
    isOpen: isChecklistModalOpen,
    onOpen: onOpenChecklistModal,
    onClose: onCloseChecklistModal
  } = useDisclosure()
  const [checklistConfirmedTarget, setChecklistConfirmedTarget] = useState(false)
  const [checklistConfirmedNew, setChecklistConfirmedNew] = useState(false)
  const isChecklistReady = checklistConfirmedTarget && checklistConfirmedNew

  // Copy modal state
  const { isOpen: isCopyModalOpen, onOpen: onOpenCopyModal, onClose: onCloseCopyModal } = useDisclosure()
  // copySourceStrategy: 'manual' | 'saved-local' | 'saved-s3'
  const [copySourceStrategy, setCopySourceStrategy] = useState('manual')
  const [copySourceMode, setCopySourceMode] = useState('restic-local')
  const [copySourceRepo, setCopySourceRepo] = useState('')
  const [copySourcePassword, setCopySourcePassword] = useState('')
  const [copySourceKeyHint, setCopySourceKeyHint] = useState('')
  const [copySourceAwsEndpoint, setCopySourceAwsEndpoint] = useState('')
  const [copySourceAwsBucket, setCopySourceAwsBucket] = useState('')
  const [copySourceAwsPrefix, setCopySourceAwsPrefix] = useState('')
  const [copySourceAwsAccessKeyId, setCopySourceAwsAccessKeyId] = useState('')
  const [copySourceAwsAccessSecret, setCopySourceAwsAccessSecret] = useState('')
  const [copySourceAwsRegion, setCopySourceAwsRegion] = useState('')
  const [copyInitDestination, setCopyInitDestination] = useState(false)
  const [copyCopyChunkerParams, setCopyCopyChunkerParams] = useState(false)
  const [copySnapshotIds, setCopySnapshotIds] = useState('')
  const [copyFilterHost, setCopyFilterHost] = useState('')
  const [copyFilterPath, setCopyFilterPath] = useState('')
  const [copyFilterTag, setCopyFilterTag] = useState('')
  const [copyError, setCopyError] = useState(null)
  const [copySubmitting, setCopySubmitting] = useState(false)
  const [showCopyAdvancedFilters, setShowCopyAdvancedFilters] = useState(false)
  const copySubmitGenRef = useRef(0)
  // savedS3Available: structured bucket config OR a legacy backup-restic-repository S3 URL
  const savedS3LegacyAvailable = !awsBucket && legacyRepoPath.startsWith('s3:')
  const savedS3Available = Boolean(awsBucket) || savedS3LegacyAvailable

  const handleCheckRepo = async () => {
    setIsCheckLoading(true)
    setCheckResult(null)
    try {
      const result = await dispatch(resticCheckConfig({ clusterName })).unwrap()
      const data = result?.data
      setCheckResult(data)
      if (data?.status === 'initialization_required') {
        setChecklistConfirmedTarget(false)
        setChecklistConfirmedNew(false)
        onOpenChecklistModal()
      }
    } catch (error) {
      setCheckResult({ status: 'error', message: error?.errorMessage || error?.message || String(error) })
    } finally {
      setIsCheckLoading(false)
    }
  }

  const handleChecklistConfirmInit = async () => {
    onCloseChecklistModal()
    try {
      await dispatch(resticInitRepo({ clusterName, force: false })).unwrap()
      setCheckResult({ status: 'ok', message: 'Repository initialized. Snapshot refresh queued.' })
    } catch (error) {
      setCheckResult({ status: 'error', message: error?.errorMessage || error?.message || String(error) })
    }
  }

  const resetCopyForm = () => {
    setCopySourceStrategy('manual')
    setCopySourceMode('restic-local')
    setCopySourceRepo('')
    setCopySourcePassword('')
    setCopySourceKeyHint('')
    setCopySourceAwsEndpoint('')
    setCopySourceAwsBucket('')
    setCopySourceAwsPrefix('')
    setCopySourceAwsAccessKeyId('')
    setCopySourceAwsAccessSecret('')
    setCopySourceAwsRegion('')
    setCopyInitDestination(false)
    setCopyCopyChunkerParams(false)
    setCopySnapshotIds('')
    setCopyFilterHost('')
    setCopyFilterPath('')
    setCopyFilterTag('')
    setCopyError(null)
    setCopySubmitting(false)
    setShowCopyAdvancedFilters(false)
  }

  const handleCloseCopyModal = () => {
    copySubmitGenRef.current++
    resetCopyForm()
    onCloseCopyModal()
  }

  const validateCopyForm = () => {
    if (copySourceStrategy === 'manual') {
      if (!copySourcePassword.trim()) return 'Source password is required.'
      if ((copySourceMode === 'restic-local' || copySourceMode === 'restic-sftp') && !copySourceRepo.trim()) {
        return 'Source repository path is required.'
      }
      if (copySourceMode === 'restic-aws' && !copySourceAwsBucket.trim()) {
        return 'Source S3 bucket is required.'
      }
    }
    if (copyCopyChunkerParams && !copyInitDestination) {
      return 'Copy chunker parameters requires init destination to be enabled.'
    }
    return null
  }

  const buildCopyPayload = () => {
    const splitFilter = (text) => text.split(',').map((s) => s.trim()).filter(Boolean)
    const snapshotIds = splitFilter(copySnapshotIds)
    const host = snapshotIds.length > 0 ? [] : splitFilter(copyFilterHost)
    const path = snapshotIds.length > 0 ? [] : splitFilter(copyFilterPath)
    const tag = snapshotIds.length > 0 ? [] : splitFilter(copyFilterTag)

    let source
    if (copySourceStrategy === 'saved-local') {
      source = { mode: 'restic-local', use_saved_config: true }
      if (copySourceKeyHint.trim()) source.key_hint = copySourceKeyHint.trim()
    } else if (copySourceStrategy === 'saved-s3') {
      source = { mode: 'restic-aws', use_saved_config: true }
      if (copySourceKeyHint.trim()) source.key_hint = copySourceKeyHint.trim()
    } else {
      source = { mode: copySourceMode, password: copySourcePassword }
      if (copySourceKeyHint.trim()) source.key_hint = copySourceKeyHint.trim()
      if (copySourceMode === 'restic-aws') {
        source.aws = {
          endpoint: copySourceAwsEndpoint.trim(),
          bucket: copySourceAwsBucket.trim(),
          prefix: copySourceAwsPrefix.trim(),
          access_key_id: copySourceAwsAccessKeyId.trim(),
          access_secret: copySourceAwsAccessSecret,
          region: copySourceAwsRegion.trim()
        }
      } else {
        source.repository = copySourceRepo.trim()
      }
    }
    return {
      source,
      init_destination: copyInitDestination,
      copy_chunker_params: copyCopyChunkerParams,
      snapshot_ids: snapshotIds,
      host,
      path,
      tag
    }
  }

  const handleCopySubmit = async () => {
    const err = validateCopyForm()
    if (err) { setCopyError(err); return }
    setCopyError(null)
    const submitGen = ++copySubmitGenRef.current
    setCopySubmitting(true)
    try {
      await dispatch(resticCopyRepo(clusterName, buildCopyPayload()))
      if (copySubmitGenRef.current === submitGen) {
        onCloseCopyModal()
        resetCopyForm()
        dispatch(getResticCurrentTask({ clusterName }))
      }
    } catch (error) {
      if (copySubmitGenRef.current === submitGen) {
        setCopyError(error?.errorMessage || error?.message || 'Copy request failed.')
        setCopySubmitting(false)
      }
    }
  }

  // Wipe modal state
  const { isOpen: isWipeModalOpen, onOpen: onOpenWipeModal, onClose: onCloseWipeModal } = useDisclosure()
  const [wipeTypedConfirm, setWipeTypedConfirm] = useState('')
  const [wipeAllowEmptyPrefix, setWipeAllowEmptyPrefix] = useState(false)
  const [wipeSubmitting, setWipeSubmitting] = useState(false)
  const [wipeTargetLoading, setWipeTargetLoading] = useState(false)
  const [wipeError, setWipeError] = useState(null)
  const [wipeResult, setWipeResult] = useState(null)
  // Server-fetched preview fields; null means not yet loaded
  const [wipeCanonicalTarget, setWipeCanonicalTarget] = useState(null)
  const [wipeServerEmptyPrefix, setWipeServerEmptyPrefix] = useState(false)
  const [wipeCanWipe, setWipeCanWipe] = useState(null)
  const [wipeServerMessage, setWipeServerMessage] = useState(null)
  const [wipeServerBackend, setWipeServerBackend] = useState(null)

  const wipeDisplayTarget = wipeCanonicalTarget ?? ''
  const isWipeTypedMatch = wipeTypedConfirm.trim() === wipeDisplayTarget && wipeDisplayTarget !== ''
  const isWipeEmptyPrefixRisk = wipeServerEmptyPrefix
  const isWipeSubmitBlocked = wipeTargetLoading || wipeCanonicalTarget === null || !isWipeTypedMatch || (isWipeEmptyPrefixRisk && !wipeAllowEmptyPrefix) || wipeSubmitting || wipeCanWipe === false

  const handleOpenWipeModal = async () => {
    setWipeTypedConfirm('')
    setWipeAllowEmptyPrefix(false)
    setWipeError(null)
    setWipeResult(null)
    setWipeCanonicalTarget(null)
    setWipeServerEmptyPrefix(false)
    setWipeCanWipe(null)
    setWipeServerMessage(null)
    setWipeServerBackend(null)
    onOpenWipeModal()
    setWipeTargetLoading(true)
    try {
      const result = await dispatch(resticCheckConfig({ clusterName, skipFetch: true })).unwrap()
      setWipeCanonicalTarget(result?.data?.repo_path ?? '')
      setWipeServerEmptyPrefix(Boolean(result?.data?.is_s3_empty_prefix))
      setWipeCanWipe(result?.data?.can_wipe !== false)
      setWipeServerMessage(result?.data?.wipe_message ?? null)
      setWipeServerBackend(result?.data?.backend ?? null)
    } catch (error) {
      setWipeError('Failed to fetch wipe target from server: ' + (error?.errorMessage || error?.message || String(error)))
      setWipeCanonicalTarget('')
      setWipeCanWipe(false)
    } finally {
      setWipeTargetLoading(false)
    }
  }

  const handleCloseWipeModal = () => {
    setWipeTypedConfirm('')
    setWipeAllowEmptyPrefix(false)
    setWipeError(null)
    setWipeCanonicalTarget(null)
    setWipeCanWipe(null)
    setWipeServerMessage(null)
    setWipeServerBackend(null)
    onCloseWipeModal()
  }

  const handleWipeSubmit = async () => {
    setWipeSubmitting(true)
    setWipeError(null)
    try {
      await dispatch(resticWipeRepo(clusterName, {
        confirm: true,
        typed_target_confirm: wipeTypedConfirm.trim(),
        allow_empty_prefix: wipeAllowEmptyPrefix
      }))
      handleCloseWipeModal()
      setWipeResult({ status: 'ok', message: 'Repository wiped. It must be re-initialized before use.' })
    } catch (error) {
      setWipeError(error?.errorMessage || error?.message || 'Wipe request failed.')
    } finally {
      setWipeSubmitting(false)
    }
  }

  const toggleSection = (section, setter) => {
    setter((prev) => {
      const next = !prev
      persistStoredState(section, next)
      return next
    })
  }

  const setAllSectionsState = (nextState) => {
    setIsConnectionOpen(nextState)
    persistStoredState('connection-credentials', nextState)
    setIsStorageOpen(nextState)
    persistStoredState('storage', nextState)
    setIsTaggingOpen(nextState)
    persistStoredState('tagging', nextState)
  }

  const renderSectionPanel = ({ sectionKey, title, description, isOpen, setOpen, controlsId, content }) => (
    <Box className={styles.panel} w='full'>
      <HStack
        as='button'
        type='button'
        spacing={2}
        onClick={() => toggleSection(sectionKey, setOpen)}
        aria-expanded={isOpen}
        aria-controls={controlsId}
        className={styles.panelHeader}
      >
        <Stack spacing={1} className={styles.panelHeaderContent}>
          {typeof title === 'string' ? <Text className={styles.panelTitle}>{title}</Text> : title}
          <Text className={styles.panelDescription}>{description}</Text>
        </Stack>
        <Box className={styles.panelChevron}>{isOpen ? <HiChevronUp /> : <HiChevronDown />}</Box>
      </HStack>
      <Box id={controlsId} className={styles.panelBody} display={isOpen ? 'block' : 'none'}>
        {content}
      </Box>
    </Box>
  )

  return (
    <VStack
      as='form'
      className={tableStyles.container}
      spacing={{ base: 4, lg: 5 }}
      onSubmit={(event) => {
        event.preventDefault()
      }}
    >
      <Stack spacing={{ base: 3, lg: 4 }} width='100%'>
        <Flex
          className={styles.resticMountHeaderRow}
          direction={{ base: 'column', md: 'row' }}
          align={{ base: 'flex-start', md: 'center' }}
          justify='space-between'
          gap={2}
        >
          <HStack spacing={2} className={styles.resticMountHeaderInfo}>
            <Text className={styles.resticMountHeaderTitle}>Restic repository settings</Text>
          </HStack>
          <HStack
            spacing={2}
            className={styles.resticMountHeaderActions}
            w={{ base: 'full', md: 'auto' }}
            justify={{ base: 'flex-start', md: 'flex-end' }}
            flexWrap='wrap'
          >
            <Box
              as='button'
              type='button'
              className={styles.resticMountActionButton}
              aria-expanded={areAllSectionsOpen}
              aria-controls='restic-repo-connection-credentials restic-repo-storage restic-repo-tagging'
              aria-label={
                areAllSectionsOpen
                  ? 'Hide all restic repository settings sections'
                  : 'Show all restic repository settings sections'
              }
              onClick={() => setAllSectionsState(!areAllSectionsOpen)}
            >
              {areAllSectionsOpen ? 'Hide all' : 'Show all'}
            </Box>
          </HStack>
        </Flex>
        {renderSectionPanel({
          sectionKey: 'connection-credentials',
          title: 'Connection & credentials',
          description: 'Enable restic and set binary paths plus secrets.',
          isOpen: isConnectionOpen,
          setOpen: setIsConnectionOpen,
          controlsId: 'restic-repo-connection-credentials',
          content: (
            <Stack spacing={{ base: 1, md: 2 }}>
              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <HStack spacing={1} justify='space-between' width='full'>
                    <Text>Restic repository type</Text>
                    {h(ResticRepositoryTypeHelp, 'Restic Repository Type')}
                  </HStack>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <Flex className={styles.dropdownContainer}>
                    <Dropdown
                      options={RESTIC_REPOSITORY_TYPE_OPTIONS}
                      className={styles.dropdownButton}
                      selectedValue={archiveMode}
                      isDisabled={user?.grants['cluster-settings'] == false}
                      confirmTitle={'Confirm backup-archive-mode to'}
                      onChange={(value) => handleArchiveModeChange(value)}
                    />
                  </Flex>
                </GridItem>
              </Grid>

              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <HStack spacing={1} justify='space-between' width='full'>
                    <Text>Backup restic binary path</Text>
                    {h(ResticBinaryPathHelp, 'Backup Restic Binary Path')}
                  </HStack>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <TextForm
                    value={config?.backupResticBinaryPath}
                    confirmTitle={`Confirm backup-restic-binary-path to `}
                    className={styles.textbox}
                    size='sm'
                    onSave={(value) => handleSettingChange('backup-restic-binary-path', value)}
                  />
                </GridItem>
              </Grid>

              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <HStack spacing={1} justify='space-between' width='full'>
                    <Text>Backup restic password</Text>
                    {h(ResticPasswordHelp, 'Backup Restic Password')}
                  </HStack>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <TextForm
                    value={config?.backupResticPassword}
                    confirmTitle={`Confirm backup-restic-password to `}
                    className={styles.textbox}
                    size='sm'
                    onSave={(value) => handleSettingChange('backup-restic-password', value, true)}
                  />
                </GridItem>
              </Grid>
            </Stack>
          )
        })}

        {renderSectionPanel({
          sectionKey: 'storage',
          title: 'Storage',
          description: 'Choose the repository location and backend.',
          isOpen: isStorageOpen,
          setOpen: setIsStorageOpen,
          controlsId: 'restic-repo-storage',
          content: (
            <Stack spacing={{ base: 2, md: 3 }}>
              {archiveMode === 'none' && (
                <Text className={styles.helperText}>
                  Restic backups are disabled (backup-archive-mode is set to none). Pick Local, S3, or SFTP in
                  &quot;Restic repository type&quot; (Connection &amp; credentials) to configure storage.
                </Text>
              )}

              {archiveMode === 'restic-local' && (
                <Stack spacing={{ base: 1, md: 2 }}>
                  <Grid
                    className={styles.resticMountGrid}
                    templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                    columnGap={3}
                    rowGap={1}
                    w='full'
                  >
                    <GridItem className={styles.rowLabel}>
                      <HStack spacing={1} justify='space-between' width='full'>
                        <Text>Backup restic local repository</Text>
                        {h(ResticLocalRepositoryHelp, 'Backup Restic Local Repository')}
                      </HStack>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <Stack spacing={1} width='100%'>
                        <HStack width='100%' spacing={2}>
                          <TextForm
                            value={config?.backupResticLocalRepository}
                            confirmTitle={`Confirm backup-restic-local-repository to `}
                            className={styles.textbox}
                            size='sm'
                            placeholder='/var/lib/repman/backups/archive'
                            onSave={(value) => handleSettingChange('backup-restic-local-repository', value, true)}
                          />
                          <RMButton
                            size='sm'
                            colorScheme='green'
                            onClick={handleCheckRepo}
                            isLoading={isCheckLoading}
                            isDisabled={user?.grants['cluster-settings'] === false}
                            title="Check local repository"
                          >
                            Check
                          </RMButton>
                          <RMButton
                            size='sm'
                            colorScheme='blue'
                            onClick={handleResticInit}
                            isDisabled={user?.grants['cluster-settings'] === false}
                            title="Re-initialize local repository"
                          >
                            <HiRefresh />
                          </RMButton>
                          {h(
                            'Re-initialize the local Restic repository. This creates a new repository configuration at the specified path. Use with caution - only needed if the repository is corrupted or being set up for the first time.',
                            'Re-initialize Local Repository'
                          )}
                        </HStack>
                        <HStack>
                          <RMButton
                            size='sm'
                            colorScheme='orange'
                            onClick={onOpenCopyModal}
                            isDisabled={user?.grants['cluster-process'] === false}
                            title='Migrate/copy snapshots from another repository into this one'
                          >
                            Migrate/copy snapshots
                          </RMButton>
                        </HStack>
                        {checkResult && checkResult.status !== 'initialization_required' && (
                          <Alert status={checkResult.status === 'ok' ? 'success' : 'error'} size='sm' borderRadius='md'>
                            <AlertIcon />
                            <Text fontSize='sm'>{checkResult.message}</Text>
                          </Alert>
                        )}
                      </Stack>
                    </GridItem>
                  </Grid>
                </Stack>
              )}

              {archiveMode === 'restic-sftp' && (
                <Stack spacing={{ base: 1, md: 2 }}>
                  <Grid
                    className={styles.resticMountGrid}
                    templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                    columnGap={3}
                    rowGap={1}
                    w='full'
                  >
                    <GridItem className={styles.rowLabel}>
                      <HStack spacing={1} justify='space-between' width='full'>
                        <Text>SFTP repository path</Text>
                        {h(ResticSftpHelp, 'SFTP Repository')}
                      </HStack>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <Stack spacing={1} width='100%'>
                        <HStack width='100%' spacing={2}>
                          <TextForm
                            value={config?.backupResticLocalRepository}
                            confirmTitle={`Confirm backup-restic-local-repository to `}
                            className={styles.textbox}
                            size='sm'
                            placeholder='sftp:user@host:/path/to/repo'
                            onSave={(value) => handleSettingChange('backup-restic-local-repository', value, true)}
                          />
                          <RMButton
                            size='sm'
                            colorScheme='green'
                            onClick={handleCheckRepo}
                            isLoading={isCheckLoading}
                            isDisabled={user?.grants['cluster-settings'] === false}
                            title="Check SFTP repository"
                          >
                            Check
                          </RMButton>
                          <RMButton
                            size='sm'
                            colorScheme='blue'
                            onClick={handleResticInit}
                            isDisabled={user?.grants['cluster-settings'] === false}
                            title="Re-initialize SFTP repository"
                          >
                            <HiRefresh />
                          </RMButton>
                          {h(
                            'Re-initialize the SFTP Restic repository. This creates a new repository configuration at the configured sftp:user@host:/path. Force re-initialization is not supported for SFTP repositories - remove the remote repository path manually over SSH before re-initializing.',
                            'Re-initialize SFTP Repository'
                          )}
                        </HStack>
                        <HStack>
                          <RMButton
                            size='sm'
                            colorScheme='orange'
                            onClick={onOpenCopyModal}
                            isDisabled={user?.grants['cluster-process'] === false}
                            title='Migrate/copy snapshots from another repository into this one'
                          >
                            Migrate/copy snapshots
                          </RMButton>
                        </HStack>
                        {checkResult && checkResult.status !== 'initialization_required' && (
                          <Alert status={checkResult.status === 'ok' ? 'success' : 'error'} size='sm' borderRadius='md'>
                            <AlertIcon />
                            <Text fontSize='sm'>{checkResult.message}</Text>
                          </Alert>
                        )}
                      </Stack>
                    </GridItem>
                  </Grid>
                </Stack>
              )}

              {archiveMode === 'restic-aws' && (
                <Stack spacing={{ base: 2, md: 3 }}>
                  {/* 0. S3 mode selector */}
                  <Stack spacing={{ base: 1, md: 2 }}>
                    <Grid
                      className={styles.resticMountGrid}
                      templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                      columnGap={3}
                      rowGap={1}
                      w='full'
                    >
                      <GridItem className={styles.rowLabel}>
                        <HStack spacing={1} justify='space-between' width='full'>
                          <Text>S3 source-of-truth mode</Text>
                          {h(ResticS3ModeHelp, 'S3 Source-of-Truth Mode')}
                        </HStack>
                      </GridItem>
                      <GridItem className={styles.valueCell}>
                        <Select
                          size='sm'
                          value={s3Mode}
                          isDisabled={user?.grants['cluster-settings'] === false}
                          onChange={(e) => handleSettingChange('backup-restic-s3-mode', e.target.value)}
                        >
                          <option value='auto'>Auto (probe new then legacy at startup; persist winner)</option>
                          <option value='new'>New (bucket / prefix / endpoint fields only)</option>
                          <option value='legacy'>Legacy (backup-restic-repository URL only)</option>
                        </Select>
                      </GridItem>
                    </Grid>
                  </Stack>

                  {/* 1. Provider connection */}
                  <Box className={styles.subsectionHeader}>
                    <Text className={styles.subsectionTitle}>Provider connection</Text>
                  </Box>
                  <Stack spacing={{ base: 1, md: 2 }}>
                    <Grid
                      className={styles.resticMountGrid}
                      templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                      columnGap={3}
                      rowGap={1}
                      w='full'
                    >
                      <GridItem className={styles.rowLabel}>
                        <HStack spacing={1} justify='space-between' width='full'>
                          <Text>Backup restic aws endpoint</Text>
                          {h(ResticAwsEndpointHelp, 'Backup Restic AWS Endpoint')}
                        </HStack>
                      </GridItem>
                      <GridItem className={styles.valueCell}>
                        <TextForm
                          value={config?.backupResticAwsEndpoint}
                          confirmTitle={`Confirm backup-restic-aws-endpoint to `}
                          className={styles.textbox}
                          size='sm'
                          placeholder='https://s3.amazonaws.com or https://minio.example.com'
                          regexPattern='^https?://[A-Za-z0-9.-]+(?::\\d+)?(?:/.*)?$'
                          onSave={(value) => handleSettingChange('backup-restic-aws-endpoint', value, true)}
                        />
                      </GridItem>
                    </Grid>

                    <Grid
                      className={styles.resticMountGrid}
                      templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                      columnGap={3}
                      rowGap={1}
                      w='full'
                    >
                      <GridItem className={styles.rowLabel}>
                        <HStack spacing={1} justify='space-between' width='full'>
                          <Text>Backup restic aws region</Text>
                          {h(ResticAwsRegionHelp, 'Backup Restic AWS Region')}
                        </HStack>
                      </GridItem>
                      <GridItem className={styles.valueCell}>
                        <TextForm
                          value={config?.backupResticAwsRegion}
                          confirmTitle={`Confirm backup-restic-aws-region to `}
                          className={styles.textbox}
                          size='sm'
                          placeholder='us-east-1, eu-west-1, etc.'
                          onSave={(value) => handleSettingChange('backup-restic-aws-region', value)}
                        />
                      </GridItem>
                    </Grid>
                  </Stack>

                  {/* 2. Credentials */}
                  <Box className={styles.subsectionHeader}>
                    <Text className={styles.subsectionTitle}>Credentials</Text>
                  </Box>
                  <Stack spacing={{ base: 1, md: 2 }}>
                    <Grid
                      className={styles.resticMountGrid}
                      templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                      columnGap={3}
                      rowGap={1}
                      w='full'
                    >
                      <GridItem className={styles.rowLabel}>
                        <HStack spacing={1} justify='space-between' width='full'>
                          <Text>Backup restic aws access key id</Text>
                          {h(ResticAwsAccessKeyIdHelp, 'Backup Restic AWS Access Key ID')}
                        </HStack>
                      </GridItem>
                      <GridItem className={styles.valueCell}>
                        <TextForm
                          value={config?.backupResticAwsAccessKeyId}
                          confirmTitle={`Confirm backup-restic-aws-access-key-id to `}
                          className={styles.textbox}
                          size='sm'
                          onSave={(value) => handleSettingChange('backup-restic-aws-access-key-id', value)}
                        />
                      </GridItem>
                    </Grid>

                    <Grid
                      className={styles.resticMountGrid}
                      templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                      columnGap={3}
                      rowGap={1}
                      w='full'
                    >
                      <GridItem className={styles.rowLabel}>
                        <HStack spacing={1} justify='space-between' width='full'>
                          <Text>Backup restic aws access secret</Text>
                          {h(ResticAwsAccessSecretHelp, 'Backup Restic AWS Access Secret')}
                        </HStack>
                      </GridItem>
                      <GridItem className={styles.valueCell}>
                        <TextForm
                          value={config?.backupResticAwsAccessSecret}
                          confirmTitle={`Confirm backup-restic-aws-access-secret to `}
                          className={styles.textbox}
                          size='sm'
                          onSave={(value) => handleSettingChange('backup-restic-aws-access-secret', value, true)}
                        />
                      </GridItem>
                    </Grid>
                  </Stack>

                  {/* 3. Repository target */}
                  <Box className={styles.subsectionHeader}>
                    <Text className={styles.subsectionTitle}>Repository target</Text>
                  </Box>
                  <Stack spacing={{ base: 1, md: 2 }}>
                    <Grid
                      className={styles.resticMountGrid}
                      templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                      columnGap={3}
                      rowGap={1}
                      w='full'
                    >
                      <GridItem className={styles.rowLabel}>
                        <HStack spacing={1} justify='space-between' width='full'>
                          <Text>Backup restic aws bucket</Text>
                          {h(ResticAwsBucketHelp, 'Backup Restic AWS Bucket')}
                        </HStack>
                      </GridItem>
                      <GridItem className={styles.valueCell}>
                        <TextForm
                          value={config?.backupResticAwsBucket}
                          confirmTitle={`Confirm backup-restic-aws-bucket to `}
                          className={styles.textbox}
                          size='sm'
                          placeholder='bucket-name'
                          regexPattern='^[^/\\\\]*$'
                          onSave={(value) => handleSettingChange('backup-restic-aws-bucket', value)}
                        />
                        <Text className={styles.helperText}>Bucket name must not contain &apos;/&apos; or &apos;\\&apos;.</Text>
                      </GridItem>
                    </Grid>

                    <Grid
                      className={styles.resticMountGrid}
                      templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                      columnGap={3}
                      rowGap={1}
                      w='full'
                    >
                      <GridItem className={styles.rowLabel}>
                        <HStack spacing={1} justify='space-between' width='full'>
                          <Text>Backup restic aws prefix</Text>
                          {h(ResticAwsPrefixHelp, 'Backup Restic AWS Prefix')}
                        </HStack>
                      </GridItem>
                      <GridItem className={styles.valueCell}>
                        <TextForm
                          value={config?.backupResticAwsPrefix}
                          confirmTitle={`Confirm backup-restic-aws-prefix to `}
                          className={styles.textbox}
                          size='sm'
                          placeholder='optional/prefix/path'
                          onSave={(value) => handleSettingChange('backup-restic-aws-prefix', value, true)}
                        />
                        <Text className={styles.helperText}>Optional bucket prefix/path (no leading slash).</Text>
                      </GridItem>
                    </Grid>

                    <Grid
                      className={styles.resticMountGrid}
                      templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                      columnGap={3}
                      rowGap={1}
                      w='full'
                    >
                      <GridItem className={styles.rowLabel}>
                        <HStack spacing={1} justify='space-between' width='full'>
                          <Text>Backup restic repo append cluster</Text>
                          {h(ResticRepoAppendClusterHelp, 'Restic Repo Append Cluster')}
                        </HStack>
                      </GridItem>
                      <GridItem className={styles.valueCell}>
                        <HStack spacing={2} align='center'>
                          <RMSwitch
                            isChecked={config?.backupResticRepoAppendCluster}
                            isDisabled={user?.grants['cluster-settings'] == false}
                            confirmTitle={'Confirm switch settings for backup-restic-repo-append-cluster?'}
                            onChange={() => handleSwitchChange('backup-restic-repo-append-cluster')}
                          />
                        </HStack>
                      </GridItem>
                    </Grid>

                    <Grid
                      className={styles.resticMountGrid}
                      templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                      columnGap={3}
                      rowGap={1}
                      w='full'
                    >
                      <GridItem className={styles.rowLabel}>
                        <HStack spacing={1} justify='space-between' width='full'>
                          <Text>
                            Effective S3 repository
                            {s3Mode === 'auto' && (
                              <Text as='span' fontSize='xs' color='orange.500' ml={1}>(approximate — auto mode)</Text>
                            )}
                          </Text>
                          {h(ResticEffectiveS3RepoHelp, 'Effective S3 Repository')}
                        </HStack>
                      </GridItem>
                      <GridItem className={styles.valueCell}>
                        <Text
                          fontWeight='bold'
                          fontSize='sm'
                          fontFamily='monospace'
                          bg='gray.100'
                          p={2}
                          borderRadius='md'
                          wordBreak='break-all'
                        >
                          {awsRepoPath || 's3:<bucket>/<prefix>'}
                        </Text>
                      </GridItem>
                    </Grid>
                  </Stack>

                  {/* 4. Repository actions */}
                  <Box className={styles.subsectionHeader}>
                    <Text className={styles.subsectionTitle}>Repository initialization</Text>
                  </Box>
                  <Box className={styles.repositoryActionsBox}>
                    <Stack spacing={3}>
                      <Text className={styles.helperText}>
                        Verify or initialize the effective repository shown in <strong>Repository target</strong> above. Use <em>Migrate/copy</em> to move snapshots to a new destination.
                      </Text>
                      <HStack spacing={2} flexWrap='wrap'>
                        <RMButton
                          size='sm'
                          colorScheme='green'
                          leftIcon={<HiCheckCircle />}
                          onClick={handleCheckRepo}
                          isLoading={isCheckLoading}
                          isDisabled={user?.grants['cluster-settings'] === false}
                        >
                          Check repository
                        </RMButton>
                        <RMButton
                          size='sm'
                          colorScheme='blue'
                          leftIcon={<HiDatabase />}
                          onClick={handleResticInit}
                          isDisabled={user?.grants['cluster-settings'] === false}
                        >
                          Initialize repository
                        </RMButton>
                        <RMButton
                          size='sm'
                          colorScheme='orange'
                          leftIcon={<HiArrowCircleRight />}
                          onClick={onOpenCopyModal}
                          isDisabled={user?.grants['cluster-process'] === false}
                        >
                          Migrate/copy snapshots
                        </RMButton>
                      </HStack>
                      {checkResult && checkResult.status !== 'initialization_required' && (
                        <Alert status={checkResult.status === 'ok' ? 'success' : 'error'} size='sm' borderRadius='md'>
                          <AlertIcon />
                          <Text fontSize='sm'>{checkResult.message}</Text>
                        </Alert>
                      )}
                    </Stack>
                  </Box>

                  {/* 5. Advanced compatibility fallback */}
                  <HStack
                    as='button'
                    type='button'
                    spacing={2}
                    onClick={() => setIsLegacyOpen((prev) => !prev)}
                    aria-expanded={isLegacyOpen}
                    aria-controls='restic-repo-legacy-fallback'
                    className={styles.subsectionAdvancedHeader}
                  >
                    <Text className={styles.subsectionAdvancedTitle}>Advanced compatibility fallback</Text>
                    <Box className={styles.panelChevron}>{isLegacyOpen ? <HiChevronUp /> : <HiChevronDown />}</Box>
                  </HStack>
                  {isLegacyOpen && (
                    <Box id='restic-repo-legacy-fallback' className={styles.subsectionAdvancedBody}>
                      <Stack spacing={{ base: 1, md: 2 }}>
                        <Grid
                          className={styles.resticMountGrid}
                          templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                          columnGap={3}
                          rowGap={1}
                          w='full'
                        >
                          <GridItem className={styles.rowLabel}>
                            <HStack spacing={1} justify='space-between' width='full'>
                              <Text>Legacy repository URL (fallback)</Text>
                              {h(ResticLegacyRepoUrlHelp, 'Legacy Repository URL (fallback)')}
                            </HStack>
                          </GridItem>
                          <GridItem className={styles.valueCell}>
                            <TextForm
                              value={config?.backupResticRepository}
                              confirmTitle={`Confirm backup-restic-repository to `}
                              className={styles.textbox}
                              size='sm'
                              onSave={(value) => handleSettingChange('backup-restic-repository', value, true)}
                            />
                          </GridItem>
                        </Grid>
                      </Stack>
                    </Box>
                  )}
                </Stack>
              )}

              {archiveMode !== 'none' && (
                <Stack spacing={{ base: 1, md: 2 }}>
                  {archiveMode !== 'restic-aws' && (
                    <Grid
                      className={styles.resticMountGrid}
                      templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                      columnGap={3}
                      rowGap={1}
                      w='full'
                    >
                      <GridItem className={styles.rowLabel}>
                        <HStack spacing={1} justify='space-between' width='full'>
                          <Text>Backup restic repo append cluster</Text>
                          {h(ResticRepoAppendClusterHelp, 'Restic Repo Append Cluster')}
                        </HStack>
                      </GridItem>
                      <GridItem className={styles.valueCell}>
                        <HStack spacing={2} align='center'>
                          <RMSwitch
                            isChecked={config?.backupResticRepoAppendCluster}
                            isDisabled={user?.grants['cluster-settings'] == false}
                            confirmTitle={'Confirm switch settings for backup-restic-repo-append-cluster?'}
                            onChange={() => handleSwitchChange('backup-restic-repo-append-cluster')}
                          />
                        </HStack>
                      </GridItem>
                    </Grid>
                  )}

                  <Grid
                    className={styles.resticMountGrid}
                    templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                    columnGap={3}
                    rowGap={1}
                    w='full'
                  >
                    <GridItem className={styles.rowLabel}>
                      <HStack spacing={1} justify='space-between' width='full'>
                        <Text>Backup restic additional env</Text>
                        {h(ResticAdditionalEnvHelp, 'Backup Restic Additional Env')}
                      </HStack>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <TextForm
                        value={config?.backupResticAdditionalEnv}
                        confirmTitle={`Confirm backup-restic-additional-env to `}
                        className={styles.textbox}
                        size='sm'
                        placeholder='AWS_SESSION_TOKEN, NO_PROXY="host1,host2"'
                        onSave={(value) => handleSettingChange('backup-restic-additional-env', value)}
                      />
                    </GridItem>
                  </Grid>

                  <Box
                    borderWidth='1px'
                    borderColor='red.300'
                    borderRadius='md'
                    bg='red.50'
                    p={3}
                    _dark={{ bg: 'red.900', borderColor: 'red.600' }}
                  >
                    <Stack spacing={2}>
                      <HStack spacing={2}>
                        <Icon as={HiShieldExclamation} color='red.500' boxSize={4} />
                        <Text fontSize='xs' fontWeight='700' textTransform='uppercase' letterSpacing='wide' color='red.600' _dark={{ color: 'red.300' }}>
                          Danger zone
                        </Text>
                      </HStack>
                      <Text fontSize='sm' color='gray.700' _dark={{ color: 'gray.200' }}>
                        Permanently deletes all repository contents and leaves it uninitialized. This cannot be undone.
                      </Text>
                      <Box>
                        <RMButton
                          size='sm'
                          colorScheme='red'
                          leftIcon={<HiTrash />}
                          onClick={handleOpenWipeModal}
                          isDisabled={user?.grants['db-backup'] === false}
                        >
                          Wipe repository
                        </RMButton>
                      </Box>
                      {wipeResult && (
                        <Alert status={wipeResult.status === 'ok' ? 'warning' : 'error'} size='sm' borderRadius='md'>
                          <AlertIcon />
                          <Text fontSize='sm'>{wipeResult.message}</Text>
                        </Alert>
                      )}
                    </Stack>
                  </Box>
                </Stack>
              )}
            </Stack>
          )
        })}

        {renderSectionPanel({
          sectionKey: 'tagging',
          title: 'Tagging',
          description: 'Override restic host identity and tag templates.',
          isOpen: isTaggingOpen,
          setOpen: setIsTaggingOpen,
          controlsId: 'restic-repo-tagging',
          content: (
            <Stack spacing={{ base: 1, md: 2 }}>
              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <HStack spacing={1} justify='space-between' width='full'>
                    <Text>Backup restic host override</Text>
                    {h(ResticHostHelp, 'Restic Host Override')}
                  </HStack>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <HStack width='100%'>
                    <TextForm
                      value={config?.backupResticHost}
                      confirmTitle={`Confirm backup-restic-host to `}
                      className={styles.textbox}
                      size='sm'
                      placeholder='(empty = restic default host)'
                      onSave={(value) => handleSettingChange('backup-restic-host', value)}
                    />
                  </HStack>
                </GridItem>
              </Grid>

              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <HStack spacing={1} justify='space-between' width='full'>
                    <Text>Backup restic tags</Text>
                    {h(ResticTagsHelp, 'Restic Tag Templates')}
                  </HStack>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <HStack width='100%'>
                    <TextForm
                      value={config?.backupResticTags}
                      confirmTitle={`Confirm backup-restic-tags to `}
                      className={styles.textbox}
                      size='sm'
                      placeholder='tenant,cluster,engine,version,backup-type,backup-tool,line'
                      onSave={(value) => handleSettingChange('backup-restic-tags', value)}
                    />
                  </HStack>
                </GridItem>
              </Grid>
            </Stack>
          )
        })}
      </Stack>

      <ConfirmModal
        isOpen={isChecklistModalOpen}
        closeModal={onCloseChecklistModal}
        title="Initialize Restic Repository"
        body={
          <VStack align='start' spacing={3}>
            <Text>
              The {isAws ? 'S3/MinIO' : isSftp ? 'SFTP' : 'local'} repository is not yet initialized:
            </Text>
            <Text
              fontWeight='bold'
              fontSize='sm'
              fontFamily='monospace'
              bg='gray.100'
              p={2}
              borderRadius='md'
              wordBreak='break-all'
            >
              {isAws ? awsRepoPath : config?.backupResticLocalRepository}
            </Text>
            <Divider />
            <Checkbox
              isChecked={checklistConfirmedTarget}
              onChange={(e) => setChecklistConfirmedTarget(e.target.checked)}
            >
              <Text fontSize='sm'>I confirmed this is the intended repository target.</Text>
            </Checkbox>
            <Checkbox
              isChecked={checklistConfirmedNew}
              onChange={(e) => setChecklistConfirmedNew(e.target.checked)}
            >
              <Text fontSize='sm'>I understand this will create a new restic repository here.</Text>
            </Checkbox>
          </VStack>
        }
        onConfirmClick={handleChecklistConfirmInit}
        confirmButtonText="Initialize"
        confirmButtonProps={{
          isDisabled: !isChecklistReady,
          colorScheme: 'blue'
        }}
      />

      <ConfirmModal
        isOpen={isInitModalOpen}
        closeModal={onCloseInitModal}
        title="Re-initialize Restic Repository"
        body={
          <VStack align='start' spacing={3}>
            <Text>
              Re-initialize the {isAws ? 'S3/MinIO' : isSftp ? 'SFTP' : 'local'} Restic repository:
            </Text>
            <Text 
              fontWeight='bold' 
              fontSize='sm' 
              fontFamily='monospace'
              bg='gray.100' 
              p={2} 
              borderRadius='md'
              wordBreak='break-all'
            >
              {isAws
                ? awsRepoPath
                : config?.backupResticLocalRepository}
            </Text>
            <Divider />
            {isSftp ? (
              <Alert status='info' size='sm' borderRadius='md'>
                <AlertIcon />
                <Text fontSize='sm'>
                  Force re-initialization is not supported for SFTP repositories. To re-initialize from
                  scratch, remove the remote repository path manually over SSH first.
                </Text>
              </Alert>
            ) : (
              <Checkbox
                isChecked={initForce}
                onChange={(e) => setInitForce(e.target.checked)}
              >
                <Text fontSize='sm'>
                  Force re-initialization (overwrite existing configuration)
                </Text>
              </Checkbox>
            )}
            {initForce && !isSftp && (
              <Alert status='warning' size='sm' borderRadius='md'>
                <AlertIcon />
                <Text fontSize='sm'>
                  {isAws
                    ? 'Warning: Force re-initialization deletes all objects under the configured S3 bucket/prefix before creating a new repository.'
                    : 'Warning: This will overwrite the existing repository configuration.'}
                </Text>
              </Alert>
            )}
            {initForce && isAwsPrefixEmpty && (
              <Alert status='error' size='sm' borderRadius='md'>
                <AlertIcon />
                <Text fontSize='sm'>
                  Empty S3 prefix detected. Force init will impact the entire bucket.
                </Text>
              </Alert>
            )}
            {initForce && isAwsPrefixEmpty && (
              <Checkbox
                isChecked={confirmEmptyPrefix}
                onChange={(e) => setConfirmEmptyPrefix(e.target.checked)}
              >
                <Text fontSize='sm'>
                  I understand this will affect the entire bucket.
                </Text>
              </Checkbox>
            )}
          </VStack>
        }
        onConfirmClick={handleConfirmInit}
        confirmButtonText="Initialize"
        confirmButtonProps={{
          isDisabled: isForceInitBlocked,
          colorScheme: initForce ? 'red' : 'blue'
        }}
      />

      <ConfirmModal
        isOpen={isCopyModalOpen}
        closeModal={handleCloseCopyModal}
        title="Migrate / Copy Snapshots"
        onConfirmClick={handleCopySubmit}
        confirmButtonText="Queue copy"
        confirmButtonProps={{ isLoading: copySubmitting, colorScheme: 'orange' }}
        body={
          <VStack align='start' spacing={4}>
            <Box w='full'>
              <Text fontSize='sm' fontWeight='semibold' mb={1}>Destination (read-only)</Text>
              <Text
                fontSize='sm'
                fontFamily='monospace'
                bg='gray.100'
                p={2}
                borderRadius='md'
                wordBreak='break-all'
                w='full'
              >
                {isAws ? (awsRepoPath || 's3:<bucket>/<prefix>') : (config?.backupResticLocalRepository || '(not configured)')}
              </Text>
              {!isAws && appendCluster && (
                <Text fontSize='xs' color='gray.500' mt={1}>
                  Effective path may include /{clusterName} suffix if not already present.
                </Text>
              )}
              <Text fontSize='xs' color='gray.500' mt={1}>
                Repository configuration will not be changed by this copy operation.
              </Text>
            </Box>

            <Divider />

            <FormControl>
              <FormLabel fontSize='sm'>Source input</FormLabel>
              <Select size='sm' value={copySourceStrategy} onChange={(e) => setCopySourceStrategy(e.target.value)}>
                <option value='manual'>Manual entry</option>
                <option value='saved-local'>Use saved Local/SFTP config</option>
                <option value='saved-s3' disabled={!savedS3Available}>Use saved S3 config{!savedS3Available ? ' (no S3 config stored)' : ''}</option>
              </Select>
            </FormControl>

            {copySourceStrategy === 'saved-local' && (
              <Alert status='info' size='sm' borderRadius='md'>
                <AlertIcon />
                <Text fontSize='xs'>
                  Source repository and password will be resolved from the cluster&apos;s current stored Local/SFTP configuration. No secrets are sent from this form.
                </Text>
              </Alert>
            )}

            {copySourceStrategy === 'saved-s3' && (
              <Alert status='info' size='sm' borderRadius='md'>
                <AlertIcon />
                <Text fontSize='xs'>
                  {awsBucket
                    ? <>Source repository, credentials, and password will be resolved from the cluster&apos;s current stored S3 configuration (bucket: <strong>{awsBucket}</strong>). No secrets are sent from this form.</>
                    : <>Source repository and password will be resolved from the stored S3 URL in <strong>backup-restic-repository</strong>. No secrets are sent from this form.</>
                  }
                </Text>
              </Alert>
            )}

            {copySourceStrategy === 'manual' && (
              <>
                <FormControl>
                  <FormLabel fontSize='sm'>Source repository type</FormLabel>
                  <Select size='sm' value={copySourceMode} onChange={(e) => setCopySourceMode(e.target.value)}>
                    <option value='restic-local'>Local filesystem</option>
                    <option value='restic-sftp'>SFTP</option>
                    <option value='restic-aws'>S3 / Object storage</option>
                  </Select>
                </FormControl>

                {(copySourceMode === 'restic-local' || copySourceMode === 'restic-sftp') && (
                  <FormControl isRequired>
                    <FormLabel fontSize='sm'>
                      {copySourceMode === 'restic-sftp' ? 'Source SFTP URI' : 'Source repository path'}
                    </FormLabel>
                    <Input
                      size='sm'
                      value={copySourceRepo}
                      onChange={(e) => setCopySourceRepo(e.target.value)}
                      placeholder={copySourceMode === 'restic-sftp' ? 'sftp:user@host:/path/to/repo' : '/path/to/source/repo'}
                    />
                    {copySourceMode === 'restic-sftp' && (
                      <Text fontSize='xs' color='gray.500' mt={1}>
                        SSH auth uses the running process&apos;s key-based SSH config. Password-based SSH is not supported.
                      </Text>
                    )}
                  </FormControl>
                )}

                {copySourceMode === 'restic-aws' && (
                  <VStack spacing={3} w='full' align='start'>
                    <FormControl>
                      <FormLabel fontSize='sm'>Source S3 endpoint</FormLabel>
                      <Input size='sm' value={copySourceAwsEndpoint} onChange={(e) => setCopySourceAwsEndpoint(e.target.value)} placeholder='https://minio.example.com' />
                    </FormControl>
                    <FormControl isRequired>
                      <FormLabel fontSize='sm'>Source S3 bucket</FormLabel>
                      <Input size='sm' value={copySourceAwsBucket} onChange={(e) => setCopySourceAwsBucket(e.target.value)} placeholder='bucket-name' />
                    </FormControl>
                    <FormControl>
                      <FormLabel fontSize='sm'>Source S3 prefix</FormLabel>
                      <Input size='sm' value={copySourceAwsPrefix} onChange={(e) => setCopySourceAwsPrefix(e.target.value)} placeholder='optional/prefix' />
                    </FormControl>
                    <FormControl>
                      <FormLabel fontSize='sm'>Source S3 access key ID</FormLabel>
                      <Input size='sm' value={copySourceAwsAccessKeyId} onChange={(e) => setCopySourceAwsAccessKeyId(e.target.value)} />
                    </FormControl>
                    <FormControl>
                      <FormLabel fontSize='sm'>Source S3 access secret</FormLabel>
                      <Input size='sm' type='password' value={copySourceAwsAccessSecret} onChange={(e) => setCopySourceAwsAccessSecret(e.target.value)} />
                    </FormControl>
                    <FormControl>
                      <FormLabel fontSize='sm'>Source S3 region</FormLabel>
                      <Input size='sm' value={copySourceAwsRegion} onChange={(e) => setCopySourceAwsRegion(e.target.value)} placeholder='us-east-1' />
                    </FormControl>
                  </VStack>
                )}

                <FormControl isRequired>
                  <FormLabel fontSize='sm'>Source repository password</FormLabel>
                  <Input size='sm' type='password' value={copySourcePassword} onChange={(e) => setCopySourcePassword(e.target.value)} />
                </FormControl>
              </>
            )}

            <FormControl>
              <FormLabel fontSize='sm'>
                Source key hint{' '}
                <Text as='span' fontSize='xs' color='gray.500'>(optional)</Text>
              </FormLabel>
              <Input size='sm' value={copySourceKeyHint} onChange={(e) => setCopySourceKeyHint(e.target.value)} placeholder='key ID hint for multi-key repos' />
            </FormControl>

            <Divider />

            <Checkbox
              isChecked={copyInitDestination}
              onChange={(e) => {
                setCopyInitDestination(e.target.checked)
                if (!e.target.checked) setCopyCopyChunkerParams(false)
              }}
            >
              <Text fontSize='sm'>Initialize destination before copy</Text>
            </Checkbox>
            <Checkbox
              isChecked={copyCopyChunkerParams}
              isDisabled={!copyInitDestination}
              onChange={(e) => setCopyCopyChunkerParams(e.target.checked)}
            >
              <Text fontSize='sm'>
                Copy chunker parameters{' '}
                <Text as='span' fontSize='xs' color='gray.500'>(requires init destination)</Text>
              </Text>
            </Checkbox>

            <HStack
              as='button'
              type='button'
              spacing={1}
              onClick={() => setShowCopyAdvancedFilters((prev) => !prev)}
            >
              <Text fontSize='sm' color='blue.500'>Advanced filters</Text>
              <Box fontSize='sm'>{showCopyAdvancedFilters ? <HiChevronUp /> : <HiChevronDown />}</Box>
            </HStack>

            {showCopyAdvancedFilters && (
              <VStack spacing={3} w='full' align='start'>
                <FormControl>
                  <FormLabel fontSize='sm'>
                    Snapshot IDs{' '}
                    <Text as='span' fontSize='xs' color='gray.500'>(comma-separated; overrides host/path/tag filters)</Text>
                  </FormLabel>
                  <Input size='sm' value={copySnapshotIds} onChange={(e) => setCopySnapshotIds(e.target.value)} placeholder='abc123, def456' />
                </FormControl>
                <FormControl>
                  <FormLabel fontSize='sm'>
                    Filter by host{' '}
                    <Text as='span' fontSize='xs' color='gray.500'>(comma-separated)</Text>
                  </FormLabel>
                  <Input size='sm' value={copyFilterHost} onChange={(e) => setCopyFilterHost(e.target.value)} />
                </FormControl>
                <FormControl>
                  <FormLabel fontSize='sm'>
                    Filter by path{' '}
                    <Text as='span' fontSize='xs' color='gray.500'>(comma-separated)</Text>
                  </FormLabel>
                  <Input size='sm' value={copyFilterPath} onChange={(e) => setCopyFilterPath(e.target.value)} />
                </FormControl>
                <FormControl>
                  <FormLabel fontSize='sm'>
                    Filter by tag{' '}
                    <Text as='span' fontSize='xs' color='gray.500'>(comma-separated)</Text>
                  </FormLabel>
                  <Input size='sm' value={copyFilterTag} onChange={(e) => setCopyFilterTag(e.target.value)} />
                </FormControl>
              </VStack>
            )}

            <Alert status='info' size='sm' borderRadius='md'>
              <AlertIcon />
              <Text fontSize='xs'>
                Snapshots will be copied into the destination repository above. The copy job will appear in the Restic task queue and may take time to complete.
              </Text>
            </Alert>

            {copyError && (
              <Alert status='error' size='sm' borderRadius='md'>
                <AlertIcon />
                <Text fontSize='sm'>{copyError}</Text>
              </Alert>
            )}
          </VStack>
        }
      />

      <ConfirmModal
        isOpen={isWipeModalOpen}
        closeModal={handleCloseWipeModal}
        title="Wipe Restic Repository"
        onConfirmClick={handleWipeSubmit}
        confirmButtonText="Wipe repository"
        confirmButtonProps={{ isLoading: wipeSubmitting, isDisabled: isWipeSubmitBlocked, colorScheme: 'red' }}
        body={
          <VStack align='start' spacing={3}>
            <Alert status='error' size='sm' borderRadius='md' variant='left-accent'>
              <AlertIcon as={HiShieldExclamation} />
              <Text fontSize='sm'>
                <strong>Destructive — cannot be undone.</strong> All repository contents will be permanently deleted.
                The repository will be left empty and uninitialized.
              </Text>
            </Alert>

            <Box w='full' borderWidth='1px' borderColor='gray.200' borderRadius='md' overflow='hidden' _dark={{ borderColor: 'gray.600' }}>
              <Box px={3} py={1} bg='gray.100' borderBottomWidth='1px' borderColor='gray.200' _dark={{ bg: 'gray.700', borderColor: 'gray.600' }}>
                <HStack spacing={2} justify='space-between'>
                  <Text fontSize='xs' fontWeight='600' textTransform='uppercase' letterSpacing='wide' color='gray.500'>
                    Target
                  </Text>
                  <Text fontSize='xs' color='gray.500'>
                    {
                      wipeServerBackend === 'restic-aws' ? 'S3 / Object storage'
                      : wipeServerBackend === 'restic-sftp' ? 'SFTP remote'
                      : wipeServerBackend === 'restic-local' ? 'Local filesystem'
                      : isAws ? 'S3 / Object storage' : isSftp ? 'SFTP remote' : 'Local filesystem'
                    }
                  </Text>
                </HStack>
              </Box>
              <Box px={3} py={2}>
                {wipeTargetLoading ? (
                  <Text fontSize='sm' color='gray.400' fontFamily='monospace'>Loading…</Text>
                ) : (
                  <Text fontSize='sm' fontFamily='monospace' wordBreak='break-all' color={wipeDisplayTarget ? 'inherit' : 'gray.400'}>
                    {wipeDisplayTarget || '(not configured)'}
                  </Text>
                )}
              </Box>
            </Box>

            {wipeCanWipe === false && wipeServerMessage && (
              <Alert status='warning' size='sm' borderRadius='md'>
                <AlertIcon />
                <Text fontSize='sm'>{wipeServerMessage}</Text>
              </Alert>
            )}

            {(wipeServerBackend === 'restic-sftp' || (!wipeServerBackend && isSftp)) && (
              <Alert status='info' size='sm' borderRadius='md'>
                <AlertIcon />
                <Text fontSize='sm'>
                  SFTP wipe runs over SSH. The process must have key-based SSH access to the remote host.
                  The remote repository root directory will be preserved; only its contents will be deleted.
                </Text>
              </Alert>
            )}

            {isWipeEmptyPrefixRisk && (
              <Alert status='error' size='sm' borderRadius='md'>
                <AlertIcon />
                <Text fontSize='sm'>
                  <strong>Empty S3 prefix detected.</strong> Wiping will affect the entire bucket, not just a repository prefix.
                </Text>
              </Alert>
            )}

            {isWipeEmptyPrefixRisk && (
              <Checkbox
                isChecked={wipeAllowEmptyPrefix}
                onChange={(e) => setWipeAllowEmptyPrefix(e.target.checked)}
              >
                <Text fontSize='sm'>I understand this will wipe the entire bucket.</Text>
              </Checkbox>
            )}

            <Divider />

            <Box w='full'>
              <HStack spacing={1} mb={1}>
                <Icon as={HiLockClosed} color='gray.500' boxSize={3} />
                <Text fontSize='sm' fontWeight='medium'>
                  Type the target path to confirm:
                </Text>
              </HStack>
              <Input
                size='sm'
                value={wipeTypedConfirm}
                onChange={(e) => setWipeTypedConfirm(e.target.value)}
                placeholder={wipeDisplayTarget || 'repository path'}
                fontFamily='monospace'
                isDisabled={wipeTargetLoading || wipeCanonicalTarget === null || wipeCanWipe === false}
                borderColor={wipeTypedConfirm && !isWipeTypedMatch ? 'red.400' : undefined}
                _focus={{ borderColor: wipeTypedConfirm && !isWipeTypedMatch ? 'red.400' : 'blue.400', boxShadow: 'none' }}
              />
              {wipeTypedConfirm && !isWipeTypedMatch && (
                <Text fontSize='xs' color='red.500' mt={1}>Does not match the server-computed target.</Text>
              )}
              {wipeTypedConfirm && isWipeTypedMatch && (
                <HStack spacing={1} mt={1}>
                  <Icon as={HiCheckCircle} color='green.500' boxSize={3} />
                  <Text fontSize='xs' color='green.600'>Target confirmed.</Text>
                </HStack>
              )}
            </Box>

            {wipeError && (
              <Alert status='error' size='sm' borderRadius='md'>
                <AlertIcon />
                <Text fontSize='sm'>{wipeError}</Text>
              </Alert>
            )}
          </VStack>
        }
      />
    </VStack>
  )
}

export default ResticRepositorySettings
