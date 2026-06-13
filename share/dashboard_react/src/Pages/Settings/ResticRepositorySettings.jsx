import { Box, Flex, Grid, GridItem, HStack, Stack, Text, VStack, Checkbox, Alert, AlertIcon, Divider, useDisclosure } from '@chakra-ui/react'
import React, { useState } from 'react'
import { HiChevronDown, HiChevronUp, HiQuestionMarkCircle, HiRefresh } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'
import RMButton from '../../components/RMButton'
import RMSwitch from '../../components/RMSwitch'
import TextForm from '../../components/TextForm'
import Dropdown from '../../components/Dropdown'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import { resticInitRepo } from '../../redux/clusterSlice'
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

Leave empty to fall back to the default AWS credential chain (environment variables, shared credentials file, or instance role).

Config: \`backup-restic-aws-access-key-id\``

const ResticAwsAccessSecretHelp = `**Backup Restic AWS Access Secret**

Secret access key paired with "Backup restic aws access key id". Stored encrypted.

Leave empty to fall back to the default AWS credential chain.

Config: \`backup-restic-aws-access-secret\``

const ResticAwsRegionHelp = `**Backup Restic AWS Region**

AWS region of the S3 bucket (e.g. \`us-east-1\`, \`eu-west-1\`).

Some S3-compatible services require this even when a custom endpoint is set. Leave empty to use the AWS SDK's default region resolution.

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

const ResticEffectiveS3RepoHelp = `**Effective S3 Repository**

Read-only preview of the full \`s3:\` repository path that restic will use, built from the endpoint, bucket, and prefix above plus the append-cluster rule.

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
  const awsRepoPath = awsBucket
    ? `s3:${trimmedEndpoint ? `${trimmedEndpoint}/` : ''}${awsBucket}${effectivePrefix ? `/${effectivePrefix}` : ''}`
    : effectiveLegacyRepoPath
  const isAwsPrefixEmpty = isAws && awsBucket && !awsPrefix
  const isForceInitBlocked = initForce && ((isAwsPrefixEmpty && !confirmEmptyPrefix) || isSftp)

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
                  <Text className={styles.helperText}>
                    backup-archive-mode selects the Restic storage backend. None disables Restic backups. Local and
                    SFTP use the local repository path (Storage section). S3 enables the AWS/S3 settings below.
                    Switching does not erase the values stored for other types.
                  </Text>
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
                      <Text className={styles.helperText}>
                        Stored in backup-restic-local-repository using restic&apos;s sftp syntax
                        (sftp:user@host:/path/to/repo). The user in the path is an SSH login authenticated via
                        SSH key only (no password). See (?) for SSH key setup and how this differs from the
                        repository password above.
                      </Text>
                    </GridItem>
                  </Grid>
                </Stack>
              )}

              {archiveMode === 'restic-aws' && (
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
                      <Text className={styles.helperText}>Empty uses AWS SDK default region resolution.</Text>
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
                      <Text className={styles.helperText}>
                        Optional custom S3 endpoint (http/https with host; leave empty for AWS). Do not use s3:// here.
                      </Text>
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
                        <Text>Effective S3 repository</Text>
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
                      <Text className={styles.helperText}>
                        Preview of the S3 repository path after applying append-cluster rules.
                      </Text>
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
                        <Text>Legacy repository URL (fallback)</Text>
                        {h(ResticLegacyRepoUrlHelp, 'Legacy Repository URL (fallback)')}
                      </HStack>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <HStack width='100%' spacing={2}>
                        <TextForm
                          value={config?.backupResticRepository}
                          confirmTitle={`Confirm backup-restic-repository to `}
                          className={styles.textbox}
                          size='sm'
                          onSave={(value) => handleSettingChange('backup-restic-repository', value, true)}
                        />
                        <RMButton
                          size='sm'
                          colorScheme='blue'
                          onClick={handleResticInit}
                          isDisabled={user?.grants['cluster-settings'] === false}
                          title="Re-initialize S3 repository"
                        >
                          <HiRefresh />
                        </RMButton>
                        {h(
                          'Re-initialize the S3/MinIO Restic repository. This creates a new repository configuration at the specified S3 bucket/path. Force re-initialization deletes all objects under the configured bucket/prefix. Ensure AWS credentials and bucket path are correct before initializing.',
                          'Re-initialize S3 Repository'
                        )}
                      </HStack>
                      <Text className={styles.helperText}>
                        Used only when AWS is enabled and the S3 bucket field is empty. For custom S3 services use
                        s3:https://server:port/bucket in this field.
                      </Text>
                    </GridItem>
                  </Grid>
                </Stack>
              )}

              {archiveMode !== 'none' && (
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
                      <Text className={styles.helperText}>
                        Optional env vars to pass to restic (comma or space separated KEY or KEY=VALUE). Quote values with commas.
                      </Text>
                    </GridItem>
                  </Grid>
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
    </VStack>
  )
}

export default ResticRepositorySettings
