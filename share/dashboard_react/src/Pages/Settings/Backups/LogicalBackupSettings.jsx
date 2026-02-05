import { Box, Flex, Grid, GridItem, HStack, Stack, Text } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from '../styles.module.scss'
import Dropdown from '../../../components/Dropdown'
import TextForm from '../../../components/TextForm'
import RMIconButton from '../../../components/RMIconButton'
import RMSwitch from '../../../components/RMSwitch'
import { HiQuestionMarkCircle, HiChevronDown, HiChevronUp } from 'react-icons/hi'
import { setSetting, switchSetting } from '../../../redux/settingsSlice'

const BackupSaveScriptRequirement = `Backup save script execute a backup script and will not execute other logical backup tools.  
The script must be able to handle the following parameters:  
1. DB Server Host
2. Master Host
3. DB Server Port
4. Master Port
5. DB User
6. DB Password
7. Cluster Name
`

const BackupLoadScriptRequirement = `Backup load script will execute a script.  
The script will be executed with the following parameters:  
1. DB Server Host
2. Master Host
3. DB Server Port
4. Master Port
5. DB User
6. DB Password
7. Cluster Name
`

const BackupPostScriptRequirement = `Post-backup script will execute a script.  
The script will be executed with the following parameters:  
1. Cluster name
2. DB Server Host
3. DB Server Port
4. Backup Path
`

