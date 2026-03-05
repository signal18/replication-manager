import { Box, Flex, Grid, GridItem, HStack, Stack, Text, VStack, Checkbox, Alert, AlertIcon, Divider, useDisclosure } from '@chakra-ui/react'
import React, { useState } from 'react'
import { HiChevronDown, HiChevronUp, HiQuestionMarkCircle, HiRefresh } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'
import RMButton from '../../components/RMButton'
import RMSwitch from '../../components/RMSwitch'
import TextForm from '../../components/TextForm'
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

function ResticRepositorySettings({
  clusterName,
  config,
  user,
  dispatch,
  onOpenInfoModal
}) {
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

  const handleResticInit = () => {
    setInitForce(false) // Reset force flag
    onOpenInitModal()
  }

  const handleConfirmInit = async () => {
    try {
      await dispatch(resticInitRepo({ 
        clusterName, 
        force: initForce
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
                  <Text>Use Restic For Backup</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <RMSwitch
                    isChecked={config?.backupRestic}
                    isDisabled={user?.grants['cluster-settings'] == false}
                    confirmTitle={'Confirm switch settings for backup-restic?'}
                    onChange={() => handleSwitchChange('backup-restic')}
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
                  <Text>Backup restic binary path</Text>
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
                  <Text>Backup restic password</Text>
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
              <Stack spacing={{ base: 1, md: 2 }}>
                <Grid
                  className={styles.resticMountGrid}
                  templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                  columnGap={3}
                  rowGap={1}
                  w='full'
                >
                  <GridItem className={styles.rowLabel}>
                    <Text>Backup restic local repository</Text>
                  </GridItem>
                  <GridItem className={styles.valueCell}>
                    <HStack width='100%' spacing={2}>
                      <TextForm
                        value={config?.backupResticLocalRepository}
                        confirmTitle={`Confirm backup-restic-local-repository to `}
                        className={styles.textbox}
                        size='sm'
                        onSave={(value) => handleSettingChange('backup-restic-local-repository', value, true)}
                      />
                      <RMButton
                        size='sm'
                        colorScheme='blue'
                        onClick={handleResticInit}
                        isDisabled={user?.grants['cluster-settings'] === false || config?.backupResticAws}
                        title="Re-initialize local repository"
                      >
                        <HiRefresh />
                      </RMButton>
                      <RMIconButton
                        icon={HiQuestionMarkCircle}
                        onClick={() => {
                          onOpenInfoModal(
                            'Re-initialize Local Repository',
                            'Re-initialize the local Restic repository. This creates a new repository configuration at the specified path. Use with caution - only needed if the repository is corrupted or being set up for the first time.'
                          )
                        }}
                      />
                    </HStack>
                  </GridItem>
                </Grid>
              </Stack>

              <Stack spacing={{ base: 1, md: 2 }}>
                <Grid
                  className={styles.resticMountGrid}
                  templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                  columnGap={3}
                  rowGap={1}
                  w='full'
                >
                  <GridItem className={styles.rowLabel}>
                    <Text>Backup restic aws</Text>
                  </GridItem>
                  <GridItem className={styles.valueCell}>
                    <RMSwitch
                      isChecked={config?.backupResticAws}
                      isDisabled={user?.grants['cluster-settings'] == false}
                      confirmTitle={'Confirm switch settings for backup-restic-aws?'}
                      onChange={() => handleSwitchChange('backup-restic-aws')}
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
                    <Text>Backup restic aws access key id</Text>
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
                    <Text>Backup restic aws access secret</Text>
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
                    <Text>Backup restic aws region</Text>
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
                    <Text>Backup restic additional env</Text>
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

                <Grid
                  className={styles.resticMountGrid}
                  templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
                  columnGap={3}
                  rowGap={1}
                  w='full'
                >
                  <GridItem className={styles.rowLabel}>
                    <Text>Backup restic aws bucket</Text>
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
                        isDisabled={user?.grants['cluster-settings'] === false || !config?.backupResticAws}
                        title="Re-initialize S3 repository"
                      >
                        <HiRefresh />
                      </RMButton>
                      <RMIconButton
                        icon={HiQuestionMarkCircle}
                        onClick={() => {
                          onOpenInfoModal(
                            'Re-initialize S3 Repository',
                            'Re-initialize the S3/MinIO Restic repository. This creates a new repository configuration at the specified S3 bucket/path. Force re-initialization deletes all objects under the configured bucket/prefix. Ensure AWS credentials and bucket path are correct before initializing.'
                          )
                        }}
                      />
                    </HStack>
                  </GridItem>
                </Grid>
              </Stack>
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
                  <Text>Backup restic host override</Text>
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
                    <RMIconButton
                      icon={HiQuestionMarkCircle}
                      onClick={() => {
                        onOpenInfoModal('Restic Host Override', ResticHostHelp)
                      }}
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
                  <Text>Backup restic tags</Text>
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
                    <RMIconButton
                      icon={HiQuestionMarkCircle}
                      onClick={() => {
                        onOpenInfoModal('Restic Tag Templates', ResticTagsHelp)
                      }}
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
              Re-initialize the {config?.backupResticAws ? 'S3/MinIO' : 'local'} Restic repository:
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
              {config?.backupResticAws 
                ? config?.backupResticRepository 
                : config?.backupResticLocalRepository}
            </Text>
            <Divider />
            <Checkbox
              isChecked={initForce}
              isDisabled={config?.backupResticAws}
              onChange={(e) => setInitForce(e.target.checked)}
            >
              <Text fontSize='sm'>
                Force re-initialization (overwrite existing configuration)
              </Text>
            </Checkbox>
            {config?.backupResticAws && (
              <Text fontSize='sm' color='gray.600'>
                Force re-initialization is disabled when backup-restic-aws is enabled (S3/MinIO mode).
              </Text>
            )}
            {initForce && (
              <Alert status='warning' size='sm' borderRadius='md'>
                <AlertIcon />
                <Text fontSize='sm'>
                  {config?.backupResticAws
                    ? 'Warning: Force re-initialization deletes all objects under the configured S3 bucket/prefix before creating a new repository.'
                    : 'Warning: This will overwrite the existing repository configuration.'}
                </Text>
              </Alert>
            )}
          </VStack>
        }
        onConfirmClick={handleConfirmInit}
        confirmButtonText="Initialize"
        confirmButtonProps={{ 
          colorScheme: initForce ? 'red' : 'blue' 
        }}
      />
    </VStack>
  )
}

export default ResticRepositorySettings
