import { Box, Grid, GridItem, HStack, Stack, Text } from '@chakra-ui/react'
import React, { useState } from 'react'
import { HiChevronDown, HiChevronUp } from 'react-icons/hi'
import TextForm from '../../../components/TextForm'
import { setSetting } from '../../../redux/settingsSlice'
import styles from '../styles.module.scss'

function BackupStreamingSettings({ selectedCluster, dispatch }) {
  const [isStreamingOpen, setIsStreamingOpen] = useState(true)
  const clusterName = selectedCluster?.name
  const config = selectedCluster?.config

  const handleSettingChange = (setting, value) =>
    dispatch(
      setSetting({
        clusterName,
        setting,
        value
      })
    )

  return (
    <Box className={styles.panel} w='full'>
      <HStack
        as='button'
        type='button'
        spacing={2}
        onClick={() => setIsStreamingOpen((prev) => !prev)}
        aria-expanded={isStreamingOpen}
        aria-controls='backup-streaming-content'
        className={styles.panelHeader}
      >
        <Stack spacing={1} className={styles.panelHeaderContent}>
          <Text className={styles.panelTitle}>Backup streaming</Text>
          <Text className={styles.panelDescription}>
            Configure the destination used for streaming backups.
          </Text>
        </Stack>
        <Box className={styles.panelChevron}>{isStreamingOpen ? <HiChevronUp /> : <HiChevronDown />}</Box>
      </HStack>
      <Box id='backup-streaming-content' className={styles.panelBody} display={isStreamingOpen ? 'block' : 'none'}>
        <Stack spacing={{ base: 1, md: 2 }}>
          <Grid
            className={styles.resticMountGrid}
            templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
            columnGap={3}
            rowGap={1}
            w='full'
          >
            <GridItem className={styles.rowLabel}>
              <Text>Backup Streaming Endpoint</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                value={config?.backupStreamingEndpoint}
                confirmTitle={`Confirm backup-streaming-endpoint to `}
                className={styles.textbox}
                size='sm'
                onSave={(value) => handleSettingChange('backup-streaming-endpoint', value)}
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
              <Text>Backup Streaming Region</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                value={config?.backupStreamingRegion}
                confirmTitle={`Confirm backup-streaming-region to `}
                className={styles.textbox}
                size='sm'
                onSave={(value) => handleSettingChange('backup-streaming-region', value)}
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
              <Text>Backup Streaming Bucket</Text>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                value={config?.backupStreamingBucket}
                confirmTitle={`Confirm backup-streaming-bucket to `}
                className={styles.textbox}
                size='sm'
                onSave={(value) => handleSettingChange('backup-streaming-bucket', value)}
              />
            </GridItem>
          </Grid>
        </Stack>
      </Box>
    </Box>
  )
}

export default BackupStreamingSettings
