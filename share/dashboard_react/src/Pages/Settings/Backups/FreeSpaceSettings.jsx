import { Grid, GridItem, Stack, Text } from '@chakra-ui/react'
import React from 'react'
import NumberInput from '../../../components/NumberInput'
import RMSwitch from '../../../components/RMSwitch'
import { setSetting, switchSetting } from '../../../redux/settingsSlice'
import styles from '../styles.module.scss'

function FreeSpaceSettings({ selectedCluster, user, dispatch }) {
  const isEnabled = selectedCluster?.config?.backupCheckFreeSpace
  const isReadOnly = user?.grants['cluster-settings'] == false

  const handleSwitchChange = (setting) =>
    dispatch(
      switchSetting({
        clusterName: selectedCluster?.name,
        setting
      })
    )

  const handleSettingChange = (setting, value) =>
    dispatch(
      setSetting({
        clusterName: selectedCluster?.name,
        setting,
        value
      })
    )

  return (
    <Stack spacing={{ base: 1, md: 2 }}>
      <Grid
        className={styles.resticMountGrid}
        templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
        columnGap={3}
        rowGap={1}
        w='full'
      >
        <GridItem className={styles.rowLabel}>
          <Text>Check free space before backup</Text>
        </GridItem>
        <GridItem className={styles.valueCell}>
          <RMSwitch
            isChecked={isEnabled}
            isDisabled={isReadOnly}
            confirmTitle={'Confirm switch settings for backup-check-free-space?'}
            onChange={() => handleSwitchChange('backup-check-free-space')}
          />
        </GridItem>
      </Grid>

      {isEnabled && (
        <>
          <Grid
            className={styles.resticMountGrid}
            templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
            columnGap={3}
            rowGap={1}
            w='full'
          >
            <GridItem className={styles.rowLabel}>
              <Text>Disk Usage Warning Threshold</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <NumberInput
                min={1}
                max={100}
                value={selectedCluster?.config?.backupDiskTresholdWarn}
                showEditButton={true}
                showConfirmModal={true}
                confirmTitle={`Confirm change 'backup-disk-treshold-warn' to: `}
                onConfirm={(value) => handleSettingChange('backup-disk-treshold-warn', value)}
              />
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
              <Text>Disk Usage Critical Threshold</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <NumberInput
                min={1}
                max={100}
                value={selectedCluster?.config?.backupDiskTresholdCrit}
                showEditButton={true}
                showConfirmModal={true}
                confirmTitle={`Confirm change 'backup-disk-treshold-crit' to: `}
                onConfirm={(value) => handleSettingChange('backup-disk-treshold-crit', value)}
              />
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
              <Text>Custom Threshold for Purging Old Restic Backups (0 means follow critical threshold)</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <NumberInput
                min={0}
                max={100}
                value={selectedCluster?.config?.backupResticPurgeOldestOnDiskThreshold}
                showEditButton={true}
                showConfirmModal={true}
                confirmTitle={`Confirm change restic threshold to: `}
                onConfirm={(value) => handleSettingChange('backup-restic-purge-oldest-on-disk-threshold', value)}
              />
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
              <Text>Purge oldest restic backups if disk usage exceed threshold</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <RMSwitch
                isChecked={selectedCluster?.config?.backupResticPurgeOldestOnDiskSpace}
                isDisabled={isReadOnly}
                confirmTitle={'Confirm switch settings for backup-restic-purge-oldest-on-disk-space?'}
                onChange={() => handleSwitchChange('backup-restic-purge-oldest-on-disk-space')}
              />
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
              <Text>Estimate backup size</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <RMSwitch
                isChecked={selectedCluster?.config?.backupEstimateSize}
                isDisabled={isReadOnly}
                confirmTitle={'Confirm switch settings for backup-estimate-size?'}
                onChange={() => handleSwitchChange('backup-estimate-size')}
              />
            </GridItem>
          </Grid>

          {selectedCluster?.config?.backupEstimateSize && (
            <>
              <Grid
                className={styles.resticMountGrid}
                templateColumns={{ base: '1fr', md: 'minmax(180px, 0.7fr) minmax(240px, 1fr)' }}
                columnGap={3}
                rowGap={1}
                w='full'
              >
                <GridItem className={styles.rowLabel}>
                  <Text>Last Backup Growth Percentage (0 means same with last backup)</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <NumberInput
                    min={1}
                    value={selectedCluster?.config?.backupGrowthPercentage}
                    showEditButton={true}
                    showConfirmModal={true}
                    confirmTitle={`Confirm change 'backup-growth-percentage' to: `}
                    onConfirm={(value) => handleSettingChange('backup-growth-percentage', value)}
                  />
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
                  <Text>Backup estimation percentage ratio from information_schema (if last backup not exist)</Text>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <NumberInput
                    min={1}
                    value={selectedCluster?.config?.backupEstimateSizePercentage}
                    showEditButton={true}
                    showConfirmModal={true}
                    confirmTitle={`Confirm change 'backup-estimate-size-percentage' to: `}
                    onConfirm={(value) => handleSettingChange('backup-estimate-size-percentage', value)}
                  />
                </GridItem>
              </Grid>
            </>
          )}
        </>
      )}
    </Stack>
  )
}

export default FreeSpaceSettings
