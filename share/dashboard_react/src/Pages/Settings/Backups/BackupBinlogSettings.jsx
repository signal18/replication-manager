import { Box, Flex, Grid, GridItem, HStack, Stack, VStack, Text } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import { HiChevronDown, HiChevronUp } from 'react-icons/hi'
import { useDispatch, useSelector } from 'react-redux'
import Dropdown from '../../../components/Dropdown'
import NumberInput from '../../../components/NumberInput'
import RMSwitch from '../../../components/RMSwitch'
import TextForm from '../../../components/TextForm'
import { setSetting, switchSetting } from '../../../redux/settingsSlice'
import { convertObjectToArrayForDropdown } from '../../../utility/common'
import styles from '../styles.module.scss'
import tableStyles from '../../../components/TableType2/styles.module.scss'

function BackupBinlogSettings({ clusterName, config, user }) {
  const dispatch = useDispatch()
  const isBrowser = typeof window !== 'undefined'
  const storagePrefix = 'settings:backup-binlog-settings'
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
  const [isBinlogToolsOpen, setIsBinlogToolsOpen] = useState(() => readStoredState('binlog-tools', true))
  const [isBackupBinlogsOpen, setIsBackupBinlogsOpen] = useState(() => readStoredState('backup-binlogs', true))
  const [isBinlogPurgeOpen, setIsBinlogPurgeOpen] = useState(() => readStoredState('binlog-purge', false))
  const [isOpen, setIsOpen] = useState(true)
  const areAllSectionsOpen = isBinlogToolsOpen && isBackupBinlogsOpen && isBinlogPurgeOpen
  const [binlogBackupOptions, setBinlogBackupOptions] = useState([])
  const [binlogParseOptions, setBinlogParseOptions] = useState([])
  const [selectedBinlogBackupType, setSelectedBinlogBackupType] = useState('')
  const showScriptPath = selectedBinlogBackupType === 'script' || config?.binlogCopyMode === 'script'

  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)

  useEffect(() => {
    setSelectedBinlogBackupType(config?.binlogCopyMode || '')
  }, [config?.binlogCopyMode])

  useEffect(() => {
    if (monitor?.backupBinlogList) {
      setBinlogBackupOptions(convertObjectToArrayForDropdown(monitor.backupBinlogList))
    }
    if (monitor?.binlogParseList) {
      setBinlogParseOptions(convertObjectToArrayForDropdown(monitor.binlogParseList))
    }
  }, [monitor?.backupBinlogList, monitor?.binlogParseList])

  const handleSettingChange = (setting, value) =>
    dispatch(
      setSetting({
        clusterName,
        setting,
        value
      })
    )

  const handleSwitchChange = (setting) =>
    dispatch(
      switchSetting({
        clusterName,
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
    setIsBinlogToolsOpen(nextState)
    persistStoredState('binlog-tools', nextState)
    setIsBackupBinlogsOpen(nextState)
    persistStoredState('backup-binlogs', nextState)
    setIsBinlogPurgeOpen(nextState)
    persistStoredState('binlog-purge', nextState)
  }

  const renderPanel = ({ sectionKey, isOpen, setOpen, title, description, controlsId, content }) => (
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
    <Box className={styles.panel} w='full'>
      <HStack
        as='button'
        type='button'
        spacing={2}
        onClick={() => setIsOpen((prev) => !prev)}
        aria-expanded={isOpen}
        aria-controls='binlog-settings-content'
        className={styles.panelHeader}
      >
        <Stack spacing={1} className={styles.panelHeaderContent}>
          <Text className={styles.panelTitle}>Binlog backup</Text>
          <Text className={styles.panelDescription}>
            Configure binlog backup tools, retention, and purge behavior.
          </Text>
        </Stack>
        <Box className={styles.panelChevron}>{isOpen ? <HiChevronUp /> : <HiChevronDown />}</Box>
      </HStack>
      <Box id='binlog-settings-content' className={styles.panelBody} display={isOpen ? 'block' : 'none'}>
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
              justify='flex-end'
              gap={2}
            >
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
                  aria-controls='binlog-tools-content binlog-backup-content binlog-purge-content'
                  aria-label={
                    areAllSectionsOpen
                      ? 'Hide all binlog backup settings sections'
                      : 'Show all binlog backup settings sections'
                  }
                  onClick={() => setAllSectionsState(!areAllSectionsOpen)}
                >
                  {areAllSectionsOpen ? 'Hide all' : 'Show all'}
                </Box>
              </HStack>
            </Flex>

            {renderPanel({
              sectionKey: 'binlog-tools',
              isOpen: isBinlogToolsOpen,
              setOpen: setIsBinlogToolsOpen,
          title: 'Binlog tools',
          description: 'Select the binlog backup tool and parser for this cluster.',
          controlsId: 'binlog-tools-content',
          content: (
            <Stack spacing={{ base: 1, md: 2 }}>
              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <Text>Binlog backup tool</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <Stack spacing={2} align='flex-start'>
                    <Dropdown
                      options={binlogBackupOptions}
                      className={styles.dropdownButton}
                      selectedValue={selectedBinlogBackupType || config?.binlogCopyMode}
                      confirmTitle={`Confirm Binlog backup to`}
                      onChange={(backupType) => {
                        const nextValue =
                          typeof backupType === 'string'
                            ? backupType
                            : backupType?.value || backupType?.name || ''
                        setSelectedBinlogBackupType(nextValue)
                        if (nextValue !== 'script') {
                          handleSettingChange('backup-binlog-type', nextValue)
                        }
                      }}
                    />
                    {showScriptPath && (
                      <TextForm
                        label={'Backup binlog script path'}
                        direction='column'
                        className={styles.textbox}
                        value={config?.binlogCopyScript}
                        confirmTitle='Confirm binlog backup to script with value '
                        onSave={(scriptValue) => {
                          handleSettingChange('backup-binlog-script', scriptValue)
                          handleSettingChange('backup-binlog-type', 'script')
                        }}
                      />
                    )}
                  </Stack>
                </GridItem>
              </Grid>

              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <Text>Binlog parse mode</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <Dropdown
                    options={binlogParseOptions}
                    className={styles.dropdownButton}
                    selectedValue={config?.binlogParseMode}
                    confirmTitle={`Confirm binlog parse mode to`}
                    onChange={(mode) => handleSettingChange('binlog-parse-mode', mode)}
                  />
                  <Text className={styles.helperText}>Choose how binlogs are parsed after backup.</Text>
                </GridItem>
              </Grid>
            </Stack>
          )
        })}

            {renderPanel({
              sectionKey: 'backup-binlogs',
              isOpen: isBackupBinlogsOpen,
              setOpen: setIsBackupBinlogsOpen,
          title: 'Backup binlogs',
          description: 'Enable binlog backups and control retention of local files.',
          controlsId: 'binlog-backup-content',
          content: (
            <Stack spacing={{ base: 1, md: 2 }}>
              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <Text>Enable binlog backups</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <RMSwitch
                    isChecked={config?.autorejoinBackupBinlog}
                    isDisabled={user?.grants['cluster-settings'] == false}
                    confirmTitle={'Confirm switch settings for backup-binlogs?'}
                    onChange={() => handleSwitchChange('backup-binlogs')}
                  />
                  <Text className={styles.helperText}>Keeps binlog backups to support autorejoin and recovery.</Text>
                </GridItem>
              </Grid>

              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <Text>Keep binlog files</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <NumberInput
                    min={0}
                    value={config?.backupBinlogsKeep}
                    step={1}
                    secondaryStep={5}
                    showEditButton={true}
                    showConfirmModal={true}
                    confirmTitle='Confirm change keep binlogs files to: '
                    onConfirm={(val) => handleSettingChange('backup-binlogs-keep', val)}
                  />
                  <Text className={styles.helperText}>Number of binlog files retained on disk. Min 0, no max limit.</Text>
                </GridItem>
              </Grid>
            </Stack>
          )
        })}

            {renderPanel({
              sectionKey: 'binlog-purge',
              isOpen: isBinlogPurgeOpen,
              setOpen: setIsBinlogPurgeOpen,
          title: 'Binlog purge',
          description: 'Force purge behavior and safety checks for binlog cleanup on remote DB nodes.',
          controlsId: 'binlog-purge-content',
          content: (
            <Stack spacing={{ base: 1, md: 2 }}>
              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <Text>Enforce binlog purge</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <RMSwitch
                    isChecked={config?.forceBinlogPurge}
                    isDisabled={user?.grants['cluster-settings'] == false}
                    confirmTitle={'Confirm switch settings for force-binlog-purge?'}
                    onChange={() => handleSwitchChange('force-binlog-purge')}
                  />
                  <Text className={styles.helperText}>
                    Automatically purge binlog files on remote DB nodes when limits are reached.
                  </Text>
                </GridItem>
              </Grid>

              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <Text>Max binlog total size (GB)</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <NumberInput
                    min={0}
                    max={256}
                    value={config?.forceBinlogPurgeTotalSize}
                    step={1}
                    secondaryStep={10}
                    showEditButton={true}
                    showConfirmModal={true}
                    confirmTitle='Confirm change force-binlog-purge-total-size to: '
                    onConfirm={(val) => handleSettingChange('force-binlog-purge-total-size', val)}
                  />
                  <Text className={styles.helperText}>Total size threshold for triggering purge. Min 0, max 256.</Text>
                </GridItem>
              </Grid>

              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <Text>Minimum replicas for purge</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <NumberInput
                    min={0}
                    max={12}
                    value={config?.forceBinlogPurgeMinReplica}
                    step={1}
                    secondaryStep={3}
                    showEditButton={true}
                    showConfirmModal={true}
                    confirmTitle='Confirm change force-binlog-purge-min-replica to: '
                    onConfirm={(val) => handleSettingChange('force-binlog-purge-min-replica', val)}
                  />
                  <Text className={styles.helperText}>Require enough replicas before purging binlogs. Min 0, max 12.</Text>
                </GridItem>
              </Grid>

              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <Text>Purge on restore</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <RMSwitch
                    isChecked={config?.forceBinlogPurgeOnRestore}
                    isDisabled={user?.grants['cluster-settings'] == false}
                    confirmTitle={'Confirm switch settings for force-binlog-purge-on-restore?'}
                    onChange={() => handleSwitchChange('force-binlog-purge-on-restore')}
                  />
                  <Text className={styles.helperText}>Apply purge rules after restores or reseeds.</Text>
                </GridItem>
              </Grid>

              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <Text>Purge on replicas</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <RMSwitch
                    isChecked={config?.forceBinlogPurgeReplicas}
                    isDisabled={user?.grants['cluster-settings'] == false}
                    confirmTitle={'Confirm switch settings for force-binlog-purge-replicas?'}
                    onChange={() => handleSwitchChange('force-binlog-purge-replicas')}
                  />
                  <Text className={styles.helperText}>Enforce purge policies on replica nodes.</Text>
                </GridItem>
              </Grid>
            </Stack>
          )
        })}
          </Stack>
        </VStack>
      </Box>
    </Box>
  )
}

export default BackupBinlogSettings