function LogicalBackupSettings({ selectedCluster, user, dispatch, logicalBackupOptions, onOpenInfoModal }) {

  const [isLogicalBackupOpen, setIsLogicalBackupOpen] = useState(true)

  return (
    <Box className={styles.panel} w='full'>
      <HStack
        as='button'
        type='button'
        spacing={2}
        onClick={() => setIsLogicalBackupOpen((prev) => !prev)}
        aria-expanded={isLogicalBackupOpen}
        aria-controls='logical-backup-content'
        className={styles.panelHeader}
      >
        <Stack spacing={1} className={styles.panelHeaderContent}>
          <Text className={styles.panelTitle}>Logical backup</Text>
          <Text className={styles.panelDescription}>
            Configure logical backup scripts, tools, and restore behavior.
          </Text>
        </Stack>
        <Box className={styles.panelChevron}>{isLogicalBackupOpen ? <HiChevronUp /> : <HiChevronDown />}</Box>
      </HStack>
      <Box
        id='logical-backup-content'
        className={styles.panelBody}
        display={isLogicalBackupOpen ? 'block' : 'none'}
      >
        <Stack spacing={{ base: 1, md: 2 }}>
          <Grid
            className={styles.resticMountGrid}
            templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
            columnGap={3}
            rowGap={1}
            w='full'
          >
            <GridItem className={styles.rowLabel}>
              <Stack spacing={1}>
                <Text>Custom Backup Script</Text>
                <Text className={styles.helperText}>
                  Will not use other logical backup options if set.
                </Text>
              </Stack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <HStack width={'100%'}>
                <TextForm
                  value={selectedCluster?.config?.backupSaveScript}
                  confirmTitle={`Confirm backup-save-script to `}
                  maxLength={1024}
                  className={styles.textbox}
                  size='sm'
                  onSave={(value) =>
                    dispatch(
                      setSetting({
                        clusterName: selectedCluster?.name,
                        setting: 'backup-save-script',
                        value: btoa(value)
                      })
                    )
                  }
                />
                <RMIconButton
                  icon={HiQuestionMarkCircle}
                  onClick={() => onOpenInfoModal('Custom Backup Save Script', BackupSaveScriptRequirement)}
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
              <Text>Custom Load Script</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <HStack width={'100%'}>
                <TextForm
                  value={selectedCluster?.config?.backupLoadScript}
                  confirmTitle={`Confirm backup-load-script to `}
                  maxLength={1024}
                  className={styles.textbox}
                  size='sm'
                  onSave={(value) =>
                    dispatch(
                      setSetting({
                        clusterName: selectedCluster?.name,
                        setting: 'backup-load-script',
                        value: btoa(value)
                      })
                    )
                  }
                />
                <RMIconButton
                  icon={HiQuestionMarkCircle}
                  onClick={() => onOpenInfoModal('Custom Backup Load Script', BackupLoadScriptRequirement)}
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
              <Text>Logical Backup</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <Flex className={styles.dropdownContainer}>
                <Dropdown
                  options={logicalBackupOptions}
                  className={styles.dropdownButton}
                  selectedValue={selectedCluster?.config?.backupLogicalType}
                  confirmTitle={`Confirm logical backup to`}
                  onChange={(backupType) => {
                    dispatch(
                      setSetting({
                        clusterName: selectedCluster?.name,
                        setting: 'backup-logical-type',
                        value: backupType
                      })
                    )
                  }}
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
              <Text>Logical Backup Post-Script</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <HStack width={'100%'}>
                <TextForm
                  value={selectedCluster?.config?.backupLogicalPostScript}
                  confirmTitle={`Confirm backup-logical-post-script to `}
                  maxLength={1024}
                  className={styles.textbox}
                  size='sm'
                  onSave={(value) =>
                    dispatch(
                      setSetting({
                        clusterName: selectedCluster?.name,
                        setting: 'backup-logical-post-script',
                        value: btoa(value)
                      })
                    )
                  }
                />
                <RMIconButton
                  icon={HiQuestionMarkCircle}
                  onClick={() => onOpenInfoModal('Backup Logical Post-Script', BackupPostScriptRequirement)}
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
              <Text>DB Client options</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                value={selectedCluster?.config?.backupMysqlclientOptions}
                confirmTitle={`Confirm backup-mysqlclient-options to `}
                maxLength={1024}
                className={styles.textbox}
                size='sm'
                onSave={(value) =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'backup-mysqlclient-options',
                      value: btoa(value)
                    })
                  )
                }
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
              <Text>Mysqldump options</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                value={selectedCluster?.config?.backupMysqldumpOptions}
                confirmTitle={`Confirm backup-mysqldump-options to `}
                maxLength={1024}
                className={styles.textbox}
                size='sm'
                onSave={(value) =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'backup-mysqldump-options',
                      value: btoa(value)
                    })
                  )
                }
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
              <Text>Mydumper options</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                value={selectedCluster?.config?.backupMyDumperOptions}
                confirmTitle={`Confirm backup-mydumper-options to `}
                maxLength={1024}
                className={styles.textbox}
                size='sm'
                onSave={(value) =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'backup-mydumper-options',
                      value: btoa(value)
                    })
                  )
                }
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
              <Text>Mydumper Regex</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                value={selectedCluster?.config?.backupMyDumperRegex}
                confirmTitle={`Confirm backup-mydumper-regex to `}
                maxLength={1024}
                className={styles.textbox}
                size='sm'
                onSave={(value) =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'backup-mydumper-regex',
                      value: btoa(value)
                    })
                  )
                }
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
              <Text>Myloader options</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                value={selectedCluster?.config?.backupMyLoaderOptions}
                confirmTitle={`Confirm backup-myloader-options to `}
                maxLength={1024}
                className={styles.textbox}
                size='sm'
                onSave={(value) =>
                  dispatch(
                    setSetting({
                      clusterName: selectedCluster?.name,
                      setting: 'backup-myloader-options',
                      value: btoa(value)
                    })
                  )
                }
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
              <Text>Split Logical Dump with DB Credentials</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <Box>
                <RMSwitch
                  isChecked={selectedCluster?.config?.backupSplitMysqlUser}
                  isDisabled={user?.grants['cluster-settings'] == false}
                  confirmTitle={'Confirm switch settings for backup-split-mysql-user?'}
                  onChange={() =>
                    dispatch(
                      switchSetting({
                        clusterName: selectedCluster?.name,
                        setting: 'backup-split-mysql-user'
                      })
                    )
                  }
                />
              </Box>
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
              <Text>Restore User When Reseed</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <Box>
                <RMSwitch
                  isChecked={selectedCluster?.config?.backupRestoreMysqlUser}
                  isDisabled={user?.grants['cluster-settings'] == false}
                  confirmTitle={'Confirm switch settings for backup-restore-mysql-user?'}
                  onChange={() =>
                    dispatch(
                      switchSetting({
                        clusterName: selectedCluster?.name,
                        setting: 'backup-restore-mysql-user'
                      })
                    )
                  }
                />
              </Box>
            </GridItem>
          </Grid>
        </Stack>
      </Box>
    </Box>
  )
}

export default React.memo(LogicalBackupSettings)
