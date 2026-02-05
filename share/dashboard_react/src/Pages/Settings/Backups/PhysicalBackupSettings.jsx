import { Box, Grid, GridItem, HStack, Stack, Text } from '@chakra-ui/react'
import React, { useState } from 'react'
import { HiChevronDown, HiChevronUp, HiQuestionMarkCircle } from 'react-icons/hi'
import { useDispatch } from 'react-redux'
import Dropdown from '../../../components/Dropdown'
import RMIconButton from '../../../components/RMIconButton'
import TextForm from '../../../components/TextForm'
import { setSetting } from '../../../redux/settingsSlice'
import styles from '../styles.module.scss'

function PhysicalBackupSettings({
  clusterName,
  config,
  user,
  physicalBackupOptions,
  onOpenPhysicalPostScriptInfo
}) {
  const dispatch = useDispatch()
  const [isPhysicalOpen, setIsPhysicalOpen] = useState(true)
  const isReadOnly = user?.grants['cluster-settings'] == false

  const handleSettingChange = (setting, value) =>
    dispatch(
      setSetting({
        clusterName,
        setting,
        value
      })
    )

  const renderPanel = ({ isOpen, setOpen, title, description, controlsId, content }) => (
    <Box className={styles.panel} w='full'>
      <HStack
        as='button'
        type='button'
        spacing={2}
        onClick={() => setOpen((prev) => !prev)}
        aria-expanded={isOpen}
        aria-controls={controlsId}
        className={styles.panelHeader}
      >
        <Stack spacing={1} className={styles.panelHeaderContent}>
          <Text className={styles.panelTitle}>{title}</Text>
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
      <Stack spacing={{ base: 3, lg: 4 }}>
        {renderPanel({
          isOpen: isPhysicalOpen,
          setOpen: setIsPhysicalOpen,
          title: 'Physical backup',
          description: 'Choose tooling and automation for file-based backups.',
          controlsId: 'physical-backup-content',
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
                  <Text>Physical Backup</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <Dropdown
                    options={physicalBackupOptions}
                    selectedValue={config?.backupPhysicalType}
                    confirmTitle={`Confirm physical backup to`}
                    isDisabled={isReadOnly}
                    onChange={(backupType) => handleSettingChange('backup-physical-type', backupType)}
                  />
                  <Text className={styles.helperText}>
                    Choose the physical backup tool used for file-based backups.
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
                  <Text>Physical Backup Post-Script</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <HStack width={'100%'}>
                    <TextForm
                      value={config?.backupPhysicalPostScript}
                      confirmTitle={`Confirm backup-physical-post-script to `}
                      maxLength={1024}
                      className={styles.textbox}
                      size='sm'
                      onSave={(value) =>
                        handleSettingChange('backup-physical-post-script', btoa(value))
                      }
                    />
                    <RMIconButton
                      icon={HiQuestionMarkCircle}
                      onClick={() => onOpenPhysicalPostScriptInfo?.()}
                    />
                  </HStack>
                </GridItem>
              </Grid>
            </Stack>
          )
        })}
      </Stack>
  )
}

export default PhysicalBackupSettings
