import {
  Box,
  Button,
  Collapse,
  Divider,
  Flex,
  Heading,
  HStack,
  SimpleGrid,
  Stack,
  VStack,
  Text,
  useColorModeValue
} from '@chakra-ui/react'
import React, { useState } from 'react'
import { useDispatch } from 'react-redux'
import TextForm from '../../components/TextForm'
import RMSwitch from '../../components/RMSwitch'
import NumberInput from '../../components/NumberInput'
import RMIconButton from '../../components/RMIconButton'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
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
    readStoredState('mount-destination', true)
  )
  const [isSnapshotFiltersOpen, setIsSnapshotFiltersOpen] = useState(() =>
    readStoredState('snapshot-filters', true)
  )
  const [isMountTemplatesOpen, setIsMountTemplatesOpen] = useState(() =>
    readStoredState('mount-templates', true)
  )
  const [isPermissionsOpen, setIsPermissionsOpen] = useState(() => readStoredState('permissions', true))
  const [isRuntimeBehaviorOpen, setIsRuntimeBehaviorOpen] = useState(() =>
    readStoredState('runtime-behavior', true)
  )
  const areAllSectionsOpen =
    isMountDestinationOpen &&
    isSnapshotFiltersOpen &&
    isMountTemplatesOpen &&
    isPermissionsOpen &&
    isRuntimeBehaviorOpen
  const cardBg = useColorModeValue('gray.50', 'gray.800')
  const cardBorder = useColorModeValue('gray.200', 'whiteAlpha.300')
  const dividerColor = useColorModeValue('gray.200', 'whiteAlpha.300')
  const headingColor = useColorModeValue('gray.700', 'gray.100')
  const mutedText = useColorModeValue('gray.600', 'gray.400')
  const [action, setAction] = useState({
    title: '',
    body: <></>
  })
  const { title, body } = action

  const ResticMountFiltersHelp = `backup-restic-mount-host, backup-restic-mount-tag, and backup-restic-mount-path control which snapshots appear in the mount.  
Filters are ANDed across host/tag/path; within each field, comma-separated values are OR.  
Empty values mean no filter. Path filters must be absolute (e.g. /var/lib/mysql).`

  const ResticMountTemplatesHelp = `backup-restic-mount-path-template controls the virtual layout in the mount (comma-separated).  
Defaults: ids/%i (replication-manager); common restic layouts: snapshots/%T, hosts/%h/%T, tags/%t/%T, ids/%i.  
Tokens: %i=short ID, %I=full ID, %u=user, %h=host, %t=tags, %T=time.  
backup-restic-mount-time-template uses Go time layout (e.g. 2006-01-02T15:04:05Z07:00). Leave empty for RFC3339.`

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
          <Flex align='center' justify='flex-end'>
            <Button
              size='sm'
              variant='outline'
              aria-expanded={areAllSectionsOpen}
              aria-controls='restic-mount-destination-content restic-snapshot-filters-content restic-mount-templates-content restic-permissions-content restic-runtime-behavior-content'
              aria-label={
                areAllSectionsOpen
                  ? 'Hide all restic mount settings sections'
                  : 'Show all restic mount settings sections'
              }
              onClick={() => setAllSectionsState(!areAllSectionsOpen)}
            >
              {areAllSectionsOpen ? 'Hide All' : 'Show All'}
            </Button>
          </Flex>
          <Box borderWidth='1px' borderColor={cardBorder} borderRadius='md' bg={cardBg} p={{ base: 3, md: 5 }}>
            <Stack spacing={{ base: 2, md: 3 }}>
              <Stack spacing={1}>
                <Flex align='center' justify='space-between' gap={3} wrap='wrap'>
                  <Heading size='xs' textTransform='uppercase' letterSpacing='wide' color={headingColor}>
                    Mount destination
                  </Heading>
                  <Button
                    size='sm'
                    variant='ghost'
                    aria-expanded={isMountDestinationOpen}
                    aria-controls='restic-mount-destination-content'
                    aria-label={isMountDestinationOpen ? 'Hide mount destination section' : 'Show mount destination section'}
                    onClick={() => toggleSection('mount-destination', setIsMountDestinationOpen)}
                  >
                    {isMountDestinationOpen ? 'Hide' : 'Show'}
                  </Button>
                </Flex>
                <Text fontSize='sm' color={mutedText}>
                  Choose where restic mounts snapshots for inspection or restore.
                </Text>
              </Stack>
              <Collapse in={isMountDestinationOpen} animateOpacity>
                <Box id='restic-mount-destination-content'>
                  <Divider borderColor={dividerColor} />
                  <SimpleGrid columns={{ base: 1, md: 2 }} spacing={{ base: 3, md: 4 }} alignItems='start'>
                    <Stack spacing={1}>
                      <Text fontWeight='semibold'>Restic mount target directory</Text>
                      <Text fontSize='sm' color={mutedText}>
                        Empty value uses the default mount path.
                      </Text>
                    </Stack>
                    <Box width='100%'>
                      <TextForm
                        value={config?.backupResticMountTargetDir}
                        confirmTitle={`Confirm backup-restic-mount-target-dir to `}
                        className={styles.textbox}
                        placeholder='(empty = default mount path)'
                        onSave={(value) => handleSettingChange('backup-restic-mount-target-dir', value)}
                      />
                    </Box>
                  </SimpleGrid>
                </Box>
              </Collapse>
            </Stack>
          </Box>

          <Box borderWidth='1px' borderColor={cardBorder} borderRadius='md' bg={cardBg} p={{ base: 3, md: 5 }}>
            <Stack spacing={{ base: 2, md: 3 }}>
              <Stack spacing={1}>
                <Flex align='center' justify='space-between' gap={3} wrap='wrap'>
                  <Heading size='xs' textTransform='uppercase' letterSpacing='wide' color={headingColor}>
                    Snapshot filters
                  </Heading>
                  <Button
                    size='sm'
                    variant='ghost'
                    aria-expanded={isSnapshotFiltersOpen}
                    aria-controls='restic-snapshot-filters-content'
                    aria-label={isSnapshotFiltersOpen ? 'Hide snapshot filters section' : 'Show snapshot filters section'}
                    onClick={() => toggleSection('snapshot-filters', setIsSnapshotFiltersOpen)}
                  >
                    {isSnapshotFiltersOpen ? 'Hide' : 'Show'}
                  </Button>
                </Flex>
                <Text fontSize='sm' color={mutedText}>
                  Limit which snapshots appear inside the mount.
                </Text>
              </Stack>
              <Collapse in={isSnapshotFiltersOpen} animateOpacity>
                <Box id='restic-snapshot-filters-content'>
                  <Divider borderColor={dividerColor} />
                  <Stack spacing={{ base: 2, md: 3 }} divider={<Divider borderColor={dividerColor} />}>
                    <SimpleGrid columns={{ base: 1, md: 2 }} spacing={{ base: 2, md: 3 }} alignItems='start'>
                      <Stack spacing={1}>
                        <Text fontWeight='semibold'>Restic mount host filter</Text>
                        <Text fontSize='sm' color={mutedText}>
                          Comma-separated hostnames; empty means no filter.
                        </Text>
                      </Stack>
                      <HStack width='100%' spacing={2} align='center'>
                        <Box flex='1' width='100%'>
                          <TextForm
                            value={config?.backupResticMountHost}
                            confirmTitle={`Confirm backup-restic-mount-host to `}
                            className={styles.textbox}
                            placeholder='host1,host2'
                            onSave={(value) => handleSettingChange('backup-restic-mount-host', value)}
                          />
                        </Box>
                        <RMIconButton
                          icon={HiQuestionMarkCircle}
                          onClick={() => {
                            openInfoModal('Restic Mount Filters', ResticMountFiltersHelp)
                          }}
                        />
                      </HStack>
                    </SimpleGrid>

                    <SimpleGrid columns={{ base: 1, md: 2 }} spacing={{ base: 2, md: 3 }} alignItems='start'>
                      <Stack spacing={1}>
                        <Text fontWeight='semibold'>Restic mount tag filter</Text>
                        <Text fontSize='sm' color={mutedText}>
                          Comma-separated tags; empty means no filter.
                        </Text>
                      </Stack>
                      <HStack width='100%' spacing={2} align='center'>
                        <Box flex='1' width='100%'>
                          <TextForm
                            value={config?.backupResticMountTag}
                            confirmTitle={`Confirm backup-restic-mount-tag to `}
                            className={styles.textbox}
                            placeholder='tag1,tag2'
                            onSave={(value) => handleSettingChange('backup-restic-mount-tag', value)}
                          />
                        </Box>
                        <RMIconButton
                          icon={HiQuestionMarkCircle}
                          onClick={() => {
                            openInfoModal('Restic Mount Filters', ResticMountFiltersHelp)
                          }}
                        />
                      </HStack>
                    </SimpleGrid>

                    <SimpleGrid columns={{ base: 1, md: 2 }} spacing={{ base: 2, md: 3 }} alignItems='start'>
                      <Stack spacing={1}>
                        <Text fontWeight='semibold'>Restic mount path filter</Text>
                        <Text fontSize='sm' color={mutedText}>
                          Absolute paths only; empty means no filter.
                        </Text>
                      </Stack>
                      <HStack width='100%' spacing={2} align='center'>
                        <Box flex='1' width='100%'>
                          <TextForm
                            value={config?.backupResticMountPath}
                            confirmTitle={`Confirm backup-restic-mount-path to `}
                            className={styles.textbox}
                            placeholder='/var/lib/mysql,/srv/data'
                            onSave={(value) => handleSettingChange('backup-restic-mount-path', value)}
                          />
                        </Box>
                        <RMIconButton
                          icon={HiQuestionMarkCircle}
                          onClick={() => {
                            openInfoModal('Restic Mount Filters', ResticMountFiltersHelp)
                          }}
                        />
                      </HStack>
                    </SimpleGrid>
                  </Stack>
                </Box>
              </Collapse>
            </Stack>
          </Box>

          <Box borderWidth='1px' borderColor={cardBorder} borderRadius='md' bg={cardBg} p={{ base: 3, md: 5 }}>
            <Stack spacing={{ base: 2, md: 3 }}>
              <Stack spacing={1}>
                <Flex align='center' justify='space-between' gap={3} wrap='wrap'>
                  <Heading size='xs' textTransform='uppercase' letterSpacing='wide' color={headingColor}>
                    Mount templates
                  </Heading>
                  <Button
                    size='sm'
                    variant='ghost'
                    aria-expanded={isMountTemplatesOpen}
                    aria-controls='restic-mount-templates-content'
                    aria-label={isMountTemplatesOpen ? 'Hide mount templates section' : 'Show mount templates section'}
                    onClick={() => toggleSection('mount-templates', setIsMountTemplatesOpen)}
                  >
                    {isMountTemplatesOpen ? 'Hide' : 'Show'}
                  </Button>
                </Flex>
                <Text fontSize='sm' color={mutedText}>
                  Control the virtual layout and timestamp formatting inside the mount.
                </Text>
              </Stack>
              <Collapse in={isMountTemplatesOpen} animateOpacity>
                <Box id='restic-mount-templates-content'>
                  <Divider borderColor={dividerColor} />
                  <Stack spacing={{ base: 2, md: 3 }} divider={<Divider borderColor={dividerColor} />}>
                    <SimpleGrid columns={{ base: 1, md: 2 }} spacing={{ base: 2, md: 3 }} alignItems='start'>
                      <Stack spacing={1}>
                        <Text fontWeight='semibold'>Restic mount path template</Text>
                        <Text fontSize='sm' color={mutedText}>
                          Multiple templates allowed; leave empty for defaults.
                        </Text>
                      </Stack>
                      <HStack width='100%' spacing={2} align='center'>
                        <Box flex='1' width='100%'>
                          <TextForm
                            value={config?.backupResticMountPathTemplate}
                            confirmTitle={`Confirm backup-restic-mount-path-template to `}
                            className={styles.textbox}
                            placeholder='(comma-separated templates)'
                            onSave={(value) => handleSettingChange('backup-restic-mount-path-template', value)}
                          />
                        </Box>
                        <RMIconButton
                          icon={HiQuestionMarkCircle}
                          onClick={() => {
                            openInfoModal('Restic Mount Templates', ResticMountTemplatesHelp)
                          }}
                        />
                      </HStack>
                    </SimpleGrid>

                    <SimpleGrid columns={{ base: 1, md: 2 }} spacing={{ base: 2, md: 3 }} alignItems='start'>
                      <Stack spacing={1}>
                        <Text fontWeight='semibold'>Restic mount time template</Text>
                        <Text fontSize='sm' color={mutedText}>
                          Use Go time layout; leave empty for defaults.
                        </Text>
                      </Stack>
                      <HStack width='100%' spacing={2} align='center'>
                        <Box flex='1' width='100%'>
                          <TextForm
                            value={config?.backupResticMountTimeTemplate}
                            confirmTitle={`Confirm backup-restic-mount-time-template to `}
                            className={styles.textbox}
                            placeholder='2006-01-02T15:04:05Z07:00'
                            onSave={(value) => handleSettingChange('backup-restic-mount-time-template', value)}
                          />
                        </Box>
                        <RMIconButton
                          icon={HiQuestionMarkCircle}
                          onClick={() => {
                            openInfoModal('Restic Mount Templates', ResticMountTemplatesHelp)
                          }}
                        />
                      </HStack>
                    </SimpleGrid>
                  </Stack>
                </Box>
              </Collapse>
            </Stack>
          </Box>

          <Box borderWidth='1px' borderColor={cardBorder} borderRadius='md' bg={cardBg} p={{ base: 3, md: 5 }}>
            <Stack spacing={{ base: 2, md: 3 }}>
              <Stack spacing={1}>
                <Flex align='center' justify='space-between' gap={3} wrap='wrap'>
                  <Heading size='xs' textTransform='uppercase' letterSpacing='wide' color={headingColor}>
                    Permissions
                  </Heading>
                  <Button
                    size='sm'
                    variant='ghost'
                    aria-expanded={isPermissionsOpen}
                    aria-controls='restic-permissions-content'
                    aria-label={isPermissionsOpen ? 'Hide permissions section' : 'Show permissions section'}
                    onClick={() => toggleSection('permissions', setIsPermissionsOpen)}
                  >
                    {isPermissionsOpen ? 'Hide' : 'Show'}
                  </Button>
                </Flex>
                <Text fontSize='sm' color={mutedText}>
                  Control mount ownership and permission handling.
                </Text>
              </Stack>
              <Collapse in={isPermissionsOpen} animateOpacity>
                <Box id='restic-permissions-content'>
                  <Divider borderColor={dividerColor} />
                  <Stack spacing={{ base: 2, md: 3 }} divider={<Divider borderColor={dividerColor} />}>
                    <SimpleGrid columns={{ base: 1, md: 2 }} spacing={{ base: 2, md: 3 }} alignItems='start'>
                      <Stack spacing={1}>
                        <Text fontWeight='semibold'>Restic mount allow other users</Text>
                        <Text fontSize='sm' color={mutedText}>
                          Requires FUSE user_allow_other; allows all local users to access the mount.
                        </Text>
                      </Stack>
                      <Flex width='100%' align='center'>
                        <RMSwitch
                          isChecked={config?.backupResticMountAllowOther}
                          isDisabled={user?.grants['cluster-settings'] == false}
                          confirmTitle={'Confirm switch settings for backup-restic-mount-allow-other?'}
                          onChange={() => handleSwitchChange('backup-restic-mount-allow-other')}
                        />
                      </Flex>
                    </SimpleGrid>

                    <SimpleGrid columns={{ base: 1, md: 2 }} spacing={{ base: 2, md: 3 }} alignItems='start'>
                      <Stack spacing={1}>
                        <Text fontWeight='semibold'>Restic mount ignore default permissions</Text>
                        <Text fontSize='sm' color={mutedText}>
                          Disables kernel permission checks (default_permissions) and ignores Unix mode bits.
                        </Text>
                      </Stack>
                      <Flex width='100%' align='center'>
                        <RMSwitch
                          isChecked={config?.backupResticMountNoDefaultPermissions}
                          isDisabled={user?.grants['cluster-settings'] == false}
                          confirmTitle={'Confirm switch settings for backup-restic-mount-no-default-permissions?'}
                          onChange={() => handleSwitchChange('backup-restic-mount-no-default-permissions')}
                        />
                      </Flex>
                    </SimpleGrid>

                    <SimpleGrid columns={{ base: 1, md: 2 }} spacing={{ base: 2, md: 3 }} alignItems='start'>
                      <Stack spacing={1}>
                        <Text fontWeight='semibold'>Restic mount owner root</Text>
                        <Text fontSize='sm' color={mutedText}>
                          Show mounted files as owned by root.
                        </Text>
                      </Stack>
                      <Flex width='100%' align='center'>
                        <RMSwitch
                          isChecked={config?.backupResticMountOwnerRoot}
                          isDisabled={user?.grants['cluster-settings'] == false}
                          confirmTitle={'Confirm switch settings for backup-restic-mount-owner-root?'}
                          onChange={() => handleSwitchChange('backup-restic-mount-owner-root')}
                        />
                      </Flex>
                    </SimpleGrid>
                  </Stack>
                </Box>
              </Collapse>
            </Stack>
          </Box>

          <Box borderWidth='1px' borderColor={cardBorder} borderRadius='md' bg={cardBg} p={{ base: 3, md: 5 }}>
            <Stack spacing={{ base: 2, md: 3 }}>
              <Stack spacing={1}>
                <Flex align='center' justify='space-between' gap={3} wrap='wrap'>
                  <Heading size='xs' textTransform='uppercase' letterSpacing='wide' color={headingColor}>
                    Runtime behavior
                  </Heading>
                  <Button
                    size='sm'
                    variant='ghost'
                    aria-expanded={isRuntimeBehaviorOpen}
                    aria-controls='restic-runtime-behavior-content'
                    aria-label={isRuntimeBehaviorOpen ? 'Hide runtime behavior section' : 'Show runtime behavior section'}
                    onClick={() => toggleSection('runtime-behavior', setIsRuntimeBehaviorOpen)}
                  >
                    {isRuntimeBehaviorOpen ? 'Hide' : 'Show'}
                  </Button>
                </Flex>
                <Text fontSize='sm' color={mutedText}>
                  Tune logging and mount safety behavior.
                </Text>
              </Stack>
              <Collapse in={isRuntimeBehaviorOpen} animateOpacity>
                <Box id='restic-runtime-behavior-content'>
                  <Divider borderColor={dividerColor} />
                  <Stack spacing={{ base: 2, md: 3 }} divider={<Divider borderColor={dividerColor} />}>
                    <SimpleGrid columns={{ base: 1, md: 2 }} spacing={{ base: 2, md: 3 }} alignItems='start'>
                      <Stack spacing={1}>
                        <Text fontWeight='semibold'>Restic mount no lock</Text>
                        <Text fontSize='sm' color={mutedText}>
                          Skip repository locking during mount.
                        </Text>
                      </Stack>
                      <Flex width='100%' align='center'>
                        <RMSwitch
                          isChecked={config?.backupResticMountNoLock}
                          isDisabled={user?.grants['cluster-settings'] == false}
                          confirmTitle={'Confirm switch settings for backup-restic-mount-no-lock?'}
                          onChange={() => handleSwitchChange('backup-restic-mount-no-lock')}
                        />
                      </Flex>
                    </SimpleGrid>

                    <SimpleGrid columns={{ base: 1, md: 2 }} spacing={{ base: 2, md: 3 }} alignItems='start'>
                      <Stack spacing={1}>
                        <Text fontWeight='semibold'>Restic mount verbose level (0-3)</Text>
                        <Text fontSize='sm' color={mutedText}>
                          Range 0-3; 0 is default, 1-3 increase detail (quiet requires 0).
                        </Text>
                      </Stack>
                      <Box width='100%'>
                        <NumberInput
                          min={0}
                          max={3}
                          value={config?.backupResticMountVerbose}
                          showEditButton={true}
                          showConfirmModal={true}
                          confirmTitle={`Confirm backup-restic-mount-verbose to: `}
                          onConfirm={(value) => handleSettingChange('backup-restic-mount-verbose', value)}
                        />
                      </Box>
                    </SimpleGrid>

                    <SimpleGrid columns={{ base: 1, md: 2 }} spacing={{ base: 2, md: 3 }} alignItems='start'>
                      <Stack spacing={1}>
                        <Text fontWeight='semibold'>Restic mount quiet mode</Text>
                        <Text fontSize='sm' color={mutedText}>
                          Reduce output to minimal status messages.
                        </Text>
                      </Stack>
                      <Flex width='100%' align='center'>
                        <RMSwitch
                          isChecked={config?.backupResticMountQuiet}
                          isDisabled={user?.grants['cluster-settings'] == false}
                          confirmTitle={'Confirm switch settings for backup-restic-mount-quiet?'}
                          onChange={() => handleSwitchChange('backup-restic-mount-quiet')}
                        />
                      </Flex>
                    </SimpleGrid>

                    <SimpleGrid columns={{ base: 1, md: 2 }} spacing={{ base: 2, md: 3 }} alignItems='start'>
                      <Stack spacing={1}>
                        <Text fontWeight='semibold'>Allow unsafe restic mount (reuse external mount)</Text>
                        <Text fontSize='sm' color={mutedText}>
                          Allow reuse of an existing mount point.
                        </Text>
                      </Stack>
                      <Flex width='100%' align='center'>
                        <RMSwitch
                          isChecked={config?.backupResticAllowUnsafeMount}
                          isDisabled={user?.grants['cluster-settings'] == false}
                          confirmTitle={'Confirm switch settings for backup-restic-allow-unsafe-mount?'}
                          onChange={() => handleSwitchChange('backup-restic-allow-unsafe-mount')}
                        />
                      </Flex>
                    </SimpleGrid>
                  </Stack>
                </Box>
              </Collapse>
            </Stack>
          </Box>
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
