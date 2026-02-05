import {
  Box,
  Flex,
  Grid,
  GridItem,
  HStack,
  Stack,
  VStack,
  Text
} from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import { useDispatch } from 'react-redux'
import TextForm from '../../components/TextForm'
import RMSwitch from '../../components/RMSwitch'
import NumberInput from '../../components/NumberInput'
import RMIconButton from '../../components/RMIconButton'
import { HiChevronDown, HiChevronUp, HiQuestionMarkCircle } from 'react-icons/hi'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { clusterService } from '../../services/clusterService'
import styles from './styles.module.scss'
import tableStyles from '../../components/TableType2/styles.module.scss'

function ResticMountSettings({ clusterName, config, user }) {
  const dispatch = useDispatch()
  const joinClasses = (...classes) => classes.filter(Boolean).join(' ')
  const isBrowser = typeof window !== 'undefined'
  const storagePrefix = 'settings:restic-mount-settings'
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
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)
  const [isMountDestinationOpen, setIsMountDestinationOpen] = useState(() =>
    readStoredState('mount-destination', false)
  )
  const [isMountControlOpen, setIsMountControlOpen] = useState(() =>
    readStoredState('mount-control', true)
  )
  const [isSnapshotFiltersOpen, setIsSnapshotFiltersOpen] = useState(() =>
    readStoredState('snapshot-filters', false)
  )
  const [isMountTemplatesOpen, setIsMountTemplatesOpen] = useState(() =>
    readStoredState('mount-templates', false)
  )
  const [isPermissionsOpen, setIsPermissionsOpen] = useState(() => readStoredState('permissions', false))
  const [isRuntimeBehaviorOpen, setIsRuntimeBehaviorOpen] = useState(() =>
    readStoredState('runtime-behavior', false)
  )
  const areAllSectionsOpen =
    isMountControlOpen &&
    isMountDestinationOpen &&
    isSnapshotFiltersOpen &&
    isMountTemplatesOpen &&
    isPermissionsOpen &&
    isRuntimeBehaviorOpen
  const [action, setAction] = useState({
    title: '',
    body: <></>
  })
  const { title, body } = action
  const allowOtherEnabled = Boolean(config?.backupResticMountAllowOther)
  const [mountStatus, setMountStatus] = useState(null)
  const [mountStatusError, setMountStatusError] = useState('')
  const [isMountStatusLoading, setIsMountStatusLoading] = useState(false)
  const [isMountActionLoading, setIsMountActionLoading] = useState(false)
  const isMounted = Boolean(mountStatus?.is_mounted)
  const mountPath = mountStatus?.mount_path || ''
  const refCount = Number(mountStatus?.ref_count ?? 0)
  const activeUsers = Array.isArray(mountStatus?.active_users) ? mountStatus.active_users : []
  const mountActionDisabled =
    isMountActionLoading || isMountStatusLoading || !clusterName || user?.grants['cluster-settings'] == false

  const ResticMountFiltersHelp = `Controls which snapshots appear in the mount.  
Filters are AND across host/tag/path; multiple values within a field are OR.  
Host/path filters accept comma or space separated lists.  
Tag filters are space-separated taglists (each entry maps to restic --tag).  
Commas inside a taglist are preserved and mean ALL tags must match (e.g. tenant:acme,cluster:prod).  
Use quotes to keep a taglist with spaces together. Leave empty to match all.  
Path filters must be absolute (e.g. /var/lib/mysql).`

  const ResticMountTemplatesHelp = `Controls the virtual layout and timestamp formatting in the mount.  
Path template is comma-separated; defaults: ids/%i (replication-manager).  
Common layouts: snapshots/%T, hosts/%h/%T, tags/%t/%T, ids/%i.  
Tokens: %i=short ID, %I=full ID, %u=user, %h=host, %t=tags, %T=time.  
Time template uses Go layout (e.g. 2006-01-02T15:04:05Z07:00); empty = RFC3339.`

  const ResticMountControlHelp = `Manual mounts are *pinned* and stay active until you click Unmount or the manager shuts down.  
Reseed mounts are *not pinned*; they auto-unmount once all reseed jobs release their mount reference.  
If a reseed is running, Unmount will wait until the job finishes before stopping the mount.`

  const openCommonModal = () => {
    setIsCommonModalOpen(true)
  }

  const closeCommonModal = () => {
    setIsCommonModalOpen(false)
  }

  const openInfoModal = (modalTitle, content) => {
    setAction({
      title: modalTitle,
      body: (
        <Box className={joinClasses(modalStyles.infoTooltip, styles.infoTooltip)}>
          <Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown>
        </Box>
      )
    })
    openCommonModal()
  }

  const handleSettingChange = (setting, value) =>
    dispatch(
      setSetting({
        clusterName: clusterName,
        setting,
        value
      })
    )

  const handleSwitchChange = (setting) =>
    dispatch(
      switchSetting({
        clusterName: clusterName,
        setting
      })
    )

  const toggleSection = (section, setter) => {
    setter((prev) => {
      const next = !prev
      persistStoredState(section, next)
      return next
    })
  }

  const setAllSectionsState = (nextState) => {
    setIsMountControlOpen(nextState)
    persistStoredState('mount-control', nextState)
    setIsMountDestinationOpen(nextState)
    persistStoredState('mount-destination', nextState)
    setIsSnapshotFiltersOpen(nextState)
    persistStoredState('snapshot-filters', nextState)
    setIsMountTemplatesOpen(nextState)
    persistStoredState('mount-templates', nextState)
    setIsPermissionsOpen(nextState)
    persistStoredState('permissions', nextState)
    setIsRuntimeBehaviorOpen(nextState)
    persistStoredState('runtime-behavior', nextState)
  }

  const fetchMountStatus = async () => {
    if (!clusterName) return
    setIsMountStatusLoading(true)
    setMountStatusError('')
    try {
      const { data, status } = await clusterService.getResticMountStatus(clusterName)
      if (status >= 200 && status < 300) {
        setMountStatus(data)
      } else {
        setMountStatusError(`Failed to fetch mount status (status ${status})`)
      }
    } catch (error) {
      setMountStatusError(error?.message || 'Failed to fetch mount status')
    } finally {
      setIsMountStatusLoading(false)
    }
  }

  const handleMountToggle = async (nextAction) => {
    if (!clusterName) return
    setIsMountActionLoading(true)
    setMountStatusError('')
    try {
      const { data, status } = await clusterService.resticMountToggle(clusterName, nextAction)
      if (status >= 200 && status < 300) {
        setMountStatus(data)
      } else {
        setMountStatusError(`Failed to ${nextAction} restic mount (status ${status})`)
      }
    } catch (error) {
      setMountStatusError(error?.message || `Failed to ${nextAction} restic mount`)
    } finally {
      setIsMountActionLoading(false)
    }
  }

  useEffect(() => {
    fetchMountStatus()
  }, [clusterName])

  const renderPanel = ({
    sectionKey,
    isOpen,
    setOpen,
    title,
    description,
    controlsId,
    content
  }) => (
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
    <>
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
              <Text className={styles.resticMountHeaderTitle}>Restic mount settings</Text>
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
                aria-controls='restic-mount-control-content restic-mount-destination-content restic-snapshot-filters-content restic-mount-templates-content restic-permissions-content restic-runtime-behavior-content'
                aria-label={
                  areAllSectionsOpen
                    ? 'Hide all restic mount settings sections'
                    : 'Show all restic mount settings sections'
                }
                onClick={() => setAllSectionsState(!areAllSectionsOpen)}
              >
                {areAllSectionsOpen ? 'Hide all' : 'Show all'}
              </Box>
            </HStack>
          </Flex>
          {renderPanel({
            sectionKey: 'mount-control',
            isOpen: isMountControlOpen,
            setOpen: setIsMountControlOpen,
            title: (
              <HStack spacing={2} align='center'>
                <Text className={styles.panelTitle}>Mount control</Text>
                <RMIconButton
                  icon={HiQuestionMarkCircle}
                  onClick={(event) => {
                    event.stopPropagation()
                    openInfoModal('Restic Mount Control', ResticMountControlHelp)
                  }}
                />
              </HStack>
            ),
            description: 'Manually mount or unmount the restic repository using current settings.',
            controlsId: 'restic-mount-control-content',
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
                    <Text>Mount status</Text>
                  </GridItem>
                  <GridItem className={styles.valueCell}>
                    <Text>{isMountStatusLoading ? 'Loading…' : isMounted ? 'Mounted' : 'Not mounted'}</Text>
                    {mountStatusError && <Text className={styles.helperText}>{mountStatusError}</Text>}
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
                    <Text>Mount path</Text>
                  </GridItem>
                  <GridItem className={styles.valueCell}>
                    <Text>{mountPath || '(none)'}</Text>
                    {isMounted && refCount > 0 && (
                      <Text className={styles.helperText}>
                        In use by {refCount} job{refCount === 1 ? '' : 's'}. Unmount waits for active users.
                      </Text>
                    )}
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
                    <Text>Active users</Text>
                  </GridItem>
                  <GridItem className={styles.valueCell}>
                    <Text>{activeUsers.length > 0 ? activeUsers.join(', ') : '(none)'}</Text>
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
                    <Text>Actions</Text>
                  </GridItem>
                  <GridItem className={styles.valueCell}>
                    <HStack spacing={2} flexWrap='wrap'>
                      <Box
                        as='button'
                        type='button'
                        className={styles.resticMountActionButton}
                        onClick={() => fetchMountStatus()}
                        disabled={isMountStatusLoading}
                      >
                        Refresh
                      </Box>
                      <Box
                        as='button'
                        type='button'
                        className={styles.resticMountActionButton}
                        onClick={() => handleMountToggle('mount')}
                        disabled={mountActionDisabled || isMounted}
                      >
                        Mount
                      </Box>
                      <Box
                        as='button'
                        type='button'
                        className={styles.resticMountActionButton}
                        onClick={() => handleMountToggle('unmount')}
                        disabled={mountActionDisabled || !isMounted}
                      >
                        Unmount
                      </Box>
                    </HStack>
                  </GridItem>
                </Grid>
              </Stack>
            )
          })}
          {renderPanel({
            sectionKey: 'mount-destination',
            isOpen: isMountDestinationOpen,
            setOpen: setIsMountDestinationOpen,
            title: 'Mount destination',
            description: 'Choose where restic mounts snapshots for inspection or restore.',
            controlsId: 'restic-mount-destination-content',
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
                    <Text>Restic mount target directory</Text>
                  </GridItem>
                  <GridItem className={styles.valueCell}>
                    <TextForm
                      value={config?.backupResticMountTargetDir}
                      confirmTitle={`Confirm backup-restic-mount-target-dir to `}
                      className={styles.textbox}
                      size='sm'
                      placeholder='(empty = default mount path)'
                      onSave={(value) => handleSettingChange('backup-restic-mount-target-dir', value)}
                    />
                    <Text className={styles.helperText}>Empty value uses the default mount path.</Text>
                  </GridItem>
                </Grid>
              </Stack>
            )
          })}

          {renderPanel({
            sectionKey: 'snapshot-filters',
            isOpen: isSnapshotFiltersOpen,
            setOpen: setIsSnapshotFiltersOpen,
            title: (
              <HStack spacing={2} align='center'>
                <Text className={styles.panelTitle}>Snapshot filters</Text>
                <RMIconButton
                  icon={HiQuestionMarkCircle}
                  onClick={(event) => {
                    event.stopPropagation()
                    openInfoModal('Restic Mount Filters', ResticMountFiltersHelp)
                  }}
                />
              </HStack>
            ),
            description: 'Limit which snapshots appear inside the mount.',
            controlsId: 'restic-snapshot-filters-content',
            content: (
               <Stack spacing={{ base: 1, md: 2 }}>
                <Stack spacing={{ base: 1, md: 2 }}>
                  <Grid
                    className={styles.resticMountGrid}
                    templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                    columnGap={3}
                    rowGap={1}
                    w='full'
                  >
                    <GridItem className={styles.rowLabel}>
                      <Text>Restic mount host filter</Text>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <TextForm
                        value={config?.backupResticMountHost}
                        confirmTitle={`Confirm backup-restic-mount-host to `}
                        className={styles.textbox}
                        size='sm'
                        placeholder='host1,host2'
                        onSave={(value) => handleSettingChange('backup-restic-mount-host', value)}
                      />
                      <Text className={styles.helperText}>Comma-separated hostnames; empty means no filter.</Text>
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
                      <Text>Restic mount tag filter</Text>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <TextForm
                        value={config?.backupResticMountTag}
                        confirmTitle={`Confirm backup-restic-mount-tag to `}
                        className={styles.textbox}
                        size='sm'
                        placeholder='tag1 tag2'
                        onSave={(value) => handleSettingChange('backup-restic-mount-tag', value)}
                      />
                      <Text className={styles.helperText}>
                        Space-separated tags; commas inside a tag mean AND. Empty means no filter.
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
                      <Text>Restic mount path filter</Text>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <TextForm
                        value={config?.backupResticMountPath}
                        confirmTitle={`Confirm backup-restic-mount-path to `}
                        className={styles.textbox}
                        size='sm'
                        placeholder='/var/lib/mysql,/srv/data'
                        onSave={(value) => handleSettingChange('backup-restic-mount-path', value)}
                      />
                      <Text className={styles.helperText}>Absolute paths only; empty means no filter.</Text>
                    </GridItem>
                  </Grid>
                </Stack>
              </Stack>
            )
          })}

          {renderPanel({
            sectionKey: 'mount-templates',
            isOpen: isMountTemplatesOpen,
            setOpen: setIsMountTemplatesOpen,
            title: (
              <HStack spacing={2} align='center'>
                <Text className={styles.panelTitle}>Mount templates</Text>
                <RMIconButton
                  icon={HiQuestionMarkCircle}
                  onClick={(event) => {
                    event.stopPropagation()
                    openInfoModal('Restic Mount Templates', ResticMountTemplatesHelp)
                  }}
                />
              </HStack>
            ),
            description: 'Control the virtual layout and timestamp formatting inside the mount.',
            controlsId: 'restic-mount-templates-content',
            content: (
               <Stack spacing={{ base: 1, md: 2 }}>
                <Stack spacing={{ base: 1, md: 2 }}>
                  <Grid
                    className={styles.resticMountGrid}
                    templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                    columnGap={3}
                    rowGap={1}
                    w='full'
                  >
                    <GridItem className={styles.rowLabel}>
                      <Text>Restic mount path template</Text>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <TextForm
                        value={config?.backupResticMountPathTemplate}
                        confirmTitle={`Confirm backup-restic-mount-path-template to `}
                        className={styles.textbox}
                        size='sm'
                        placeholder='(comma-separated templates)'
                        onSave={(value) => handleSettingChange('backup-restic-mount-path-template', value)}
                      />
                      <Text className={styles.helperText}>
                        Multiple templates allowed; leave empty for defaults.
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
                      <Text>Restic mount time template</Text>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <TextForm
                        value={config?.backupResticMountTimeTemplate}
                        confirmTitle={`Confirm backup-restic-mount-time-template to `}
                        className={styles.textbox}
                        size='sm'
                        placeholder='2006-01-02T15:04:05Z07:00'
                        onSave={(value) => handleSettingChange('backup-restic-mount-time-template', value)}
                      />
                      <Text className={styles.helperText}>Use Go time layout; leave empty for defaults.</Text>
                    </GridItem>
                  </Grid>
                </Stack>
              </Stack>
            )
          })}

          {renderPanel({
            sectionKey: 'permissions',
            isOpen: isPermissionsOpen,
            setOpen: setIsPermissionsOpen,
            title: 'Permissions',
            description: 'Control mount ownership and permission handling.',
            controlsId: 'restic-permissions-content',
            content: (
               <Stack spacing={{ base: 1, md: 2 }}>
                <Stack spacing={{ base: 1, md: 2 }}>
                  <Grid
                    className={styles.resticMountGrid}
                    templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                    columnGap={3}
                    rowGap={1}
                    w='full'
                  >
                    <GridItem className={styles.rowLabel}>
                      <Text>Restic mount allow other users</Text>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <RMSwitch
                        isChecked={allowOtherEnabled}
                        isDisabled={user?.grants['cluster-settings'] == false}
                        confirmTitle={'Confirm switch settings for backup-restic-mount-allow-other?'}
                        onChange={() => handleSwitchChange('backup-restic-mount-allow-other')}
                      />
                      <Text className={styles.helperText}>
                        {allowOtherEnabled
                          ? 'Warning: enable user_allow_other in /etc/fuse.conf; all local users can access the mount.'
                          : 'Enabling requires FUSE user_allow_other; all local users can access the mount.'}
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
                      <Text>Restic mount ignore default permissions</Text>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <RMSwitch
                        isChecked={config?.backupResticMountNoDefaultPermissions}
                        isDisabled={user?.grants['cluster-settings'] == false}
                        confirmTitle={'Confirm switch settings for backup-restic-mount-no-default-permissions?'}
                        onChange={() => handleSwitchChange('backup-restic-mount-no-default-permissions')}
                      />
                      <Text className={styles.helperText}>
                        Disables kernel permission checks (default_permissions) and ignores Unix mode bits.
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
                      <Text>Restic mount owner root</Text>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <RMSwitch
                        isChecked={config?.backupResticMountOwnerRoot}
                        isDisabled={user?.grants['cluster-settings'] == false}
                        confirmTitle={'Confirm switch settings for backup-restic-mount-owner-root?'}
                        onChange={() => handleSwitchChange('backup-restic-mount-owner-root')}
                      />
                      <Text className={styles.helperText}>Show mounted files as owned by root.</Text>
                    </GridItem>
                  </Grid>
                </Stack>
              </Stack>
            )
          })}

          {renderPanel({
            sectionKey: 'runtime-behavior',
            isOpen: isRuntimeBehaviorOpen,
            setOpen: setIsRuntimeBehaviorOpen,
            title: 'Runtime behavior',
            description: 'Tune logging and mount safety behavior.',
            controlsId: 'restic-runtime-behavior-content',
            content: (
               <Stack spacing={{ base: 1, md: 2 }}>
                <Stack spacing={{ base: 1, md: 2 }}>
                  <Grid
                    className={styles.resticMountGrid}
                    templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                    columnGap={3}
                    rowGap={1}
                    w='full'
                  >
                    <GridItem className={styles.rowLabel}>
                      <Text>Restic mount no lock</Text>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <RMSwitch
                        isChecked={config?.backupResticMountNoLock}
                        isDisabled={user?.grants['cluster-settings'] == false}
                        confirmTitle={'Confirm switch settings for backup-restic-mount-no-lock?'}
                        onChange={() => handleSwitchChange('backup-restic-mount-no-lock')}
                      />
                      <Text className={styles.helperText}>Skip repository locking during mount.</Text>
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
                      <Text>Restic mount verbose level (0-3)</Text>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <NumberInput
                        min={0}
                        max={3}
                        value={config?.backupResticMountVerbose}
                        showEditButton={true}
                        showConfirmModal={true}
                        confirmTitle={`Confirm backup-restic-mount-verbose to: `}
                        onConfirm={(value) => handleSettingChange('backup-restic-mount-verbose', value)}
                      />
                      <Text className={styles.helperText}>
                        Range 0-3; 0 is default, 1-3 increase detail (quiet requires 0).
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
                      <Text>Restic mount quiet mode</Text>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <RMSwitch
                        isChecked={config?.backupResticMountQuiet}
                        isDisabled={user?.grants['cluster-settings'] == false}
                        confirmTitle={'Confirm switch settings for backup-restic-mount-quiet?'}
                        onChange={() => handleSwitchChange('backup-restic-mount-quiet')}
                      />
                      <Text className={styles.helperText}>Reduce output to minimal status messages.</Text>
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
                      <Text>Allow unsafe restic mount (reuse external mount)</Text>
                    </GridItem>
                    <GridItem className={styles.valueCell}>
                      <RMSwitch
                        isChecked={config?.backupResticAllowUnsafeMount}
                        isDisabled={user?.grants['cluster-settings'] == false}
                        confirmTitle={'Confirm switch settings for backup-restic-allow-unsafe-mount?'}
                        onChange={() => handleSwitchChange('backup-restic-allow-unsafe-mount')}
                      />
                      <Text className={styles.helperText}>Allow reuse of an existing mount point.</Text>
                    </GridItem>
                  </Grid>
                </Stack>
              </Stack>
            )
          })}
        </Stack>
      </VStack>
      {isCommonModalOpen && (
        <CommonModal
          isOpen={isCommonModalOpen}
          size='lg'
          title={title}
          body={body}
          contentClassName={joinClasses(modalStyles.infoModalContent, styles.infoModalContent)}
          headerClassName={joinClasses(modalStyles.infoModalHeader, styles.infoModalHeader)}
          bodyClassName={joinClasses(modalStyles.infoModalBody, styles.infoModalBody)}
          closeModal={() => {
            closeCommonModal()
          }}
        />
      )}
    </>
  )
}

export default ResticMountSettings
